package snapshot

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gogo-assets/internal/model"
)

func sampleSnapshot() model.Snapshot {
	ts := time.Date(2026, 6, 11, 4, 0, 0, 0, time.UTC)
	return model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		RunDate:       "2026-06-11",
		RunTimestamp:  ts,
		JumpCloud: model.JumpCloudShard{
			Devices: []model.JCDevice{{SystemID: "s1", Hostname: "host-1"}},
		},
	}
}

func TestWriteSnapshotIdempotent(t *testing.T) {
	store := NewStore(t.TempDir())
	snap := sampleSnapshot()

	r1, err := store.WriteSnapshot(snap)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	first, err := os.ReadFile(r1.Path)
	if err != nil {
		t.Fatalf("read current: %v", err)
	}

	// Re-run for the same run_date with identical input → byte-identical, no dup.
	if _, err := store.WriteSnapshot(snap); err != nil {
		t.Fatalf("second write: %v", err)
	}
	second, err := os.ReadFile(r1.Path)
	if err != nil {
		t.Fatalf("re-read current: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("re-run produced different bytes (not idempotent)")
	}

	// daily/<run-folder>/ holds exactly one snapshot.tar.gz (the compressed
	// form), not a pile and not the plain JSON.
	dailyDir := filepath.Join(store.root, tierDaily, RunFolder(snap.RunDate))
	entries, err := os.ReadDir(dailyDir)
	if err != nil {
		t.Fatalf("read daily dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "snapshot.tar.gz" {
		t.Errorf("daily dir = %v, want exactly [snapshot.tar.gz]", entries)
	}

	// The compressed form is byte-stable across identical re-runs.
	gz1, _ := os.ReadFile(filepath.Join(dailyDir, "snapshot.tar.gz"))
	if _, err := store.WriteSnapshot(snap); err != nil {
		t.Fatalf("third write: %v", err)
	}
	gz2, _ := os.ReadFile(filepath.Join(dailyDir, "snapshot.tar.gz"))
	if !bytes.Equal(gz1, gz2) {
		t.Errorf("snapshot.tar.gz is not byte-stable across identical runs")
	}
}

func TestWriteSaaSWritesCurrentAndDaily(t *testing.T) {
	store := NewStore(t.TempDir())
	export := map[string]any{"schema_version": "1.0", "count": 1}

	res, err := store.WriteSaaS(export, "2026-06-11")
	if err != nil {
		t.Fatalf("write saas: %v", err)
	}
	if filepath.Base(res.Path) != "saas.json" {
		t.Errorf("current path = %q, want .../saas.json", res.Path)
	}

	// Both the live copy and the dated daily mirror must exist.
	current := filepath.Join(store.root, tierCurrent, "saas.json")
	daily := filepath.Join(store.root, tierDaily, RunFolder("2026-06-11"), "saas.json")
	for _, p := range []string{current, daily} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s: %v", p, err)
		}
	}
}

func TestWriteDailyJSONWritesRunFolderOnly(t *testing.T) {
	store := NewStore(t.TempDir())
	export := map[string]any{"schema_version": "2.0", "view": "drift", "count": 1}

	res, err := store.WriteDailyJSON("2026-05-05", "jc-drift.json", export)
	if err != nil {
		t.Fatalf("write daily json: %v", err)
	}

	// The run date is prettified into a readable folder name (may05-2026).
	daily := filepath.Join(store.root, tierDaily, "may05-2026", "jc-drift.json")
	if res.Path != daily {
		t.Errorf("path = %q, want %q", res.Path, daily)
	}
	if _, err := os.Stat(daily); err != nil {
		t.Errorf("expected %s: %v", daily, err)
	}
	// …and NOT in current/ (per-service files live only in the run folder).
	if _, err := os.Stat(filepath.Join(store.root, tierCurrent, "jc-drift.json")); !os.IsNotExist(err) {
		t.Errorf("jc-drift.json unexpectedly written to current/ (err=%v)", err)
	}
}

func TestWriteLeavesNoTempFiles(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.WriteSnapshot(sampleSnapshot()); err != nil {
		t.Fatalf("write: %v", err)
	}
	current := filepath.Join(store.root, tierCurrent)
	entries, err := os.ReadDir(current)
	if err != nil {
		t.Fatalf("read current: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

// A failed write must not corrupt an existing current/snapshot.json: the new
// content goes to a temp file first, so a rename that never happens leaves the
// previous file intact. We approximate the failure by writing good content,
// then confirming a second valid write fully replaces it (the temp-then-rename
// path), and that the file is always complete/parseable.
func TestWriteAtomicReplacesCleanly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "current", "snapshot.json")

	if err := writeAtomic(path, []byte("FIRST")); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := writeAtomic(path, []byte("SECOND")); err != nil {
		t.Fatalf("second: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "SECOND" {
		t.Errorf("content = %q, want SECOND", got)
	}
}

func TestPrune(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)

	mkDaily := func(runDate string) {
		if err := os.MkdirAll(filepath.Join(store.root, tierDaily, RunFolder(runDate)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mkArchive := func(name string) {
		p := filepath.Join(store.root, tierArchive, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mkDaily("2026-06-10")               // 1 day old — keep
	mkDaily("2026-04-01")               // ~71 days old — prune (>30d)
	mkDaily("not-a-date")               // malformed — keep untouched
	mkArchive("2026-06-01_digest.json") // 10 days — keep
	mkArchive("2025-10-01_digest.json") // >180 days — prune
	mkArchive("stray.json")             // malformed — keep

	if err := store.Prune(now); err != nil {
		t.Fatalf("prune: %v", err)
	}

	exists := func(elem ...string) bool {
		_, err := os.Stat(filepath.Join(append([]string{store.root}, elem...)...))
		return err == nil
	}
	tests := []struct {
		path []string
		want bool
	}{
		{[]string{tierDaily, RunFolder("2026-06-10")}, true},
		{[]string{tierDaily, RunFolder("2026-04-01")}, false},
		{[]string{tierDaily, "not-a-date"}, true},
		{[]string{tierArchive, "2026-06-01_digest.json"}, true},
		{[]string{tierArchive, "2025-10-01_digest.json"}, false},
		{[]string{tierArchive, "stray.json"}, true},
	}
	for _, tt := range tests {
		if got := exists(tt.path...); got != tt.want {
			t.Errorf("exists(%v) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
