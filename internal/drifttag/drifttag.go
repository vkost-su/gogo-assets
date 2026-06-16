// Package drifttag reads the `drift` struct tags that drive the drift engine.
//
// Every canonical snapshot field that participates in drift detection carries a
// tag (see package model):
//
//	drift:"monitored,sev=crit"   compared against the baseline (crit|high|med|low)
//	drift:"volatile"             stored for context, never compared
//	drift:"identity"             a matching key (serial, email) — stored, not compared
//
// Fields walks a struct type and returns its drift fields; Value reads one
// field's canonical string form from a struct instance, distinguishing "not
// collected" (nil pointer) from a concrete value.
//
// A malformed tag is a programmer error, not a runtime condition: parsing
// panics. Because Fields caches per type and the engine warms that cache at
// startup, the panic surfaces immediately on a bad build, never mid-run.
package drifttag

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"gogo-assets/internal/model"
)

// Category is a field's drift role.
const (
	CategoryMonitored = "monitored"
	CategoryVolatile  = "volatile"
	CategoryIdentity  = "identity"
)

// Field describes one drift-tagged struct field.
type Field struct {
	JSONName string         // the field's JSON key (drift tag values match on this)
	Category string         // CategoryMonitored | CategoryVolatile | CategoryIdentity
	Severity model.Severity // populated only for monitored fields
}

var (
	_cache    sync.Map // reflect.Type -> []Field
	_timeType = reflect.TypeOf(time.Time{})
)

// Fields returns the drift fields of struct type T, parsed once and cached.
// It panics if T is not a struct or carries a malformed drift tag.
func Fields[T any]() []Field {
	var zero T
	t := reflect.TypeOf(&zero).Elem() // works even when T's zero value is nil-able
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		panic(fmt.Sprintf("drifttag: Fields requires a struct type, got %s", t.Kind()))
	}
	if cached, ok := _cache.Load(t); ok {
		return cached.([]Field)
	}
	fields := parse(t)
	_cache.Store(t, fields)
	return fields
}

// Value returns the canonical string form of the field whose JSON key is name,
// read from struct (or struct-pointer) v, and whether it is present.
//
// A nil pointer field, or a nil v, is absent (present=false) — the DATA_GAP
// signal. Non-pointer fields are always present. An unknown name is absent.
func Value(v any, name string) (value string, present bool) {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return "", false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return "", false
	}
	t := rv.Type()
	for i := 0; i < t.NumField(); i++ {
		if jsonFieldName(t.Field(i)) != name {
			continue
		}
		return stringify(rv.Field(i))
	}
	return "", false
}

// parse extracts every drift-tagged field from a struct type.
func parse(t reflect.Type) []Field {
	var out []Field
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		tag, ok := sf.Tag.Lookup("drift")
		if !ok {
			continue
		}
		cat, sev := parseTag(t, sf, tag)
		out = append(out, Field{JSONName: jsonFieldName(sf), Category: cat, Severity: sev})
	}
	return out
}

// parseTag interprets one drift tag, panicking on anything unexpected.
func parseTag(t reflect.Type, sf reflect.StructField, tag string) (category string, sev model.Severity) {
	where := fmt.Sprintf("%s.%s", t.Name(), sf.Name)
	parts := strings.Split(tag, ",")
	category = strings.TrimSpace(parts[0])

	switch category {
	case CategoryVolatile, CategoryIdentity:
		if len(parts) > 1 {
			panic(fmt.Sprintf("drifttag: %s: %q takes no options, got %q", where, category, tag))
		}
		return category, ""
	case CategoryMonitored:
		// Pointer rule (ТЗ §11): nil must be distinguishable from a zero value.
		if sf.Type.Kind() != reflect.Pointer {
			panic(fmt.Sprintf("drifttag: %s: monitored field must be a pointer, got %s", where, sf.Type.Kind()))
		}
		if len(parts) != 2 {
			panic(fmt.Sprintf("drifttag: %s: monitored requires sev=<level>, got %q", where, tag))
		}
		return category, parseSeverity(where, strings.TrimSpace(parts[1]))
	default:
		panic(fmt.Sprintf("drifttag: %s: unknown category %q", where, category))
	}
}

// parseSeverity maps a sev=<level> option to a model.Severity, panicking on a
// missing prefix or an unknown level.
func parseSeverity(where, opt string) model.Severity {
	level, ok := strings.CutPrefix(opt, "sev=")
	if !ok {
		panic(fmt.Sprintf("drifttag: %s: expected sev=<level>, got %q", where, opt))
	}
	switch level {
	case "crit":
		return model.SevCrit
	case "high":
		return model.SevHigh
	case "med":
		return model.SevMed
	case "low":
		return model.SevLow
	default:
		panic(fmt.Sprintf("drifttag: %s: unknown severity %q (want crit|high|med|low)", where, level))
	}
}

// jsonFieldName returns the field's JSON key (the json tag minus options),
// falling back to the Go field name when no json tag is present.
func jsonFieldName(sf reflect.StructField) string {
	tag := sf.Tag.Get("json")
	if tag == "" {
		return sf.Name
	}
	if name, _, _ := strings.Cut(tag, ","); name != "" {
		return name
	}
	return sf.Name
}

// stringify renders a field value to its canonical comparison string and
// reports presence. Nil pointers are absent; everything else is present.
func stringify(rv reflect.Value) (string, bool) {
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return "", false
		}
		return stringify(rv.Elem())
	case reflect.Bool:
		return strconv.FormatBool(rv.Bool()), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), true
	case reflect.String:
		return rv.String(), true
	case reflect.Slice:
		if rv.Type().Elem().Kind() == reflect.String {
			out := make([]string, rv.Len())
			for i := range out {
				out[i] = rv.Index(i).String()
			}
			return strings.Join(out, ","), true
		}
	case reflect.Struct:
		if rv.Type() == _timeType {
			return rv.Interface().(time.Time).Format(time.RFC3339), true
		}
	}
	return fmt.Sprintf("%v", rv.Interface()), true
}
