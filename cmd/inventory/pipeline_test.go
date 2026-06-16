package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gogo-assets/internal/baseline"
	"gogo-assets/internal/digest"
	"gogo-assets/internal/logging"
	"gogo-assets/internal/model"
	"gogo-assets/internal/snapshot"
)

func init() { logging.Configure("ERROR") } // keep test output quiet

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func boolp(b bool) *bool { return &b }

// driftSnapshot returns a snapshot with one non-compliant mac device, used by
// the drift end-to-end test.
func driftSnapshot() model.Snapshot {
	return model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		RunDate:       "2026-06-12",
		RunTimestamp:  time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC),
		JumpCloud: model.JumpCloudShard{
			Devices: []model.JCDevice{
				{SystemID: "mac-1", Hostname: "mac1", OSFamily: "darwin", DiskEncrypted: boolp(false)},
			},
		},
	}
}

// TestRunDrift_EndToEnd exercises the full orchestration offline: a baseline on
// disk, a snapshot with a drift + a census mismatch, and the three persisted
// outputs (snapshot / classification / digest).
func TestRunDrift_EndToEnd(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "baseline", "classes.json"),
		`{"classes":[{"id":"mac","priority":10,"match":{"os_family":"darwin"},"expected":{"disk_encrypted":"true"}}]}`)
	writeFile(t, filepath.Join(root, "baseline", "baseline.meta.json"),
		`{"version":"v1","census":{"devices":["ghost-device"],"users":[]}}`)

	store := snapshot.NewStore(root)
	snap := driftSnapshot()
	log := logging.For("test")
	now := snap.RunTimestamp

	if _, err := store.WriteSnapshot(snap); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	findings, err := runDrift(log, store, snap, now, 51200)
	if err != nil {
		t.Fatalf("runDrift: %v", err)
	}

	byKind := map[model.FindingKind]int{}
	for _, f := range findings {
		byKind[f.Kind]++
	}
	if byKind[model.KindBaselineDrift] != 1 {
		t.Errorf("BASELINE_DRIFT = %d, want 1", byKind[model.KindBaselineDrift])
	}
	if byKind[model.KindNewEntity] != 1 {
		t.Errorf("NEW_ENTITY = %d, want 1 (mac-1 not in census)", byKind[model.KindNewEntity])
	}
	if byKind[model.KindEntityDisappeared] != 1 {
		t.Errorf("ENTITY_DISAPPEARED = %d, want 1 (ghost-device)", byKind[model.KindEntityDisappeared])
	}

	// All three current/ artifacts must exist.
	for _, name := range []string{"snapshot.json", "classification.json", "digest.json"} {
		if _, err := os.Stat(filepath.Join(root, "current", name)); err != nil {
			t.Errorf("current/%s missing: %v", name, err)
		}
	}
	// The archived digest is keyed by run_date.
	if _, err := os.Stat(filepath.Join(root, "archive", "2026-06-12_digest.json")); err != nil {
		t.Errorf("archived digest missing: %v", err)
	}

	// The persisted digest must round-trip and carry the findings.
	var d digest.Digest
	if err := store.ReadJSON(&d, "current", "digest.json"); err != nil {
		t.Fatalf("read digest: %v", err)
	}
	if d.SchemaVersion != model.SchemaVersion || len(d.Findings) != 3 {
		t.Errorf("digest schema=%q findings=%d, want %q/3", d.SchemaVersion, len(d.Findings), model.SchemaVersion)
	}
	if d.Counts.DevicesTotal != 1 {
		t.Errorf("digest devices_total = %d, want 1", d.Counts.DevicesTotal)
	}
}

// TestRunDrift_NoBaselineIsGraceful confirms a missing classes.json skips the
// engine without error and writes no digest.
func TestRunDrift_NoBaselineIsGraceful(t *testing.T) {
	root := t.TempDir()
	store := snapshot.NewStore(root)
	findings, err := runDrift(logging.For("test"), store, driftSnapshot(), time.Now(), 51200)
	if err != nil {
		t.Fatalf("want graceful skip, got error: %v", err)
	}
	if findings != nil {
		t.Errorf("want nil findings without a baseline, got %d", len(findings))
	}
	if _, err := os.Stat(filepath.Join(root, "current", "digest.json")); !os.IsNotExist(err) {
		t.Errorf("no digest should be written without a baseline")
	}
}

// TestApproveBaseline_WritesCensus confirms --approve-baseline captures the
// snapshot's entity identities into baseline.meta.json.
func TestApproveBaseline_WritesCensus(t *testing.T) {
	root := t.TempDir()
	store := snapshot.NewStore(root)
	snap := model.Snapshot{
		RunDate: "2026-06-12",
		JumpCloud: model.JumpCloudShard{
			Devices:  []model.JCDevice{{SystemID: "mac-1"}},
			Identity: []model.JCUser{{Email: "a@x.com"}},
		},
		Sophos: model.SophosShard{
			Endpoints: []model.SophosEndpoint{{EndpointID: "ep-1"}},
		},
		GoogleWorkspace: model.GWSShard{
			Identity: []model.GWSUser{{Email: "a@x.com"}}, // dup email with JC → deduped
		},
	}

	if err := approveBaseline(logging.For("test"), store, snap, "admin@x.com", time.Now()); err != nil {
		t.Fatalf("approveBaseline: %v", err)
	}

	var m baseline.MetaFile
	if err := store.ReadJSON(&m, "baseline", "baseline.meta.json"); err != nil {
		t.Fatalf("read meta: %v", err)
	}
	if m.Version != "approved-2026-06-12" || m.ApprovedBy != "admin@x.com" {
		t.Errorf("meta version/approver wrong: %+v", m)
	}
	if len(m.Census.Devices) != 2 { // mac-1 + ep-1
		t.Errorf("census devices = %v, want [ep-1 mac-1]", m.Census.Devices)
	}
	if len(m.Census.Users) != 1 { // a@x.com deduped across JC + GWS
		t.Errorf("census users = %v, want [a@x.com]", m.Census.Users)
	}

	// The approved baseline must load cleanly afterwards (needs classes.json).
	writeFile(t, filepath.Join(root, "baseline", "classes.json"),
		`{"classes":[{"id":"any","match":{"system_id":"mac-1"},"expected":{}}]}`)
	b, err := baseline.Load(store.BaselineDir())
	if err != nil {
		t.Fatalf("reload approved baseline: %v", err)
	}
	if len(b.Census.Devices) != 2 {
		t.Errorf("reloaded census devices = %d, want 2", len(b.Census.Devices))
	}
}
