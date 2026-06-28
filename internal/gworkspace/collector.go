package gworkspace

import (
	"context"
	"log/slog"
	"time"

	"gogo-assets/internal/collector"
	"gogo-assets/internal/logging"
)

// Compile-time check that Collector satisfies the collector.Collector interface.
var _ collector.Collector = (*Collector)(nil)

// Output holds the results of a completed GWS collection.
type Output struct {
	Records map[string]*UserRecord `json:"records"` // keyed by primary email
	Queries []string               `json:"queries"` // concrete API query templates issued this run
}

const (
	defaultWindowDays = 7
	defaultEnrichGap  = 800 * time.Millisecond
)

// CollectorOpts tunes the GWS collection cadence. Zero values use sensible defaults.
type CollectorOpts struct {
	WindowDays  int           // Reports API window (default: 7)
	EnrichDelay time.Duration // pause between users (default: 0.8s)
}

// Collector runs the 4-stage GWS collection:
//
//  1. Discovery   — list users
//  2. Enrichment  — per-user login + token + tokens snapshot (sequential, rate-limited)
//  3. Devices     — list mobile devices, attach to owner
//  4. Assembly    — already done in steps 1–3
type Collector struct {
	client      *Client
	out         *Output
	windowDays  int
	enrichDelay time.Duration
	log         *slog.Logger
}

// NewCollector wraps an authenticated client. out is the sink that CollectAll
// writes results into; the caller retains a typed reference for downstream use.
func NewCollector(c *Client, out *Output, opts CollectorOpts) *Collector {
	if opts.WindowDays <= 0 {
		opts.WindowDays = defaultWindowDays
	}
	if opts.EnrichDelay <= 0 {
		opts.EnrichDelay = defaultEnrichGap
	}
	return &Collector{
		client:      c,
		out:         out,
		windowDays:  opts.WindowDays,
		enrichDelay: opts.EnrichDelay,
		log:         logging.For("gws"),
	}
}

// Name returns the short collector identifier used in logs.
func (c *Collector) Name() string { return "gws" }

// CollectAll runs the full pipeline and writes records keyed by primary email
// into c.out.Records.
func (c *Collector) CollectAll(ctx context.Context) error {
	start := time.Now()
	rawUsers, err := c.client.ListUsers(ctx)
	if err != nil {
		return err
	}
	c.log.Info("discovered users", "count", len(rawUsers))

	records := make(map[string]*UserRecord, len(rawUsers))
	for _, u := range rawUsers {
		if u.PrimaryEmail == "" {
			c.log.Warn("skipping user without primaryEmail", "id", u.Id)
			continue
		}
		records[u.PrimaryEmail] = &UserRecord{
			Identity: MapIdentity(u),
			Auth:     MapAuth(u),
		}
	}

	if err := c.enrichAll(ctx, records); err != nil {
		return err
	}
	c.attachDevices(ctx, records)

	c.log.Info("complete",
		"users", len(records),
		"elapsed", logging.Elapsed(start))
	c.out.Records = records
	c.out.Queries = c.client.Queries()
	return nil
}

// ── Stage 2 ──────────────────────────────────────────────────────────────────

func (c *Collector) enrichAll(ctx context.Context, records map[string]*UserRecord) error {
	windowEnd := time.Now().UTC()
	windowStart := windowEnd.Add(-time.Duration(c.windowDays) * 24 * time.Hour)
	startISO := windowStart.Format(time.RFC3339)

	total := len(records)
	failed := 0
	emails := make([]string, 0, total)
	for e := range records {
		emails = append(emails, e)
	}

	t0 := time.Now()
	for i, email := range emails {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		c.log.Info("enriching", "i", i+1, "total", total, "email", email)
		rec := records[email]

		if err := c.enrichOne(ctx, rec, email, startISO, windowStart, windowEnd); err != nil {
			failed++
			c.log.Error("enrichment failed", "email", email, "err", err)
		}

		if i+1 < total {
			if err := sleepCtx(ctx, c.enrichDelay); err != nil {
				return err
			}
		}
	}

	c.log.Info("enrichment complete",
		"enriched", total-failed,
		"total", total,
		"failed", failed,
		"elapsed", logging.Elapsed(t0))
	return nil
}

func (c *Collector) enrichOne(
	ctx context.Context,
	rec *UserRecord,
	email, startISO string,
	windowStart, windowEnd time.Time,
) error {
	logins, err := c.client.ListLoginActivities(ctx, email, startISO)
	if err != nil {
		return err
	}
	tokens, err := c.client.ListTokenActivities(ctx, email, startISO)
	if err != nil {
		return err
	}
	tokSnap, err := c.client.ListUserTokens(ctx, email)
	if err != nil {
		return err
	}

	la := MapLoginActivity(logins, windowStart, windowEnd)
	rec.LoginActivity = &la
	rec.OAuthGrants = MapOAuthGrants(tokens)
	rec.ConnectedApps = make([]ConnectedApp, 0, len(tokSnap))
	for _, t := range tokSnap {
		rec.ConnectedApps = append(rec.ConnectedApps, MapConnectedApp(t))
	}
	return nil
}

// ── Stage 3 ──────────────────────────────────────────────────────────────────

func (c *Collector) attachDevices(ctx context.Context, records map[string]*UserRecord) {
	rawDevices, err := c.client.ListEndpointDevices(ctx)
	if err != nil {
		c.log.Error("device fetch failed", "err", err)
		return
	}

	attached, unowned := 0, 0
	for _, d := range rawDevices {
		dev := MapEndpointDevice(d)
		if dev.OwnerEmail == "" {
			unowned++
			continue
		}
		if r, ok := records[dev.OwnerEmail]; ok {
			r.Devices = append(r.Devices, dev)
			attached++
		} else {
			unowned++
		}
	}
	c.log.Info("devices",
		"attached", attached,
		"unowned", unowned,
		"total", len(rawDevices))
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
