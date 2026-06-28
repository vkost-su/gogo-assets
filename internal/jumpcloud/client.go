package jumpcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/time/rate"

	"gogo-assets/internal/apiquery"
	"gogo-assets/internal/httpstat"
)

// ErrNotLicensed wraps 403/404 responses from optional surfaces such as
// System Insights — callers convert these into empty results, not failures.
var ErrNotLicensed = errors.New("jumpcloud: feature not licensed")

const (
	_baseV1 = "https://console.jumpcloud.com"
	_baseV2 = "https://console.jumpcloud.com/api/v2"

	_defaultTimeout = 60 * time.Second
	_maxRetries     = 5
	_backoffBase    = 2 * time.Second
	_pageLimit      = 100

	// _defaultMaxRPS is the steady request rate the client holds itself to when
	// the caller passes nothing. JumpCloud throttles bursts with 429s, so a
	// smooth rate is far faster end-to-end than firing concurrently and eating
	// exponential backoff. Tunable via JC_MAX_RPS.
	_defaultMaxRPS = 8.0
)

// Client talks to all three JumpCloud API surfaces (v1, v2, System Insights).
// Construction is cheap and performs no I/O.
//
// Pass orgID only for MSP / multi-tenant accounts.
type Client struct {
	apiKey  string
	orgID   string
	http    *http.Client
	limiter *rate.Limiter     // shared across all goroutines; smooths the request rate
	queries *apiquery.Recorder // records the concrete endpoint templates issued
}

// New builds an authenticated client. orgID may be empty for single-tenant
// accounts. maxRPS caps the steady request rate the client holds itself to
// across all concurrent callers (≤0 ⇒ _defaultMaxRPS); a single shared token
// bucket keeps every collector under JumpCloud's limit so 429-driven backoff is
// the rare exception, not the norm. counter, when non-nil, tallies every HTTP
// response for the end-of-run report.
func New(apiKey, orgID string, maxRPS float64, counter *httpstat.Counter) *Client {
	if maxRPS <= 0 {
		maxRPS = _defaultMaxRPS
	}
	// Burst = one second's worth of tokens, so a short flurry is allowed but the
	// sustained rate stays at maxRPS.
	burst := int(maxRPS)
	if burst < 1 {
		burst = 1
	}
	var transport http.RoundTripper = http.DefaultTransport
	if counter != nil {
		transport = counter.Wrap(transport)
	}
	return &Client{
		apiKey:  apiKey,
		orgID:   orgID,
		http:    &http.Client{Timeout: _defaultTimeout, Transport: transport},
		limiter: rate.NewLimiter(rate.Limit(maxRPS), burst),
		queries: apiquery.New(),
	}
}

// Queries returns the concrete API query templates this client issued, sorted.
// It is the JumpCloud half of the run's per-service provenance manifest (system,
// directory, and SaaS endpoints share one client, hence one manifest).
func (c *Client) Queries() []string { return c.queries.Queries() }

// ── v1 ───────────────────────────────────────────────────────────────────────

// ListSystems returns all enrolled systems (v1, paginated).
func (c *Client) ListSystems(ctx context.Context) ([]map[string]any, error) {
	c.queries.Record("GET /api/systems")
	return c.paginateV1(ctx, "/api/systems")
}

// ListUsers returns all system users including ssh_keys (v1, paginated).
func (c *Client) ListUsers(ctx context.Context) ([]map[string]any, error) {
	c.queries.Record("GET /api/systemusers")
	return c.paginateV1(ctx, "/api/systemusers")
}

// GetSystem returns the full detail for one system (MDM, FDE, policyStats, …).
func (c *Client) GetSystem(ctx context.Context, systemID string) (map[string]any, error) {
	c.queries.Record("GET /api/systems/{id}")
	return c.getJSON(ctx, _baseV1+"/api/systems/"+systemID)
}

// ── v2 ───────────────────────────────────────────────────────────────────────

// GetSystemUsers returns user bindings for one system (v2, paginated).
func (c *Client) GetSystemUsers(ctx context.Context, systemID string) ([]map[string]any, error) {
	c.queries.Record("GET /api/v2/systems/{id}/users")
	return c.paginateV2(ctx, _baseV2+"/systems/"+systemID+"/users", false)
}

// GetPolicyStatuses returns last-reported policy result per policy for one system.
func (c *Client) GetPolicyStatuses(ctx context.Context, systemID string) ([]map[string]any, error) {
	c.queries.Record("GET /api/v2/systems/{id}/policystatuses")
	return c.paginateV2(ctx, _baseV2+"/systems/"+systemID+"/policystatuses", false)
}

// GetAggregatedPolicyStats returns aggregated policy stats (failedPolicies,
// pendingPolicies, policyCountData).
func (c *Client) GetAggregatedPolicyStats(ctx context.Context, systemID string) (map[string]any, error) {
	c.queries.Record("GET /api/v2/systems/{id}/aggregated-policy-stats")
	return c.getJSON(ctx, _baseV2+"/systems/"+systemID+"/aggregated-policy-stats")
}

// ── System Insights — per-system path ────────────────────────────────────────

// SIPerSystem fetches a per-system System Insights table at
// /systeminsights/{system_id}/{table}. Returns nil on 403/404.
func (c *Client) SIPerSystem(ctx context.Context, systemID, table string) ([]map[string]any, error) {
	c.queries.Record("GET /api/v2/systeminsights/{id}/{table}")
	return c.paginateV2(ctx, _baseV2+"/systeminsights/"+systemID+"/"+table, true)
}

// SIOrgWide fetches a System Insights table that has no per-system endpoint
// (deb_packages, rpm_packages, block_devices, usb_devices), filtered by system_id.
// Returns nil on 403/404.
func (c *Client) SIOrgWide(ctx context.Context, systemID, table string) ([]map[string]any, error) {
	c.queries.Record("GET /api/v2/systeminsights/{table}?filter=system_id")
	u := _baseV2 + "/systeminsights/" + table
	return c.paginateV2WithExtra(ctx, u,
		url.Values{"filter": []string{"system_id:eq:" + systemID}},
		true)
}

// ── Internals ────────────────────────────────────────────────────────────────

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if c.orgID != "" {
		req.Header.Set("x-org-id", c.orgID)
	}
}

// do executes a request with exponential backoff on 429 / 5xx; returns 403/404
// as ErrNotLicensed and any other non-2xx as a generic error.
func (c *Client) do(ctx context.Context, req *http.Request) (*http.Response, error) {
	var lastErr error
	var retryAfter time.Duration // set from a Retry-After header; overrides backoff
	for attempt := 0; attempt < _maxRetries; attempt++ {
		if attempt > 0 {
			wait := backoff(attempt)
			if retryAfter > 0 {
				wait, retryAfter = retryAfter, 0
			}
			if err := sleep(ctx, wait); err != nil {
				return nil, err
			}
		}

		// Hold to the steady rate before every attempt (including retries) so the
		// whole fleet of collectors shares one budget and rarely trips 429.
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}

		// http.Request body needs re-cloning for retries; we only retry GETs (no body) here.
		r := req.Clone(ctx)

		resp, err := c.http.Do(r)
		if err != nil {
			lastErr = fmt.Errorf("network: %w", err)
			continue
		}

		switch {
		case resp.StatusCode == 429 || resp.StatusCode/100 == 5:
			// Honour Retry-After if present — it replaces the next backoff rather
			// than stacking on top of it.
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, err := strconv.Atoi(ra); err == nil {
					retryAfter = time.Duration(secs) * time.Second
				}
			}
			lastErr = fmt.Errorf("%s %s returned %d", req.Method, req.URL.Path, resp.StatusCode)
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			continue

		case resp.StatusCode == 403 || resp.StatusCode == 404:
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return nil, ErrNotLicensed

		case resp.StatusCode/100 != 2:
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("%s %s returned %d", req.Method, req.URL.Path, resp.StatusCode)
		}
		return resp, nil
	}
	if lastErr == nil {
		lastErr = errors.New("exhausted retries")
	}
	return nil, fmt.Errorf("after %d attempts: %w", _maxRetries, lastErr)
}

func (c *Client) getJSON(ctx context.Context, url string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	c.setHeaders(req)
	resp, err := c.do(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return out, nil
}

// paginateV1 drives v1 pagination: response is {"results": [...]}; end when
// the page is shorter than the limit.
func (c *Client) paginateV1(ctx context.Context, path string) ([]map[string]any, error) {
	var all []map[string]any
	skip := 0
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, _baseV1+path, nil)
		if err != nil {
			return nil, err
		}
		q := req.URL.Query()
		q.Set("limit", strconv.Itoa(_pageLimit))
		q.Set("skip", strconv.Itoa(skip))
		q.Set("sort", "_id")
		req.URL.RawQuery = q.Encode()
		c.setHeaders(req)

		resp, err := c.do(ctx, req)
		if err != nil {
			return nil, err
		}
		var page struct {
			Results []map[string]any `json:"results"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode v1 page: %w", err)
		}
		resp.Body.Close()

		all = append(all, page.Results...)
		if len(page.Results) < _pageLimit {
			break
		}
		skip += len(page.Results)
	}
	return all, nil
}

// paginateV2 drives v2 pagination: response is a bare JSON array; end on
// empty page. allowMissing=true converts ErrNotLicensed into a nil result.
func (c *Client) paginateV2(ctx context.Context, url string, allowMissing bool) ([]map[string]any, error) {
	return c.paginateV2WithExtra(ctx, url, nil, allowMissing)
}

func (c *Client) paginateV2WithExtra(ctx context.Context, base string, extra url.Values, allowMissing bool) ([]map[string]any, error) {
	var all []map[string]any
	skip := 0
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base, nil)
		if err != nil {
			return nil, err
		}
		q := req.URL.Query()
		q.Set("limit", strconv.Itoa(_pageLimit))
		q.Set("skip", strconv.Itoa(skip))
		for k, vs := range extra {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		req.URL.RawQuery = q.Encode()
		c.setHeaders(req)

		resp, err := c.do(ctx, req)
		if errors.Is(err, ErrNotLicensed) {
			if allowMissing {
				return nil, nil
			}
			return nil, err
		}
		if err != nil {
			return nil, err
		}
		var page []map[string]any
		dec := json.NewDecoder(resp.Body)
		if err := dec.Decode(&page); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode v2 page: %w", err)
		}
		resp.Body.Close()

		all = append(all, page...)
		if len(page) < _pageLimit {
			break
		}
		skip += len(page)
	}
	return all, nil
}

func backoff(attempt int) time.Duration {
	d := _backoffBase * (1 << attempt)
	// jitter: 0–1s
	d += time.Duration(rand.Int63n(int64(time.Second)))
	return d
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
