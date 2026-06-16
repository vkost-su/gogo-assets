package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gogo-assets/internal/model"
)

// Storage tiers under the local root (ТЗ §5.1).
const (
	tierBaseline = "baseline" // approved anchor: classes.json, baseline.meta.json
	tierCurrent  = "current"  // live working set: snapshot/classification/digest
	tierDaily    = "daily"    // <run_date>/snapshot.json — full snapshots, 30-day retention
	tierArchive  = "archive"  // <run_date>_digest.json — digest-only, 180-day retention
)

// Retention windows.
const (
	dailyRetention   = 30 * 24 * time.Hour
	archiveRetention = 180 * 24 * time.Hour
)

// Store is the on-disk home for the drift pipeline, rooted at a local dir with
// baseline/current/daily/archive tiers.
//
// All writes are atomic (temp file + rename) and idempotent per run_date: a
// re-run for the same date overwrites in place rather than accumulating, so a
// reader never observes a half-written file (ТЗ §9).
type Store struct {
	root string
}

// NewStore returns a Store rooted at localDir. Directories are created lazily
// on first write.
func NewStore(localDir string) *Store {
	return &Store{root: localDir}
}

// BaselineDir is the absolute baseline tier path, where classes.json and
// baseline.meta.json live.
func (s *Store) BaselineDir() string { return filepath.Join(s.root, tierBaseline) }

// CurrentDir is the absolute current tier path.
func (s *Store) CurrentDir() string { return filepath.Join(s.root, tierCurrent) }

// path joins one or more elements under the store root.
func (s *Store) path(elem ...string) string {
	return filepath.Join(append([]string{s.root}, elem...)...)
}

// WriteSnapshot serialises snap to both current/snapshot.json (the live working
// copy) and daily/<run_date>/snapshot.json (the retained history). Both writes
// are atomic; re-running for the same run_date overwrites both.
func (s *Store) WriteSnapshot(snap model.Snapshot) (Result, error) {
	buf, err := marshal(snap)
	if err != nil {
		return Result{}, fmt.Errorf("marshal snapshot: %w", err)
	}
	current := s.path(tierCurrent, "snapshot.json")
	daily := s.path(tierDaily, snap.RunDate, "snapshot.json")
	for _, p := range []string{current, daily} {
		if err := writeAtomic(p, buf); err != nil {
			return Result{}, err
		}
	}
	return Result{Path: current, SizeBytes: int64(len(buf))}, nil
}

// WriteCurrentJSON atomically writes v as indented JSON to current/<name>
// (e.g. "classification.json").
func (s *Store) WriteCurrentJSON(name string, v any) (Result, error) {
	buf, err := marshal(v)
	if err != nil {
		return Result{}, fmt.Errorf("marshal %s: %w", name, err)
	}
	p := s.path(tierCurrent, name)
	if err := writeAtomic(p, buf); err != nil {
		return Result{}, err
	}
	return Result{Path: p, SizeBytes: int64(len(buf))}, nil
}

// WriteDigest writes the pre-serialised digest bytes to both current/digest.json
// and archive/<run_date>_digest.json. The caller serialises so it can enforce
// the size budget on the exact bytes that land on disk.
func (s *Store) WriteDigest(runDate string, digestJSON []byte) (Result, error) {
	current := s.path(tierCurrent, "digest.json")
	archive := s.path(tierArchive, runDate+"_digest.json")
	for _, p := range []string{current, archive} {
		if err := writeAtomic(p, digestJSON); err != nil {
			return Result{}, err
		}
	}
	return Result{Path: current, SizeBytes: int64(len(digestJSON))}, nil
}

// ReadJSON decodes the JSON file at the given store-relative path into v.
// It returns os.ErrNotExist (unwrapped-matchable) when the file is absent.
func (s *Store) ReadJSON(v any, elem ...string) error {
	p := s.path(elem...)
	buf, err := os.ReadFile(p)
	if err != nil {
		return err // wraps ErrNotExist; callers use errors.Is
	}
	if err := json.Unmarshal(buf, v); err != nil {
		return fmt.Errorf("decode %s: %w", p, err)
	}
	return nil
}

// Prune deletes daily snapshots older than 30 days and archive digests older
// than 180 days, relative to now. A malformed date in a filename is left alone.
func (s *Store) Prune(now time.Time) error {
	if err := pruneByDate(s.path(tierDaily), dirDate, now, dailyRetention); err != nil {
		return fmt.Errorf("prune daily: %w", err)
	}
	if err := pruneByDate(s.path(tierArchive), archiveFileDate, now, archiveRetention); err != nil {
		return fmt.Errorf("prune archive: %w", err)
	}
	return nil
}

// dirDate extracts a run_date from a daily/ entry name (the name is the date).
func dirDate(name string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", name)
	return t, err == nil
}

// archiveFileDate extracts a run_date from an archive/ filename of the form
// <run_date>_digest.json.
func archiveFileDate(name string) (time.Time, bool) {
	date, ok := strings.CutSuffix(name, "_digest.json")
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", date)
	return t, err == nil
}

// pruneByDate removes entries in dir whose parsed date is older than retention.
func pruneByDate(dir string, dateOf func(string) (time.Time, bool), now time.Time, retention time.Duration) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing collected yet
		}
		return err
	}
	cutoff := now.Add(-retention)
	for _, e := range entries {
		date, ok := dateOf(e.Name())
		if !ok || !date.Before(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// marshal renders v as 2-space-indented JSON. Field order is declaration order
// and map keys are sorted, so identical input produces identical bytes.
func marshal(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// writeAtomic writes data to a temp file in the destination directory and
// renames it into place. Rename within a directory is atomic on POSIX, so a
// concurrent reader sees either the old file or the new one, never a partial.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we bail before the rename succeeds.
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename into %s: %w", path, err)
	}
	return nil
}
