package jumpcloud

import "time"

// SaaSApp is one application discovered by JumpCloud AI & SaaS Management, with
// every enrichment surface (catalog service info, owner accounts, license &
// contract economics, SSO connections, and usage) merged into a single record.
//
// Names are not present on the applications-list entity — they are resolved from
// the catalog (catalog apps) or the application detail (custom apps). Category
// is a derived heuristic; the public API exposes no category taxonomy.
type SaaSApp struct {
	AppID string `json:"app_id"`
	Name  string `json:"name"`

	CatalogAppID string   `json:"catalog_app_id,omitempty"`
	Category     string   `json:"category,omitempty"`
	Description  string   `json:"description,omitempty"`
	Domains      []string `json:"domains,omitempty"`
	LogoURL      string   `json:"logo_url,omitempty"`

	Status            string `json:"status,omitempty"`
	AccessRestriction string `json:"access_restriction,omitempty"`

	OwnerUserID  string    `json:"owner_user_id,omitempty"`
	OwnerEmail   string    `json:"owner_email,omitempty"`
	DiscoveredAt time.Time `json:"discovered_at,omitempty"`

	DiscoverySources []string     `json:"discovery_sources,omitempty"`
	SSOApps          []SaaSSSOApp `json:"sso_apps,omitempty"`

	Accounts []SaaSAccount `json:"accounts,omitempty"`
	Licenses []SaaSLicense `json:"licenses,omitempty"`
	Contract *SaaSContract `json:"contract,omitempty"`
}

// SSOConnected reports whether any associated SSO connection is CONNECTED.
func (a SaaSApp) SSOConnected() bool {
	for _, s := range a.SSOApps {
		if s.Status == "CONNECTED" {
			return true
		}
	}
	return false
}

// AccountCount is the number of owner accounts found in the application.
func (a SaaSApp) AccountCount() int { return len(a.Accounts) }

// LicenseTotals sums the license tiers into (total, assigned, unassigned).
// Unlimited tiers contribute their assigned count to the total.
func (a SaaSApp) LicenseTotals() (total, assigned, unassigned int) {
	for _, l := range a.Licenses {
		assigned += l.Assigned
		unassigned += l.Unassigned
		if l.IsUnlimited {
			total += l.Assigned
		} else {
			total += l.Count
		}
	}
	return total, assigned, unassigned
}

// LatestUsedAt returns the most recent usage timestamp across all accounts.
func (a SaaSApp) LatestUsedAt() time.Time {
	var latest time.Time
	for _, acc := range a.Accounts {
		if acc.LatestUsedAt.After(latest) {
			latest = acc.LatestUsedAt
		}
	}
	return latest
}

// SaaSAccount is one user account found inside a SaaS application — the
// license-owner identity. UserID links to a JumpCloud directory user when
// matched; Email/Username are the account's own credentials. LatestUsedAt is
// joined from the usage endpoint by account ID.
//
// DeviceOwner is the fallback attribution for device-agent accounts that carry
// no email/username of their own: the SaaS usage was discovered on a managed
// device, so the account is attributed to that device's owner (resolved from
// the account's UserID via the directory). Empty for accounts with their own
// identity.
type SaaSAccount struct {
	AccountID    string    `json:"account_id"`
	UserID       string    `json:"user_id,omitempty"`
	Email        string    `json:"email,omitempty"`
	Username     string    `json:"username,omitempty"`
	DeviceOwner  string    `json:"device_owner,omitempty"`
	LatestUsedAt time.Time `json:"latest_used_at,omitempty"`
}

// SaaSLicense is one license tier within an application's contract.
type SaaSLicense struct {
	LicenseID      string  `json:"license_id"`
	Name           string  `json:"name,omitempty"`
	Count          int     `json:"count"`
	Assigned       int     `json:"assigned"`
	Unassigned     int     `json:"unassigned"`
	CostPerLicense float64 `json:"cost_per_license,omitempty"`
	IsUnlimited    bool    `json:"is_unlimited"`
}

// SaaSContract is the contract/cost summary for a SaaS application.
type SaaSContract struct {
	Cost        float64 `json:"cost,omitempty"`
	Currency    string  `json:"currency,omitempty"`
	Term        string  `json:"term,omitempty"`
	RenewalDate string  `json:"renewal_date,omitempty"`
	Notes       string  `json:"notes,omitempty"`
}

// SaaSSSOApp is an SSO connection associated with a SaaS application.
type SaaSSSOApp struct {
	ID           string `json:"id"`
	AppName      string `json:"app_name,omitempty"`
	DisplayLabel string `json:"display_label,omitempty"`
	TemplateName string `json:"template_name,omitempty"`
	Status       string `json:"status,omitempty"`
}

// CatalogApp is the JumpCloud global/custom catalog entry for an application —
// the source of human-readable service info (name, description, domains, logo).
type CatalogApp struct {
	ID          string
	Name        string
	Description string
	Domains     []string
	LogoURL     string
}
