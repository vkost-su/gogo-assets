package jumpcloud

import (
	"encoding/json"
	"os"
	"testing"
)

// saasFixture mirrors testdata/saas_apps.json — the captured shape of the
// JumpCloud AI & SaaS Management API, keyed by application id.
type saasFixture struct {
	Applications []map[string]any            `json:"applications"`
	Details      map[string]map[string]any   `json:"details"`
	Accounts     map[string][]map[string]any `json:"accounts"`
	Usage        map[string][]map[string]any `json:"usage"`
	Licenses     map[string][]map[string]any `json:"licenses"`
	Contracts    map[string]map[string]any   `json:"contracts"`
	Catalog      map[string]map[string]any   `json:"catalog"`
}

func loadSaaSFixture(t *testing.T) saasFixture {
	t.Helper()
	b, err := os.ReadFile("testdata/saas_apps.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx saasFixture
	if err := json.Unmarshal(b, &fx); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return fx
}

// assembleFromFixture replays the collector's pure mapping (mapSaaS*) over the
// fixture, mirroring SaaSCollector.enrichOne without the network client. It is
// the raw-map → SaaSApp leg of the SaaS pipeline.
func assembleFromFixture(fx saasFixture) []SaaSApp {
	catalog := make(map[string]CatalogApp, len(fx.Catalog))
	for id, raw := range fx.Catalog {
		c := mapCatalogApp(raw)
		if c.ID == "" {
			c.ID = id
		}
		catalog[id] = c
	}

	out := make([]SaaSApp, 0, len(fx.Applications))
	for _, raw := range fx.Applications {
		app := mapSaaSAppListEntry(raw)
		applySaaSAppDetail(&app, fx.Details[app.AppID])
		app.Accounts, _ = mapSaaSAccounts(fx.Accounts[app.AppID], fx.Usage[app.AppID], nil)
		app.Licenses = mapSaaSLicenses(fx.Licenses[app.AppID])
		app.Contract = mapSaaSContract(fx.Contracts[app.AppID])

		if cat, ok := catalog[app.CatalogAppID]; ok {
			if app.Name == "" {
				app.Name = cat.Name
			}
			app.Description = cat.Description
			app.Domains = cat.Domains
			app.LogoURL = cat.LogoURL
		}
		if app.Name == "" {
			app.Name = "(unknown)"
		}
		app.Category = deriveCategory(app.Name, app.Domains)
		out = append(out, app)
	}
	return out
}

// TestSaaSFixturePipeline drives the raw API form (testdata/saas_apps.json)
// through the mappers and asserts the enriched SaaSApp rollups — catching
// regressions in name resolution, license math, usage join, and SSO status.
func TestSaaSFixturePipeline(t *testing.T) {
	apps := assembleFromFixture(loadSaaSFixture(t))
	if len(apps) != 3 {
		t.Fatalf("apps = %d, want 3", len(apps))
	}
	byID := make(map[string]SaaSApp, len(apps))
	for _, a := range apps {
		byID[a.AppID] = a
	}

	// Figma: catalog-resolved name + domains, connected SSO, a finite license
	// rollup, a contract, and the latest usage across two accounts.
	figma := byID["app-figma"]
	if figma.Name != "Figma" || figma.Category != "Design" {
		t.Errorf("figma name/category = %q/%q, want Figma/Design", figma.Name, figma.Category)
	}
	if !figma.SSOConnected() {
		t.Error("figma SSO should be connected")
	}
	if total, assigned, unassigned := figma.LicenseTotals(); total != 10 || assigned != 7 || unassigned != 3 {
		t.Errorf("figma license totals = %d/%d/%d, want 10/7/3", total, assigned, unassigned)
	}
	if got := figma.LatestUsedAt().UTC().Format("2006-01-02"); got != "2026-06-16" {
		t.Errorf("figma latest used = %s, want 2026-06-16", got)
	}
	if figma.Contract == nil || figma.Contract.Cost != 1440 || figma.Contract.Currency != "USD" {
		t.Errorf("figma contract = %+v", figma.Contract)
	}

	// GitHub: unapproved, SSO present but not connected, an unlimited license
	// tier (total folds to the assigned count).
	gh := byID["app-github"]
	if gh.Category != "Dev Tools" || gh.Status != "UNAPPROVED" {
		t.Errorf("github category/status = %q/%q, want Dev Tools/UNAPPROVED", gh.Category, gh.Status)
	}
	if gh.SSOConnected() {
		t.Error("github SSO should not be connected")
	}
	if total, assigned, _ := gh.LicenseTotals(); total != 5 || assigned != 5 {
		t.Errorf("github unlimited tier totals = %d/%d, want 5/5", total, assigned)
	}

	// ChatGPT: a custom app — no catalog entry, so Name comes from the detail
	// response and Category is derived from the name alone.
	cgpt := byID["app-chatgpt"]
	if cgpt.Name != "ChatGPT" || cgpt.Category != "AI" {
		t.Errorf("chatgpt name/category = %q/%q, want ChatGPT/AI", cgpt.Name, cgpt.Category)
	}
	if cgpt.CatalogAppID != "" {
		t.Errorf("chatgpt should have no catalog id, got %q", cgpt.CatalogAppID)
	}
	if cgpt.AccountCount() != 1 {
		t.Errorf("chatgpt account count = %d, want 1", cgpt.AccountCount())
	}
}
