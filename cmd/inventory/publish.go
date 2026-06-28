package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"gogo-assets/internal/config"
	"gogo-assets/internal/inventory"
	"gogo-assets/internal/model"
	"gogo-assets/internal/sheets"
	"gogo-assets/internal/snapshot"
)

// inventoryPath returns the store-relative path to the inventory.json the
// `sheets` command should publish: the live source of truth by default, or a
// dated daily mirror when runDate is non-empty.
func inventoryPath(runDate string) []string {
	if runDate == "" {
		return []string{"current", "inventory.json"}
	}
	return []string{"daily", runDate, "inventory.json"}
}

// loadInventoryForPublish reads the inventory the `sheets` command will render,
// selecting current/ or the dated daily mirror via runDate. It is split out from
// publishSheets so the path-selection + read can be tested without Google Sheets.
func loadInventoryForPublish(store *snapshot.Store, runDate string) (*inventory.AssetInventory, error) {
	var inv inventory.AssetInventory
	if err := store.ReadJSON(&inv, inventoryPath(runDate)...); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if runDate != "" {
				return nil, fmt.Errorf("no daily/%s/inventory.json under %s — run a collection on that date or pick an existing one", runDate, store.DailyDir())
			}
			return nil, fmt.Errorf("no current/inventory.json under %s — run a collection first", store.CurrentDir())
		}
		return nil, fmt.Errorf("read inventory: %w", err)
	}
	return &inv, nil
}

// publishSheets is the `sheets` command: it renders Google Sheets tabs from the
// persisted source of truth without collecting anything. By default it reads
// current/inventory.json; --run-date selects a dated daily mirror instead.
// findings.json supplies the Findings tab when present. selected (--tabs) limits
// which tabs are written; nil writes every populated tab. dryRun walks all gates
// and logs which tabs would be written, touching no Google API.
//
// This is the read-from-disk twin of the auto-write path — both go through
// writeSheetSet so the rendered tabs are identical.
func publishSheets(ctx context.Context, log *slog.Logger, s config.Settings, selected map[tabKey]bool, runDate string, dryRun bool) error {
	if s.Sheets.SpreadsheetID == "" {
		return errors.New("SHEETS_SPREADSHEET_ID not set — nothing to publish to")
	}

	store := snapshot.NewStore(s.LocalDir)

	inv, err := loadInventoryForPublish(store, runDate)
	if err != nil {
		return err
	}

	// Findings are optional: absent until a drift run (target "all") has occurred.
	var findings []model.Finding
	if err := store.ReadJSON(&findings, "current", "findings.json"); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Warn("could not read findings.json — Findings tab will be skipped", "err", err)
	}

	source := "current"
	if runDate != "" {
		source = "daily/" + runDate
	}
	log.Info("publishing from snapshot",
		"source", source,
		"dry_run", dryRun,
		"users", len(inv.Users),
		"jc_systems", len(inv.JCSystems),
		"saas_apps", len(inv.SaaSApps),
		"sophos_endpoints", len(inv.SophosEndpoints),
		"findings", len(findings))

	// Dry-run: walk every gate and log the verdict, but never open the Sheets
	// service. The tab closures capture a nil service and are never invoked.
	if dryRun {
		writeSheetSet(ctx, log, nil, s, inv, findings, selected, allTargets, true)
		return nil
	}

	svc, err := sheets.Open(ctx, s.Google.SAJSONPath, s.Sheets.SpreadsheetID)
	if err != nil {
		return err
	}
	writeSheetSet(ctx, log, svc, s, inv, findings, selected, allTargets, false)
	return nil
}
