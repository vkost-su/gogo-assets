package filter

import (
	"log/slog"

	"gogo-assets/internal/logging"
)

// Log emits a filter phase summary and per-device lines for JumpCloud systems
// where the whitelist removed software or local users. software= is the
// post-whitelist count (actionable); collected= is the raw enrichment count.
func Log(log *slog.Logger, st Stats) {
	if !st.Loaded {
		return
	}
	log = logging.For("filter")
	done := logging.Phase(log, "whitelist")
	for _, d := range st.Devices {
		args := []any{
			"i", d.Index, "total", d.Total,
			"host", truncate(d.Hostname, 32),
			"owner", emailOrDash(d.Owner),
		}
		if d.softwarePurged() {
			args = append(args, "software", d.SoftwareAfter, "collected", d.SoftwareBefore)
		}
		if d.localUsersPurged() {
			args = append(args,
				"local_users", d.LocalUsersAfter,
				"collected_users", d.LocalUsersBefore,
			)
		}
		log.Info("device", args...)
	}
	done(
		"jc_software", st.SoftwareAfter, "jc_software_collected", st.SoftwareBefore,
		"jc_local_users", st.LocalUsersAfter, "jc_local_users_collected", st.LocalUsersBefore,
		"saas_accounts", st.SaaSAccountsAfter, "saas_accounts_collected", st.SaaSAccountsBefore,
		"gws_connected_apps", st.GWSAppsAfter, "gws_connected_apps_collected", st.GWSAppsBefore,
		"devices_touched", len(st.Devices),
	)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func emailOrDash(email string) string {
	if email == "" {
		return "—"
	}
	return email
}
