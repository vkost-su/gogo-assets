package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gogo-assets/internal/logging"
	"gogo-assets/internal/model"
	"gogo-assets/internal/snapshot"
)

// exportShape reads back just the wrapper fields the assertions care about.
type exportShape struct {
	Service string           `json:"service"`
	View    string           `json:"view"`
	Count   int              `json:"count"`
	Records []map[string]any `json:"records"`
}

func readExport(t *testing.T, path string) exportShape {
	t.Helper()
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var e exportShape
	if err := json.Unmarshal(buf, &e); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return e
}

// TestWriteServiceOutputs is the Phase-3 end-to-end for the per-service files:
// one snapshot + findings fills the dated run folder; the drift files exclude
// clean records; Sophos (no records) is skipped; and output is byte-stable.
func TestWriteServiceOutputs(t *testing.T) {
	root := t.TempDir()
	store := snapshot.NewStore(root)
	snap := model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		RunDate:       "2026-05-05",
		RunTimestamp:  time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC),
		JumpCloud: model.JumpCloudShard{
			Devices: []model.JCDevice{ // sorted by SystemID; d-b is the drifting one
				{SystemID: "d-a", Hostname: "a"},
				{SystemID: "d-b", Hostname: "b", DiskEncrypted: boolp(false)},
				{SystemID: "d-c", Hostname: "c"},
			},
			Software: []model.JCPersonSoftware{
				{OwnerEmail: "x@x.com", Apps: []model.JCSoftwareItem{{Name: "Slack"}}, AppCount: 1},
				{OwnerEmail: "z@x.com", SaaS: []model.JCSaaSMembership{{AppID: "a", Name: "Figma"}}, SaaSCount: 1}, // SaaS-only → not in software drift
			},
		},
		GoogleWorkspace: model.GWSShard{
			Identity: []model.GWSUser{{Email: "x@x.com"}, {Email: "y@x.com"}},
		},
		// Sophos intentionally empty → skip-empty (no sp.json / sp-drift.json).
	}
	findings := []model.Finding{
		{Kind: model.KindBaselineDrift, Service: []model.Service{model.ServiceJumpCloud},
			Entity: model.Entity{Type: model.EntityDevice, ID: "d-b"}},
	}

	writeServiceOutputs(logging.For("test"), store, snap, findings)

	folder := filepath.Join(root, "daily", snapshot.RunFolder(snap.RunDate))
	p := func(name string) string { return filepath.Join(folder, name) }

	jcFull := readExport(t, p("jc.json"))
	if jcFull.View != "full" || jcFull.Count != 3 {
		t.Errorf("jc.json = view %q count %d, want full/3", jcFull.View, jcFull.Count)
	}
	jcDrift := readExport(t, p("jc-drift.json"))
	if jcDrift.View != "drift" || jcDrift.Count != 1 {
		t.Fatalf("jc-drift.json = view %q count %d, want drift/1", jcDrift.View, jcDrift.Count)
	}
	if jcDrift.Records[0]["system_id"] != "d-b" {
		t.Errorf("jc-drift record = %v, want d-b", jcDrift.Records[0]["system_id"])
	}

	// GWS collected but clean ⇒ full has 2, drift file exists with count 0.
	if gw := readExport(t, p("gw.json")); gw.Count != 2 {
		t.Errorf("gw.json count = %d, want 2", gw.Count)
	}
	if gwd := readExport(t, p("gw-drift.json")); gwd.Count != 0 {
		t.Errorf("gw-drift.json count = %d, want 0", gwd.Count)
	}

	// jc-saas.json (full, per-person software) has both people; software is
	// pre-filtered at collect time, so the drift file is an explicit empty view.
	if saas := readExport(t, p("jc-saas.json")); saas.Count != 2 {
		t.Errorf("jc-saas.json count = %d, want 2", saas.Count)
	}
	if sd := readExport(t, p("jc-saas-drift.json")); sd.Count != 0 {
		t.Errorf("jc-saas-drift.json count = %d, want 0 (pre-filtered software)", sd.Count)
	}

	// Skip-empty: Sophos had no records ⇒ no files written.
	for _, name := range []string{"sp.json", "sp-drift.json"} {
		if _, err := os.Stat(p(name)); !os.IsNotExist(err) {
			t.Errorf("%s should be skipped for an empty service (err=%v)", name, err)
		}
	}

	// Determinism: re-running the same input reproduces byte-identical files.
	before, _ := os.ReadFile(p("jc-drift.json"))
	writeServiceOutputs(logging.For("test"), store, snap, findings)
	after, _ := os.ReadFile(p("jc-drift.json"))
	if string(before) != string(after) {
		t.Errorf("jc-drift.json not byte-stable across identical runs")
	}
}
