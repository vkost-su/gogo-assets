package peopleforce

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"gogo-assets/internal/apiquery"
	"gogo-assets/internal/httpstat"
)

const (
	_defaultBaseURL = "https://app.peopleforce.io/api/public/v3"
	_defaultTimeout = 30 * time.Second
	_defaultMaxRPS  = 5.0 // conservative default; PF docs don't publish a limit
)

// Client is an authenticated read-only client for the PeopleForce v3 API.
// Construction is cheap and performs no I/O.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
	limiter *rate.Limiter
	queries *apiquery.Recorder
}

// New builds an authenticated client. baseURL may be empty (uses the
// production endpoint). maxRPS caps the steady request rate (≤0 ⇒ default).
// counter, when non-nil, tallies every HTTP response.
func New(apiKey, baseURL string, maxRPS float64, counter *httpstat.Counter) *Client {
	if baseURL == "" {
		baseURL = _defaultBaseURL
	}
	if maxRPS <= 0 {
		maxRPS = _defaultMaxRPS
	}
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
		baseURL: baseURL,
		http:    &http.Client{Timeout: _defaultTimeout, Transport: transport},
		limiter: rate.NewLimiter(rate.Limit(maxRPS), burst),
		queries: apiquery.New(),
	}
}

// Queries returns the concrete API query templates this client issued, sorted.
func (c *Client) Queries() []string { return c.queries.Queries() }

// ── Public list methods ──────────────────────────────────────────────────────

// ListAssets returns all assets across all pages.
func (c *Client) ListAssets(ctx context.Context) ([]map[string]any, error) {
	c.queries.Record("GET /assets")
	return c.paginatePage(ctx, "/assets")
}

// ListAssetCategories returns all asset categories.
func (c *Client) ListAssetCategories(ctx context.Context) ([]map[string]any, error) {
	c.queries.Record("GET /asset_categories")
	return c.paginatePage(ctx, "/asset_categories")
}

// ListEmployees returns all employees (active and terminated) across all pages.
// Terminated employees are included because they may still have unreturned assets
// assigned to them.
func (c *Client) ListEmployees(ctx context.Context) ([]map[string]any, error) {
	c.queries.Record("GET /employees")
	return c.paginateWithParams(ctx, "/employees",
		"employment_status[]=active&employment_status[]=terminated")
}

// ── Internals ────────────────────────────────────────────────────────────────

// paginatePage drives page-based pagination for endpoints with no extra params.
func (c *Client) paginatePage(ctx context.Context, path string) ([]map[string]any, error) {
	return c.paginateWithParams(ctx, path, "")
}

// paginateWithParams drives page-based pagination (PF v3: ?page=N;
// metadata.pages is the total page count). extraParams is appended as-is after
// the page param, e.g. "employment_status[]=active&employment_status[]=terminated".
func (c *Client) paginateWithParams(ctx context.Context, path, extraParams string) ([]map[string]any, error) {
	var all []map[string]any
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s%s?page=%d", c.baseURL, path, page)
		if extraParams != "" {
			url += "&" + extraParams
		}

		var resp struct {
			Data     []map[string]any `json:"data"`
			Metadata struct {
				Pagination struct {
					Page  int `json:"page"`
					Pages int `json:"pages"`
					Count int `json:"count"`
					Items int `json:"items"`
				} `json:"pagination"`
			} `json:"metadata"`
		}
		if err := c.doJSON(ctx, url, &resp); err != nil {
			return nil, fmt.Errorf("%s page %d: %w", path, page, err)
		}
		all = append(all, resp.Data...)
		p := resp.Metadata.Pagination
		if p.Pages == 0 || page >= p.Pages {
			break
		}
	}
	return all, nil
}

// doJSON fires a GET request with X-API-KEY auth, rate-limits before sending,
// and decodes the JSON response into out.
func (c *Client) doJSON(ctx context.Context, url string, out any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limiter: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-API-KEY", c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("network: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("GET %s returned %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}
