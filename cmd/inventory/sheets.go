package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"gogo-assets/internal/config"
	"gogo-assets/internal/inventory"
	"gogo-assets/internal/model"
	"gogo-assets/internal/sheets"
)

// tabKey identifies a Sheets tab in both the CLI (--tabs) and the writer
// registry below.
type tabKey string

const (
	tabGW       tabKey = "gw"
	tabJC       tabKey = "jc"
	tabSaaS     tabKey = "saas"
	tabSophos   tabKey = "sophos"
	tabUsersAll tabKey = "usersall"
	tabFindings tabKey = "findings"
)

var allTabKeys = []tabKey{tabGW, tabJC, tabSaaS, tabSophos, tabUsersAll, tabFindings}

var validTab = func() map[tabKey]bool {
	m := make(map[tabKey]bool, len(allTabKeys))
	for _, k := range allTabKeys {
		m[k] = true
	}
	return m
}()

// parseTabs parses the --tabs comma list into a selection set. An empty value or
// "all" means "no filter" (nil ⇒ every eligible tab). An unknown key is an
// error so a typo never silently writes nothing.
func parseTabs(s string) (map[tabKey]bool, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	out := make(map[tabKey]bool)
	for _, part := range strings.Split(s, ",") {
		k := tabKey(strings.ToLower(strings.TrimSpace(part)))
		if k == "" {
			continue
		}
		if k == "all" {
			return nil, nil
		}
		if !validTab[k] {
			return nil, fmt.Errorf("unknown tab %q (valid: gw, jc, saas, sophos, usersall, findings, all)", part)
		}
		out[k] = true
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// targetTabs maps a collection target to the tabs the auto-write path may write:
// gw→GW, jc→JC+SaaS, sp→Sophos, all→everything.
func targetTabs(target string) func(tabKey) bool {
	var set map[tabKey]bool
	switch target {
	case "gw":
		set = map[tabKey]bool{tabGW: true}
	case "jc":
		set = map[tabKey]bool{tabJC: true, tabSaaS: true}
	case "sp":
		set = map[tabKey]bool{tabSophos: true}
	default: // "all"
		set = map[tabKey]bool{tabGW: true, tabJC: true, tabSaaS: true, tabSophos: true, tabUsersAll: true, tabFindings: true}
	}
	return func(k tabKey) bool { return set[k] }
}

// allTargets is the target gate for the publish path: every tab is eligible
// (the selection and data-availability gates still apply).
func allTargets(tabKey) bool { return true }

// sheetTab is one entry in the writer registry.
type sheetTab struct {
	key   tabKey
	name  string
	has   bool         // data is available for this tab
	write func() error // delegates to the matching sheets.WriteXxx
}

// buildSheetTabs is the single registry of every tab and how to write it. Both
// the auto-write path and the `sheets` publish command go through this so they
// can never diverge. Every writer consumes the rich inventory.AssetInventory —
// exactly what we persist as the source of truth.
func buildSheetTabs(ctx context.Context, svc *sheets.Service, s config.Settings, inv *inventory.AssetInventory, findings []model.Finding) []sheetTab {
	return []sheetTab{
		{tabGW, s.Sheets.Worksheet, len(inv.Users) > 0, func() error { return sheets.WriteGWS(ctx, svc, s.Sheets.Worksheet, inv) }},
		{tabJC, s.Sheets.JCWorksheet, len(inv.JCSystems) > 0, func() error { return sheets.WriteJC(ctx, svc, s.Sheets.JCWorksheet, inv) }},
		{tabSaaS, s.Sheets.SaaSWorksheet, len(inv.SaaSApps) > 0, func() error { return sheets.WriteSaaS(ctx, svc, s.Sheets.SaaSWorksheet, inv) }},
		{tabSophos, s.Sheets.SophosWorksheet, len(inv.SophosEndpoints) > 0, func() error { return sheets.WriteSophos(ctx, svc, s.Sheets.SophosWorksheet, inv) }},
		{tabUsersAll, s.Sheets.MergedWorksheet, len(inv.Users) > 0, func() error { return sheets.WriteMerged(ctx, svc, s.Sheets.MergedWorksheet, inv) }},
		{tabFindings, s.Sheets.FindingsWorksheet, len(findings) > 0, func() error { return sheets.WriteFindings(ctx, svc, s.Sheets.FindingsWorksheet, findings) }},
	}
}

// writeSheetSet writes the tabs that pass all three gates: target eligibility,
// explicit --tabs selection (nil ⇒ all), and data availability. A tab with no
// data is skipped — never recreated empty — so a partial run or partial file can
// never clobber a populated tab in the spreadsheet. Per-tab errors are logged,
// not fatal.
//
// When dryRun is set, each tab that passes the gates is logged instead of
// written, and svc is never touched (it may be nil).
func writeSheetSet(
	ctx context.Context,
	log *slog.Logger,
	svc *sheets.Service,
	s config.Settings,
	inv *inventory.AssetInventory,
	findings []model.Finding,
	selected map[tabKey]bool,
	targetOK func(tabKey) bool,
	dryRun bool,
) {
	for _, t := range buildSheetTabs(ctx, svc, s, inv, findings) {
		switch {
		case !targetOK(t.key):
			continue
		case selected != nil && !selected[t.key]:
			continue
		case !t.has:
			if selected != nil && selected[t.key] {
				log.Warn("skip tab — explicitly selected but no data", "tab", t.name)
			}
			continue
		}
		if dryRun {
			log.Info("would write tab", "tab", t.name, "key", string(t.key))
			continue
		}
		if err := t.write(); err != nil {
			log.Error("sheet write failed", "tab", t.name, "err", err)
		}
	}
}

// writeSheets is the auto-write entry used after a collection run: it opens the
// Sheets service and writes the tabs relevant to target, filtered by selection.
func writeSheets(ctx context.Context, log *slog.Logger, s config.Settings, inv *inventory.AssetInventory, findings []model.Finding, target string, selected map[tabKey]bool) error {
	svc, err := sheets.Open(ctx, s.Google.SAJSONPath, s.Sheets.SpreadsheetID)
	if err != nil {
		return err
	}
	writeSheetSet(ctx, log, svc, s, inv, findings, selected, targetTabs(target), false)
	return nil
}
