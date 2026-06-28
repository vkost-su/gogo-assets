// Command inventory collects asset data from Google Workspace, JumpCloud, and
// Sophos Central, assembles a canonical snapshot, runs the drift engine against
// the approved baseline, and writes the results to a local snapshot store and
// to Google Sheets.
//
// Usage:
//
//	inventory [target] [flags]
//
// Targets / commands:
//
//	gw       Google Workspace only
//	jc       JumpCloud only
//	sp       Sophos only
//	all      all of the above (default)
//	sheets   publish the persisted snapshot to Google Sheets (no collection)
//	help     print usage and exit
//	version  print build version and exit
//
// Flags:
//
//	--json              print the canonical snapshot JSON to stdout
//	--no-sheets         skip the Google Sheets write
//	--tabs              comma-separated tabs to write (gw,jc,saas,sophos,usersall,findings)
//	--run-date          (sheets) publish a dated daily mirror instead of current
//	--dry-run           (sheets) log which tabs would be written; touch no API
//	--approve-baseline  write the baseline census from this run and skip drift
//	--prune             prune expired daily/archive tiers after the run (default true)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"gogo-assets/internal/assemble"
	"gogo-assets/internal/config"
	"gogo-assets/internal/httpstat"
	"gogo-assets/internal/inventory"
	"gogo-assets/internal/jumpcloud"
	"gogo-assets/internal/logging"
	"gogo-assets/internal/model"
	"gogo-assets/internal/service"
	"gogo-assets/internal/snapshot"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	// help / version are intercepted before any flag parsing so they always
	// succeed (exit 0) and print to stdout, never to stderr.
	if len(args) > 0 {
		switch args[0] {
		case "help", "-h", "--help":
			printUsage(os.Stdout)
			return nil
		case "version", "--version":
			printVersion(os.Stdout)
			return nil
		}
	}

	// The target (gw|jc|sp|all) is a bare positional that may appear before or
	// after the flags. Go's flag package stops at the first non-flag arg, so we
	// pull the target out first and parse the rest as flags — letting users
	// write "all --json" or "--json all" interchangeably.
	target := "all"
	targetSet := false
	publishOnly := false
	var flagArgs []string
	for _, a := range args {
		if !targetSet {
			switch a {
			case "gw", "jc", "sp", "all":
				target, targetSet = a, true
				continue
			case "sheets":
				// Publish-only mode: render Sheets from the persisted snapshot,
				// no collection. target is left unused in this branch.
				publishOnly, targetSet = true, true
				continue
			}
		}
		flagArgs = append(flagArgs, a)
	}

	fs := flag.NewFlagSet("inventory", flag.ContinueOnError)
	fs.Usage = func() { printUsage(os.Stdout) }
	emitJSON := fs.Bool("json", false, "print the canonical snapshot JSON to stdout")
	noSheets := fs.Bool("no-sheets", false, "skip the Google Sheets write")
	tabsFlag := fs.String("tabs", "", "comma-separated tabs to write (gw,jc,saas,sophos,usersall,findings); default all")
	runDateFlag := fs.String("run-date", "", "(sheets) publish the daily/<YYYY-MM-DD>/inventory.json mirror instead of current")
	dryRun := fs.Bool("dry-run", false, "(sheets) log which tabs would be written without touching the Google API")
	approve := fs.Bool("approve-baseline", false, "write the baseline census from this run and skip drift")
	approveFromCurrent := fs.Bool("approve-from-current", false, "write the baseline census from the existing local/current/snapshot.json (no collection) and exit")
	prune := fs.Bool("prune", true, "prune expired daily/archive tiers after the run")
	if err := fs.Parse(flagArgs); err != nil {
		// -h/--help anywhere in the flags prints usage (via fs.Usage) and is not
		// an error.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse args: %w", err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (targets: gw|jc|sp|all|sheets; try `inventory help`)", fs.Arg(0))
	}

	// --run-date and --dry-run only mean something for the `sheets` command.
	if !publishOnly {
		if *runDateFlag != "" {
			return errors.New("--run-date is valid only with the sheets command")
		}
		if *dryRun {
			return errors.New("--dry-run is valid only with the sheets command")
		}
	}

	tabsSel, err := parseTabs(*tabsFlag)
	if err != nil {
		return err
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
		"persist_local", settings.PersistLocal,
		"sheets", settings.Sheets.SpreadsheetID != "",
		"log_level", settings.LogLevel)

	store := snapshot.NewStore(settings.LocalDir)

	// Local persistence is the default; a GitHub-hosted runner (ephemeral
	// filesystem) publishes to Sheets only — see config.persistLocal. For a
	// persistent run, lay out the tier tree up front so a fresh checkout or a
	// self-hosted runner has local/{baseline,current,daily,archive} ready.
	if settings.PersistLocal {
		if err := store.EnsureDirs(); err != nil {
			return fmt.Errorf("prepare local dir: %w", err)
		}
	} else {
		log.Info("ephemeral run — local storage disabled, publishing to Sheets only")
		if settings.Sheets.SpreadsheetID == "" {
			log.Warn("ephemeral run with no SHEETS_SPREADSHEET_ID — results will not be persisted anywhere")
		}
	}

	// Re-approve the census from the snapshot already on disk, without touching
	// any API. Useful right after a collection to anchor NEW/GONE detection.
	if *approveFromCurrent {
		return approveFromCurrentSnapshot(log, store, settings.Google.AdminEmail, runTimestamp)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ── Publish-only (`sheets`) ────────────────────────────────────────────────
	// Render Sheets from the persisted source of truth (current/inventory.json)
	// without collecting. --tabs limits which tabs are written.
	if publishOnly {
		doneSheets := logging.Phase(log, "sheets", "mode", "publish")
		if err := publishSheets(ctx, log, settings, tabsSel, *runDateFlag, *dryRun); err != nil {
			return err
		}
		doneSheets()
		return nil
	}

	// ── Collect ──────────────────────────────────────────────────────────────
	// One HTTP counter is shared by every collector's client, so the final
	// report can show total request volume and the per-status breakdown.
	httpCounter := httpstat.New()
	doneCollect := logging.Phase(log, "collect", "target", target)
	inv, src, err := collect(ctx, log, settings, target, httpCounter)
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

	log.Info("http requests", httpCounter.Snapshot().LogArgs()...)

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
		"saas_apps", len(snap.JumpCloud.SaaS),
		"sophos_endpoints", len(snap.Sophos.Endpoints),
		"gws_identity", len(snap.GoogleWorkspace.Identity),
		"gws_devices", len(snap.GoogleWorkspace.Devices))

	if *emitJSON {
		if err := json.NewEncoder(os.Stdout).Encode(snap); err != nil {
			return fmt.Errorf("stdout JSON: %w", err)
		}
	}

	// ── Persist snapshot ─────────────────────────────────────────────────────
	// All local writes are skipped on an ephemeral (Sheets-only) run; the Sheets
	// write below renders from the in-memory inventory + findings, never disk.
	var snapRes snapshot.Result
	if settings.PersistLocal {
		donePersist := logging.Phase(log, "snapshot")
		if snapRes, err = store.WriteSnapshot(snap); err != nil {
			log.Error("snapshot write failed", "err", err)
		} else {
			donePersist("path", snapRes.Path, "size", snapshot.HumanBytes(snapRes.SizeBytes), "run_date", runDate)
		}

		// Persist the rich inventory — the source of truth the Sheets tabs render
		// from, on demand via the `sheets` command.
		if invRes, err := store.WriteInventory(inv, runDate); err != nil {
			log.Error("inventory write failed", "err", err)
		} else {
			log.Info("inventory persisted", "path", invRes.Path, "size", snapshot.HumanBytes(invRes.SizeBytes))
		}

		// Persist the standalone SaaS export — the full nested structures behind
		// the SaaS tab, as a self-contained file. Written only when SaaS apps were
		// collected, so an unlicensed/partial run never clobbers a populated file.
		if len(inv.SaaSApps) > 0 {
			export := jumpcloud.NewSaaSExport(inv.SaaSApps, runDate, runTimestamp)
			if saasRes, err := store.WriteSaaS(export, runDate); err != nil {
				log.Error("saas export write failed", "err", err)
			} else {
				log.Info("saas export persisted", "path", saasRes.Path, "apps", len(inv.SaaSApps), "size", snapshot.HumanBytes(saasRes.SizeBytes))
			}
		}
	}

	// ── Drift engine / baseline approval ───────────────────────────────────────
	var findings []model.Finding
	switch {
	case target != "all":
		log.Info("drift skipped — run target 'all' for baseline drift detection", "target", target)
	case *approve:
		if !settings.PersistLocal {
			log.Warn("--approve-baseline ignored on an ephemeral run — the baseline write would be discarded; run on a persistent host and commit local/baseline")
			break
		}
		doneApprove := logging.Phase(log, "approve-baseline")
		if err := approveBaseline(log, store, snap, settings.Google.AdminEmail, runTimestamp); err != nil {
			log.Error("approve baseline failed", "err", err)
		} else {
			doneApprove()
		}
	default:
		if findings, err = runDrift(log, store, snap, runTimestamp, settings.DigestMaxBytes, settings.PersistLocal); err != nil {
			log.Error("drift engine failed", "err", err)
		}
	}

	// Persist findings every run (nil/empty when drift didn't run) so the
	// `sheets` command's Findings tab always matches the latest run. Skipped on
	// an ephemeral run — there is no later `sheets` republish to feed.
	if settings.PersistLocal {
		if _, err := store.WriteCurrentJSON("findings.json", findings); err != nil {
			log.Error("findings write failed", "err", err)
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
		if err := writeSheets(ctx, log, settings, inv, findings, target, tabsSel); err != nil {
			log.Error("sheets write failed", "err", err)
		} else {
			doneSheets()
		}
	}

	// ── Prune retention tiers ──────────────────────────────────────────────────
	// Nothing to prune on an ephemeral run — the tiers were never written.
	if *prune && settings.PersistLocal {
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
		"saas_apps", len(inv.SaaSApps),
		"endpoints", len(inv.SophosEndpoints),
		"findings", len(findings))
	logProvenance(log, snap.Provenance)
	log.Info("http requests", httpCounter.Snapshot().LogArgs()...)
	log.Info("elapsed", "time", logging.Elapsed(runStart))
	return nil
}

// printUsage writes the structured CLI reference to w. It is shared by the
// `help` command and the flag package's usage hook so the two never diverge.
func printUsage(w io.Writer) {
	const usage = `inventory — collect Google Workspace, JumpCloud & Sophos assets, run drift,
publish to Google Sheets.

Usage:
  inventory [target|command] [flags]

Targets (collect, then auto-publish the matching tabs):
  gw        Google Workspace only
  jc        JumpCloud only (incl. SaaS App Management)
  sp        Sophos Central only
  all       all of the above + drift engine (default)

Commands:
  sheets    republish persisted data to Sheets — no collection
  help      print this usage and exit
  version   print build version and exit

Flags:
  --json              print the canonical snapshot JSON to stdout
  --no-sheets         skip the Google Sheets write
  --tabs <list>       comma-separated tabs to write (default: all eligible)
  --run-date <date>   (sheets) publish daily/<YYYY-MM-DD>/inventory.json instead of current
  --dry-run           (sheets) log which tabs would be written; touch no API
  --approve-baseline  write the baseline census from this run and skip drift
  --approve-from-current  re-approve the census from the on-disk snapshot, then exit
  --prune             prune expired daily/archive tiers after the run (default true)

--tabs keys:
  gw         Google Workspace users
  jc         JumpCloud systems
  saas       JumpCloud SaaS App Management
  sophos     Sophos endpoints
  usersall   cross-source per-user summary
  findings   drift-engine findings

Examples:
  inventory all                       full run: collect, correlate, drift, sheets
  inventory all --no-sheets           collect + drift, skip Sheets
  inventory gw                        collect Google Workspace only
  inventory all --json --no-sheets    print the canonical snapshot to stdout
  inventory all --approve-baseline    anchor the census for NEW/GONE detection
  inventory sheets                    republish every populated tab from the last run
  inventory sheets --tabs jc,saas     rewrite only those two tabs
  inventory sheets --run-date 2026-06-15   republish a specific dated snapshot
  inventory sheets --dry-run          show which tabs would be written

Targets and flags are order-independent. The drift engine runs only on 'all'.
`
	fmt.Fprint(w, usage)
}

// printVersion writes build provenance from the embedded Go build info
// (module version + VCS revision/time), with no external dependencies.
func printVersion(w io.Writer) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		fmt.Fprintln(w, "inventory (build info unavailable)")
		return
	}

	version := info.Main.Version
	if version == "" || version == "(devel)" {
		version = "devel"
	}

	var rev, vcsTime string
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			vcsTime = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}

	fmt.Fprintf(w, "inventory %s\n", version)
	if rev != "" {
		if len(rev) > 12 {
			rev = rev[:12]
		}
		if modified {
			rev += "-dirty"
		}
		fmt.Fprintf(w, "  revision: %s\n", rev)
	}
	if vcsTime != "" {
		fmt.Fprintf(w, "  built:    %s\n", vcsTime)
	}
	fmt.Fprintf(w, "  go:       %s\n", info.GoVersion)
}

// collect runs the per-source collectors selected by target, finalises the
// inventory cross-source merge, and returns both the merged inventory (which
// drives the Sheets tabs) and the raw collector outputs (which feed the
// canonical snapshot assembler).
func collect(ctx context.Context, log *slog.Logger, s config.Settings, target string, httpCounter *httpstat.Counter) (*inventory.AssetInventory, assemble.Sources, error) {
	inv, src, _, err := service.Collect(ctx, service.DefaultRegistry(), service.Runtime{
		Settings:    s,
		HTTPCounter: httpCounter,
		Log:         log,
	}, target)
	return inv, src, err
}

// logProvenance logs the concrete API query manifest per service, skipping any
// service that issued none (skipped collector). Each line lists the deduplicated
// endpoint templates that service actually hit this run.
func logProvenance(log *slog.Logger, p model.Provenance) {
	for _, svc := range []struct {
		name    string
		queries []string
	}{
		{"google_workspace", p.GoogleWorkspace},
		{"jumpcloud", p.JumpCloud},
		{"sophos", p.Sophos},
	} {
		if len(svc.queries) > 0 {
			log.Info("api queries", "service", svc.name, "count", len(svc.queries), "endpoints", svc.queries)
		}
	}
}
