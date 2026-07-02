package sheets

import (
	"context"
	"strconv"
	"strings"

	"gogo-assets/internal/model"
)

func fmtSoftwareItems(items []model.JCSoftwareItem) string {
	parts := make([]string, 0, len(items))
	for _, it := range items {
		label := it.Name
		if it.Version != "" {
			label += " (" + it.Version + ")"
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, "; ")
}

func fmtMemberships(ms []model.JCSaaSMembership) string {
	parts := make([]string, 0, len(ms))
	for _, m := range ms {
		label := m.Name
		if m.Status != "" {
			label += " (" + m.Status + ")"
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, "; ")
}

// jcSoftwareColumns render one row per person: their SaaS-app memberships and the
// apps/extensions found across the devices they own (email→device join). The
// same columns back both the full tab and its (Drift) companion (where only
// non-allowlisted apps/extensions remain).
var jcSoftwareColumns = []Column[model.JCPersonSoftware]{
	col("Person", "Owner Email", func(p model.JCPersonSoftware) string { return p.OwnerEmail }),
	{
		Group:   "Person",
		Header:  "Devices",
		Extract: func(p model.JCPersonSoftware) string { return strings.Join(p.Devices, "; ") },
		Wrap:    true,
	},

	col("SaaS", "SaaS Count", func(p model.JCPersonSoftware) string { return strconv.Itoa(p.SaaSCount) }),
	{
		Group:   "SaaS",
		Header:  "SaaS Apps",
		Extract: func(p model.JCPersonSoftware) string { return fmtMemberships(p.SaaS) },
		Wrap:    true,
	},

	col("Software", "App Count", func(p model.JCPersonSoftware) string { return strconv.Itoa(p.AppCount) }),
	{
		Group:   "Software",
		Header:  "Applications",
		Extract: func(p model.JCPersonSoftware) string { return fmtSoftwareItems(p.Apps) },
		Wrap:    true,
	},
	col("Software", "Extension Count", func(p model.JCPersonSoftware) string { return strconv.Itoa(p.ExtensionCount) }),
	{
		Group:   "Software",
		Header:  "Extensions",
		Extract: func(p model.JCPersonSoftware) string { return fmtSoftwareItems(p.Extensions) },
		Wrap:    true,
	},
}

// WriteJCSoftware writes the per-person "JumpCloud Software" full tab (records)
// and, when driftTab is set and any survive, its (Drift) companion
// (driftRecords, already allowlist-filtered by the caller via
// serviceview.SoftwareDrift). Both use the same columns; skip-empty applies.
func WriteJCSoftware(ctx context.Context, s *Service, tab, driftTab string,
	records, driftRecords []model.JCPersonSoftware) error {
	if err := writeTab(ctx, s, tab, jcSoftwareColumns, records, WriteOptions{}); err != nil {
		return err
	}
	if driftTab == "" || len(driftRecords) == 0 {
		return nil // skip-empty: no unresolved software ⇒ no companion tab
	}
	return writeTab(ctx, s, driftTab, jcSoftwareColumns, driftRecords, WriteOptions{})
}
