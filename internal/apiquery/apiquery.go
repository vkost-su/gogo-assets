// Package apiquery records the concrete API query templates a collector's client
// issued during a run — e.g. "GET /api/v2/systems/{id}/users". Each service
// client holds one Recorder, registers the endpoint template (method + path,
// with dynamic segments as {placeholders}) at every call site, and exposes the
// deduplicated, sorted set as the run's per-service query manifest.
//
// The manifest answers "which concrete endpoints did this run actually hit?" —
// stamped into the snapshot's provenance block and logged at the end of a run.
package apiquery

import (
	"sort"
	"sync"
)

// Recorder collects the distinct API query templates a client issued. It is safe
// for concurrent use. A nil *Recorder is a usable no-op, so clients can record
// unconditionally without a nil check.
type Recorder struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

// New returns an empty Recorder.
func New() *Recorder {
	return &Recorder{seen: make(map[string]struct{})}
}

// Record registers one query template (e.g. "GET /api/systems/{id}").
// Duplicates collapse into a single entry. A nil Recorder is a no-op.
func (r *Recorder) Record(query string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.seen[query] = struct{}{}
	r.mu.Unlock()
}

// Queries returns the recorded templates in lexical order. A nil Recorder
// returns nil.
func (r *Recorder) Queries() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.seen))
	for q := range r.seen {
		out = append(out, q)
	}
	sort.Strings(out)
	return out
}
