package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"gogo-assets/internal/baseline"
	"gogo-assets/internal/classify"
	"gogo-assets/internal/digest"
	"gogo-assets/internal/drift"
	"gogo-assets/internal/logging"
	"gogo-assets/internal/model"
	"gogo-assets/internal/snapshot"
)

// runDrift runs the drift engine against the approved baseline and persists its
// outputs, returning the findings (so the caller can surface them in Sheets).
//
// The pipeline is:
//
//  1. baseline.Load — the approved class taxonomy + optional census.
//  2. classify.Run  — phase 1: assign each entity to a class (emits the
//     UNCLASSIFIED / CLASS_CONFLICT coverage findings).
//  3. drift.Run     — phase 2: compare monitored fields to expectations and
//     diff the entity set against the census (BASELINE_DRIFT / DATA_GAP /
//     NEW_ENTITY / ENTITY_DISAPPEARED).
//  4. digest.Build  — roll the findings into the Claude-facing digest,
//     reconciling first_seen against the previous run.
//
// When classes.json is absent the engine is skipped (not an error): a snapshot
// without a baseline is still a valid, useful run. Drift is only meaningful on
// a full snapshot, so the caller gates this on target == "all" — a partial
// target would make the census diff flag every uncollected entity as gone.
func runDrift(log *slog.Logger, store *snapshot.Store, snap model.Snapshot, now time.Time, maxBytes int) ([]model.Finding, error) {
	b, err := baseline.Load(store.BaselineDir())
	if errors.Is(err, baseline.ErrNoBaseline) {
		log.Info("no baseline (classes.json absent) — skipping drift", "baseline_dir", store.BaselineDir())
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load baseline: %w", err)
	}
	log.Info("baseline loaded",
		"version", b.Version,
		"classes", len(b.Classes),
		"census_devices", len(b.Census.Devices),
		"census_users", len(b.Census.Users))

	// ── Phase 1: classify ──────────────────────────────────────────────────
	doneClassify := logging.Phase(log, "classify")
	results, coverage := classify.Run(snap, b, now)
	classified, unclassified := 0, 0
	for _, r := range results {
		if r.ClassID == "" {
			unclassified++
		} else {
			classified++
		}
	}
	doneClassify(
		"entities", len(results),
		"classified", classified,
		"unclassified", unclassified,
		"coverage_findings", len(coverage))

	if _, err := store.WriteCurrentJSON("classification.json", results); err != nil {
		log.Error("classification write failed", "err", err)
	}

	// ── Phase 2: compare ───────────────────────────────────────────────────
	doneDrift := logging.Phase(log, "drift")
	driftFindings := drift.Run(results, b, now)
	doneDrift("findings", len(driftFindings))

	findings := append(coverage, driftFindings...)

	// ── Phase 3: digest ────────────────────────────────────────────────────
	doneDigest := logging.Phase(log, "digest")
	prev := readPrevDigest(log, store)
	d, buf, err := digest.Build(snap, b.Version, findings, prev, now, maxBytes)
	if err != nil {
		return findings, fmt.Errorf("build digest: %w", err)
	}
	res, err := store.WriteDigest(snap.RunDate, buf)
	if err != nil {
		return findings, fmt.Errorf("write digest: %w", err)
	}
	doneDigest(
		"findings", len(d.Findings),
		"crit", d.Counts.FindingsBySeverity[string(model.SevCrit)],
		"high", d.Counts.FindingsBySeverity[string(model.SevHigh)],
		"med", d.Counts.FindingsBySeverity[string(model.SevMed)],
		"low", d.Counts.FindingsBySeverity[string(model.SevLow)],
		"truncated", d.Truncated,
		"path", res.Path,
		"size", snapshot.HumanBytes(res.SizeBytes))

	return findings, nil
}

// readPrevDigest loads the previous run's digest so digest.Build can carry
// first_seen forward for chronic findings. A missing file is the normal
// first-run case (nil, no log); any other read error is non-fatal but logged.
func readPrevDigest(log *slog.Logger, store *snapshot.Store) *digest.Digest {
	var prev digest.Digest
	switch err := store.ReadJSON(&prev, "current", "digest.json"); {
	case err == nil:
		return &prev
	case errors.Is(err, os.ErrNotExist):
		return nil
	default:
		log.Warn("could not read previous digest — first_seen will reset", "err", err)
		return nil
	}
}
