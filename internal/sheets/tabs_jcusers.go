package sheets

import (
	"context"
	"sort"
	"strings"

	"gogo-assets/internal/inventory"
	"gogo-assets/internal/jumpcloud"
)

func jcuSSHKeys(u jumpcloud.User) string {
	if len(u.SSHKeys) == 0 {
		return ""
	}
	names := make([]string, 0, len(u.SSHKeys))
	for _, k := range u.SSHKeys {
		names = append(names, k.Name)
	}
	return strings.Join(names, "; ")
}

// jcUserColumns render one row per JumpCloud directory user. The same columns
// back the full tab and its (Drift) companion (users the engine flagged — MFA
// off, password never expires, …). This is the per-service view JumpCloud users
// lacked: the devices tab only shows owners of a device, and its drift companion
// keys on device findings, so a user with a clean device but no MFA was invisible.
var jcUserColumns = []Column[jumpcloud.User]{
	col("Identity", "Email", func(u jumpcloud.User) string { return u.Email }),
	col("Identity", "Username", func(u jumpcloud.User) string { return u.Username }),
	col("Identity", "Full Name", func(u jumpcloud.User) string { return u.FullName }),

	{
		Group:    "MFA",
		Header:   "MFA Configured",
		Extract:  func(u jumpcloud.User) string { return BoolValue(u.MFAConfigured) },
		AlertRed: func(v string) bool { return v == "No" },
	},
	{
		Group:    "MFA",
		Header:   "TOTP Enrolled",
		Extract:  func(u jumpcloud.User) string { return BoolValue(u.TOTPEnabled) },
		AlertRed: func(v string) bool { return v == "No" },
	},
	col("MFA", "MFA Required", func(u jumpcloud.User) string { return BoolValue(u.MFARequired) }),

	{
		Group:    "Password",
		Header:   "Password",
		Extract:  func(u jumpcloud.User) string { return fmtPasswordStatus(&u) },
		AlertRed: func(v string) bool { return v == "Expired" || v == "Never expires" },
	},

	{
		Group:    "Account",
		Header:   "Locked",
		Extract:  func(u jumpcloud.User) string { return BoolValue(u.AccountLocked) },
		AlertRed: func(v string) bool { return v == "Yes" },
	},
	col("Account", "Activated", func(u jumpcloud.User) string { return BoolValue(u.Activated) }),
	col("Account", "Suspended", func(u jumpcloud.User) string { return BoolValue(u.Suspended) }),

	col("Access", "SSH Keys", jcuSSHKeys),
}

// WriteJCUsers writes the per-user JumpCloud Users full tab and, when driftTab is
// set, its (Drift) companion — the same columns, only users the drift engine
// flagged (drifted holds their emails). Skip-empty applies to both.
func WriteJCUsers(ctx context.Context, s *Service, tab, driftTab string, inv *inventory.AssetInventory, drifted map[string]struct{}) error {
	rows := make([]jumpcloud.User, 0, len(inv.JCUsers))
	for _, u := range inv.JCUsers {
		rows = append(rows, u)
	}
	sort.Slice(rows, func(i, j int) bool {
		return strings.ToLower(rows[i].Email) < strings.ToLower(rows[j].Email)
	})
	return writeFullAndDrift(ctx, s, tab, driftTab, jcUserColumns, rows,
		func(u jumpcloud.User) string { return u.Email },
		drifted, WriteOptions{})
}
