// Command inventory collects asset data from Google Workspace, JumpCloud, and
// Sophos Central, assembles a canonical snapshot, runs the drift engine against
// the approved baseline, and writes the results to a local snapshot store and
// to Google Sheets.
//
// Usage:
//
//	inventory [target] [flags]
//
// Targets:
//
//	gw   Google Workspace only
//	jc   JumpCloud only
//	sp   Sophos only
//	all  all of the above (default)
//
// Flags:
//
//	--json              print the canonical snapshot JSON to stdout
//	--no-sheets         skip the Google Sheets write
//	--approve-baseline  write the baseline census from this run and skip drift
//	--prune             prune expired daily/archive tiers after the run (default true)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gogo-assets/internal/assemble"
	"gogo-assets/internal/config"
	"gogo-assets/internal/gworkspace"
	"gogo-assets/internal/inventory"
	"gogo-assets/internal/jumpcloud"
	"gogo-assets/internal/logging"
	"gogo-assets/internal/model"
	"gogo-assets/internal/sheets"
	"gogo-assets/internal/snapshot"
	"gogo-assets/internal/sophos"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	// The target (gw|jc|sp|all) is a bare positional that may appear before or
	// after the flags. Go's flag package stops at the first non-flag arg, so we
	// pull the target out first and parse the rest as flags — letting users
	// write "all --json" or "--json all" interchangeably.
	target := "all"
	targetSet := false
	var flagArgs []string
	for _, a := range args {
		if !targetSet && (a == "gw" || a == "jc" || a == "sp" || a == "all") {
			target, targetSet = a, true
			continue
		}
		flagArgs = append(flagArgs, a)
	}

	fs := flag.NewFlagSet("inventory", flag.ContinueOnError)
	emitJSON := fs.Bool("json", false, "print the canonical snapshot JSON to stdout")
	noSheets := fs.Bool("no-sheets", false, "skip the Google Sheets write")
	approve := fs.Bool("approve-baseline", false, "write the baseline census from this run and skip drift")
	approveFromCurrent := fs.Bool("approve-from-current", false, "write the baseline census from the existing local/current/snapshot.json (no collection) and exit")
	prune := fs.Bool("prune", true, "prune expired daily/archive tiers after the run")
	if err := fs.Parse(flagArgs); err != nil {
		return fmt.Errorf("parse args: %w", err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (targets: gw|jc|sp|all)", fs.Arg(0))
	}

	// Early logger so config-load errors are visible.
	logging.Configure("INFO")

	settings, err := config.Load("")
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	logging.Configure(settings.LogLevel)

	log := logging.For("main")
	runStart := time.Now()
	runTimestamp := runStart.UTC()
	runDate := runTimestamp.Format("2006-01-02")
	log.Info("starting run",
		"target", target,
		"run_date", runDate,
		"local_dir", settings.LocalDir,
		"sheets", settings.Sheets.SpreadsheetID != "",
		"log_level", settings.LogLevel)

	store := snapshot.NewStore(settings.LocalDir)

	// Re-approve the census from the snapshot already on disk, without touching
	// any API. Useful right after a collection to anchor NEW/GONE detection.
	if *approveFromCurrent {
		return approveFromCurrentSnapshot(log, store, settings.Google.AdminEmail, runTimestamp)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ── Collect ──────────────────────────────────────────────────────────────
	doneCollect := logging.Phase(log, "collect", "target", target)
	inv, src, err := collect(ctx, log, settings, target)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			log.Warn("interrupted")
			return nil
		}
		return fmt.Errorf("collection failed: %w", err)
	}
	doneCollect(
		"users", len(inv.Users),
		"jc_systems", len(inv.JCSystems),
		"sophos_endpoints", len(inv.SophosEndpoints))

	if inv.MatchStats != nil {
		log.Info("match",
			"paired", inv.MatchStats["paired"],
			"jc_only", inv.MatchStats["jc_only"],
			"sophos_only", inv.MatchStats["sophos_only"],
			"unowned", inv.MatchStats["unowned"],
			"owner_mismatch", inv.MatchStats["owner_mismatch"],
			"bare_matched", inv.MatchStats["bare_username_matched"],
			"bare_unmatched", inv.MatchStats["bare_username_unmatched"])
	}

	// ── Assemble canonical snapshot ──────────────────────────────────────────
	doneAssemble := logging.Phase(log, "assemble")
	snap := assemble.Build(src, runTimestamp, runDate)
	doneAssemble(
		"jc_devices", len(snap.JumpCloud.Devices),
		"jc_identity", len(snap.JumpCloud.Identity),
		"policy_enforcement", len(snap.JumpCloud.PolicyEnforcement),
		"sophos_endpoints", len(snap.Sophos.Endpoints),
		"gws_identity", len(snap.GoogleWorkspace.Identity),
		"gws_devices", len(snap.GoogleWorkspace.Devices))

	if *emitJSON {
		if err := json.NewEncoder(os.Stdout).Encode(snap); err != nil {
			return fmt.Errorf("stdout JSON: %w", err)
		}
	}

	// ── Persist snapshot ─────────────────────────────────────────────────────
	donePersist := logging.Phase(log, "snapshot")
	snapRes, err := store.WriteSnapshot(snap)
	if err != nil {
		log.Error("snapshot write failed", "err", err)
	} else {
		donePersist("path", snapRes.Path, "size", snapshot.HumanBytes(snapRes.SizeBytes), "run_date", runDate)
	}

	// ── Drift engine / baseline approval ───────────────────────────────────────
	var findings []model.Finding
	driftRan := false
	switch {
	case target != "all":
		log.Info("drift skipped — run target 'all' for baseline drift detection", "target", target)
	case *approve:
		doneApprove := logging.Phase(log, "approve-baseline")
		if err := approveBaseline(log, store, snap, settings.Google.AdminEmail, runTimestamp); err != nil {
			log.Error("approve baseline failed", "err", err)
		} else {
			doneApprove()
		}
	default:
		driftRan = true
		if findings, err = runDrift(log, store, snap, runTimestamp, settings.DigestMaxBytes); err != nil {
			log.Error("drift engine failed", "err", err)
		}
	}

	// ── Sheets ───────────────────────────────────────────────────────────────
	switch {
	case *noSheets:
		log.Info("--no-sheets — skipping Sheets write")
	case settings.Sheets.SpreadsheetID == "":
		log.Info("SHEETS_SPREADSHEET_ID not set — skipping Sheets write")
	default:
		doneSheets := logging.Phase(log, "sheets")
		if err := writeSheets(ctx, log, settings, inv, findings, driftRan, target); err != nil {
			log.Error("sheets write failed", "err", err)
		} else {
			doneSheets()
		}
	}

	// ── Prune retention tiers ──────────────────────────────────────────────────
	if *prune {
		donePrune := logging.Phase(log, "prune")
		if err := store.Prune(runTimestamp); err != nil {
			log.Error("prune failed", "err", err)
		} else {
			donePrune("daily_retention", "30d", "archive_retention", "180d")
		}
	}

	// ── Summary ──────────────────────────────────────────────────────────────
	log.Info("─── summary ───")
	if settings.Sheets.SpreadsheetID != "" && !*noSheets {
		log.Info("spreadsheet", "id", settings.Sheets.SpreadsheetID)
	}
	if snapRes.Path != "" {
		log.Info("snapshot", "path", snapRes.Path, "size", snapshot.HumanBytes(snapRes.SizeBytes))
	}
	log.Info("counts",
		"users", len(inv.Users),
		"systems", len(inv.JCSystems),
		"endpoints", len(inv.SophosEndpoints),
		"findings", len(findings))
	log.Info("elapsed", "time", logging.Elapsed(runStart))
	return nil
}

// collect runs the per-source collectors selected by target, finalises the
// inventory cross-source merge, and returns both the merged inventory (which
// drives the Sheets tabs) and the raw collector outputs (which feed the
// canonical snapshot assembler).
func collect(ctx context.Context, log *slog.Logger, s config.Settings, target string) (*inventory.AssetInventory, assemble.Sources, error) {
	inv := inventory.New()
	var src assemble.Sources

	needGW := target == "gw" || target == "all"
	needJC := target == "jc" || target == "all"
	needSophos := target == "sp" || target == "all"

	if needGW {
		gwsClient, err := gworkspace.New(s.Google.SAJSONPath, s.Google.AdminEmail, s.Google.CustomerID)
		if err != nil {
			return nil, src, fmt.Errorf("gws client: %w", err)
		}
		records, err := gworkspace.NewCollector(gwsClient, gworkspace.CollectorOpts{EnrichDelay: s.EnrichDelay}).CollectAll(ctx)
		if err != nil {
			return nil, src, fmt.Errorf("gws collect: %w", err)
		}
		inv.AddGoogle(records)
		src.GWS = records
	}

	if needJC {
		if s.JumpCloud.APIKey == "" {
			log.Info("JC_API_KEY not set — skipping JumpCloud")
		} else {
			jcClient := jumpcloud.New(s.JumpCloud.APIKey, s.JumpCloud.OrgID)
			systems, users, err := jumpcloud.NewCollector(jcClient, jumpcloud.CollectorOpts{}).CollectAll(ctx)
			if err != nil {
				return nil, src, fmt.Errorf("jc collect: %w", err)
			}
			inv.AddJC(systems, users)
			src.JCSystems = systems
			src.JCUsers = users
		}
	}

	if needSophos {
		if s.Sophos.ClientID == "" || s.Sophos.ClientSecret == "" {
			log.Info("SOPHOS_CLIENT_ID/SECRET not set — skipping Sophos")
		} else {
			spClient := sophos.New(s.Sophos.ClientID, s.Sophos.ClientSecret)
			if err := spClient.Bootstrap(ctx); err != nil {
				return nil, src, fmt.Errorf("sophos bootstrap: %w", err)
			}
			endpoints, err := sophos.NewCollector(spClient).CollectAll(ctx)
			if err != nil {
				return nil, src, fmt.Errorf("sophos collect: %w", err)
			}
			inv.AddSophos(endpoints)
			src.Endpoints = endpoints
		}
	}

	inv.Finalize()
	return inv, src, nil
}

func writeSheets(
	ctx context.Context,
	log *slog.Logger,
	s config.Settings,
	inv *inventory.AssetInventory,
	findings []model.Finding,
	driftRan bool,
	target string,
) error {
	svc, err := sheets.Open(ctx, s.Google.SAJSONPath, s.Sheets.SpreadsheetID)
	if err != nil {
		return err
	}

	type writeJob struct {
		when bool
		name string
		fn   func() error
	}
	jobs := []writeJob{
		{
			when: target == "gw" || target == "all",
			name: s.Sheets.Worksheet,
			fn:   func() error { return sheets.WriteGWS(ctx, svc, s.Sheets.Worksheet, inv) },
		},
		{
			when: (target == "jc" || target == "all") && len(inv.JCSystems) > 0,
			name: s.Sheets.JCWorksheet,
			fn:   func() error { return sheets.WriteJC(ctx, svc, s.Sheets.JCWorksheet, inv) },
		},
		{
			when: (target == "sp" || target == "all") && len(inv.SophosEndpoints) > 0,
			name: s.Sheets.SophosWorksheet,
			fn:   func() error { return sheets.WriteSophos(ctx, svc, s.Sheets.SophosWorksheet, inv) },
		},
		{
			when: target == "all",
			name: s.Sheets.MergedWorksheet,
			fn:   func() error { return sheets.WriteMerged(ctx, svc, s.Sheets.MergedWorksheet, inv) },
		},
		{
			when: driftRan,
			name: s.Sheets.FindingsWorksheet,
			fn:   func() error { return sheets.WriteFindings(ctx, svc, s.Sheets.FindingsWorksheet, findings) },
		},
	}
	for _, j := range jobs {
		if !j.when {
			continue
		}
		if err := j.fn(); err != nil {
			log.Error("sheet write failed", "tab", j.name, "err", err)
		}
	}
	return nil
}
