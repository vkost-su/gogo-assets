package sophos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ErrNotLicensed is returned when an optional API surface (detections, user
// directory, policies) responds with 403/404 due to missing license entitlements.
// Callers usually translate this into an empty result rather than a fatal error.
var ErrNotLicensed = errors.New("sophos: feature not licensed")

const (
	_tokenURL         = "https://id.sophos.com/api/v2/oauth2/token"
	_whoamiURL        = "https://api.central.sophos.com/whoami/v1"
	_refreshBuffer    = 5 * time.Minute
	_defaultTimeout   = 30 * time.Second
	_detectionTimeout = 60 * time.Second
)

// Client is an authenticated client for Sophos Central endpoint and common APIs.
// It is safe for concurrent use; token refresh is serialised internally.
//
// Call Bootstrap (or Run) before any other method — it runs the 3-step auth
// flow (token → whoami → discover regional URL).
type Client struct {
	clientID     string
	clientSecret string
	http         *http.Client

	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time
	tenantID    string
	baseURL     string
}

// New builds a Sophos client. It performs no I/O.
func New(clientID, clientSecret string) *Client {
	return &Client{
		clientID:     clientID,
		clientSecret: clientSecret,
		http:         &http.Client{Timeout: _defaultTimeout},
	}
}

// Bootstrap runs the 3-step auth: get token, then GET /whoami to discover the
// tenant ID and regional API host. Must be called before any list method.
func (c *Client) Bootstrap(ctx context.Context) error {
	if err := c.fetchToken(ctx); err != nil {
		return fmt.Errorf("sophos bootstrap: %w", err)
	}
	if err := c.discoverTenant(ctx); err != nil {
		return fmt.Errorf("sophos bootstrap: %w", err)
	}
	return nil
}

// ListEndpoints returns all endpoints from the regional API (cursor-paginated).
func (c *Client) ListEndpoints(ctx context.Context) ([]map[string]any, error) {
	return c.paginate(ctx, "/endpoint/v1/endpoints", url.Values{"pageSize": []string{"100"}}, false)
}

// ListAlerts returns all open alerts (cursor-paginated).
func (c *Client) ListAlerts(ctx context.Context) ([]map[string]any, error) {
	return c.paginate(ctx, "/common/v1/alerts", url.Values{"pageSize": []string{"200"}}, false)
}

// ListUsers returns all users from the Sophos directory.
// Returns ErrNotLicensed when the directory is unavailable on this license tier.
func (c *Client) ListUsers(ctx context.Context) ([]map[string]any, error) {
	return c.paginate(ctx, "/common/v1/directory/users", url.Values{"pageSize": []string{"100"}}, true)
}

// ListPolicies returns all endpoint policies. Returns ErrNotLicensed on 403/404.
func (c *Client) ListPolicies(ctx context.Context) ([]map[string]any, error) {
	return c.paginate(ctx, "/endpoint/v1/policies", url.Values{"pageSize": []string{"100"}}, true)
}

// ListDetections runs the async detections query for the last `days` days and
// returns all result items. Returns an error (not ErrNotLicensed) on submit
// failure, poll timeout, or unsupported license — the collector flips
// fetch_error=true for all endpoints when this fails.
func (c *Client) ListDetections(ctx context.Context, days int) ([]map[string]any, error) {
	now := time.Now().UTC()
	body := map[string]any{
		"dateRange": map[string]string{
			"from": now.Add(-time.Duration(days) * 24 * time.Hour).Format("2006-01-02T15:04:05.000Z"),
			"to":   now.Format("2006-01-02T15:04:05.000Z"),
		},
	}
	buf, _ := json.Marshal(body)

	// Step 1: submit
	var submit struct {
		ID string `json:"id"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/detections/v1/queries/detections",
		bytes.NewReader(buf), &submit); err != nil {
		return nil, fmt.Errorf("detections submit: %w", err)
	}
	runID := submit.ID

	// Step 2: poll up to 60s
	deadline := time.Now().Add(_detectionTimeout)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}

		var poll struct {
			Status string `json:"status"`
		}
		if err := c.doJSON(ctx, http.MethodGet,
			"/detections/v1/queries/detections/"+runID, nil, &poll); err != nil {
			return nil, fmt.Errorf("detections poll: %w", err)
		}
		switch poll.Status {
		case "succeeded":
			return c.paginate(ctx,
				"/detections/v1/queries/detections/"+runID+"/results",
				url.Values{"pageSize": []string{"200"}}, false)
		case "running", "pending", "starting":
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("detections query timed out after %s", _detectionTimeout)
			}
		default:
			return nil, fmt.Errorf("detections query ended with status %q", poll.Status)
		}
	}
}

// ── Internals ────────────────────────────────────────────────────────────────

// fetchToken POSTs client credentials and stores the access token.
func (c *Client) fetchToken(ctx context.Context) error {
	form := url.Values{
		"grant_type":    []string{"client_credentials"},
		"client_id":     []string{c.clientID},
		"client_secret": []string{c.clientSecret},
		"scope":         []string{"token"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, _tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("token network: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("token request returned %d", resp.StatusCode)
	}

	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("decode token: %w", err)
	}

	c.mu.Lock()
	c.accessToken = body.AccessToken
	expIn := body.ExpiresIn
	if expIn == 0 {
		expIn = 3600
	}
	c.tokenExpiry = time.Now().Add(time.Duration(expIn) * time.Second)
	c.mu.Unlock()
	return nil
}

// discoverTenant calls /whoami to set tenantID + baseURL.
func (c *Client) discoverTenant(ctx context.Context) error {
	var body struct {
		ID       string `json:"id"`
		APIHosts struct {
			DataRegion string `json:"dataRegion"`
		} `json:"apiHosts"`
	}
	// whoami uses absolute URL, not baseURL — call via http directly.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, _whoamiURL, nil)
	if err != nil {
		return fmt.Errorf("build whoami request: %w", err)
	}
	c.setAuthHeader(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("whoami network: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("whoami returned %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("decode whoami: %w", err)
	}

	c.mu.Lock()
	c.tenantID = body.ID
	c.baseURL = strings.TrimRight(body.APIHosts.DataRegion, "/")
	c.mu.Unlock()
	return nil
}

// ensureToken refreshes the access token if it is within _refreshBuffer of expiry.
func (c *Client) ensureToken(ctx context.Context) error {
	c.mu.Lock()
	stale := time.Now().After(c.tokenExpiry.Add(-_refreshBuffer))
	c.mu.Unlock()
	if !stale {
		return nil
	}
	return c.fetchToken(ctx)
}

// setAuthHeader writes the current Bearer + X-Tenant-ID headers under the mutex.
func (c *Client) setAuthHeader(req *http.Request) {
	c.mu.Lock()
	tok, tid := c.accessToken, c.tenantID
	c.mu.Unlock()
	req.Header.Set("Authorization", "Bearer "+tok)
	if tid != "" {
		req.Header.Set("X-Tenant-ID", tid)
	}
}

// doJSON executes a request against baseURL+path and decodes a JSON response into out.
// `body` may be nil for GET. Status 403/404 returns ErrNotLicensed.
func (c *Client) doJSON(ctx context.Context, method, path string, body io.Reader, out any) error {
	if err := c.ensureToken(ctx); err != nil {
		return err
	}

	c.mu.Lock()
	base := c.baseURL
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	c.setAuthHeader(req)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("network: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return ErrNotLicensed
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("GET %s returned %d", path, resp.StatusCode)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// paginate drives Sophos cursor pagination. allowMissing=true returns nil on
// 403/404 instead of ErrNotLicensed (mirroring the Python client_allow_missing).
func (c *Client) paginate(ctx context.Context, path string, params url.Values, allowMissing bool) ([]map[string]any, error) {
	var all []map[string]any
	cur := cloneValues(params)

	for {
		var page struct {
			Items []map[string]any `json:"items"`
			Pages struct {
				NextKey string `json:"nextKey"`
			} `json:"pages"`
		}
		fullPath := path
		if enc := cur.Encode(); enc != "" {
			fullPath += "?" + enc
		}
		err := c.doJSON(ctx, http.MethodGet, fullPath, nil, &page)
		if errors.Is(err, ErrNotLicensed) {
			if allowMissing {
				return nil, nil
			}
			return nil, err
		}
		if err != nil {
			return nil, err
		}

		all = append(all, page.Items...)
		if page.Pages.NextKey == "" {
			break
		}
		cur = cloneValues(params)
		cur.Set("pageFromKey", page.Pages.NextKey)
	}
	return all, nil
}

func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vals := range v {
		out[k] = append([]string(nil), vals...)
	}
	return out
}
