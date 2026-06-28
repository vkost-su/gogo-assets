package jumpcloud

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"gogo-assets/internal/collector"
	"gogo-assets/internal/logging"
)

// Compile-time check that Collector satisfies the collector.Collector interface.
var _ collector.Collector = (*Collector)(nil)

// Output holds the results of a completed JumpCloud collection.
type Output struct {
	Systems  []System        `json:"systems"`
	Users    map[string]User `json:"users"`
	SaaSApps []SaaSApp       `json:"saas_apps"`
	Queries  []string        `json:"queries"` // concrete API query templates issued this run
}

// CollectorOpts tunes the per-system fan-out. Zero values fall back to defaults.
type CollectorOpts struct {
	MaxWorkers    int // default: 8
	SaaSUsageDays int // trailing usage window for SaaS App Management, 1..90; default 30
}

// Collector runs the JumpCloud 2-stage pipeline.
//
// Stage 1: list systems + list users in parallel (2 goroutines).
//
// Stage 2: per system, enrich with v1 detail, v2 user bindings, policy
// statuses, and System Insights tables (parallel fan-out, up to MaxWorkers).
//
// After the system/user pipeline, the SaaS App Management surface is collected
// with a best-effort pass (unlicensed or failed → empty, never fatal).
type Collector struct {
	client        *Client
	out           *Output
	maxWorkers    int
	saasUsageDays int
	log           *slog.Logger
}

// NewCollector wraps an authenticated client. out is the sink that CollectAll
// writes results into; the caller retains a typed reference for downstream use.
func NewCollector(c *Client, out *Output, opts CollectorOpts) *Collector {
	if opts.MaxWorkers <= 0 {
		opts.MaxWorkers = 8
	}
	if opts.SaaSUsageDays <= 0 {
		opts.SaaSUsageDays = 30
	}
	return &Collector{
		client:        c,
		out:           out,
		maxWorkers:    opts.MaxWorkers,
		saasUsageDays: opts.SaaSUsageDays,
		log:           logging.For("jc"),
	}
}

// Name returns the short collector identifier used in logs.
func (c *Collector) Name() string { return "jc" }

// CollectAll runs the systems+users pipeline followed by a best-effort SaaS
// collection, writing all results into c.out.
func (c *Collector) CollectAll(ctx context.Context) error {
	start := time.Now()
	systemsRaw, usersRaw, err := c.stage1Bulk(ctx)
	if err != nil {
		return err
	}

	usersByID := make(map[string]User, len(usersRaw))
	for _, u := range usersRaw {
		uid := asString(u["_id"])
		if uid == "" {
			continue
		}
		usersByID[uid] = MapUser(u)
	}

	systems, err := c.stage2Enrich(ctx, systemsRaw, usersByID)
	if err != nil {
		return err
	}

	usersByEmail := make(map[string]User, len(usersByID))
	for _, u := range usersByID {
		if u.Email != "" {
			usersByEmail[u.Email] = u
		}
	}
	c.log.Info("complete",
		"systems", len(systems),
		"users", len(usersByEmail),
		"elapsed", logging.Elapsed(start))

	c.out.Systems = systems
	c.out.Users = usersByEmail

	// SaaS App Management is an optional surface: unlicensed or failed folds to
	// empty and never aborts the run.
	saasApps, err := NewSaaSCollector(c.client, SaaSCollectorOpts{UsageDays: c.saasUsageDays}).CollectAll(ctx, usersByEmail)
	if err != nil {
		c.log.Warn("saas collect failed — continuing without SaaS", "err", err)
	} else {
		c.out.SaaSApps = saasApps
	}

	// Recorded last so the manifest covers the system, directory, and SaaS
	// endpoints that share this one client.
	c.out.Queries = c.client.Queries()
	return nil
}

// ── Stage 1 ──────────────────────────────────────────────────────────────────

func (c *Collector) stage1Bulk(ctx context.Context) ([]map[string]any, []map[string]any, error) {
	var (
		systems []map[string]any
		users   []map[string]any
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		s, err := c.client.ListSystems(gctx)
		if err != nil {
			return err
		}
		systems = s
		return nil
	})
	g.Go(func() error {
		u, err := c.client.ListUsers(gctx)
		if err != nil {
			return err
		}
		users = u
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, nil, err
	}
	c.log.Info("discovered", "systems", len(systems), "users", len(users))
	return systems, users, nil
}

// ── Stage 2 ──────────────────────────────────────────────────────────────────

func (c *Collector) stage2Enrich(
	ctx context.Context,
	systemsRaw []map[string]any,
	usersByID map[string]User,
) ([]System, error) {
	total := len(systemsRaw)
	start := time.Now()

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(c.maxWorkers)

	var (
		mu       sync.Mutex
		results  = make([]System, 0, total)
		done     int
		failures int
	)

	for _, raw := range systemsRaw {
		raw := raw // capture
		g.Go(func() error {
			result, err := c.enrichOne(gctx, raw, usersByID)
			mu.Lock()
			done++
			if err != nil {
				failures++
				c.log.Error("enrichment failed",
					"system_id", asString(raw["_id"]),
					"err", err)
				mu.Unlock()
				// Don't propagate: one bad system must not kill the whole run.
				return nil
			}
			sw := len(result.Apps) + len(result.Programs) +
				len(result.DEBPackages) + len(result.RPMPackages)
			results = append(results, result)
			c.log.Info("enriched",
				"i", done, "total", total,
				"host", truncate(result.Hostname, 32),
				"owner", emailOrDash(result.OwnerEmail),
				"software", sw)
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	c.log.Info("stage2 complete",
		"enriched", len(results),
		"total", total,
		"failed", failures,
		"elapsed", logging.Elapsed(start))
	return results, nil
}

func (c *Collector) enrichOne(
	ctx context.Context,
	raw map[string]any,
	usersByID map[string]User,
) (System, error) {
	systemID := asString(raw["_id"])
	if systemID == "" {
		return System{}, errors.New("system row missing _id")
	}

	ownerEmail := c.resolveOwner(ctx, systemID, usersByID)

	// System detail — merge over the list-level fields.
	merged := cloneMap(raw)
	if detail, err := c.client.GetSystem(ctx, systemID); err == nil {
		for k, v := range detail {
			if _, exists := merged[k]; !exists {
				merged[k] = v
			}
		}
		merged["mdm"] = detail["mdm"]
		if merged["mdm"] == nil {
			merged["mdm"] = map[string]any{}
		}
		merged["policyStats"] = detail["policyStats"]
	}

	siEnabled := false
	if sysIns := asMap(merged["systemInsights"]); sysIns != nil {
		siEnabled = asString(sysIns["state"]) == "enabled"
	}

	policyStatuses := safeList(c.client.GetPolicyStatuses, ctx, systemID)
	aggStats := safeMap(c.client.GetAggregatedPolicyStats, ctx, systemID)

	// System Insights — all-OS tables.
	var (
		systemInfo     []map[string]any
		osVersionSI    []map[string]any
		diskEnc        []map[string]any
		bitlocker      []map[string]any
		ifaceDetails   []map[string]any
		localUsers     []map[string]any
		usbDevices     []map[string]any
		browserPlugins []map[string]any
		chromeExt      []map[string]any
		firefoxAddons  []map[string]any
		etcHosts       []map[string]any
		appsRaw        []map[string]any
		programsRaw    []map[string]any
		debRaw         []map[string]any
		rpmRaw         []map[string]any
		safariExt      []map[string]any
		mounts         []map[string]any
		diskInfo       []map[string]any
		blockDevices   []map[string]any
	)

	if siEnabled {
		systemInfo = safeSITable(c.client, ctx, systemID, "system_info", false)
		osVersionSI = safeSITable(c.client, ctx, systemID, "os_version", false)
		diskEnc = safeSITable(c.client, ctx, systemID, "disk_encryption", false)
		bitlocker = safeSITable(c.client, ctx, systemID, "bitlocker_info", false)
		ifaceDetails = safeSITable(c.client, ctx, systemID, "interface_details", false)
		localUsers = safeSITable(c.client, ctx, systemID, "users", false)
		usbDevices = safeSITable(c.client, ctx, systemID, "usb_devices", true)
		browserPlugins = safeSITable(c.client, ctx, systemID, "browser_plugins", false)
		chromeExt = safeSITable(c.client, ctx, systemID, "chrome_extensions", false)
		firefoxAddons = safeSITable(c.client, ctx, systemID, "firefox_addons", false)
		etcHosts = safeSITable(c.client, ctx, systemID, "etc_hosts", false)

		switch strings.ToLower(asString(merged["osFamily"])) {
		case "darwin":
			appsRaw = safeSITable(c.client, ctx, systemID, "apps", false)
			safariExt = safeSITable(c.client, ctx, systemID, "safari_extensions", false)
			mounts = safeSITable(c.client, ctx, systemID, "mounts", false)
		case "windows":
			programsRaw = safeSITable(c.client, ctx, systemID, "programs", false)
			diskInfo = safeSITable(c.client, ctx, systemID, "disk_info", false)
		case "linux":
			debRaw = safeSITable(c.client, ctx, systemID, "deb_packages", true)
			rpmRaw = safeSITable(c.client, ctx, systemID, "rpm_packages", true)
			blockDevices = safeSITable(c.client, ctx, systemID, "block_devices", true)
		}
	}

	return MapSystem(MapSystemInput{
		Raw:               merged,
		OwnerEmail:        ownerEmail,
		PolicyStatuses:    policyStatuses,
		AggPolicyStats:    aggStats,
		SystemInfo:        systemInfo,
		OSVersionSI:       osVersionSI,
		DiskEnc:           diskEnc,
		Bitlocker:         bitlocker,
		InterfaceDetails:  ifaceDetails,
		LocalUsers:        localUsers,
		USBDevicesRaw:     usbDevices,
		AppsRaw:           appsRaw,
		ProgramsRaw:       programsRaw,
		DEBRaw:            debRaw,
		RPMRaw:            rpmRaw,
		BrowserPluginsRaw: browserPlugins,
		ChromeExtRaw:      chromeExt,
		FirefoxAddonsRaw:  firefoxAddons,
		SafariExtRaw:      safariExt,
		EtcHostsRaw:       etcHosts,
		Mounts:            mounts,
		DiskInfo:          diskInfo,
		BlockDevices:      blockDevices,
	}), nil
}

func (c *Collector) resolveOwner(ctx context.Context, systemID string, usersByID map[string]User) string {
	bindings, err := c.client.GetSystemUsers(ctx, systemID)
	if err != nil {
		c.log.Warn("user binding fetch failed", "system_id", systemID, "err", err)
		return ""
	}
	for _, b := range bindings {
		uid := asString(b["id"])
		if u, ok := usersByID[uid]; ok && u.Email != "" {
			return u.Email
		}
	}
	return ""
}

// ── Internals ────────────────────────────────────────────────────────────────

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func emailOrDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// safeList calls a list-returning client method; ErrNotLicensed and any other
// error collapse to nil — caller treats unavailable data as missing, not fatal.
func safeList(
	fn func(context.Context, string) ([]map[string]any, error),
	ctx context.Context, systemID string,
) []map[string]any {
	out, err := fn(ctx, systemID)
	if err != nil {
		return nil
	}
	return out
}

// safeMap calls a map-returning client method; errors collapse to nil.
func safeMap(
	fn func(context.Context, string) (map[string]any, error),
	ctx context.Context, systemID string,
) map[string]any {
	out, err := fn(ctx, systemID)
	if err != nil {
		return nil
	}
	return out
}

// safeSITable wraps SIPerSystem / SIOrgWide with the ErrNotLicensed → nil fold.
func safeSITable(c *Client, ctx context.Context, systemID, table string, orgWide bool) []map[string]any {
	fn := c.SIPerSystem
	if orgWide {
		fn = c.SIOrgWide
	}
	out, err := fn(ctx, systemID, table)
	if err != nil {
		return nil
	}
	return out
}
