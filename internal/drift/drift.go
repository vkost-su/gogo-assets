// Package drift is phase 2 of the engine: it compares each classified entity's
// monitored fields against its class expectations and diffs the entity set
// against the baseline census (ТЗ §6.2, §6.4).
//
// Comparison is fully reflective via package drifttag — there is no per-field
// special case. For each expectation on a resolved class:
//
//	field not collected (nil)  → DATA_GAP   (data quality, not drift)
//	field value ≠ expectation  → BASELINE_DRIFT
//
// Census diff (only when the baseline carries one):
//
//	in census, gone from snapshot → ENTITY_DISAPPEARED
//	in snapshot, not in census    → NEW_ENTITY
package drift

import (
	"fmt"
	"sort"
	"time"

	"gogo-assets/internal/baseline"
	"gogo-assets/internal/classify"
	"gogo-assets/internal/drifttag"
	"gogo-assets/internal/model"
)

// Run produces the phase-2 findings for the classified results. The
// UNCLASSIFIED / CLASS_CONFLICT coverage findings come from package classify;
// Run emits BASELINE_DRIFT, DATA_GAP, NEW_ENTITY, and ENTITY_DISAPPEARED.
//
// now stamps DetectedAt/FirstSeen; package digest later reconciles FirstSeen
// against the previous run. Findings are returned in entity order; the digest
// applies the final severity ordering.
func Run(results []classify.Result, b *baseline.Baseline, now time.Time) []model.Finding {
	monitored := baseline.MonitoredFields()

	var findings []model.Finding
	for _, r := range results {
		if r.ClassID == "" {
			continue // unclassified: no expectations to check
		}
		for _, field := range sortedKeys(r.Expected) {
			exp := r.Expected[field]
			got, present := drifttag.Value(r.Val, field)
			if !present {
				findings = append(findings, dataGap(r, field, now))
				continue
			}
			if got != exp.Value {
				findings = append(findings, baselineDrift(r, field, exp, got, monitored, now))
			}
		}
	}
	findings = append(findings, census(results, b.Census, now)...)
	return findings
}

func baselineDrift(r classify.Result, field string, exp baseline.Expectation, got string,
	monitored map[string]model.Severity, now time.Time) model.Finding {
	sev := exp.Severity
	if sev == "" {
		sev = monitored[field]
	}
	f := newFinding(model.KindBaselineDrift, sev, r, now)
	f.Field = field
	f.Was = exp.Value
	f.Now = got
	f.Summary = fmt.Sprintf("%s expected %q but found %q (class %s)", field, exp.Value, got, r.ClassID)
	return f
}

func dataGap(r classify.Result, field string, now time.Time) model.Finding {
	f := newFinding(model.KindDataGap, model.SevDataGap, r, now)
	f.Field = field
	f.Summary = fmt.Sprintf("%s was not collected (nil) — data-collection gap, not drift", field)
	return f
}

// census diffs the current classified entity set against the baseline census,
// per entity type. A type with no census entries is skipped (no false NEW/GONE).
func census(results []classify.Result, c baseline.Census, now time.Time) []model.Finding {
	curDevices := make(map[string]classify.Result)
	curUsers := make(map[string]classify.Result)
	for _, r := range results {
		switch r.Entity.Type {
		case model.EntityDevice:
			curDevices[r.Entity.ID] = r
		case model.EntityUser:
			curUsers[r.Entity.ID] = r
		}
	}

	var out []model.Finding
	if len(c.Devices) > 0 {
		out = append(out, censusDiff(c.Devices, curDevices, model.EntityDevice, now)...)
	}
	if len(c.Users) > 0 {
		out = append(out, censusDiff(c.Users, curUsers, model.EntityUser, now)...)
	}
	return out
}

func censusDiff(censusIDs []string, current map[string]classify.Result, etype string, now time.Time) []model.Finding {
	inCensus := make(map[string]struct{}, len(censusIDs))
	for _, id := range censusIDs {
		inCensus[id] = struct{}{}
	}

	var out []model.Finding

	// Disappeared: in the census, absent now. Only the id is known.
	sorted := append([]string(nil), censusIDs...)
	sort.Strings(sorted)
	for _, id := range sorted {
		if _, ok := current[id]; ok {
			continue
		}
		f := model.Finding{
			Kind:       model.KindEntityDisappeared,
			Severity:   model.SevEntityDisappeared,
			Entity:     model.Entity{Type: etype, ID: id},
			Summary:    "in the baseline census but absent from this snapshot — offboarding / dead agent / theft?",
			DetectedAt: now,
			FirstSeen:  now,
		}
		out = append(out, f)
	}

	// New: present now, absent from the census.
	curIDs := make([]string, 0, len(current))
	for id := range current {
		curIDs = append(curIDs, id)
	}
	sort.Strings(curIDs)
	for _, id := range curIDs {
		if _, ok := inCensus[id]; ok {
			continue
		}
		r := current[id]
		f := newFinding(model.KindNewEntity, model.SevNewEntity, r, now)
		f.Summary = "absent from the baseline census — classify and onboard"
		out = append(out, f)
	}
	return out
}

// newFinding seeds a finding from a classified result (service, entity,
// class, evidence) for the comparison kinds.
func newFinding(kind model.FindingKind, sev model.Severity, r classify.Result, now time.Time) model.Finding {
	return model.Finding{
		Kind:        kind,
		Severity:    sev,
		Service:     []model.Service{r.Service},
		Entity:      r.Entity,
		ClassID:     r.ClassID,
		DetectedAt:  now,
		FirstSeen:   now,
		EvidenceRef: r.EvidenceRef,
	}
}

func sortedKeys(m map[string]baseline.Expectation) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
