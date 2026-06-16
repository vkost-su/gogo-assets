// Package baseline loads the approved drift baseline: the class taxonomy that
// says which entities should look like what, plus an optional census of known
// entities used for appearance/disappearance detection.
//
// The baseline is configuration, not code (ТЗ §11): a class expresses its match
// conditions and expectations as maps keyed by canonical JSON field names, so
// changing policy never means recompiling.
//
// On-disk layout (under the baseline tier):
//
//	classes.json        { "classes": [ ... ] }            required
//	baseline.meta.json  { version, approved_*, census }   optional
package baseline

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gogo-assets/internal/drifttag"
	"gogo-assets/internal/model"
)

// ErrNoBaseline is returned by Load when classes.json is absent. Callers treat
// this as "skip the drift engine" rather than a hard failure — collection still
// produces a snapshot.
var ErrNoBaseline = errors.New("no baseline: classes.json not found")

// Baseline is the loaded, validated baseline.
type Baseline struct {
	Version    string
	ApprovedBy string
	ApprovedAt time.Time
	Classes    []Class
	Census     Census
}

// Class is one membership rule. An entity belongs to the class when every
// Match condition holds (AND). Expected lists the monitored fields the class
// pins and the value each must hold.
type Class struct {
	ID       string                 `json:"id"`
	Priority int                    `json:"priority"`
	Match    map[string]string      `json:"match"`    // json-field -> required value
	Expected map[string]Expectation `json:"expected"` // monitored json-field -> expectation
}

// Expectation is the required value for a monitored field, with an optional
// per-class severity override. When Severity is empty the field's own drift-tag
// severity is used (severity belongs to the field — ТЗ §4.2.1).
//
// It unmarshals from either a bare string ("true") or an object
// ({"value":"true","severity":"high"}) for ergonomic authoring.
type Expectation struct {
	Value    string
	Severity model.Severity
}

// UnmarshalJSON accepts "value" or {"value":..,"severity":..}.
func (e *Expectation) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		e.Value = s
		return nil
	}
	var obj struct {
		Value    string         `json:"value"`
		Severity model.Severity `json:"severity"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return fmt.Errorf("expectation must be a string or {value,severity}: %w", err)
	}
	e.Value = obj.Value
	e.Severity = obj.Severity
	return nil
}

// Census is the set of entity identifiers the baseline was approved against,
// used to detect NEW_ENTITY / ENTITY_DISAPPEARED. Empty census → those finding
// kinds are skipped.
type Census struct {
	Devices []string `json:"devices"` // system_id / endpoint_id
	Users   []string `json:"users"`   // email
}

// Load reads and validates the baseline from dir. It returns ErrNoBaseline when
// classes.json is missing; baseline.meta.json is optional.
func Load(dir string) (*Baseline, error) {
	var file struct {
		Classes []Class `json:"classes"`
	}
	if err := readJSON(filepath.Join(dir, "classes.json"), &file); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoBaseline
		}
		return nil, err
	}

	b := &Baseline{Classes: file.Classes, Version: "unversioned"}

	var meta struct {
		Version    string    `json:"version"`
		ApprovedBy string    `json:"approved_by"`
		ApprovedAt time.Time `json:"approved_at"`
		Census     Census    `json:"census"`
	}
	switch err := readJSON(filepath.Join(dir, "baseline.meta.json"), &meta); {
	case err == nil:
		if meta.Version != "" {
			b.Version = meta.Version
		}
		b.ApprovedBy = meta.ApprovedBy
		b.ApprovedAt = meta.ApprovedAt
		b.Census = meta.Census
	case errors.Is(err, os.ErrNotExist):
		// optional — leave defaults
	default:
		return nil, err
	}

	if err := b.validate(); err != nil {
		return nil, err
	}
	return b, nil
}

// validate enforces structural invariants: unique non-empty class IDs and
// Expected keys that name real monitored fields.
func (b *Baseline) validate() error {
	monitored := MonitoredFields()
	seen := make(map[string]struct{}, len(b.Classes))
	for _, c := range b.Classes {
		if c.ID == "" {
			return errors.New("baseline: class with empty id")
		}
		if _, dup := seen[c.ID]; dup {
			return fmt.Errorf("baseline: duplicate class id %q", c.ID)
		}
		seen[c.ID] = struct{}{}
		if len(c.Match) == 0 {
			return fmt.Errorf("baseline: class %q has no match conditions", c.ID)
		}
		for field := range c.Expected {
			if _, ok := monitored[field]; !ok {
				return fmt.Errorf("baseline: class %q expects unknown monitored field %q", c.ID, field)
			}
		}
	}
	return nil
}

// MonitoredFields returns the union of monitored field names across every
// classified entity type, mapped to each field's drift-tag severity. It is the
// source of truth for both baseline validation and drift severity lookup.
func MonitoredFields() map[string]model.Severity {
	out := make(map[string]model.Severity)
	collect := func(fields []drifttag.Field) {
		for _, f := range fields {
			if f.Category == drifttag.CategoryMonitored {
				out[f.JSONName] = f.Severity
			}
		}
	}
	collect(drifttag.Fields[model.JCDevice]())
	collect(drifttag.Fields[model.JCUser]())
	collect(drifttag.Fields[model.SophosEndpoint]())
	collect(drifttag.Fields[model.GWSUser]())
	return out
}

func readJSON(path string, v any) error {
	buf, err := os.ReadFile(path)
	if err != nil {
		return err // wraps os.ErrNotExist; caller matches with errors.Is
	}
	if err := json.Unmarshal(buf, v); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
