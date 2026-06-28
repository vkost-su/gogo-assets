package httpstat

import (
	"errors"
	"net/http"
	"reflect"
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

func TestStatsLogArgsSortedAscending(t *testing.T) {
	s := Stats{Total: 612, Errors: 2, ByStatus: map[int]int{500: 12, 200: 500, 429: 100}}
	got := s.LogArgs()
	want := []any{"total", 612, "errors", 2, "status_200", 500, "status_429", 100, "status_500", 12}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LogArgs() = %v, want %v", got, want)
	}
}
