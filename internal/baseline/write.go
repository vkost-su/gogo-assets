package baseline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// MetaFile is the on-disk shape of baseline.meta.json — the approval metadata
// and census that Load reads back. It is written by the --approve-baseline flow
// to pin the current entity set as the approved anchor.
type MetaFile struct {
	Version    string    `json:"version"`
	ApprovedBy string    `json:"approved_by"`
	ApprovedAt time.Time `json:"approved_at"`
	Census     Census    `json:"census"`
}

// WriteMeta writes baseline.meta.json into dir, creating the directory if
// needed. The write is atomic (temp file + rename) so a concurrent Load never
// observes a half-written baseline.
func WriteMeta(dir string, m MetaFile) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	buf, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal baseline meta: %w", err)
	}

	path := filepath.Join(dir, "baseline.meta.json")
	tmp, err := os.CreateTemp(dir, ".tmp-meta-*")
	if err != nil {
		return fmt.Errorf("temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // best-effort if we bail before rename

	if _, err := tmp.Write(buf); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename into %s: %w", path, err)
	}
	return nil
}
