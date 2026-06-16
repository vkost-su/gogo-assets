package drifttag

import (
	"testing"
	"time"

	"gogo-assets/internal/model"
)

// goodStruct exercises every category and a few untagged fields (which must be
// ignored).
type goodStruct struct {
	Serial    string     `json:"serial"        drift:"identity"`
	Encrypted *bool      `json:"disk_encrypted" drift:"monitored,sev=crit"`
	ASPCount  *int       `json:"asp_count"     drift:"monitored,sev=high"`
	LastSeen  *time.Time `json:"last_seen"     drift:"volatile"`
	Ignored   string     `json:"ignored"` // no drift tag → not returned
}

func TestFields(t *testing.T) {
	got := Fields[goodStruct]()
	want := []Field{
		{JSONName: "serial", Category: CategoryIdentity},
		{JSONName: "disk_encrypted", Category: CategoryMonitored, Severity: model.SevCrit},
		{JSONName: "asp_count", Category: CategoryMonitored, Severity: model.SevHigh},
		{JSONName: "last_seen", Category: CategoryVolatile},
	}
	if len(got) != len(want) {
		t.Fatalf("Fields returned %d fields, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("field %d = %+v, want %+v", i, got[i], w)
		}
	}
}

func TestFieldsCached(t *testing.T) {
	// Second call must return the cached slice, identical contents.
	if a, b := Fields[goodStruct](), Fields[goodStruct](); len(a) != len(b) {
		t.Fatalf("cache returned different lengths: %d vs %d", len(a), len(b))
	}
}

func TestValue(t *testing.T) {
	yes := true
	no := false
	two := 2
	now := time.Date(2026, 6, 11, 4, 0, 0, 0, time.UTC)
	v := struct {
		Serial   string     `json:"serial"`
		Enc      *bool      `json:"disk_encrypted"`
		Off      *bool      `json:"tamper"`
		MissBool *bool      `json:"missing_bool"`
		Count    *int       `json:"asp_count"`
		Seen     *time.Time `json:"last_seen"`
		Plain    int        `json:"plain"`
	}{
		Serial: "ABC123",
		Enc:    &yes,
		Off:    &no,
		Count:  &two,
		Seen:   &now,
		Plain:  7,
	}

	tests := []struct {
		give        string
		wantValue   string
		wantPresent bool
	}{
		{give: "serial", wantValue: "ABC123", wantPresent: true},
		{give: "disk_encrypted", wantValue: "true", wantPresent: true},
		{give: "tamper", wantValue: "false", wantPresent: true},   // *false ≠ nil — collected & off
		{give: "missing_bool", wantValue: "", wantPresent: false}, // nil → DATA_GAP
		{give: "asp_count", wantValue: "2", wantPresent: true},
		{give: "last_seen", wantValue: "2026-06-11T04:00:00Z", wantPresent: true},
		{give: "plain", wantValue: "7", wantPresent: true},
		{give: "no_such_field", wantValue: "", wantPresent: false},
	}
	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			gotValue, gotPresent := Value(v, tt.give)
			if gotValue != tt.wantValue || gotPresent != tt.wantPresent {
				t.Errorf("Value(%q) = (%q, %v), want (%q, %v)",
					tt.give, gotValue, gotPresent, tt.wantValue, tt.wantPresent)
			}
		})
	}
}

func TestValueNilPointer(t *testing.T) {
	var p *goodStruct
	if v, present := Value(p, "serial"); present || v != "" {
		t.Errorf("Value on nil pointer = (%q, %v), want (\"\", false)", v, present)
	}
}

// Malformed tags are programmer errors: Fields must panic, not return.
func TestFieldsPanics(t *testing.T) {
	tests := []struct {
		name string
		call func()
	}{
		{"unknown category", func() {
			type bad struct {
				X string `json:"x" drift:"sometimes"`
			}
			Fields[bad]()
		}},
		{"monitored without sev", func() {
			type bad struct {
				X *bool `json:"x" drift:"monitored"`
			}
			Fields[bad]()
		}},
		{"monitored not a pointer", func() {
			type bad struct {
				X bool `json:"x" drift:"monitored,sev=crit"`
			}
			Fields[bad]()
		}},
		{"unknown severity", func() {
			type bad struct {
				X *bool `json:"x" drift:"monitored,sev=urgent"`
			}
			Fields[bad]()
		}},
		{"volatile with option", func() {
			type bad struct {
				X string `json:"x" drift:"volatile,sev=low"`
			}
			Fields[bad]()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("Fields did not panic on %s", tt.name)
				}
			}()
			tt.call()
		})
	}
}

// The canonical model must parse without panicking — this is the startup
// guarantee the engine relies on.
func TestCanonicalModelParses(t *testing.T) {
	Fields[model.JCDevice]()
	Fields[model.JCUser]()
	Fields[model.JCPolicyEnforcement]()
	Fields[model.SophosEndpoint]()
	Fields[model.GWSUser]()
	Fields[model.GWSDevice]()
}
