package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"gogo-assets/internal/baseline"
	"gogo-assets/internal/model"
	"gogo-assets/internal/snapshot"
)

// approveFromCurrentSnapshot approves the baseline census from the snapshot
// already written to local/current/snapshot.json, without collecting again.
// This is the fast path for anchoring NEW/GONE detection right after a run.
func approveFromCurrentSnapshot(log *slog.Logger, store *snapshot.Store, approvedBy string, now time.Time) error {
	var snap model.Snapshot
	if err := store.ReadJSON(&snap, "current", "snapshot.json"); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no current/snapshot.json under %s — run a collection first", store.CurrentDir())
		}
		return fmt.Errorf("read current snapshot: %w", err)
	}
	log.Info("approving baseline from existing snapshot", "run_date", snap.RunDate)
	return approveBaseline(log, store, snap, approvedBy, now)
}

// approveBaseline pins the current snapshot's entity set as the approved census
// and writes baseline.meta.json. After approval, subsequent runs report
// NEW_ENTITY / ENTITY_DISAPPEARED relative to this set.
//
// It does not touch classes.json — the class taxonomy is hand-authored policy,
// not something to be auto-captured from a live run.
func approveBaseline(log *slog.Logger, store *snapshot.Store, snap model.Snapshot, approvedBy string, now time.Time) error {
	census := censusFromSnapshot(snap)
	meta := baseline.MetaFile{
		Version:    "approved-" + snap.RunDate,
		ApprovedBy: approvedBy,
		ApprovedAt: now,
		Census:     census,
	}
	if err := baseline.WriteMeta(store.BaselineDir(), meta); err != nil {
		return err
	}
	log.Info("baseline census approved",
		"version", meta.Version,
		"approved_by", approvedBy,
		"devices", len(census.Devices),
		"users", len(census.Users),
		"baseline_dir", store.BaselineDir())
	return nil
}

// censusFromSnapshot collects every classified entity's identity into a census:
// device IDs (JC system_id + Sophos endpoint_id) and user emails (JC + GWS),
// each sorted and de-duplicated for a stable, comparable anchor.
func censusFromSnapshot(snap model.Snapshot) baseline.Census {
	devices := make([]string, 0, len(snap.JumpCloud.Devices)+len(snap.Sophos.Endpoints))
	for _, d := range snap.JumpCloud.Devices {
		devices = append(devices, d.SystemID)
	}
	for _, e := range snap.Sophos.Endpoints {
		devices = append(devices, e.EndpointID)
	}

	users := make([]string, 0, len(snap.JumpCloud.Identity)+len(snap.GoogleWorkspace.Identity))
	for _, u := range snap.JumpCloud.Identity {
		users = append(users, u.Email)
	}
	for _, u := range snap.GoogleWorkspace.Identity {
		users = append(users, u.Email)
	}

	return baseline.Census{
		Devices: sortedUnique(devices),
		Users:   sortedUnique(users),
	}
}

// sortedUnique sorts in, drops empties, and removes adjacent duplicates,
// returning a non-nil slice (so the census marshals as [] not null).
func sortedUnique(in []string) []string {
	sort.Strings(in)
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if n := len(out); n > 0 && out[n-1] == s {
			continue
		}
		out = append(out, s)
	}
	return out
}
