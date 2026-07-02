// Package serviceview produces the per-service full and drift views written to
// each run's dated folder (jc.json / jc-drift.json, gw.json / gw-drift.json,
// sp.json / sp-drift.json, …).
//
// It is one generic mechanism, not a per-service copy: the same DriftedIDs +
// Split + Wrap path serves every service. The full view is the whole shard; the
// drift view keeps only the entities the drift engine flagged.
//
// Records must already be sorted by their identity key — package assemble
// guarantees this — so both views are byte-stable. Like the drift engine, this
// package depends only on the standard library and package model.
package serviceview

import (
	"time"

	"gogo-assets/internal/allowlist"
	"gogo-assets/internal/model"
)

// Export is the self-describing wrapper for one per-service full or drift JSON
// file. It carries enough provenance for the external report step to consume a
// drift file standalone (schema version, run date/timestamp, count).
type Export[T any] struct {
	SchemaVersion string    `json:"schema_version"`
	Service       string    `json:"service"`           // "jumpcloud" | "sophos" | "google_workspace"
	View          string    `json:"view"`              // "full" | "drift"
	RunDate       string    `json:"run_date"`          // YYYY-MM-DD
	RunTimestamp  time.Time `json:"run_timestamp_utc"` // exact UTC instant of the run
	Count         int       `json:"count"`
	Records       []T       `json:"records"`
}

// View names, used as the Export.View value and the drift-file suffix.
const (
	ViewFull  = "full"
	ViewDrift = "drift"
)

// DriftedIDs returns the set of entity identity keys (Finding.Entity.ID) that
// carry at least one finding for the given service and entity type. The keys are
// the shard's identity field — system_id for JumpCloud/Sophos devices, email for
// users — so the caller can test a record's key for membership directly.
func DriftedIDs(findings []model.Finding, svc model.Service, etype string) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, f := range findings {
		if f.Entity.Type != etype || f.Entity.ID == "" {
			continue
		}
		if hasService(f.Service, svc) {
			ids[f.Entity.ID] = struct{}{}
		}
	}
	return ids
}

// Split partitions records into a full export (every record) and a drift export
// (only records whose identity key is in drifted). keyOf maps a record to its
// identity key; drifted comes from DriftedIDs. Both exports share the run stamp
// and are byte-stable when records is pre-sorted.
func Split[T any](records []T, keyOf func(T) string, drifted map[string]struct{},
	service, runDate string, ts time.Time) (full, drift Export[T]) {
	driftRecs := Filter(records, func(r T) bool {
		_, ok := drifted[keyOf(r)]
		return ok
	})
	return Wrap(records, service, ViewFull, runDate, ts),
		Wrap(driftRecs, service, ViewDrift, runDate, ts)
}

// Filter returns the records for which keep reports true, in their original
// order. The result is a fresh slice that never aliases records.
func Filter[T any](records []T, keep func(T) bool) []T {
	out := make([]T, 0, len(records))
	for _, r := range records {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}

// Wrap builds a self-describing Export around records. Count is always
// len(records) so a drift export with no drifting records is a valid, explicit
// "nothing to review" document.
func Wrap[T any](records []T, service, view, runDate string, ts time.Time) Export[T] {
	return Export[T]{
		SchemaVersion: model.SchemaVersion,
		Service:       service,
		View:          view,
		RunDate:       runDate,
		RunTimestamp:  ts,
		Count:         len(records),
		Records:       records,
	}
}

// SoftwareDrift returns the per-person JumpCloud software drift view. After the
// early whitelist purge at collection time, the full shard already contains
// only non-allowlisted software — there is nothing further to filter here, so
// this returns nil and drift companions stay empty.
func SoftwareDrift(people []model.JCPersonSoftware, allow *allowlist.List) []model.JCPersonSoftware {
	_ = people
	_ = allow
	return nil
}

func hasService(list []model.Service, svc model.Service) bool {
	for _, s := range list {
		if s == svc {
			return true
		}
	}
	return false
}
