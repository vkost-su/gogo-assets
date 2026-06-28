package sheets

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gogo-assets/internal/inventory"
	"gogo-assets/internal/jumpcloud"
)

// titleizeEnum turns an API SCREAMING_SNAKE enum into a display label, e.g.
// "NEWLY_DISCOVERED" → "Newly Discovered".
func titleizeEnum(s string) string {
	if s == "" {
		return ""
	}
	words := strings.Split(strings.ToLower(s), "_")
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

func saasSSO(a jumpcloud.SaaSApp) string {
	if len(a.SSOApps) == 0 {
		return ""
	}
	if a.SSOConnected() {
		return "Connected"
	}
	return "Not connected"
}

// saasAccountsDetail lists each owner account, one per line, with its last-used
// date when known — the spelled-out form of the Count column.
func saasAccountsDetail(a jumpcloud.SaaSApp) string {
	lines := make([]string, 0, len(a.Accounts))
	for _, acc := range a.Accounts {
		label := acc.Email
		if label == "" {
			label = acc.Username
		}
		if label == "" && acc.DeviceOwner != "" {
			label = acc.DeviceOwner + " (via device)"
		}
		if label == "" {
			label = acc.AccountID
		}
		if !acc.LatestUsedAt.IsZero() {
			label += " [" + acc.LatestUsedAt.UTC().Format("2006-01-02") + "]"
		}
		lines = append(lines, label)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func saasSeats(a jumpcloud.SaaSApp) string {
	total, assigned, _ := a.LicenseTotals()
	if total == 0 && assigned == 0 && len(a.Licenses) == 0 {
		return ""
	}
	for _, l := range a.Licenses {
		if l.IsUnlimited {
			return fmt.Sprintf("%d / ∞", assigned)
		}
	}
	return fmt.Sprintf("%d / %d", assigned, total)
}

func saasCost(a jumpcloud.SaaSApp) string {
	if a.Contract == nil || a.Contract.Cost == 0 {
		return ""
	}
	cost := a.Contract.Cost
	var amount string
	if cost == float64(int64(cost)) {
		amount = strconv.FormatInt(int64(cost), 10)
	} else {
		amount = strconv.FormatFloat(cost, 'f', 2, 64)
	}
	return strings.TrimSpace(amount + " " + a.Contract.Currency)
}

func saasRenewal(a jumpcloud.SaaSApp) string {
	if a.Contract == nil {
		return ""
	}
	return a.Contract.RenewalDate
}

func saasTerm(a jumpcloud.SaaSApp) string {
	if a.Contract == nil {
		return ""
	}
	return titleizeEnum(a.Contract.Term)
}

var saasColumns = []Column[jumpcloud.SaaSApp]{
	// ── Service ─────────────────────────────────────────────────────────────────
	col("Service", "Name", func(a jumpcloud.SaaSApp) string { return a.Name }),
	col("Service", "Category", func(a jumpcloud.SaaSApp) string { return a.Category }),
	{
		Group:       "Service",
		Header:      "Status",
		Extract:     func(a jumpcloud.SaaSApp) string { return titleizeEnum(a.Status) },
		AlertRed:    func(v string) bool { return v == "Unapproved" },
		AlertYellow: func(v string) bool { return v == "Newly Discovered" },
	},
	{Group: "Service", Header: "Domains", Extract: func(a jumpcloud.SaaSApp) string { return fmtList(a.Domains) }, Wrap: true},
	col("Service", "Discovery", func(a jumpcloud.SaaSApp) string { return fmtList(a.DiscoverySources) }),

	// ── Access ──────────────────────────────────────────────────────────────────
	col("Access", "Restriction", func(a jumpcloud.SaaSApp) string { return titleizeEnum(a.AccessRestriction) }),
	{
		Group:       "Access",
		Header:      "SSO",
		Extract:     saasSSO,
		AlertYellow: func(v string) bool { return v == "Not connected" },
	},
	col("Access", "Owner", func(a jumpcloud.SaaSApp) string { return a.OwnerEmail }),

	// ── Accounts ────────────────────────────────────────────────────────────────
	col("Accounts", "Count", func(a jumpcloud.SaaSApp) string { return strconv.Itoa(a.AccountCount()) }),
	{Group: "Accounts", Header: "Owner Accounts", Extract: saasAccountsDetail, Wrap: true},
	col("Accounts", "Last Used", func(a jumpcloud.SaaSApp) string { return fmtDtRelative(a.LatestUsedAt()) }),

	// ── Licenses ────────────────────────────────────────────────────────────────
	{
		Group:       "Licenses",
		Header:      "Seats (Assigned/Total)",
		Extract:     saasSeats,
		AlertYellow: func(v string) bool { return saasHasUnassignedSeats(v) },
	},
	col("Licenses", "Cost/yr", saasCost),
	col("Licenses", "Renewal", saasRenewal),
	col("Licenses", "Term", saasTerm),
}

// saasHasUnassignedSeats reports whether an "assigned / total" cell shows idle
// (unassigned) seats — assigned strictly below a finite total.
func saasHasUnassignedSeats(v string) bool {
	a, b, ok := strings.Cut(v, " / ")
	if !ok || b == "∞" {
		return false
	}
	assigned, err1 := strconv.Atoi(strings.TrimSpace(a))
	total, err2 := strconv.Atoi(strings.TrimSpace(b))
	if err1 != nil || err2 != nil {
		return false
	}
	return total > assigned
}

// WriteSaaS writes the JumpCloud SaaS App Management tab — one row per
// discovered application, grouped by derived category then name.
func WriteSaaS(ctx context.Context, s *Service, tab string, inv *inventory.AssetInventory) error {
	rows := make([]jumpcloud.SaaSApp, len(inv.SaaSApps))
	copy(rows, inv.SaaSApps)
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Category != rows[j].Category {
			return rows[i].Category < rows[j].Category
		}
		return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
	})
	return writeTab(ctx, s, tab, saasColumns, rows, WriteOptions{})
}
