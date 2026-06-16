package digest

import (
	"testing"
	"time"

	"gogo-assets/internal/model"
)

var (
	_now  = time.Date(2026, 6, 11, 4, 0, 0, 0, time.UTC)
	_past = time.Date(2026, 6, 1, 4, 0, 0, 0, time.UTC)
)

func sampleSnapshot() model.Snapshot {
	return model.Snapshot{
		RunDate:      "2026-06-11",
		RunTimestamp: _now,
		JumpCloud:    model.JumpCloudShard{Devices: make([]model.JCDevice, 10)},
		GoogleWorkspace: model.GWSShard{
			Identity: make([]model.GWSUser, 5),
		},
	}
}

func mkFindings(n int, sev model.Severity) []model.Finding {
	out := make([]model.Finding, n)
	for i := range out {
		out[i] = model.Finding{
			Kind:       model.KindBaselineDrift,
			Severity:   sev,
			Entity:     model.Entity{Type: model.EntityDevice, ID: string(rune('a' + i%26))},
			Field:      "disk_encrypted",
			Summary:    "padding finding to grow the digest beyond its byte budget for the truncation test",
			DetectedAt: _now,
			FirstSeen:  _now,
		}
	}
	return out
}

func TestBuildWithinBudget(t *testing.T) {
	findings := []model.Finding{
		{Kind: model.KindBaselineDrift, Severity: model.SevCrit, Entity: model.Entity{ID: "s1"}, Field: "disk_encrypted", Was: "true", Now: "false", DetectedAt: _now, FirstSeen: _now},
		{Kind: model.KindUnclassified, Severity: model.SevMed, Entity: model.Entity{ID: "u1"}, DetectedAt: _now, FirstSeen: _now},
	}
	d, buf, err := Build(sampleSnapshot(), "2026-06-01.1", findings, nil, _now, 51200)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if d.Truncated {
		t.Errorf("small digest should not be truncated")
	}
	if len(buf) > 51200 {
		t.Errorf("digest %d bytes exceeds budget", len(buf))
	}
	if d.Counts.DevicesTotal != 10 || d.Counts.UsersTotal != 5 {
		t.Errorf("counts = devices %d users %d, want 10/5", d.Counts.DevicesTotal, d.Counts.UsersTotal)
	}
	if d.Counts.Unclassified != 1 {
		t.Errorf("unclassified = %d, want 1", d.Counts.Unclassified)
	}
	if d.Counts.FindingsBySeverity["CRIT"] != 1 {
		t.Errorf("CRIT count = %d, want 1", d.Counts.FindingsBySeverity["CRIT"])
	}
}

func TestBuildTruncates(t *testing.T) {
	findings := mkFindings(500, model.SevLow)
	const budget = 4096
	d, buf, err := Build(sampleSnapshot(), "v", findings, nil, _now, budget)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !d.Truncated {
		t.Errorf("expected Truncated=true when 500 findings exceed %d bytes", budget)
	}
	if len(buf) > budget {
		t.Errorf("digest %d bytes still exceeds budget %d after truncation", len(buf), budget)
	}
	if len(d.Findings) >= 500 {
		t.Errorf("findings not truncated: %d", len(d.Findings))
	}
	// Counts must still reflect ALL findings, not the truncated list.
	total := 0
	for _, n := range d.Counts.FindingsByKind {
		total += n
	}
	if total != 500 {
		t.Errorf("findings_by_kind sums to %d, want 500 (counts must be complete)", total)
	}
}

func TestBuildSortsBySeverity(t *testing.T) {
	findings := []model.Finding{
		{Kind: model.KindNewEntity, Severity: model.SevLow, Entity: model.Entity{ID: "a"}},
		{Kind: model.KindBaselineDrift, Severity: model.SevCrit, Entity: model.Entity{ID: "b"}},
		{Kind: model.KindBaselineDrift, Severity: model.SevHigh, Entity: model.Entity{ID: "c"}},
	}
	d, _, err := Build(sampleSnapshot(), "v", findings, nil, _now, 51200)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if d.Findings[0].Severity != model.SevCrit || d.Findings[2].Severity != model.SevLow {
		t.Errorf("findings not severity-sorted: %v", []model.Severity{
			d.Findings[0].Severity, d.Findings[1].Severity, d.Findings[2].Severity})
	}
}

func TestReconcileFirstSeen(t *testing.T) {
	prev := &Digest{Findings: []model.Finding{
		{Kind: model.KindBaselineDrift, Entity: model.Entity{ID: "s1"}, Field: "disk_encrypted", FirstSeen: _past},
	}}
	findings := []model.Finding{
		{Kind: model.KindBaselineDrift, Severity: model.SevCrit, Entity: model.Entity{ID: "s1"}, Field: "disk_encrypted", FirstSeen: _now}, // chronic
		{Kind: model.KindBaselineDrift, Severity: model.SevCrit, Entity: model.Entity{ID: "s2"}, Field: "mfa_enabled", FirstSeen: _now},    // new
	}
	d, _, err := Build(sampleSnapshot(), "v", findings, prev, _now, 51200)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	byID := map[string]model.Finding{}
	for _, f := range d.Findings {
		byID[f.Entity.ID] = f
	}
	if !byID["s1"].FirstSeen.Equal(_past) {
		t.Errorf("chronic finding first_seen = %v, want carried-forward %v", byID["s1"].FirstSeen, _past)
	}
	if !byID["s2"].FirstSeen.Equal(_now) {
		t.Errorf("new finding first_seen = %v, want %v", byID["s2"].FirstSeen, _now)
	}
}
