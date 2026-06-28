package jumpcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// _baseSaaS is the JumpCloud AI & SaaS Management surface. It shares the v2 host
// and x-api-key auth but uses a distinct pagination model (limit/offset with a
// {results,totalCount} envelope) — see paginateSaaS.
const _baseSaaS = _baseV2 + "/saas-management"

// saasEnvelope is the common SaaS list response shape.
type saasEnvelope struct {
	Results    []map[string]any `json:"results"`
	TotalCount int              `json:"totalCount"`
}

// ── Applications ──────────────────────────────────────────────────────────────

// ListSaaSApps returns every discovered SaaS application (paginated). The
// discovery_source_types expansion is requested so each entry carries how its
// accounts were discovered. Returns nil when SaaS Management is unlicensed.
func (c *Client) ListSaaSApps(ctx context.Context) ([]map[string]any, error) {
	c.queries.Record("GET /api/v2/saas-management/applications")
	return c.paginateSaaS(ctx, _baseSaaS+"/applications",
		url.Values{"expand": []string{"discovery_source_types"}}, true)
}

// GetSaaSApp returns the detail for one application, including its name and (via
// the sso_apps expansion) the associated SSO connections.
func (c *Client) GetSaaSApp(ctx context.Context, appID string) (map[string]any, error) {
	c.queries.Record("GET /api/v2/saas-management/applications/{id}?expand=sso_apps")
	return c.getJSON(ctx, _baseSaaS+"/applications/"+appID+"?expand=sso_apps")
}

// ListSaaSAccounts returns the owner accounts found inside one application
// (paginated). Returns nil when unlicensed.
func (c *Client) ListSaaSAccounts(ctx context.Context, appID string) ([]map[string]any, error) {
	c.queries.Record("GET /api/v2/saas-management/applications/{id}/accounts")
	return c.paginateSaaS(ctx, _baseSaaS+"/applications/"+appID+"/accounts", nil, true)
}

// GetSaaSUsage returns per-account last-used timestamps for one application over
// the trailing dayCount days (1..90). Returns nil when unlicensed.
func (c *Client) GetSaaSUsage(ctx context.Context, appID string, dayCount int) ([]map[string]any, error) {
	if dayCount < 1 {
		dayCount = 1
	}
	if dayCount > 90 {
		dayCount = 90
	}
	c.queries.Record("GET /api/v2/saas-management/applications/{id}/usage?day_count")
	return c.paginateSaaS(ctx, _baseSaaS+"/applications/"+appID+"/usage",
		url.Values{"day_count": []string{strconv.Itoa(dayCount)}}, true)
}

// ── Licenses ──────────────────────────────────────────────────────────────────

// ListSaaSAppLicenses returns the license tiers and contract for one application.
// The response carries both a results array and a single contract object; both
// are returned. A nil contract means none was set. Returns (nil, nil) when
// unlicensed.
func (c *Client) ListSaaSAppLicenses(ctx context.Context, appID string) ([]map[string]any, map[string]any, error) {
	c.queries.Record("GET /api/v2/saas-management/application-licenses/{id}")
	var out struct {
		Results  []map[string]any `json:"results"`
		Contract map[string]any   `json:"contract"`
	}
	if err := c.getJSONInto(ctx, _baseSaaS+"/application-licenses/"+appID, &out); err != nil {
		if errors.Is(err, ErrNotLicensed) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	return out.Results, out.Contract, nil
}

// ── Catalog ───────────────────────────────────────────────────────────────────

// GetCatalogApp returns the catalog entry (name/description/domains/logo) for a
// catalog application ID — the source of human-readable service info. Returns
// (nil, nil) when the entry is absent or SaaS Management is unlicensed.
func (c *Client) GetCatalogApp(ctx context.Context, catalogID string) (map[string]any, error) {
	c.queries.Record("GET /api/v2/saas-management/application-catalog/{id}")
	m, err := c.getJSON(ctx, _baseSaaS+"/application-catalog/"+catalogID)
	if errors.Is(err, ErrNotLicensed) {
		return nil, nil
	}
	return m, err
}

// ── Internals ────────────────────────────────────────────────────────────────

// paginateSaaS drives SaaS pagination: limit/offset query params and a
// {results,totalCount} envelope. It stops on a short page or once totalCount is
// reached. allowMissing=true converts ErrNotLicensed into a nil result.
func (c *Client) paginateSaaS(ctx context.Context, base string, extra url.Values, allowMissing bool) ([]map[string]any, error) {
	var all []map[string]any
	offset := 0
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base, nil)
		if err != nil {
			return nil, err
		}
		q := req.URL.Query()
		q.Set("limit", strconv.Itoa(_pageLimit))
		q.Set("offset", strconv.Itoa(offset))
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

		var page saasEnvelope
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode saas page: %w", err)
		}
		resp.Body.Close()

		all = append(all, page.Results...)
		if len(page.Results) < _pageLimit {
			break
		}
		if page.TotalCount > 0 && len(all) >= page.TotalCount {
			break
		}
		offset += len(page.Results)
	}
	return all, nil
}

// getJSONInto is getJSON with a caller-supplied decode target, for responses
// that are not a flat map[string]any.
func (c *Client) getJSONInto(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	c.setHeaders(req)
	resp, err := c.do(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}
