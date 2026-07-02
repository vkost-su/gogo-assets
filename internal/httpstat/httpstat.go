// Package httpstat provides a request-counting http.RoundTripper that tallies
// HTTP responses by status code. One Counter is shared across every collector
// in a run so the end-of-run report can show the total request volume and the
// per-status breakdown — most usefully, how many 429s the rate limiter still
// let through.
//
// It also keeps a per-endpoint breakdown of the 404 responses so a run can
// always show which endpoints came back Not Found, not just how many.
package httpstat

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Counter tallies HTTP responses by exact status code. It is safe for
// concurrent use and is meant to be shared across all clients in a run.
type Counter struct {
	mu       sync.Mutex
	total    int
	errors   int // transport-level failures that produced no response
	byStatus map[int]int
	notFound map[string]int // endpoint template → count, for 404 responses
}

// New returns an empty Counter.
func New() *Counter {
	return &Counter{byStatus: make(map[int]int), notFound: make(map[string]int)}
}

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
		if resp.StatusCode == http.StatusNotFound {
			rt.c.notFound[endpoint(req)]++
		}
	}
	rt.c.mu.Unlock()
	return resp, err
}

// endpoint renders a request as a stable "METHOD host/path" template, collapsing
// id-like path segments to {id} so many concrete 404s group into a few lines.
func endpoint(req *http.Request) string {
	if req == nil || req.URL == nil {
		return "?"
	}
	return req.Method + " " + req.URL.Host + normalizePath(req.URL.Path)
}

// normalizePath replaces id-like path segments (all-digit, or long hex/UUID)
// with {id}, so /api/systems/62f…/apps becomes /api/systems/{id}/apps.
func normalizePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if isIDLike(s) {
			segs[i] = "{id}"
		}
	}
	return strings.Join(segs, "/")
}

func isIDLike(s string) bool {
	if len(s) == 0 {
		return false
	}
	allDigits := true
	for _, r := range s {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return true
	}
	// Mongo ObjectID / UUID: length ≥ 16, only hex digits and dashes.
	if len(s) < 16 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F', r == '-':
		default:
			return false
		}
	}
	return true
}

// Stats is an immutable snapshot of a Counter.
type Stats struct {
	Total    int
	Errors   int
	ByStatus map[int]int
	NotFound map[string]int // endpoint template → count (404 responses)
}

// Snapshot returns the counts so far.
func (c *Counter) Snapshot() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := make(map[int]int, len(c.byStatus))
	for k, v := range c.byStatus {
		m[k] = v
	}
	nf := make(map[string]int, len(c.notFound))
	for k, v := range c.notFound {
		nf[k] = v
	}
	return Stats{Total: c.total, Errors: c.errors, ByStatus: m, NotFound: nf}
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

// NotFoundArgs renders the 404 breakdown as slog key/value pairs: a "total" and
// an "endpoints" list of "METHOD host/path ×count", ordered by count desc then
// name. It returns nil when there were no 404s, so callers can skip the line.
func (s Stats) NotFoundArgs() []any {
	if len(s.NotFound) == 0 {
		return nil
	}
	type kv struct {
		line string
		n    int
	}
	items := make([]kv, 0, len(s.NotFound))
	total := 0
	for line, n := range s.NotFound {
		items = append(items, kv{line, n})
		total += n
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].n != items[j].n {
			return items[i].n > items[j].n
		}
		return items[i].line < items[j].line
	})
	lines := make([]string, len(items))
	for i, it := range items {
		lines[i] = fmt.Sprintf("%s ×%d", it.line, it.n)
	}
	return []any{"total", total, "endpoints", lines}
}
