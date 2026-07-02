package sheets

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"gogo-assets/internal/gworkspace"
	"gogo-assets/internal/inventory"
)

// gwsColumns is the per-user GWS tab definition (mirrors the Python GWS_COLUMNS).
var gwsColumns = []Column[*inventory.UnifiedUserRecord]{
	// ── Sources ───────────────────────────────────────────────────────────────
	{
		Group:  "Sources",
		Header: "Sources",
		Extract: func(r *inventory.UnifiedUserRecord) string {
			parts := []string{}
			if r.Google != nil {
				parts = append(parts, "gws")
			}
			if r.JumpCloud != nil {
				parts = append(parts, "jc")
			}
			if r.Sophos != nil {
				parts = append(parts, "sophos")
			}
			return strings.Join(parts, ", ")
		},
	},

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
	col("Identity", "Admin", func(r *inventory.UnifiedUserRecord) string {
		if r.Google == nil {
			return ""
		}
		return BoolValue(r.Google.Identity.IsAdmin)
	}),
	col("Identity", "Suspended", func(r *inventory.UnifiedUserRecord) string {
		if r.Google == nil {
			return ""
		}
		return BoolValue(r.Google.Identity.IsSuspended)
	}),
	col("Identity", "Created", func(r *inventory.UnifiedUserRecord) string {
		if r.Google == nil {
			return ""
		}
		return fmtDt(r.Google.Identity.CreatedAt)
	}),
	col("Identity", "Recovery Email", func(r *inventory.UnifiedUserRecord) string {
		if r.Google == nil {
			return ""
		}
		return r.Google.Identity.RecoveryEmail
	}),

	// ── Auth ──────────────────────────────────────────────────────────────────
	{
		Group:  "Auth",
		Header: "2SV Enrolled",
		Extract: func(r *inventory.UnifiedUserRecord) string {
			if r.Google == nil {
				return ""
			}
			return BoolValue(r.Google.Auth.Is2SVEnrolled)
		},
		AlertRed: func(v string) bool { return v == "No" },
	},
	col("Auth", "2SV Enforced", func(r *inventory.UnifiedUserRecord) string {
		if r.Google == nil {
			return ""
		}
		return BoolValue(r.Google.Auth.Is2SVEnforced)
	}),
	col("Auth", "Last Login", func(r *inventory.UnifiedUserRecord) string {
		if r.Google == nil {
			return ""
		}
		return fmtDt(r.Google.Auth.LastLoginTime)
	}),
	{
		Group:  "Auth",
		Header: "Force PW Change",
		Extract: func(r *inventory.UnifiedUserRecord) string {
			if r.Google == nil {
				return ""
			}
			return BoolValue(r.Google.Auth.ChangePasswordAtNextLogin)
		},
		AlertYellow: func(v string) bool { return v == "Yes" },
	},

	// ── Activity (7d) ─────────────────────────────────────────────────────────
	col("Activity (7d)", "Last Login IP", func(r *inventory.UnifiedUserRecord) string {
		if la := gwsActivity(r); la != nil {
			return la.LastLoginIP
		}
		return ""
	}),
	col("Activity (7d)", "Known IPs", func(r *inventory.UnifiedUserRecord) string {
		if la := gwsActivity(r); la != nil {
			return fmt.Sprintf("%d", len(la.KnownIPs))
		}
		return ""
	}),
	col("Activity (7d)", "Logins OK", func(r *inventory.UnifiedUserRecord) string {
		if la := gwsActivity(r); la != nil {
			return fmt.Sprintf("%d", la.SuccessfulLoginCount)
		}
		return ""
	}),
	{
		Group:  "Activity (7d)",
		Header: "Logins Failed",
		Extract: func(r *inventory.UnifiedUserRecord) string {
			if la := gwsActivity(r); la != nil {
				return fmt.Sprintf("%d", la.FailedLoginCount)
			}
			return ""
		},
		AlertYellow: func(v string) bool { return intGT(v, 10) },
	},
	{
		Group:  "Activity (7d)",
		Header: "Suspicious",
		Extract: func(r *inventory.UnifiedUserRecord) string {
			if la := gwsActivity(r); la != nil {
				return fmt.Sprintf("%d", la.SuspiciousLoginCount)
			}
			return ""
		},
		AlertRed: isPositiveInt,
	},

	// ── Apps ──────────────────────────────────────────────────────────────────
	col("Apps", "Connected Apps", func(r *inventory.UnifiedUserRecord) string {
		if r.Google == nil {
			return ""
		}
		names := make([]string, 0, len(r.Google.ConnectedApps))
		for _, a := range r.Google.ConnectedApps {
			names = append(names, a.DisplayText)
		}
		return strings.Join(names, "; ")
	}),

	// ── Devices ───────────────────────────────────────────────────────────────
	col("Devices", "Devices", func(r *inventory.UnifiedUserRecord) string {
		if r.Google == nil {
			return ""
		}
		var parts []string
		for _, d := range r.Google.Devices {
			model := d.Model
			if model == "" {
				model = string(d.DeviceKind)
			}
			osType := d.OSType
			if osType == "" {
				osType = "?"
			}
			parts = append(parts, model+" · "+osType)
		}
		return strings.Join(parts, "; ")
	}),
}

func gwsActivity(r *inventory.UnifiedUserRecord) *gworkspace.LoginActivity {
	if r.Google == nil {
		return nil
	}
	return r.Google.LoginActivity
}

// WriteGWS writes the per-user Google Workspace full tab and, when driftTab is
// set, its (Drift) companion — the same columns, only users the drift engine
// flagged (drifted holds their emails). Skip-empty applies to both.
func WriteGWS(ctx context.Context, s *Service, tab, driftTab string, inv *inventory.AssetInventory, drifted map[string]struct{}) error {
	records := make([]*inventory.UnifiedUserRecord, 0, len(inv.Users))
	for _, u := range inv.Users {
		records = append(records, u)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Email < records[j].Email
	})
	return writeFullAndDrift(ctx, s, tab, driftTab, gwsColumns, records,
		func(r *inventory.UnifiedUserRecord) string { return r.Email },
		drifted, WriteOptions{GrayRowHeader: "Suspended"})
}

// col is a small constructor for plain (non-alert) columns.
func col[T any](group, header string, extract func(T) string) Column[T] {
	return Column[T]{Group: group, Header: header, Extract: extract}
}
