package sheets

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"gogo-assets/internal/inventory"
)

var _osShort = map[string]string{
	"darwin": "mac", "mac os x": "mac", "macos": "mac",
	"windows": "win",
	"linux":   "lnx",
}

func deviceOS(p inventory.DevicePair) string {
	raw := ""
	if p.JC != nil {
		raw = strings.ToLower(p.JC.OSFamily)
		if raw == "" {
			raw = strings.ToLower(p.JC.OSType)
		}
	} else if p.Sophos != nil {
		raw = strings.ToLower(p.Sophos.OSPlatform)
	}
	if short, ok := _osShort[raw]; ok {
		return short
	}
	if len(raw) > 3 {
		return raw[:3]
	}
	return raw
}

func deviceHostname(p inventory.DevicePair) string {
	if p.JC != nil && p.JC.Hostname != "" {
		return p.JC.Hostname
	}
	if p.Sophos != nil && p.Sophos.Hostname != "" {
		return p.Sophos.Hostname
	}
	return ""
}

// formatDevice renders one DevicePair on a single line.
//
// Layout: hostname · os · coverage [· problems] [· health]
//
//	coverage : "JC+SP" / "JC" / "SP"
//	problems : only when ✗ — e.g. "enc✗ MDM✗"
//	health   : only when bad/suspicious
func formatDevice(p inventory.DevicePair) string {
	var cov []string
	if p.JC != nil {
		cov = append(cov, "JC")
	}
	if p.Sophos != nil {
		cov = append(cov, "SP")
	}

	var problems []string
	if p.JC != nil {
		if p.JC.DiskEncrypted != nil && !*p.JC.DiskEncrypted {
			problems = append(problems, "enc✗")
		}
		if !p.JC.MDMEnrolled {
			problems = append(problems, "MDM✗")
		}
	}

	health := ""
	if p.Sophos != nil && (p.Sophos.HealthOverall == "bad" || p.Sophos.HealthOverall == "suspicious") {
		health = p.Sophos.HealthOverall
	}

	parts := []string{deviceHostname(p), deviceOS(p), strings.Join(cov, "+")}
	if len(problems) > 0 {
		parts = append(parts, strings.Join(problems, " "))
	}
	if health != "" {
		parts = append(parts, health)
	}
	// drop empty parts
	clean := parts[:0]
	for _, p := range parts {
		if p != "" {
			clean = append(clean, p)
		}
	}
	return strings.Join(clean, " · ")
}

func devicesDetail(r *inventory.UnifiedUserRecord) string {
	if len(r.Devices) == 0 {
		return ""
	}
	ordered := make([]inventory.DevicePair, len(r.Devices))
	copy(ordered, r.Devices)
	sort.Slice(ordered, func(i, j int) bool {
		return strings.ToLower(deviceHostname(ordered[i])) < strings.ToLower(deviceHostname(ordered[j]))
	})
	lines := make([]string, 0, len(ordered))
	for _, p := range ordered {
		lines = append(lines, formatDevice(p))
	}
	return strings.Join(lines, "\n")
}

func devicesMatch(r *inventory.UnifiedUserRecord) string {
	if len(r.Devices) == 0 {
		return ""
	}
	paired, jcOnly, spOnly := 0, 0, 0
	for _, d := range r.Devices {
		switch {
		case d.JC != nil && d.Sophos != nil:
			paired++
		case d.JC != nil && d.Sophos == nil:
			jcOnly++
		case d.JC == nil && d.Sophos != nil:
			spOnly++
		}
	}
	var parts []string
	if paired > 0 {
		parts = append(parts, fmt.Sprintf("%dP", paired))
	}
	if jcOnly > 0 {
		parts = append(parts, fmt.Sprintf("%dJC", jcOnly))
	}
	if spOnly > 0 {
		parts = append(parts, fmt.Sprintf("%dSP", spOnly))
	}
	return strings.Join(parts, " ")
}

func sophosWorstHealth(r *inventory.UnifiedUserRecord) string {
	if r.Sophos == nil || len(r.Sophos.Endpoints) == 0 {
		return ""
	}
	order := map[string]int{"bad": 0, "suspicious": 1, "unknown": 2, "good": 3}
	worst := ""
	best := 99
	for _, e := range r.Sophos.Endpoints {
		if e.HealthOverall == "" {
			continue
		}
		v, ok := order[e.HealthOverall]
		if !ok {
			v = 99
		}
		if v < best {
			best = v
			worst = e.HealthOverall
		}
	}
	return worst
}

// devicesOS lists the distinct OS short-names across a user's devices, e.g.
// "mac" or "mac, win" — a sortable rollup of what the Detail column spells out
// per device.
func devicesOS(r *inventory.UnifiedUserRecord) string {
	seen := make(map[string]struct{})
	var oses []string
	for _, p := range r.Devices {
		os := deviceOS(p)
		if os == "" {
			continue
		}
		if _, dup := seen[os]; dup {
			continue
		}
		seen[os] = struct{}{}
		oses = append(oses, os)
	}
	sort.Strings(oses)
	return strings.Join(oses, ", ")
}

// devicesEnc rolls up disk-encryption posture across a user's JC devices:
// "✗" if any device reports unencrypted, "✓" if at least one reports encrypted
// and none unencrypted, "" when every device's state is unknown.
func devicesEnc(r *inventory.UnifiedUserRecord) string {
	anyEncrypted, anyPlaintext := false, false
	for _, p := range r.Devices {
		if p.JC == nil || p.JC.DiskEncrypted == nil {
			continue
		}
		if *p.JC.DiskEncrypted {
			anyEncrypted = true
		} else {
			anyPlaintext = true
		}
	}
	switch {
	case anyPlaintext:
		return "✗"
	case anyEncrypted:
		return "✓"
	default:
		return ""
	}
}

// devicesMDM rolls up MDM enrollment across a user's JC devices: "✗" if any JC
// device is not enrolled, "✓" if all are, "" when the user has no JC device.
func devicesMDM(r *inventory.UnifiedUserRecord) string {
	hasJC, allEnrolled := false, true
	for _, p := range r.Devices {
		if p.JC == nil {
			continue
		}
		hasJC = true
		if !p.JC.MDMEnrolled {
			allEnrolled = false
		}
	}
	if !hasJC {
		return ""
	}
	if allEnrolled {
		return "✓"
	}
	return "✗"
}

// devicesLastSeen returns the most recent contact across a user's devices —
// the later of JumpCloud LastContact and Sophos LastSeenAt — so stale devices
// are visible without opening the Detail cell.
func devicesLastSeen(r *inventory.UnifiedUserRecord) string {
	var latest time.Time
	for _, p := range r.Devices {
		if p.JC != nil && p.JC.LastContact.After(latest) {
			latest = p.JC.LastContact
		}
		if p.Sophos != nil && p.Sophos.LastSeenAt.After(latest) {
			latest = p.Sophos.LastSeenAt
		}
	}
	return fmtDt(latest)
}

var mergedColumns = []Column[*inventory.UnifiedUserRecord]{
	// ── Identity ──────────────────────────────────────────────────────────────
	col("Identity", "Email", func(r *inventory.UnifiedUserRecord) string { return r.Email }),
	col("Identity", "Full Name", func(r *inventory.UnifiedUserRecord) string {
		if r.Google == nil {
			return ""
		}
		return r.Google.Identity.FullName
	}),
	col("Identity", "Org Unit", func(r *inventory.UnifiedUserRecord) string {
		if r.Google == nil {
			return ""
		}
		return r.Google.Identity.OrgUnitPath
	}),
	{
		Group:  "Identity",
		Header: "Admin",
		Extract: func(r *inventory.UnifiedUserRecord) string {
			if r.Google == nil || !r.Google.Identity.IsAdmin {
				return ""
			}
			return "Yes"
		},
		AlertYellow: func(v string) bool { return v == "Yes" },
	},

	// ── Coverage ──────────────────────────────────────────────────────────────
	{
		Group:  "Coverage",
		Header: "GWS",
		Extract: func(r *inventory.UnifiedUserRecord) string {
			if r.Google != nil {
				return "✓"
			}
			return "—"
		},
		AlertRed: func(v string) bool { return v == "—" },
	},
	{
		Group:  "Coverage",
		Header: "JC",
		Extract: func(r *inventory.UnifiedUserRecord) string {
			if r.JumpCloud != nil && len(r.JumpCloud.Systems) > 0 {
				return "✓"
			}
			return "—"
		},
		AlertYellow: func(v string) bool { return v == "—" },
	},
	{
		Group:  "Coverage",
		Header: "SP",
		Extract: func(r *inventory.UnifiedUserRecord) string {
			if r.Sophos != nil && len(r.Sophos.Endpoints) > 0 {
				return "✓"
			}
			return "—"
		},
		AlertYellow: func(v string) bool { return v == "—" },
	},
	{
		Group:  "Coverage",
		Header: "PF",
		Extract: func(r *inventory.UnifiedUserRecord) string {
			if r.PeopleForce != nil && len(r.PeopleForce.Assets) > 0 {
				return "✓"
			}
			return "—"
		},
		AlertYellow: func(v string) bool { return v == "—" },
	},

	// ── GWS posture ───────────────────────────────────────────────────────────
	col("GWS", "Suspended", func(r *inventory.UnifiedUserRecord) string {
		if r.Google == nil {
			return ""
		}
		return BoolValue(r.Google.Identity.IsSuspended)
	}),
	{
		Group:  "GWS",
		Header: "2SV",
		Extract: func(r *inventory.UnifiedUserRecord) string {
			if r.Google == nil {
				return ""
			}
			return BoolValue(r.Google.Auth.Is2SVEnrolled)
		},
		AlertRed: func(v string) bool { return v == "No" },
	},
	col("GWS", "Last Login", func(r *inventory.UnifiedUserRecord) string {
		if r.Google == nil {
			return ""
		}
		return fmtDt(r.Google.Auth.LastLoginTime)
	}),

	// ── Devices (joined JC ↔ Sophos) ──────────────────────────────────────────
	col("Devices", "Count", func(r *inventory.UnifiedUserRecord) string {
		if len(r.Devices) == 0 {
			return ""
		}
		return fmt.Sprintf("%d", len(r.Devices))
	}),
	col("Devices", "Match", devicesMatch),
	col("Devices", "OS", devicesOS),
	{
		Group:    "Devices",
		Header:   "Enc",
		Extract:  devicesEnc,
		AlertRed: func(v string) bool { return v == "✗" },
	},
	{
		Group:       "Devices",
		Header:      "MDM",
		Extract:     devicesMDM,
		AlertYellow: func(v string) bool { return v == "✗" },
	},
	col("Devices", "Last Seen", devicesLastSeen),
	{
		Group:   "Devices",
		Header:  "Detail",
		Extract: devicesDetail,
		Wrap:    true,
	},

	// ── PeopleForce assets ────────────────────────────────────────────────────
	col("PF Assets", "Count", func(r *inventory.UnifiedUserRecord) string {
		if r.PeopleForce == nil || len(r.PeopleForce.Assets) == 0 {
			return ""
		}
		return fmt.Sprintf("%d", len(r.PeopleForce.Assets))
	}),
	{
		Group:  "PF Assets",
		Header: "Detail",
		Extract: func(r *inventory.UnifiedUserRecord) string {
			if r.PeopleForce == nil {
				return ""
			}
			lines := make([]string, 0, len(r.PeopleForce.Assets))
			for _, a := range r.PeopleForce.Assets {
				label := a.Name
				if a.CategoryName != "" {
					label = a.CategoryName + ": " + a.Name
				}
				if a.SerialNumber != "" {
					label += " (" + a.SerialNumber + ")"
				}
				lines = append(lines, label)
			}
			sort.Strings(lines)
			return strings.Join(lines, "\n")
		},
		Wrap: true,
	},

	// ── Alerts ────────────────────────────────────────────────────────────────
	{
		Group:    "Alerts",
		Header:   "Health",
		Extract:  sophosWorstHealth,
		AlertRed: func(v string) bool { return v == "bad" || v == "suspicious" },
	},
	{
		Group:  "Alerts",
		Header: "Open",
		Extract: func(r *inventory.UnifiedUserRecord) string {
			if r.Sophos == nil {
				return ""
			}
			total := 0
			for _, e := range r.Sophos.Endpoints {
				total += e.AlertCount
			}
			return fmt.Sprintf("%d", total)
		},
		AlertRed: isPositiveInt,
	},
}

// WriteMerged writes the cross-source merged view — one row per unique user.
// Users with at least one paired/owned device float to the top.
func WriteMerged(ctx context.Context, s *Service, tab string, inv *inventory.AssetInventory) error {
	rows := make([]*inventory.UnifiedUserRecord, 0, len(inv.Users))
	for _, u := range inv.Users {
		rows = append(rows, u)
	}
	sort.Slice(rows, func(i, j int) bool {
		ai := 0
		if len(rows[i].Devices) == 0 {
			ai = 1
		}
		aj := 0
		if len(rows[j].Devices) == 0 {
			aj = 1
		}
		if ai != aj {
			return ai < aj
		}
		return strings.ToLower(rows[i].Email) < strings.ToLower(rows[j].Email)
	})
	return writeTab(ctx, s, tab, mergedColumns, rows, WriteOptions{
		GrayRowHeader: "Suspended",
	})
}
