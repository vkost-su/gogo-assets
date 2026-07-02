package snapshot

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
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
	tierDaily    = "daily"    // <run-folder>/ — snapshot.tar.gz + per-service full/drift, 30-day retention
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

// DailyDir is the absolute daily tier path, parent of the dated run-folder
// mirror directories.
func (s *Store) DailyDir() string { return filepath.Join(s.root, tierDaily) }

// RunFolder converts a logical run date (YYYY-MM-DD) into the human-readable
// name of that run's daily folder, e.g. "2026-05-05" → "may05-2026". A run date
// that does not parse is returned unchanged, so a caller never loses data to a
// format slip. The YYYY-MM-DD form is still what every artifact stores as
// run_date; only the on-disk folder name is prettified. The prune parser
// (dirDate) reads this same form back.
func RunFolder(runDate string) string {
	t, err := time.Parse("2006-01-02", runDate)
	if err != nil {
		return runDate
	}
	return strings.ToLower(t.Format("Jan02-2006"))
}

// path joins one or more elements under the store root.
func (s *Store) path(elem ...string) string {
	return filepath.Join(append([]string{s.root}, elem...)...)
}

// EnsureDirs materialises the full tier tree (baseline/current/daily/archive)
// under the local root. Individual writes create their own parent directory
// lazily, so this is only needed to lay out the empty structure up front — e.g.
// on a self-hosted runner or a fresh local checkout where a persistent run is
// expected to populate it.
func (s *Store) EnsureDirs() error {
	for _, tier := range []string{tierBaseline, tierCurrent, tierDaily, tierArchive} {
		if err := os.MkdirAll(s.path(tier), 0o755); err != nil {
			return fmt.Errorf("ensure %s tier: %w", tier, err)
		}
	}
	return nil
}

// WriteSnapshot serialises snap to current/snapshot.json (the live working copy)
// and daily/<run-folder>/snapshot.tar.gz (the retained history, gzip-tarred).
// current/ keeps the plain form for readers (baseline approval, --json
// inspection, ad-hoc jq); the dated run folder gets the compressed form. Both
// writes are atomic; re-running for the same run date overwrites both, and
// identical input yields byte-identical output (the tar entry is stamped with
// the run timestamp, the gzip header carries none).
func (s *Store) WriteSnapshot(snap model.Snapshot) (Result, error) {
	buf, err := marshal(snap)
	if err != nil {
		return Result{}, fmt.Errorf("marshal snapshot: %w", err)
	}
	current := s.path(tierCurrent, "snapshot.json")
	if err := writeAtomic(current, buf); err != nil {
		return Result{}, err
	}
	gz, err := gzipTar("snapshot.json", buf, snap.RunTimestamp)
	if err != nil {
		return Result{}, fmt.Errorf("compress snapshot: %w", err)
	}
	daily := s.path(tierDaily, RunFolder(snap.RunDate), "snapshot.tar.gz")
	if err := writeAtomic(daily, gz); err != nil {
		return Result{}, err
	}
	return Result{Path: current, SizeBytes: int64(len(buf))}, nil
}

// WriteInventory serialises the rich asset inventory — the source of truth the
// Sheets tabs render from — to both current/inventory.json (the live working
// copy the `sheets` command publishes from) and daily/<run_date>/inventory.json
// (retained history, pruned with the daily tier). Both writes are atomic.
//
// inv is taken as any so the store stays free of an import on package inventory;
// callers pass a *inventory.AssetInventory.
func (s *Store) WriteInventory(inv any, runDate string) (Result, error) {
	buf, err := marshal(inv)
	if err != nil {
		return Result{}, fmt.Errorf("marshal inventory: %w", err)
	}
	current := s.path(tierCurrent, "inventory.json")
	daily := s.path(tierDaily, RunFolder(runDate), "inventory.json")
	for _, p := range []string{current, daily} {
		if err := writeAtomic(p, buf); err != nil {
			return Result{}, err
		}
	}
	return Result{Path: current, SizeBytes: int64(len(buf))}, nil
}

// WriteSaaS serialises the standalone SaaS export — the full nested SaaSApp
// structures that back the SaaS sheet tab — to both current/saas.json (the live
// working copy) and daily/<run_date>/saas.json (retained history, pruned with
// the daily tier). Both writes are atomic.
//
// export is taken as any so the store stays free of an import on the jumpcloud
// package; callers pass a jumpcloud.SaaSExport.
func (s *Store) WriteSaaS(export any, runDate string) (Result, error) {
	buf, err := marshal(export)
	if err != nil {
		return Result{}, fmt.Errorf("marshal saas: %w", err)
	}
	current := s.path(tierCurrent, "saas.json")
	daily := s.path(tierDaily, RunFolder(runDate), "saas.json")
	for _, p := range []string{current, daily} {
		if err := writeAtomic(p, buf); err != nil {
			return Result{}, err
		}
	}
	return Result{Path: current, SizeBytes: int64(len(buf))}, nil
}

// WriteDailyJSON atomically writes v as indented JSON to
// daily/<run-folder>/<name>, where the run folder is RunFolder(runDate). It is
// the shared writer for the per-service full/drift outputs (jc.json,
// jc-drift.json, gw.json, …), which live only in the dated run folder — the
// external report step reads them from there. v is a self-describing
// serviceview.Export. Same atomic temp-file+rename pattern as WriteSaaS.
func (s *Store) WriteDailyJSON(runDate, name string, v any) (Result, error) {
	buf, err := marshal(v)
	if err != nil {
		return Result{}, fmt.Errorf("marshal %s: %w", name, err)
	}
	p := s.path(tierDaily, RunFolder(runDate), name)
	if err := writeAtomic(p, buf); err != nil {
		return Result{}, err
	}
	return Result{Path: p, SizeBytes: int64(len(buf))}, nil
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

// ReadSnapshot reads the canonical snapshot back for republish: current/
// snapshot.json when runDate is empty, or the dated run folder's snapshot.tar.gz
// (decompressed) otherwise. It is the read twin of WriteSnapshot. A missing file
// returns an os.ErrNotExist-matchable error.
func (s *Store) ReadSnapshot(runDate string) (model.Snapshot, error) {
	var snap model.Snapshot
	if runDate == "" {
		return snap, s.ReadJSON(&snap, tierCurrent, "snapshot.json")
	}
	p := s.path(tierDaily, RunFolder(runDate), "snapshot.tar.gz")
	buf, err := os.ReadFile(p)
	if err != nil {
		return snap, err // wraps ErrNotExist
	}
	data, err := ungzipTar(buf)
	if err != nil {
		return snap, fmt.Errorf("decompress %s: %w", p, err)
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		return snap, fmt.Errorf("decode %s: %w", p, err)
	}
	return snap, nil
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

// dirDate extracts a run_date from a daily/ entry name. The name is the
// human-readable run-folder form written by RunFolder (e.g. "may05-2026");
// time.Parse reads the lower-cased month case-insensitively.
func dirDate(name string) (time.Time, bool) {
	t, err := time.Parse("Jan02-2006", name)
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

// gzipTar wraps a single named file's bytes in a gzip-compressed tar archive —
// how the dated run folder stores the combined snapshot as snapshot.tar.gz.
// modTime stamps the tar entry and the gzip header carries no timestamp, so
// identical input yields byte-identical output. USTAR format keeps the archive
// free of PAX sub-headers that would vary the bytes.
func gzipTar(name string, data []byte, modTime time.Time) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{
		Name:    name,
		Mode:    0o644,
		Size:    int64(len(data)),
		ModTime: modTime.UTC().Truncate(time.Second),
		Format:  tar.FormatUSTAR,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return nil, fmt.Errorf("tar header: %w", err)
	}
	if _, err := tw.Write(data); err != nil {
		return nil, fmt.Errorf("tar body: %w", err)
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("tar close: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("gzip close: %w", err)
	}
	return buf.Bytes(), nil
}

// ungzipTar reverses gzipTar: it decompresses the archive and returns the bytes
// of its single entry.
func ungzipTar(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	if _, err := tr.Next(); err != nil {
		return nil, fmt.Errorf("tar next: %w", err)
	}
	return io.ReadAll(tr)
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
