package jumpcloud

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"gogo-assets/internal/logging"
)

// SaaSCollectorOpts tunes the per-application fan-out. Zero values fall back to
// defaults.
type SaaSCollectorOpts struct {
	MaxWorkers int // default: 4
	UsageDays  int // trailing usage window, 1..90; default 30
}

// SaaSCollector pulls the JumpCloud AI & SaaS Management surface: discovered
// applications enriched with catalog service info, owner accounts, usage,
// licenses, and SSO connections.
//
// The whole surface is optional — if SaaS Management is unlicensed every call
// folds to empty (the client maps 403/404 → ErrNotLicensed) and CollectAll
// returns an empty slice, mirroring System Insights.
type SaaSCollector struct {
	client     *Client
	maxWorkers int
	usageDays  int
	log        *slog.Logger
}

// NewSaaSCollector wraps an authenticated client.
func NewSaaSCollector(c *Client, opts SaaSCollectorOpts) *SaaSCollector {
	if opts.MaxWorkers <= 0 {
		opts.MaxWorkers = 4
	}
	if opts.UsageDays <= 0 {
		opts.UsageDays = 30
	}
	return &SaaSCollector{
		client:     c,
		maxWorkers: opts.MaxWorkers,
		usageDays:  opts.UsageDays,
		log:        logging.For("jc-saas"),
	}
}

// CollectAll returns every discovered SaaS application, fully enriched. users is
// the directory keyed by email (from the systems collector) and is used to
// resolve owner_user_id → owner email. A nil/empty result means SaaS Management
// is unlicensed or no apps were discovered.
func (c *SaaSCollector) CollectAll(ctx context.Context, users map[string]User) ([]SaaSApp, error) {
	start := time.Now()

	appsRaw, err := c.client.ListSaaSApps(ctx)
	if err != nil {
		return nil, err
	}
	if len(appsRaw) == 0 {
		c.log.Info("no SaaS applications (unlicensed or none discovered)")
		return nil, nil
	}

	// owner_user_id → email, from the directory.
	emailByUserID := make(map[string]string, len(users))
	for _, u := range users {
		if u.UserID != "" && u.Email != "" {
			emailByUserID[u.UserID] = u.Email
		}
	}

	apps := make([]SaaSApp, 0, len(appsRaw))
	catalogIDs := make(map[string]struct{})
	for _, raw := range appsRaw {
		a := mapSaaSAppListEntry(raw)
		if a.AppID == "" {
			continue
		}
		apps = append(apps, a)
		if a.CatalogAppID != "" {
			catalogIDs[a.CatalogAppID] = struct{}{}
		}
	}
	c.log.Info("discovered", "apps", len(apps), "catalog_apps", len(catalogIDs))

	catalog := c.fetchCatalog(ctx, catalogIDs)
	enriched := c.enrichApps(ctx, apps, catalog, emailByUserID)

	sort.Slice(enriched, func(i, j int) bool {
		if enriched[i].Category != enriched[j].Category {
			return enriched[i].Category < enriched[j].Category
		}
		return strings.ToLower(enriched[i].Name) < strings.ToLower(enriched[j].Name)
	})

	c.log.Info("complete", "apps", len(enriched), "elapsed", logging.Elapsed(start))
	return enriched, nil
}

// fetchCatalog resolves catalog service info for the distinct catalog IDs, in
// parallel. Missing/unlicensed entries are simply absent from the map.
func (c *SaaSCollector) fetchCatalog(ctx context.Context, ids map[string]struct{}) map[string]CatalogApp {
	out := make(map[string]CatalogApp, len(ids))
	if len(ids) == 0 {
		return out
	}

	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(c.maxWorkers)
	for id := range ids {
		id := id
		g.Go(func() error {
			raw, err := c.client.GetCatalogApp(gctx, id)
			if err != nil || raw == nil {
				return nil // best-effort: catalog info is optional
			}
			cat := mapCatalogApp(raw)
			if cat.ID == "" {
				cat.ID = id
			}
			mu.Lock()
			out[id] = cat
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()
	return out
}

// enrichApps fans out per-application enrichment (detail, accounts, usage,
// licenses) up to maxWorkers. One failing application never kills the run.
func (c *SaaSCollector) enrichApps(
	ctx context.Context,
	apps []SaaSApp,
	catalog map[string]CatalogApp,
	emailByUserID map[string]string,
) []SaaSApp {
	total := len(apps)
	var (
		mu      sync.Mutex
		results = make([]SaaSApp, 0, total)
		done    int
	)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(c.maxWorkers)
	for i := range apps {
		app := apps[i]
		g.Go(func() error {
			skipped := c.enrichOne(gctx, &app, catalog, emailByUserID)
			mu.Lock()
			done++
			results = append(results, app)
			c.log.Info("enriched",
				"i", done, "total", total,
				"app", truncate(app.Name, 32),
				"accounts", len(app.Accounts),
				"skipped_accounts", skipped,
				"licenses", len(app.Licenses))
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()
	return results
}

// enrichOne fills one application in place from its detail, accounts, usage, and
// license endpoints, then resolves catalog service info, owner email, and the
// derived category. It returns the number of synthetic device-agent accounts
// dropped from this app's owner accounts.
func (c *SaaSCollector) enrichOne(
	ctx context.Context,
	app *SaaSApp,
	catalog map[string]CatalogApp,
	emailByUserID map[string]string,
) int {
	if detail, err := c.client.GetSaaSApp(ctx, app.AppID); err == nil {
		applySaaSAppDetail(app, detail)
	}

	accountsRaw, _ := c.client.ListSaaSAccounts(ctx, app.AppID)
	usageRaw, _ := c.client.GetSaaSUsage(ctx, app.AppID, c.usageDays)
	accounts, dropped := mapSaaSAccounts(accountsRaw, usageRaw, emailByUserID)
	app.Accounts = accounts
	if len(dropped) > 0 {
		// These had no own identity and no resolvable device owner. Log the raw
		// fields of one so we can see whether the API offers another attribution
		// handle (a device/system id) worth wiring in.
		c.log.Debug("dropped unattributable saas accounts",
			"app", truncate(app.Name, 32),
			"count", len(dropped),
			"sample_fields", rawKeys(dropped[0]))
	}

	if licRaw, contractRaw, err := c.client.ListSaaSAppLicenses(ctx, app.AppID); err == nil {
		app.Licenses = mapSaaSLicenses(licRaw)
		app.Contract = mapSaaSContract(contractRaw)
	}

	// Catalog service info (name fallback + description/domains/logo).
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

	if email, ok := emailByUserID[app.OwnerUserID]; ok {
		app.OwnerEmail = email
	}

	app.Category = deriveCategory(app.Name, app.Domains)
	return len(dropped)
}
