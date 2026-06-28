package gworkspace

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	admin "google.golang.org/api/admin/directory/v1"
	reports "google.golang.org/api/admin/reports/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"gogo-assets/internal/apiquery"
	"gogo-assets/internal/httpstat"
)

// Required OAuth scopes for the service-account principal under domain-wide delegation.
var _scopes = []string{
	admin.AdminDirectoryUserReadonlyScope,
	admin.AdminDirectoryUserSecurityScope,
	admin.AdminDirectoryDeviceMobileReadonlyScope,
	reports.AdminReportsAuditReadonlyScope,
}

// Client wraps the Admin SDK Directory and Reports services with
// service-account / DWD authentication.
//
// Build one per run; the embedded services are safe for concurrent use.
type Client struct {
	adminEmail string
	customerID string

	queries     *apiquery.Recorder // records the concrete endpoint templates issued
	mu          sync.Mutex
	directory   *admin.Service
	reports     *reports.Service
	credsJSON   []byte
	httpCounter *httpstat.Counter
}

// New constructs an unconnected client. Call EnsureClients (or any list method)
// before use — they lazily build the API services.
func New(saJSONPath, adminEmail, customerID string, counter *httpstat.Counter) (*Client, error) {
	data, err := os.ReadFile(saJSONPath)
	if err != nil {
		return nil, fmt.Errorf("read SA JSON: %w", err)
	}
	if customerID == "" {
		customerID = "my_customer"
	}
	return &Client{
		adminEmail:  adminEmail,
		customerID:  customerID,
		credsJSON:   data,
		httpCounter: counter,
		queries:     apiquery.New(),
	}, nil
}

// Queries returns the concrete API query templates this client issued, sorted.
// It is the Google Workspace half of the run's per-service provenance manifest.
func (c *Client) Queries() []string { return c.queries.Queries() }

// EnsureClients builds the directory + reports services with DWD impersonation.
// Idempotent — subsequent calls are no-ops.
func (c *Client) EnsureClients(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.directory != nil && c.reports != nil {
		return nil
	}

	cfg, err := google.JWTConfigFromJSON(c.credsJSON, _scopes...)
	if err != nil {
		return fmt.Errorf("parse SA JSON: %w", err)
	}
	cfg.Subject = c.adminEmail // domain-wide delegation

	ts := cfg.TokenSource(ctx)

	// Build the service options. When a counter is set, we drive auth through an
	// explicit *http.Client whose transport both injects the OAuth token and
	// tallies every response; otherwise the SDK builds its own from the token
	// source. WithHTTPClient is exclusive with WithTokenSource, so it's one path
	// or the other.
	var opts []option.ClientOption
	if c.httpCounter != nil {
		hc := &http.Client{Transport: &oauth2.Transport{
			Source: ts,
			Base:   c.httpCounter.Wrap(http.DefaultTransport),
		}}
		opts = []option.ClientOption{option.WithHTTPClient(hc)}
	} else {
		opts = []option.ClientOption{option.WithTokenSource(ts)}
	}

	if c.directory == nil {
		s, err := admin.NewService(ctx, opts...)
		if err != nil {
			return fmt.Errorf("build directory service: %w", err)
		}
		c.directory = s
	}
	if c.reports == nil {
		s, err := reports.NewService(ctx, opts...)
		if err != nil {
			return fmt.Errorf("build reports service: %w", err)
		}
		c.reports = s
	}
	return nil
}

// CustomerID exposes the configured customer ID (defaults to "my_customer").
func (c *Client) CustomerID() string { return c.customerID }

// ListUsers paginates directory.users.list for the configured customer.
func (c *Client) ListUsers(ctx context.Context) ([]*admin.User, error) {
	if err := c.EnsureClients(ctx); err != nil {
		return nil, err
	}
	c.queries.Record("GET /admin/directory/v1/users")
	var out []*admin.User
	call := c.directory.Users.List().Customer(c.customerID).MaxResults(500).Context(ctx)
	err := call.Pages(ctx, func(resp *admin.Users) error {
		out = append(out, resp.Users...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return out, nil
}

// ListEndpointDevices paginates directory.mobiledevices.list domain-wide.
func (c *Client) ListEndpointDevices(ctx context.Context) ([]*admin.MobileDevice, error) {
	if err := c.EnsureClients(ctx); err != nil {
		return nil, err
	}
	c.queries.Record("GET /admin/directory/v1/customer/{customerId}/devices/mobile")
	var out []*admin.MobileDevice
	call := c.directory.Mobiledevices.List(c.customerID).MaxResults(100).Context(ctx)
	err := call.Pages(ctx, func(resp *admin.MobileDevices) error {
		out = append(out, resp.Mobiledevices...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	return out, nil
}

// ListUserTokens returns currently-authorised OAuth tokens for one user.
// Returns an empty slice (no error) on 404 — no tokens.
func (c *Client) ListUserTokens(ctx context.Context, userEmail string) ([]*admin.Token, error) {
	if err := c.EnsureClients(ctx); err != nil {
		return nil, err
	}
	c.queries.Record("GET /admin/directory/v1/users/{userKey}/tokens")
	resp, err := c.directory.Tokens.List(userEmail).Context(ctx).Do()
	if err != nil {
		if isHTTPStatus(err, 404) {
			return nil, nil
		}
		return nil, fmt.Errorf("tokens.list %s: %w", userEmail, err)
	}
	return resp.Items, nil
}

// ListLoginActivities paginates activities.list("login") for one user
// over a time window starting `startTimeRFC3339`.
func (c *Client) ListLoginActivities(ctx context.Context, userEmail, startTimeRFC3339 string) ([]*reports.Activity, error) {
	return c.listActivities(ctx, userEmail, "login", startTimeRFC3339)
}

// ListTokenActivities paginates activities.list("token") for one user.
func (c *Client) ListTokenActivities(ctx context.Context, userEmail, startTimeRFC3339 string) ([]*reports.Activity, error) {
	return c.listActivities(ctx, userEmail, "token", startTimeRFC3339)
}

func (c *Client) listActivities(ctx context.Context, userEmail, app, startTime string) ([]*reports.Activity, error) {
	if err := c.EnsureClients(ctx); err != nil {
		return nil, err
	}
	c.queries.Record("GET /admin/reports/v1/activity/users/{userKey}/applications/" + app)
	call := c.reports.Activities.List(userEmail, app).
		StartTime(startTime).
		MaxResults(1000).
		Context(ctx)

	var out []*reports.Activity
	err := call.Pages(ctx, func(resp *reports.Activities) error {
		out = append(out, resp.Items...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("activities.list %s/%s: %w", userEmail, app, err)
	}
	return out, nil
}

// isHTTPStatus reports whether err is a Google API error with the given HTTP status.
func isHTTPStatus(err error, status int) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == status
	}
	return false
}
