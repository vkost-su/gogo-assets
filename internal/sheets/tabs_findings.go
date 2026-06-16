package sheets

import (
	"context"
	"sort"
	"strings"

	"gogo-assets/internal/model"
)

var findingsColumns = []Column[model.Finding]{
	// ── Finding ─────────────────────────────────────────────────────────────
	{
		Group:       "Finding",
		Header:      "Severity",
		Extract:     func(f model.Finding) string { return string(f.Severity) },
		AlertRed:    func(v string) bool { return v == string(model.SevCrit) || v == string(model.SevHigh) },
		AlertYellow: func(v string) bool { return v == string(model.SevMed) },
	},
	col("Finding", "Kind", func(f model.Finding) string { return string(f.Kind) }),
	col("Finding", "Service", func(f model.Finding) string { return joinServices(f.Service) }),
	col("Finding", "Class", func(f model.Finding) string { return f.ClassID }),

	// ── Entity ──────────────────────────────────────────────────────────────
	col("Entity", "Type", func(f model.Finding) string { return f.Entity.Type }),
	col("Entity", "ID", func(f model.Finding) string { return f.Entity.ID }),
	col("Entity", "Hostname", func(f model.Finding) string { return f.Entity.Hostname }),
	col("Entity", "Owner", func(f model.Finding) string { return f.Entity.OwnerEmail }),

	// ── Detail ──────────────────────────────────────────────────────────────
	col("Detail", "Field", func(f model.Finding) string { return f.Field }),
	col("Detail", "Expected", func(f model.Finding) string { return f.Was }),
	col("Detail", "Actual", func(f model.Finding) string { return f.Now }),
	{
		Group:   "Detail",
		Header:  "Summary",
		Extract: func(f model.Finding) string { return f.Summary },
		Wrap:    true,
	},

	// ── Tracking ────────────────────────────────────────────────────────────
	col("Tracking", "First Seen", func(f model.Finding) string { return fmtDt(f.FirstSeen) }),
	col("Tracking", "Detected", func(f model.Finding) string { return fmtDt(f.DetectedAt) }),
	{
		Group:   "Tracking",
		Header:  "Evidence",
		Extract: func(f model.Finding) string { return f.EvidenceRef },
		Wrap:    true,
	},
}

// WriteFindings writes the drift-engine findings tab, ordered by severity
// (CRIT first) so the most urgent rows sit at the top. An empty findings slice
// still (re)writes the tab — an empty Findings tab is a meaningful "all clear"
// after a run, not a skipped one.
func WriteFindings(ctx context.Context, s *Service, tab string, findings []model.Finding) error {
	rows := make([]model.Finding, len(findings))
	copy(rows, findings)
	sort.SliceStable(rows, func(i, j int) bool {
		if ri, rj := rows[i].Severity.Rank(), rows[j].Severity.Rank(); ri != rj {
			return ri > rj
		}
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind < rows[j].Kind
		}
		if rows[i].Entity.ID != rows[j].Entity.ID {
			return rows[i].Entity.ID < rows[j].Entity.ID
		}
		return rows[i].Field < rows[j].Field
	})
	return writeTab(ctx, s, tab, findingsColumns, rows, WriteOptions{})
}

func joinServices(svcs []model.Service) string {
	parts := make([]string, len(svcs))
	for i, s := range svcs {
		parts[i] = string(s)
	}
	return strings.Join(parts, ", ")
}
