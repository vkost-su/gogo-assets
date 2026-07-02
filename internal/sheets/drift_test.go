package sheets

import "testing"

// TestDriftSubset is the drift-tab filter: a clean row (key not in the drift set)
// is absent from the companion; a drifting row is present; order is preserved.
func TestDriftSubset(t *testing.T) {
	type row struct {
		id   string
		name string
	}
	rows := []row{{"u1", "clean"}, {"u2", "drift"}, {"u3", "clean"}, {"u4", "drift"}}
	drifted := map[string]struct{}{"u2": {}, "u4": {}}

	got := driftSubset(rows, func(r row) string { return r.id }, drifted)

	if len(got) != 2 {
		t.Fatalf("drift subset = %d rows, want 2", len(got))
	}
	if got[0].id != "u2" || got[1].id != "u4" {
		t.Errorf("drift subset = %v, want u2,u4 in order", got)
	}
	// Clean users must be absent.
	for _, r := range got {
		if r.name == "clean" {
			t.Errorf("clean row %q leaked into drift subset", r.id)
		}
	}
}

// TestDriftSubsetEmptySet: no drift ⇒ empty companion (skip-empty upstream).
func TestDriftSubsetEmptySet(t *testing.T) {
	type row struct{ id string }
	rows := []row{{"a"}, {"b"}}
	if got := driftSubset(rows, func(r row) string { return r.id }, map[string]struct{}{}); len(got) != 0 {
		t.Errorf("empty drift set = %d rows, want 0", len(got))
	}
}
