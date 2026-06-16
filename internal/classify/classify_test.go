package classify

import (
	"testing"
	"time"

	"gogo-assets/internal/baseline"
	"gogo-assets/internal/model"
)

var _testNow = time.Date(2026, 6, 11, 4, 0, 0, 0, time.UTC)

// oneDevice runs classify over a single macOS device owned by a@x.com against
// the given classes, returning that entity's result and the findings.
func oneDevice(t *testing.T, classes []baseline.Class) (Result, []model.Finding) {
	t.Helper()
	snap := model.Snapshot{
		JumpCloud: model.JumpCloudShard{
			Devices: []model.JCDevice{{SystemID: "s1", OSFamily: "darwin", OwnerEmail: "a@x.com"}},
		},
	}
	results, findings := Run(snap, &baseline.Baseline{Classes: classes}, _testNow)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	return results[0], findings
}

func hasKind(findings []model.Finding, kind model.FindingKind) bool {
	for _, f := range findings {
		if f.Kind == kind {
			return true
		}
	}
	return false
}

func TestResolveSpecificityWins(t *testing.T) {
	classes := []baseline.Class{
		{ID: "all-macos", Match: map[string]string{"os_family": "darwin"}},
		{ID: "macos-of-a", Match: map[string]string{"os_family": "darwin", "owner_email": "a@x.com"}},
	}
	r, findings := oneDevice(t, classes)
	if r.ClassID != "macos-of-a" {
		t.Errorf("ClassID = %q, want macos-of-a (more specific wins)", r.ClassID)
	}
	if !hasKind(findings, model.KindClassConflict) {
		t.Errorf("expected CLASS_CONFLICT (overlap is advisory even when resolved)")
	}
}

func TestResolvePriorityBreaksSpecificityTie(t *testing.T) {
	// Same specificity (1 condition each, both match) → higher priority wins.
	classes := []baseline.Class{
		{ID: "low", Priority: 1, Match: map[string]string{"os_family": "darwin"}},
		{ID: "high", Priority: 5, Match: map[string]string{"owner_email": "a@x.com"}},
	}
	r, _ := oneDevice(t, classes)
	if r.ClassID != "high" {
		t.Errorf("ClassID = %q, want high (priority breaks specificity tie)", r.ClassID)
	}
}

func TestResolveStrictestOnFullTie(t *testing.T) {
	// Same specificity AND priority → true tie. Winner is deterministic (id asc)
	// and expectations merge by strictest severity.
	classes := []baseline.Class{
		{
			ID: "b-class", Priority: 1,
			Match:    map[string]string{"os_family": "darwin"},
			Expected: map[string]baseline.Expectation{"disk_encrypted": {Value: "true"}}, // crit (field tag)
		},
		{
			ID: "a-class", Priority: 1,
			Match:    map[string]string{"owner_email": "a@x.com"},
			Expected: map[string]baseline.Expectation{"mfa_enabled": {Value: "true"}}, // crit
		},
	}
	r, findings := oneDevice(t, classes)
	if r.ClassID != "a-class" {
		t.Errorf("ClassID = %q, want a-class (id asc on full tie)", r.ClassID)
	}
	// Strictest merge unions both fields' expectations.
	if _, ok := r.Expected["disk_encrypted"]; !ok {
		t.Errorf("merged expectations missing disk_encrypted: %+v", r.Expected)
	}
	if _, ok := r.Expected["mfa_enabled"]; !ok {
		t.Errorf("merged expectations missing mfa_enabled: %+v", r.Expected)
	}
	if !hasKind(findings, model.KindClassConflict) {
		t.Errorf("expected CLASS_CONFLICT on full tie")
	}
}

func TestUnclassified(t *testing.T) {
	classes := []baseline.Class{
		{ID: "windows-only", Match: map[string]string{"os_family": "windows"}},
	}
	r, findings := oneDevice(t, classes)
	if r.ClassID != "" {
		t.Errorf("ClassID = %q, want empty (unclassified)", r.ClassID)
	}
	if !hasKind(findings, model.KindUnclassified) {
		t.Errorf("expected UNCLASSIFIED")
	}
}

func TestSingleMatchNoConflict(t *testing.T) {
	classes := []baseline.Class{
		{ID: "macos", Match: map[string]string{"os_family": "darwin"},
			Expected: map[string]baseline.Expectation{"disk_encrypted": {Value: "true"}}},
	}
	r, findings := oneDevice(t, classes)
	if r.ClassID != "macos" {
		t.Errorf("ClassID = %q, want macos", r.ClassID)
	}
	if hasKind(findings, model.KindClassConflict) {
		t.Errorf("single match must not emit CLASS_CONFLICT")
	}
	if got := r.Expected["disk_encrypted"].Value; got != "true" {
		t.Errorf("expectation passed through wrong: %q", got)
	}
}
