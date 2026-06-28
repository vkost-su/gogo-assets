package sophos

import (
	"context"
	"errors"
	"time"

	"gogo-assets/internal/collector"
	"gogo-assets/internal/logging"
)

// Compile-time check that Collector satisfies the collector.Collector interface.
var _ collector.Collector = (*Collector)(nil)

const _detectionDays = 30

// Output holds the results of a completed Sophos collection.
type Output struct {
	Endpoints []Endpoint `json:"endpoints"`
	Queries   []string   `json:"queries"` // concrete API query templates issued this run
}

// Collector orchestrates the 6-step Sophos collection pipeline and produces
// fully-enriched Endpoint records.
type Collector struct {
	client *Client
	out    *Output
	log    interface {
		Info(msg string, args ...any)
		Warn(msg string, args ...any)
		Error(msg string, args ...any)
	}
}

// NewCollector wraps a Sophos Client. out is the sink that CollectAll writes
// results into. Bootstrap is run as the first step of CollectAll.
func NewCollector(c *Client, out *Output) *Collector {
	return &Collector{
		client: c,
		out:    out,
		log:    logging.For("sophos"),
	}
}

// Name returns the short collector identifier used in logs.
func (c *Collector) Name() string { return "sophos" }

// CollectAll runs the pipeline:
//
//  0. bootstrap      — authenticate and discover the regional API host
//  1. list_endpoints
//  2. list_users         (best-effort — for owner email)
//  3. list_alerts        (count per endpoint)
//  4. list_detections    (30d; sets fetch_error on failure)
//  5. list_policies      (build endpoint→[]policy_name map)
//  6. map_endpoint with all enrichment attached
func (c *Collector) CollectAll(ctx context.Context) error {
	if err := c.client.Bootstrap(ctx); err != nil {
		return err
	}

	start := time.Now()

	c.log.Info("fetching endpoints")
	rawEndpoints, err := c.client.ListEndpoints(ctx)
	if err != nil {
		return err
	}
	c.log.Info("discovered endpoints", "count", len(rawEndpoints))

	c.log.Info("fetching user directory")
	userEmails := c.buildUserEmailMap(ctx)

	c.log.Info("fetching alerts")
	alertCounts := c.buildAlertCounts(ctx)

	c.log.Info("fetching detections", "days", _detectionDays)
	detectionCounts, detectionErr := c.buildDetectionCounts(ctx)

	c.log.Info("fetching policies")
	policyMap := c.buildPolicyMap(ctx)

	total := len(rawEndpoints)
	endpoints := make([]Endpoint, 0, total)
	for i, raw := range rawEndpoints {
		eid := asString(raw["id"])
		person := asMap(raw["associatedPerson"])
		uid := asString(person["id"])
		email := ""
		if uid != "" {
			email = userEmails[uid]
		}
		ep := MapEndpoint(raw, alertCounts[eid], detectionCounts[eid], detectionErr, email, policyMap[eid])
		endpoints = append(endpoints, ep)

		c.log.Info("mapped",
			"i", i+1, "total", total,
			"host", ep.Hostname,
			"owner", emailOrDash(ep.OwnerEmail),
			"alerts", ep.AlertCount)
	}

	c.log.Info("complete",
		"endpoints", len(endpoints),
		"elapsed", logging.Elapsed(start))
	c.out.Endpoints = endpoints
	c.out.Queries = c.client.Queries()
	return nil
}

func emailOrDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func (c *Collector) buildUserEmailMap(ctx context.Context) map[string]string {
	users, err := c.client.ListUsers(ctx)
	if err != nil {
		c.log.Warn("user directory fetch failed", "err", err)
		return nil
	}
	out := make(map[string]string, len(users))
	for _, u := range users {
		uid := asString(u["id"])
		email := asString(u["email"])
		if uid != "" && email != "" {
			out[uid] = email
		}
	}
	c.log.Info("user directory loaded", "mappings", len(out))
	return out
}

func (c *Collector) buildAlertCounts(ctx context.Context) map[string]int {
	alerts, err := c.client.ListAlerts(ctx)
	if err != nil {
		c.log.Error("alerts fetch failed", "err", err)
		return nil
	}
	counts := make(map[string]int)
	for _, a := range alerts {
		agent := asMap(a["managedAgent"])
		if eid := asString(agent["id"]); eid != "" {
			counts[eid]++
		}
	}
	c.log.Info("alerts loaded", "alerts", len(alerts), "endpoints", len(counts))
	return counts
}

// buildDetectionCounts returns (counts, hadError). hadError=true → all
// endpoints get FetchError=true so the sheet writer shows "?" not "0".
func (c *Collector) buildDetectionCounts(ctx context.Context) (map[string]int, bool) {
	dets, err := c.client.ListDetections(ctx, _detectionDays)
	if err != nil {
		if errors.Is(err, ErrNotLicensed) {
			c.log.Warn("detections not licensed")
		} else {
			c.log.Warn("detections fetch failed", "err", err)
		}
		return nil, true
	}
	counts := make(map[string]int)
	for _, d := range dets {
		device := asMap(d["device"])
		eid := asString(device["id"])
		if eid == "" {
			eid = asString(d["endpointId"])
		}
		if eid != "" {
			counts[eid]++
		}
	}
	c.log.Info("detections loaded",
		"detections", len(dets),
		"endpoints", len(counts),
		"days", _detectionDays)
	return counts, false
}

// buildPolicyMap returns endpoint_id → []policy_name. Sophos exposes endpoint
// IDs under applied.endpoints[].id (v2) or a flat endpointIds[] in some tenants.
func (c *Collector) buildPolicyMap(ctx context.Context) map[string][]string {
	policies, err := c.client.ListPolicies(ctx)
	if err != nil {
		c.log.Warn("policies fetch failed", "err", err)
		return nil
	}
	out := make(map[string][]string)
	for _, p := range policies {
		name := asString(p["name"])
		if name == "" {
			continue
		}

		seen := make(map[string]bool)
		applied := asMap(p["applied"])
		for _, item := range asSlice(applied["endpoints"]) {
			im := asMap(item)
			if eid := asString(im["id"]); eid != "" {
				seen[eid] = true
			}
		}
		for _, eid := range asStringSlice(p["endpointIds"]) {
			seen[eid] = true
		}
		for eid := range seen {
			out[eid] = append(out[eid], name)
		}
	}
	c.log.Info("policies loaded", "policies", len(policies), "endpoints", len(out))
	return out
}
