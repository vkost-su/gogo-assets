package sheets

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"gogo-assets/internal/inventory"
	"gogo-assets/internal/sophos"
)

var sophosColumns = []Column[sophos.Endpoint]{
	// ── Endpoint ──────────────────────────────────────────────────────────────
	col("Endpoint", "Hostname", func(e sophos.Endpoint) string { return e.Hostname }),
	col("Endpoint", "OS Platform", func(e sophos.Endpoint) string { return e.OSPlatform }),
	col("Endpoint", "OS Version", func(e sophos.Endpoint) string { return e.OSVersion }),
	col("Endpoint", "Owner Email", func(e sophos.Endpoint) string { return e.OwnerEmail }),
	col("Endpoint", "Owner Login", func(e sophos.Endpoint) string { return e.OwnerLogin }),
	col("Endpoint", "Last Seen", func(e sophos.Endpoint) string { return fmtDtRelative(e.LastSeenAt) }),

	// ── Status ────────────────────────────────────────────────────────────────
	col("Status", "Online", func(e sophos.Endpoint) string { return BoolValue(e.Online) }),
	{
		Group:    "Status",
		Header:   "Health Overall",
		Extract:  func(e sophos.Endpoint) string { return e.HealthOverall },
		AlertRed: func(v string) bool { return v == "bad" || v == "suspicious" },
	},
	{
		Group:    "Status",
		Header:   "Threat Status",
		Extract:  func(e sophos.Endpoint) string { return e.HealthThreats },
		AlertRed: func(v string) bool { return v == "bad" || v == "suspicious" },
	},
	col("Status", "Services Status", func(e sophos.Endpoint) string { return e.HealthServices }),
	{
		Group:    "Status",
		Header:   "Tamper Protection",
		Extract:  func(e sophos.Endpoint) string { return BoolValue(e.TamperProtected) },
		AlertRed: func(v string) bool { return v == "No" },
	},
	{
		Group:    "Status",
		Header:   "Alerts (open)",
		Extract:  func(e sophos.Endpoint) string { return fmt.Sprintf("%d", e.AlertCount) },
		AlertRed: isPositiveInt,
	},
	{
		Group:  "Status",
		Header: "Detections (30d)",
		Extract: func(e sophos.Endpoint) string {
			if e.FetchError {
				return "?"
			}
			return fmt.Sprintf("%d", e.DetectionCount30d)
		},
		AlertYellow: isPositiveInt,
	},
	col("Status", "Assigned Products", func(e sophos.Endpoint) string {
		return fmtList(e.AssignedProducts)
	}),

	// ── Policies ──────────────────────────────────────────────────────────────
	col("Policies", "Applied Policies", func(e sophos.Endpoint) string {
		return fmtList(e.Policies)
	}),
}

// WriteSophos writes the per-endpoint Sophos full tab and, when driftTab is set,
// its (Drift) companion — the same columns, only endpoints the drift engine
// flagged (drifted holds their endpoint IDs). Skip-empty applies to both.
func WriteSophos(ctx context.Context, s *Service, tab, driftTab string, inv *inventory.AssetInventory, drifted map[string]struct{}) error {
	rows := make([]sophos.Endpoint, len(inv.SophosEndpoints))
	copy(rows, inv.SophosEndpoints)
	sort.Slice(rows, func(i, j int) bool {
		return strings.ToLower(rows[i].Hostname) < strings.ToLower(rows[j].Hostname)
	})
	return writeFullAndDrift(ctx, s, tab, driftTab, sophosColumns, rows,
		func(e sophos.Endpoint) string { return e.EndpointID },
		drifted, WriteOptions{})
}
