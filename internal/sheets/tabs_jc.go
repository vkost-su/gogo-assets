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
}

func jcSystem(r JCSystemRow) jumpcloud.System { return r.System }
func jcUser(r JCSystemRow) *jumpcloud.User    { return r.User }

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

func fmtApps(apps []jumpcloud.App) string {
	parts := make([]string, 0, len(apps))
	for _, a := range apps {
		label := a.Name
		if a.Version != "" {
			label += " (" + a.Version + ")"
		}
		if a.LastOpened != "" {
			label += " [" + a.LastOpened + "]"
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, "; ")
}

func fmtPolicyStatuses(r JCSystemRow) string {
	parts := make([]string, 0, len(r.System.PolicyStatuses))
	for _, p := range r.System.PolicyStatuses {
		parts = append(parts, p.Name+": "+p.Status)
	}
	return strings.Join(parts, "; ")
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

func jcSoftware(r JCSystemRow) string {
	s := r.System
	switch {
	case len(s.Apps) > 0:
		return fmtApps(s.Apps)
	case len(s.Programs) > 0:
		return fmtApps(s.Programs)
	case len(s.DEBPackages) > 0:
		return fmtApps(s.DEBPackages)
	case len(s.RPMPackages) > 0:
		return fmtApps(s.RPMPackages)
	}
	return ""
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
		Group:  "User",
		Header: "Password",
		Extract: func(r JCSystemRow) string {
			u := jcUser(r)
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
		},
		AlertRed: func(v string) bool { return v == "Expired" },
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
	col("Endpoint", "Hostname", func(r JCSystemRow) string { return jcSystem(r).Hostname }),
	col("Endpoint", "Display Name", func(r JCSystemRow) string { return jcSystem(r).DisplayName }),
	col("Endpoint", "OS", func(r JCSystemRow) string { return jcSystem(r).OSType }),
	col("Endpoint", "OS Version", func(r JCSystemRow) string { return jcSystem(r).OSVersion }),
	{
		Group:       "Endpoint",
		Header:      "Active",
		Extract:     func(r JCSystemRow) string { return BoolValue(jcSystem(r).Active) },
		AlertYellow: func(v string) bool { return v == "No" },
	},
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
	col("Policies", "All Policies", fmtPolicyStatuses),

	// ── Local Users ───────────────────────────────────────────────────────────
	col("Local Users", "All Users", func(r JCSystemRow) string {
		return strings.Join(jcSystem(r).LocalUsers, "; ")
	}),
	{
		Group:  "Local Users",
		Header: "Unexpected Users",
		Extract: func(r JCSystemRow) string {
			return strings.Join(jcSystem(r).UnexpectedLocalUsers, "; ")
		},
		AlertRed: func(v string) bool { return v != "" },
	},

	// ── USB ───────────────────────────────────────────────────────────────────
	col("USB", "USB Devices", func(r JCSystemRow) string {
		return strings.Join(jcSystem(r).USBDevices, "; ")
	}),

	// ── Software ──────────────────────────────────────────────────────────────
	col("Software", "Applications", jcSoftware),
	col("Software", "Browser Plugins", func(r JCSystemRow) string {
		return fmtApps(jcSystem(r).BrowserPlugins)
	}),
	col("Software", "Chrome Extensions", func(r JCSystemRow) string {
		return fmtApps(jcSystem(r).ChromeExtensions)
	}),
	col("Software", "Firefox Add-ons", func(r JCSystemRow) string {
		return fmtApps(jcSystem(r).FirefoxAddons)
	}),
	col("Software", "Safari Extensions", func(r JCSystemRow) string {
		return fmtApps(jcSystem(r).SafariExtensions)
	}),

	// ── Network Config ────────────────────────────────────────────────────────
	col("Network Config", "ETC Hosts", func(r JCSystemRow) string {
		return strings.Join(jcSystem(r).EtcHosts, "; ")
	}),
}

// WriteJC writes the per-system JumpCloud tab.
func WriteJC(ctx context.Context, s *Service, tab string, inv *inventory.AssetInventory) error {
	rows := make([]JCSystemRow, 0, len(inv.JCSystems))
	for i := range inv.JCSystems {
		sys := inv.JCSystems[i]
		var u *jumpcloud.User
		if sys.OwnerEmail != "" {
			if user, ok := inv.JCUsers[sys.OwnerEmail]; ok {
				u = &user
			}
		}
		rows = append(rows, JCSystemRow{System: sys, User: u})
	}
	sort.Slice(rows, func(i, j int) bool {
		return strings.ToLower(rows[i].System.Hostname) < strings.ToLower(rows[j].System.Hostname)
	})
	return writeTab(ctx, s, tab, jcColumns, rows, WriteOptions{})
}
