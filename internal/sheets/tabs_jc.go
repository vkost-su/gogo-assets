package sheets

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"gogo-assets/internal/inventory"
	"gogo-assets/internal/jumpcloud"
)

// JCSystemRow pairs a system with its owning user for the JC tab.
type JCSystemRow struct {
	System jumpcloud.System
	User   *jumpcloud.User

	// LocalUsers is the device's local OS accounts with the OS-appropriate
	// allowlisted system accounts removed — i.e. the unexpected ones.
	LocalUsers []string
}

func jcSystem(r JCSystemRow) jumpcloud.System { return r.System }
func jcUser(r JCSystemRow) *jumpcloud.User    { return r.User }

// fmtJCHostname shows the endpoint hostname; when JumpCloud's display name
// differs it is appended in parentheses so we do not need a duplicate column.
func fmtJCHostname(s jumpcloud.System) string {
	h := strings.TrimSpace(s.Hostname)
	d := strings.TrimSpace(s.DisplayName)
	if d == "" || strings.EqualFold(d, h) {
		return h
	}
	return h + " (" + d + ")"
}

// JCDeviceDrift merges device-level drift IDs with devices whose owner directory
// user was flagged — so user posture (e.g. password never expires) surfaces on
// the main JumpCloud devices (Drift) companion, not only on JumpCloud Users.
func JCDeviceDrift(inv *inventory.AssetInventory, deviceDrift, userDrift map[string]struct{}) map[string]struct{} {
	if len(userDrift) == 0 {
		return deviceDrift
	}
	out := make(map[string]struct{}, len(deviceDrift)+8)
	for id := range deviceDrift {
		out[id] = struct{}{}
	}
	for _, sys := range inv.JCSystems {
		if sys.OwnerEmail == "" {
			continue
		}
		if _, ok := userDrift[sys.OwnerEmail]; ok {
			out[sys.SystemID] = struct{}{}
		}
	}
	return out
}

func fmtPasswordStatus(u *jumpcloud.User) string {
	if u == nil {
		return ""
	}
	switch {
	case u.PasswordExpired:
		return "Expired"
	case u.PasswordNeverExpires:
		return "Never expires"
	case !u.PasswordExpirationDate.IsZero():
		return u.PasswordExpirationDate.Format("2006-01-02")
	}
	return ""
}

func fmtSpecs(r JCSystemRow) string {
	s := r.System
	var parts []string
	if s.CPUBrand != "" {
		core := ""
		if s.CPUPhysicalCores != nil {
			core = fmt.Sprintf(" (%dP", *s.CPUPhysicalCores)
			if s.CPULogicalCores != nil && *s.CPULogicalCores != *s.CPUPhysicalCores {
				core += fmt.Sprintf("/%dL", *s.CPULogicalCores)
			}
			core += ")"
		}
		parts = append(parts, s.CPUBrand+core)
	}
	if s.RAMBytes != nil {
		parts = append(parts, fmt.Sprintf("%d GB", *s.RAMBytes/(1024*1024*1024)))
	}
	if s.DiskSizeBytes != nil {
		parts = append(parts, fmt.Sprintf("%d GB SSD", *s.DiskSizeBytes/(1024*1024*1024)))
	}
	return strings.Join(parts, " · ")
}

func fmtFailedPolicies(r JCSystemRow) string { return strings.Join(r.System.FailedPolicies, "; ") }

func fmtSSHKeys(r JCSystemRow) string {
	u := r.User
	if u == nil || len(u.SSHKeys) == 0 {
		return ""
	}
	names := make([]string, 0, len(u.SSHKeys))
	for _, k := range u.SSHKeys {
		names = append(names, k.Name)
	}
	return strings.Join(names, "; ")
}

func fmtEncryption(r JCSystemRow) string {
	s := r.System
	var enc string
	switch {
	case s.DiskEncrypted == nil:
		enc = "?"
	case *s.DiskEncrypted:
		enc = "Yes"
	default:
		enc = "No"
	}
	parts := []string{enc}
	if s.EncryptionType != "" && s.EncryptionType != "FileVault" {
		parts = append(parts, s.EncryptionType)
	}
	if s.FileVaultStatus != "" {
		fv := strings.ToUpper(s.FileVaultStatus[:1]) + strings.ToLower(s.FileVaultStatus[1:])
		parts = append(parts, "FileVault: "+fv)
	} else if s.EncryptionType == "FileVault" {
		parts = append(parts, "FileVault")
	}
	return strings.Join(parts, " · ")
}

func fmtMDM(r JCSystemRow) string {
	s := r.System
	if !s.MDMEnrolled {
		return "No"
	}
	vendor := s.MDMVendor
	if vendor == "" {
		vendor = "enrolled"
	}
	parts := []string{vendor}
	if s.MDMEnrollmentType != "" {
		parts = append(parts, s.MDMEnrollmentType)
	}
	if s.MDMDEP {
		parts = append(parts, "DEP")
	}
	if s.MDMUserApproved {
		parts = append(parts, "UAMDM")
	}
	return strings.Join(parts, " · ")
}

var jcColumns = []Column[JCSystemRow]{
	// ── User Identity ─────────────────────────────────────────────────────────
	col("User", "Owner Email", func(r JCSystemRow) string { return jcSystem(r).OwnerEmail }),
	col("User", "Full Name", func(r JCSystemRow) string {
		if u := jcUser(r); u != nil {
			return u.FullName
		}
		return ""
	}),
	{
		Group:  "User",
		Header: "2FA Status",
		Extract: func(r JCSystemRow) string {
			u := jcUser(r)
			if u == nil {
				return ""
			}
			if u.MFAConfigured {
				return "Configured"
			}
			return "Not configured"
		},
		AlertRed: func(v string) bool { return v == "Not configured" },
	},
	{
		Group:  "User",
		Header: "TOTP Enrolled",
		Extract: func(r JCSystemRow) string {
			u := jcUser(r)
			if u == nil {
				return ""
			}
			return BoolValue(u.TOTPEnabled)
		},
		AlertRed: func(v string) bool { return v == "No" },
	},
	{
		Group:    "User",
		Header:   "Password",
		Extract:  func(r JCSystemRow) string { return fmtPasswordStatus(jcUser(r)) },
		AlertRed: func(v string) bool { return v == "Expired" || v == "Never expires" },
	},
	{
		Group:  "User",
		Header: "Acct Locked",
		Extract: func(r JCSystemRow) string {
			u := jcUser(r)
			if u == nil {
				return ""
			}
			return BoolValue(u.AccountLocked)
		},
		AlertRed: func(v string) bool { return v == "Yes" },
	},
	col("User", "SSH Keys", fmtSSHKeys),

	// ── Endpoint ──────────────────────────────────────────────────────────────
	col("Endpoint", "Hostname", func(r JCSystemRow) string { return fmtJCHostname(jcSystem(r)) }),
	col("Endpoint", "OS", func(r JCSystemRow) string { return jcSystem(r).OSType }),
	col("Endpoint", "OS Version", func(r JCSystemRow) string { return jcSystem(r).OSVersion }),
	col("Endpoint", "Last Contact", func(r JCSystemRow) string {
		return fmtDtRelative(jcSystem(r).LastContact)
	}),
	col("Endpoint", "Agent Version", func(r JCSystemRow) string { return jcSystem(r).AgentVersion }),

	// ── Hardware ──────────────────────────────────────────────────────────────
	col("Hardware", "Device", func(r JCSystemRow) string {
		s := jcSystem(r)
		return joinDot(s.Manufacturer, s.HardwareModel)
	}),
	col("Hardware", "Specs", fmtSpecs),
	col("Hardware", "Serial", func(r JCSystemRow) string { return jcSystem(r).SerialNumber }),

	// ── Network ───────────────────────────────────────────────────────────────
	col("Network", "Remote IP", func(r JCSystemRow) string { return jcSystem(r).RemoteIP }),

	// ── MDM ───────────────────────────────────────────────────────────────────
	{
		Group:       "MDM",
		Header:      "MDM Status",
		Extract:     fmtMDM,
		AlertYellow: func(v string) bool { return v == "No" },
	},

	// ── Encryption ────────────────────────────────────────────────────────────
	{
		Group:       "Encryption",
		Header:      "Disk Encrypted",
		Extract:     fmtEncryption,
		AlertRed:    func(v string) bool { return strings.HasPrefix(v, "No") },
		AlertYellow: func(v string) bool { return strings.HasPrefix(v, "?") },
	},

	// ── Policies ──────────────────────────────────────────────────────────────
	col("Policies", "Policy Stats", func(r JCSystemRow) string {
		ps := jcSystem(r).PolicyStats
		if len(ps) == 0 {
			return ""
		}
		return fmt.Sprintf("total=%d ok=%d fail=%d pending=%d",
			ps["total"], ps["success"], ps["failed"], ps["pending"])
	}),
	{
		Group:    "Policies",
		Header:   "Failed Policies",
		Extract:  fmtFailedPolicies,
		AlertRed: func(v string) bool { return v != "" },
	},

	// ── Local Users ───────────────────────────────────────────────────────────
	// "All Users" lists local OS users surviving the early whitelist purge;
	// any survivor is unexpected → red.
	{
		Group:    "Local Users",
		Header:   "All Users",
		Extract:  func(r JCSystemRow) string { return strings.Join(r.LocalUsers, "; ") },
		AlertRed: func(v string) bool { return v != "" },
	},

	// ── USB ───────────────────────────────────────────────────────────────────
	col("USB", "USB Devices", func(r JCSystemRow) string {
		return strings.Join(jcSystem(r).USBDevices, "; ")
	}),

	// Software/extensions moved to the "JumpCloud Software" tab (per-person,
	// email→device, allowlist-filtered in its drift companion).

	// ── Network Config ────────────────────────────────────────────────────────
	col("Network Config", "ETC Hosts", func(r JCSystemRow) string {
		return strings.Join(jcSystem(r).EtcHosts, "; ")
	}),
}

// WriteJC writes the per-device JumpCloud full tab and, when driftTab is set, its
// (Drift) companion — device findings plus devices whose owner user drifted.
func WriteJC(ctx context.Context, s *Service, tab, driftTab string, inv *inventory.AssetInventory, deviceDrift, userDrift map[string]struct{}) error {
	drifted := JCDeviceDrift(inv, deviceDrift, userDrift)
	rows := make([]JCSystemRow, 0, len(inv.JCSystems))
	for i := range inv.JCSystems {
		sys := inv.JCSystems[i]
		var u *jumpcloud.User
		if sys.OwnerEmail != "" {
			if user, ok := inv.JCUsers[sys.OwnerEmail]; ok {
				u = &user
			}
		}
		rows = append(rows, JCSystemRow{
			System:     sys,
			User:       u,
			LocalUsers: sys.LocalUsers,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return strings.ToLower(rows[i].System.Hostname) < strings.ToLower(rows[j].System.Hostname)
	})
	return writeFullAndDrift(ctx, s, tab, driftTab, jcColumns, rows,
		func(r JCSystemRow) string { return r.System.SystemID },
		drifted, WriteOptions{})
}
