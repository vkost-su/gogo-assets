package httpstat

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// stubRT returns a fixed status (or error when status == 0) for every call.
type stubRT struct {
	status int
	err    error
}

func (s stubRT) RoundTrip(*http.Request) (*http.Response, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &http.Response{StatusCode: s.status, Body: http.NoBody}, nil
}

func do(rt http.RoundTripper, n int) {
	req, _ := http.NewRequest(http.MethodGet, "http://x", nil)
	for range n {
		_, _ = rt.RoundTrip(req)
	}
}

func TestCounterTalliesByStatus(t *testing.T) {
	c := New()
	do(c.Wrap(stubRT{status: 200}), 500)
	do(c.Wrap(stubRT{status: 429}), 100)
	do(c.Wrap(stubRT{status: 500}), 12)
	do(c.Wrap(stubRT{err: errors.New("dial")}), 3)

	s := c.Snapshot()
	if s.Total != 615 {
		t.Errorf("total = %d, want 615", s.Total)
	}
	if s.Errors != 3 {
		t.Errorf("errors = %d, want 3", s.Errors)
	}
	want := map[int]int{200: 500, 429: 100, 500: 12}
	if !reflect.DeepEqual(s.ByStatus, want) {
		t.Errorf("byStatus = %v, want %v", s.ByStatus, want)
	}
}

// stubURLRT returns 404 for paths containing "missing", else 200.
type stubURLRT struct{}

func (stubURLRT) RoundTrip(req *http.Request) (*http.Response, error) {
	code := 200
	if strings.Contains(req.URL.Path, "missing") {
		code = 404
	}
	return &http.Response{StatusCode: code, Body: http.NoBody}, nil
}

func doURL(rt http.RoundTripper, url string) {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	_, _ = rt.RoundTrip(req)
}

func TestNotFoundGroupsByEndpoint(t *testing.T) {
	c := New()
	rt := c.Wrap(stubURLRT{})
	// Same endpoint template, different ids → one group.
	doURL(rt, "http://jc.example/api/systeminsights/62f0000000000000000000a1/missing")
	doURL(rt, "http://jc.example/api/systeminsights/62f0000000000000000000b2/missing")
	doURL(rt, "http://jc.example/api/systeminsights/12345/missing")
	// A different endpoint.
	doURL(rt, "http://jc.example/api/apps/999/missing")
	// A 200 must not appear in the 404 breakdown.
	doURL(rt, "http://jc.example/api/systems")

	s := c.Snapshot()
	if s.ByStatus[404] != 4 {
		t.Errorf("status_404 = %d, want 4", s.ByStatus[404])
	}
	want := map[string]int{
		"GET jc.example/api/systeminsights/{id}/missing": 3,
		"GET jc.example/api/apps/{id}/missing":           1,
	}
	if !reflect.DeepEqual(s.NotFound, want) {
		t.Errorf("NotFound = %v, want %v", s.NotFound, want)
	}

	// NotFoundArgs: total + endpoints ordered by count desc.
	args := s.NotFoundArgs()
	if args[0] != "total" || args[1] != 4 || args[2] != "endpoints" {
		t.Fatalf("NotFoundArgs head = %v, want total/4/endpoints", args[:3])
	}
	lines := args[3].([]string)
	if len(lines) != 2 || lines[0] != "GET jc.example/api/systeminsights/{id}/missing ×3" {
		t.Errorf("NotFoundArgs endpoints = %v", lines)
	}
}

func TestNotFoundArgsNilWhenNone(t *testing.T) {
	if got := (Stats{}).NotFoundArgs(); got != nil {
		t.Errorf("NotFoundArgs with no 404s = %v, want nil", got)
	}
}

func TestStatsLogArgsSortedAscending(t *testing.T) {
	s := Stats{Total: 612, Errors: 2, ByStatus: map[int]int{500: 12, 200: 500, 429: 100}}
	got := s.LogArgs()
	want := []any{"total", 612, "errors", 2, "status_200", 500, "status_429", 100, "status_500", 12}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LogArgs() = %v, want %v", got, want)
	}
}
