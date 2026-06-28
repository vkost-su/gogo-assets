// Package httpstat provides a request-counting http.RoundTripper that tallies
// HTTP responses by status code. One Counter is shared across every collector
// in a run so the end-of-run report can show the total request volume and the
// per-status breakdown — most usefully, how many 429s the rate limiter still
// let through.
package httpstat

import (
	"net/http"
	"sort"
	"strconv"
	"sync"
)

// Counter tallies HTTP responses by exact status code. It is safe for
// concurrent use and is meant to be shared across all clients in a run.
type Counter struct {
	mu       sync.Mutex
	total    int
	errors   int // transport-level failures that produced no response
	byStatus map[int]int
}

// New returns an empty Counter.
func New() *Counter { return &Counter{byStatus: make(map[int]int)} }

// Wrap returns a RoundTripper that records every response into c before
// returning it unchanged. A nil base uses http.DefaultTransport. Retries that
// re-issue a request are each counted, so a 429 that is retried and then
// succeeds shows up as both a 429 and a 200 — which is exactly the throttling
// signal we want to surface.
func (c *Counter) Wrap(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &roundTripper{c: c, base: base}
}

type roundTripper struct {
	c    *Counter
	base http.RoundTripper
}

func (rt *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := rt.base.RoundTrip(req)
	rt.c.mu.Lock()
	rt.c.total++
	if err != nil {
		rt.c.errors++
	} else {
		rt.c.byStatus[resp.StatusCode]++
	}
	rt.c.mu.Unlock()
	return resp, err
}

// Stats is an immutable snapshot of a Counter.
type Stats struct {
	Total    int
	Errors   int
	ByStatus map[int]int
}

// Snapshot returns the counts so far.
func (c *Counter) Snapshot() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := make(map[int]int, len(c.byStatus))
	for k, v := range c.byStatus {
		m[k] = v
	}
	return Stats{Total: c.total, Errors: c.errors, ByStatus: m}
}

// LogArgs renders the snapshot as slog key/value pairs: a "total", an optional
// "errors", and one "status_<code>" per observed code in ascending order — e.g.
// total=612 status_200=500 status_429=100 status_500=12.
func (s Stats) LogArgs() []any {
	args := make([]any, 0, 2+2*len(s.ByStatus)+2)
	args = append(args, "total", s.Total)
	if s.Errors > 0 {
		args = append(args, "errors", s.Errors)
	}
	codes := make([]int, 0, len(s.ByStatus))
	for code := range s.ByStatus {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	for _, code := range codes {
		args = append(args, "status_"+strconv.Itoa(code), s.ByStatus[code])
	}
	return args
}
