package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gogo-assets/internal/baseline"
	"gogo-assets/internal/digest"
	"gogo-assets/internal/inventory"
	"gogo-assets/internal/jumpcloud"
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

// TestRunHelpAndVersion confirms the help/version intercepts return nil and
// short-circuit before config loading, so they work with no environment set.
func TestRunHelpAndVersion(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help", "version", "--version"} {
		if err := run([]string{arg}); err != nil {
			t.Errorf("run([%q]) = %v, want nil", arg, err)
		}
	}
}

// TestRunRejectsRunDateOutsideSheets confirms --run-date and --dry-run are only
// valid for the sheets command, and that the guard fires before any collection.
func TestRunRejectsRunDateOutsideSheets(t *testing.T) {
	if err := run([]string{"all", "--run-date", "2026-06-15"}); err == nil {
		t.Error("--run-date with target 'all' should error")
	}
	if err := run([]string{"all", "--dry-run"}); err == nil {
		t.Error("--dry-run with target 'all' should error")
	}
}

// TestPrintUsage confirms the help text advertises the sections it promises.
func TestPrintUsage(t *testing.T) {
	var b bytes.Buffer
	printUsage(&b)
	out := b.String()
	for _, want := range []string{"Targets", "Commands", "sheets", "--tabs", "--run-date", "version"} {
		if !strings.Contains(out, want) {
			t.Errorf("usage text missing %q", want)
		}
	}
}

// TestInventoryPath covers the sheets-command source selection: current by
// default, a dated daily mirror when --run-date is given.
func TestInventoryPath(t *testing.T) {
	tests := []struct {
		name string
		give string
		want []string
	}{
		{"current by default", "", []string{"current", "inventory.json"}},
		{"dated daily mirror", "2026-06-15", []string{"daily", "2026-06-15", "inventory.json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inventoryPath(tt.give); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("inventoryPath(%q) = %v, want %v", tt.give, got, tt.want)
			}
		})
	}
}

// TestLoadInventoryForPublish confirms --run-date reads the dated daily mirror,
// an empty date reads current/, and a missing mirror yields a friendly error.
func TestLoadInventoryForPublish(t *testing.T) {
	root := t.TempDir()
	store := snapshot.NewStore(root)

	inv := inventory.New()
	inv.SaaSApps = []jumpcloud.SaaSApp{{AppID: "app1", Name: "Figma", Category: "Design"}}
	if _, err := store.WriteInventory(inv, "2026-06-15"); err != nil {
		t.Fatalf("WriteInventory: %v", err)
	}

	// --run-date hits the dated mirror.
	got, err := loadInventoryForPublish(store, "2026-06-15")
	if err != nil {
		t.Fatalf("loadInventoryForPublish(dated): %v", err)
	}
	if len(got.SaaSApps) != 1 || got.SaaSApps[0].Name != "Figma" {
		t.Errorf("dated mirror inventory = %+v, want one SaaS app Figma", got.SaaSApps)
	}

	// Empty date → current/, written by the same WriteInventory call.
	cur, err := loadInventoryForPublish(store, "")
	if err != nil {
		t.Fatalf("loadInventoryForPublish(current): %v", err)
	}
	if len(cur.SaaSApps) != 1 {
		t.Errorf("current inventory missing SaaS: %+v", cur.SaaSApps)
	}

	// A date with no mirror → error.
	if _, err := loadInventoryForPublish(store, "2099-01-01"); err == nil {
		t.Error("missing daily mirror should error")
	}
}

// TestParseTabs covers the --tabs selector parsing and validation.
func TestParseTabs(t *testing.T) {
	t.Run("empty means all (nil)", func(t *testing.T) {
		sel, err := parseTabs("")
		if err != nil || sel != nil {
			t.Fatalf("got sel=%v err=%v, want nil/nil", sel, err)
		}
	})
	t.Run("all keyword means no filter", func(t *testing.T) {
		sel, err := parseTabs("jc,all")
		if err != nil || sel != nil {
			t.Fatalf("got sel=%v err=%v, want nil/nil", sel, err)
		}
	})
	t.Run("subset, trimmed and case-insensitive", func(t *testing.T) {
		sel, err := parseTabs(" JC , SaaS ")
		if err != nil {
			t.Fatal(err)
		}
		if len(sel) != 2 || !sel[tabJC] || !sel[tabSaaS] || sel[tabGW] {
			t.Fatalf("sel=%v, want {jc,saas}", sel)
		}
	})
	t.Run("unknown tab errors", func(t *testing.T) {
		if _, err := parseTabs("jc,bogus"); err == nil {
			t.Fatal("want error for unknown tab")
		}
	})
}

// TestTargetTabs confirms the auto-write target→tabs gate.
func TestTargetTabs(t *testing.T) {
	jc := targetTabs("jc")
	if !jc(tabJC) || !jc(tabSaaS) {
		t.Error("jc target must allow jc + saas")
	}
	if jc(tabGW) || jc(tabSophos) || jc(tabUsersAll) || jc(tabFindings) {
		t.Error("jc target is too broad")
	}
	if gw := targetTabs("gw"); !gw(tabGW) || gw(tabJC) {
		t.Error("gw target wrong")
	}
	all := targetTabs("all")
	for _, k := range allTabKeys {
		if !all(k) {
			t.Errorf("all target must allow %s", k)
		}
	}
}

// TestWriteInventory_RoundTrip confirms the persisted source of truth preserves
// the fields the lean snapshot drops: JC software and the cross-source device
// join. Both must survive a WriteInventory → ReadJSON cycle, and a daily mirror
// must be written.
func TestWriteInventory_RoundTrip(t *testing.T) {
	root := t.TempDir()
	store := snapshot.NewStore(root)

	sys := jumpcloud.System{
		SystemID:     "jc-1",
		Hostname:     "mac1",
		SerialNumber: "SER1",
		Apps:         []jumpcloud.App{{Name: "Slack", Version: "4.0"}}, // dropped by the lean snapshot
	}
	inv := inventory.New()
	inv.JCSystems = []jumpcloud.System{sys}
	inv.SaaSApps = []jumpcloud.SaaSApp{{AppID: "app1", Name: "Figma", Category: "Design"}}
	inv.Users["a@x.com"] = &inventory.UnifiedUserRecord{
		Email:   "a@x.com",
		Devices: []inventory.DevicePair{{JC: &sys, MatchKey: "jc-only"}}, // not in the lean snapshot
	}

	if _, err := store.WriteInventory(inv, "2026-06-17"); err != nil {
		t.Fatalf("WriteInventory: %v", err)
	}

	var got inventory.AssetInventory
	if err := store.ReadJSON(&got, "current", "inventory.json"); err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}

	if len(got.JCSystems) != 1 || len(got.JCSystems[0].Apps) != 1 || got.JCSystems[0].Apps[0].Name != "Slack" {
		t.Errorf("JC software lost in round-trip: %+v", got.JCSystems)
	}
	u := got.Users["a@x.com"]
	if u == nil || len(u.Devices) != 1 || u.Devices[0].JC == nil || u.Devices[0].JC.Hostname != "mac1" {
		t.Errorf("device join lost in round-trip: %+v", u)
	}
	if len(got.SaaSApps) != 1 || got.SaaSApps[0].Name != "Figma" {
		t.Errorf("SaaS lost in round-trip: %+v", got.SaaSApps)
	}
	if _, err := os.Stat(filepath.Join(root, "daily", "2026-06-17", "inventory.json")); err != nil {
		t.Errorf("daily inventory mirror missing: %v", err)
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
	findings, err := runDrift(log, store, snap, now, 51200, true)
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

// TestRunDrift_EphemeralWritesNothing confirms an ephemeral run (persist=false)
// still computes the findings for the Sheets Findings tab but writes neither
// classification.json nor digest.json to disk.
func TestRunDrift_EphemeralWritesNothing(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "baseline", "classes.json"),
		`{"classes":[{"id":"mac","priority":10,"match":{"os_family":"darwin"},"expected":{"disk_encrypted":"true"}}]}`)
	writeFile(t, filepath.Join(root, "baseline", "baseline.meta.json"),
		`{"version":"v1","census":{"devices":["ghost-device"],"users":[]}}`)

	store := snapshot.NewStore(root)
	snap := driftSnapshot()

	findings, err := runDrift(logging.For("test"), store, snap, snap.RunTimestamp, 51200, false)
	if err != nil {
		t.Fatalf("runDrift: %v", err)
	}
	if len(findings) == 0 {
		t.Error("ephemeral run should still return findings for Sheets")
	}
	for _, name := range []string{"classification.json", "digest.json"} {
		if _, err := os.Stat(filepath.Join(root, "current", name)); !os.IsNotExist(err) {
			t.Errorf("ephemeral run must not write current/%s", name)
		}
	}
}

// TestRunDrift_NoBaselineIsGraceful confirms a missing classes.json skips the
// engine without error and writes no digest.
func TestRunDrift_NoBaselineIsGraceful(t *testing.T) {
	root := t.TempDir()
	store := snapshot.NewStore(root)
	findings, err := runDrift(logging.For("test"), store, driftSnapshot(), time.Now(), 51200, true)
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
