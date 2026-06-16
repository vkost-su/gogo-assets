// Package classify is phase 1 of the drift engine: it assigns each entity to a
// baseline class (ТЗ §6.1, §6.3).
//
// An entity belongs to every class whose Match conditions all hold. When more
// than one class matches, the winner is resolved deterministically:
//
//  1. Specificity — the class with more Match conditions wins (CSS-like).
//  2. Priority — on a specificity tie, the higher integer wins.
//  3. Strictest — on a remaining tie, expectations are merged taking the
//     highest-severity expectation per field, and the entity is flagged.
//
// Run emits UNCLASSIFIED (no class matched) and CLASS_CONFLICT (more than one
// class matched — advisory, even when cleanly resolved). The drift comparison
// itself is phase 2 (package drift), which consumes the Results returned here.
package classify

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"gogo-assets/internal/baseline"
	"gogo-assets/internal/drifttag"
	"gogo-assets/internal/model"
)

// Result is the classification of one entity. Val carries the concrete entity
// struct so phase 2 can read its monitored fields by reflection without a
// second pass over the snapshot.
type Result struct {
	Entity      model.Entity
	Service     model.Service
	Val         any
	EvidenceRef string

	ClassID  string                          // "" when unclassified
	Matched  []string                        // every class that matched, sorted
	Expected map[string]baseline.Expectation // effective expectations for phase 2
}

// Run classifies every classified entity in snap against b and returns the
// per-entity results plus the UNCLASSIFIED / CLASS_CONFLICT findings.
func Run(snap model.Snapshot, b *baseline.Baseline, now time.Time) ([]Result, []model.Finding) {
	monitored := baseline.MonitoredFields()
	refs := entities(snap)

	results := make([]Result, 0, len(refs))
	var findings []model.Finding
	for _, r := range refs {
		matched := matchingClasses(r.Val, b.Classes)
		r.Matched = classIDs(matched)

		switch len(matched) {
		case 0:
			findings = append(findings, finding(model.KindUnclassified, model.SevUnclassified, r, now,
				"no baseline class matched this entity — extend class coverage"))
		case 1:
			r.ClassID = matched[0].ID
			r.Expected = matched[0].Expected
		default:
			winner, expected := resolve(matched, monitored)
			r.ClassID = winner
			r.Expected = expected
			findings = append(findings, finding(model.KindClassConflict, model.SevClassConflict, r, now,
				fmt.Sprintf("matched multiple classes (%s); resolved to %s — clean up the class matrix",
					strings.Join(r.Matched, ", "), winner)))
		}
		results = append(results, r)
	}
	return results, findings
}

// matchingClasses returns every class whose Match conditions all hold for val.
func matchingClasses(val any, classes []baseline.Class) []baseline.Class {
	var out []baseline.Class
	for _, c := range classes {
		if matches(val, c.Match) {
			out = append(out, c)
		}
	}
	return out
}

// matches reports whether every condition in m holds for val (AND). A missing
// or non-matching field fails the class.
func matches(val any, m map[string]string) bool {
	for field, want := range m {
		got, present := drifttag.Value(val, field)
		if !present || got != want {
			return false
		}
	}
	return true
}

// resolve picks the winning class among >1 matches and returns its id plus the
// effective expectations. Ordering: specificity desc, priority desc, id asc.
// When the top tier is still tied, expectations are merged by strictest severity.
func resolve(matched []baseline.Class, monitored map[string]model.Severity) (string, map[string]baseline.Expectation) {
	sorted := make([]baseline.Class, len(matched))
	copy(sorted, matched)
	sort.Slice(sorted, func(i, j int) bool {
		if a, b := len(sorted[i].Match), len(sorted[j].Match); a != b {
			return a > b
		}
		if sorted[i].Priority != sorted[j].Priority {
			return sorted[i].Priority > sorted[j].Priority
		}
		return sorted[i].ID < sorted[j].ID
	})

	top := sorted[0]
	tied := []baseline.Class{top}
	for _, c := range sorted[1:] {
		if len(c.Match) == len(top.Match) && c.Priority == top.Priority {
			tied = append(tied, c)
		}
	}
	if len(tied) == 1 {
		return top.ID, top.Expected
	}
	return top.ID, strictest(tied, monitored)
}

// strictest merges the expectations of tied classes, keeping the
// highest-severity expectation for each field.
func strictest(classes []baseline.Class, monitored map[string]model.Severity) map[string]baseline.Expectation {
	out := make(map[string]baseline.Expectation)
	for _, c := range classes {
		for field, exp := range c.Expected {
			cur, ok := out[field]
			if !ok || severityOf(exp, field, monitored).Rank() > severityOf(cur, field, monitored).Rank() {
				out[field] = exp
			}
		}
	}
	return out
}

// severityOf returns an expectation's effective severity: its override when set,
// otherwise the field's drift-tag severity.
func severityOf(exp baseline.Expectation, field string, monitored map[string]model.Severity) model.Severity {
	if exp.Severity != "" {
		return exp.Severity
	}
	return monitored[field]
}

func classIDs(classes []baseline.Class) []string {
	if len(classes) == 0 {
		return nil
	}
	ids := make([]string, len(classes))
	for i, c := range classes {
		ids[i] = c.ID
	}
	sort.Strings(ids)
	return ids
}

// finding builds a coverage finding (UNCLASSIFIED / CLASS_CONFLICT) for r.
func finding(kind model.FindingKind, sev model.Severity, r Result, now time.Time, summary string) model.Finding {
	return model.Finding{
		Kind:        kind,
		Severity:    sev,
		Service:     []model.Service{r.Service},
		Entity:      r.Entity,
		ClassID:     r.ClassID,
		Summary:     summary,
		DetectedAt:  now,
		FirstSeen:   now,
		EvidenceRef: r.EvidenceRef,
	}
}
