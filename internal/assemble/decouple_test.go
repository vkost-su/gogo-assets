package assemble_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gogo-assets/internal/assemble"
	"gogo-assets/internal/baseline"
	"gogo-assets/internal/classify"
	"gogo-assets/internal/drift"
	"gogo-assets/internal/jumpcloud"
	"gogo-assets/internal/model"
	"gogo-assets/internal/serviceview"
)

func bp(b bool) *bool { return &b }

// TestServiceDriftFromRawSourcesDecoupled builds a JumpCloud-only run end to end
// straight from raw collector Sources — assemble.Build → classify → drift →
// serviceview — with every other service empty and no inventory.AssetInventory
// anywhere in the path. It proves each service's full/drift is produced from its
// own canonical shard + the engine, independent of the cross-source merge layer.
// (Structurally, none of the packages exercised here import internal/inventory.)
func TestServiceDriftFromRawSourcesDecoupled(t *testing.T) {
	// Baseline: managed macOS must have disk encryption on.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "classes.json"),
		[]byte(`{"classes":[{"id":"mac","match":{"os_family":"darwin"},"expected":{"disk_encrypted":"true"}}]}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	b, err := baseline.Load(dir)
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}

	// Raw Sources: ONLY JumpCloud populated. GWS/Sophos/PeopleForce left empty.
	src := assemble.Sources{
		JCSystems: []jumpcloud.System{
			{SystemID: "d-a", Hostname: "a", OSFamily: "darwin", DiskEncrypted: bp(true)},
			{SystemID: "d-b", Hostname: "b", OSFamily: "darwin", DiskEncrypted: bp(false)}, // drift
		},
	}
	ts := time.Date(2026, 5, 5, 9, 0, 0, 0, time.UTC)
	snap := assemble.Build(src, ts, "2026-05-05")

	// The other shards are empty — this service stands entirely alone.
	if len(snap.GoogleWorkspace.Identity) != 0 || len(snap.Sophos.Endpoints) != 0 || len(snap.PeopleForce.Assets) != 0 {
		t.Fatalf("expected only JumpCloud populated, got gws=%d sp=%d pf=%d",
			len(snap.GoogleWorkspace.Identity), len(snap.Sophos.Endpoints), len(snap.PeopleForce.Assets))
	}

	// Engine path: classify + drift on the snapshot alone.
	results, _ := classify.Run(snap, b, ts)
	findings := drift.Run(results, b, ts)

	drifted := serviceview.DriftedIDs(findings, model.ServiceJumpCloud, model.EntityDevice)
	full, driftView := serviceview.Split(snap.JumpCloud.Devices,
		func(d model.JCDevice) string { return d.SystemID }, drifted, "jumpcloud", "2026-05-05", ts)

	if full.Count != 2 {
		t.Errorf("full count = %d, want 2", full.Count)
	}
	if driftView.Count != 1 || driftView.Records[0].SystemID != "d-b" {
		t.Errorf("drift = %+v, want exactly d-b", driftView.Records)
	}
}
