// Package snapshot is the on-disk home of the canonical drift pipeline.
//
// Store (store.go) writes the model.Snapshot, classification, and digest across
// the baseline/current/daily/archive tiers with atomic, idempotent writes.
// Result and HumanBytes here are the small shared helpers every write returns
// and reports with.
package snapshot

import (
	"fmt"
)

// Result describes a file produced by a Store write: its path and byte size.
type Result struct {
	Path      string // path written
	SizeBytes int64  // file size on disk
}

// HumanBytes renders a byte count as a short human-readable string:
// "42 B", "1.2 KB", "5.8 MB", "12 GB".
func HumanBytes(n int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case n >= gb:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(gb))
	case n >= mb:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(mb))
	case n >= kb:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(kb))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
