package drift

import (
	"testing"
	"time"

	"gogo-assets/internal/baseline"
	"gogo-assets/internal/classify"
	"gogo-assets/internal/model"
)

var _now = time.Date(2026, 6, 11, 4, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

// fkey is the comparable identity of a finding for golden assertions.
type fkey struct {
	kind     model.FindingKind
	id       string
	field    string
	severity model.Severity
	was, now string
}

func keyset(findings []model.Finding) map[fkey]int {
	out := make(map[fkey]int)
	for _, f := range findings {
		out[fkey{f.Kind, f.Entity.ID, f.Field, f.Severity, f.Was, f.Now}]++
	}
	return out
}

func TestRunGolden(t *testing.T) {
	snap := model.Snapshot{
		JumpCloud: model.JumpCloudShard{
			Devices: []model.JCDevice{
				{SystemID: "s1", OSFamily: "darwin", DiskEncrypted: ptr(false)}, // drift
				{SystemID: "s2", OSFamily: "darwin", DiskEncrypted: nil},        // data gap
				{SystemID: "s3", OSFamily: "darwin", DiskEncrypted: ptr(true)},  // clean, but new
			},
		},
		GoogleWorkspace: model.GWSShard{
			Identity: []model.GWSUser{
				{Email: "admin@x.com", IsAdmin: ptr(true), MFAEnabled: ptr(false)}, // drift
			},
		},
	}
	b := &baseline.Baseline{
		Classes: []baseline.Class{
			{ID: "macos", Match: map[string]string{"os_family": "darwin"},
				Expected: map[string]baseline.Expectation{"disk_encrypted": {Value: "true"}}},
			{ID: "admins", Match: map[string]string{"is_admin": "true"},
				Expected: map[string]baseline.Expectation{"mfa_enabled": {Value: "true"}}},
		},
		Census: baseline.Census{
			Devices: []string{"s1", "s2", "ghost"}, // ghost disappeared; s3 is new
			Users:   []string{"admin@x.com"},
		},
	}

	results, classifyFindings := classify.Run(snap, b, _now)
	if len(classifyFindings) != 0 {
		t.Fatalf("expected no classify findings (every entity matches one class), got %d: %+v",
			len(classifyFindings), classifyFindings)
	}

	got := keyset(Run(results, b, _now))
	want := keyset([]model.Finding{
		{Kind: model.KindBaselineDrift, Severity: model.SevCrit, Entity: model.Entity{ID: "s1"}, Field: "disk_encrypted", Was: "true", Now: "false"},
		{Kind: model.KindDataGap, Severity: model.SevMed, Entity: model.Entity{ID: "s2"}, Field: "disk_encrypted"},
		{Kind: model.KindBaselineDrift, Severity: model.SevCrit, Entity: model.Entity{ID: "admin@x.com"}, Field: "mfa_enabled", Was: "true", Now: "false"},
		{Kind: model.KindEntityDisappeared, Severity: model.SevHigh, Entity: model.Entity{ID: "ghost"}},
		{Kind: model.KindNewEntity, Severity: model.SevMed, Entity: model.Entity{ID: "s3"}},
	})

	if len(got) != len(want) {
		t.Fatalf("got %d distinct findings, want %d\ngot:  %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("finding %+v: got %d, want %d", k, got[k], n)
		}
	}
}

// A populated field that equals its expectation must produce no finding, and a
// *false that differs is a drift, not a gap — the pointer rule in action.
func TestNoFindingWhenCompliant(t *testing.T) {
	snap := model.Snapshot{
		JumpCloud: model.JumpCloudShard{
			Devices: []model.JCDevice{{SystemID: "ok", OSFamily: "darwin", DiskEncrypted: ptr(true)}},
		},
	}
	b := &baseline.Baseline{Classes: []baseline.Class{
		{ID: "macos", Match: map[string]string{"os_family": "darwin"},
			Expected: map[string]baseline.Expectation{"disk_encrypted": {Value: "true"}}},
	}}
	results, _ := classify.Run(snap, b, _now)
	if f := Run(results, b, _now); len(f) != 0 {
		t.Errorf("compliant entity produced findings: %+v", f)
	}
}
