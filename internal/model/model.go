// Package model is the canonical snapshot schema shared by every service
// collector and the drift engine.
//
// Each service collector keeps its own raw type (jumpcloud.System,
// sophos.Endpoint, gworkspace.UserRecord) where the fetch/normalise logic
// lives; thin to_model.go converters in each package translate those raw types
// into the canonical entities defined here. The drift engine then operates
// purely on this package — it never imports a service collector.
//
// # Drift tags
//
// Every field that participates in drift detection carries a `drift` struct
// tag, parsed by package drifttag:
//
//	drift:"monitored,sev=crit"   compared against the baseline (crit|high|med|low)
//	drift:"volatile"             stored for context, never compared
//	drift:"identity"             a matching key (serial, email) — stored, not compared
//
// # The pointer rule (ТЗ §11)
//
// Every monitored field is a pointer (*bool / *int / *time.Time). The pointer
// distinguishes two states that must never be conflated:
//
//	nil      → the value was NOT collected      → DATA_GAP   (a data-quality issue)
//	*false   → the value WAS collected, and off → BASELINE_DRIFT (a real finding)
//
// drifttag enforces this structurally: a monitored field that is not a pointer
// panics at startup.
package model

import "time"

// SchemaVersion is the canonical snapshot/digest schema version.
//
// 2.1 adds JumpCloudShard.Software — the per-person JumpCloud software footprint
// (device apps/extensions + SaaS memberships), a store-only shard with no
// monitored fields.
const SchemaVersion = "2.2"

// Severity ranks a finding. Stored human-readable in JSON (CRIT/HIGH/MED/LOW).
type Severity string

// Severity levels, ordered most-to-least urgent.
const (
	SevCrit Severity = "CRIT"
	SevHigh Severity = "HIGH"
	SevMed  Severity = "MED"
	SevLow  Severity = "LOW"
)

// Rank returns a sortable weight, 4 (CRIT) down to 1 (LOW); 0 for unknown.
// Used to order and truncate findings deterministically by urgency.
func (s Severity) Rank() int {
	switch s {
	case SevCrit:
		return 4
	case SevHigh:
		return 3
	case SevMed:
		return 2
	case SevLow:
		return 1
	default:
		return 0
	}
}

// Service identifies the source system a finding or record came from.
type Service string

// Known services.
const (
	ServiceJumpCloud       Service = "JumpCloud"
	ServiceSophos          Service = "Sophos"
	ServiceGoogleWorkspace Service = "GoogleWorkspace"
	ServicePeopleForce     Service = "PeopleForce"
)

// Meta is the provenance stamped onto every canonical entity.
type Meta struct {
	CollectedAt time.Time `json:"collected_at"`
	SourceAPI   string    `json:"source_api"` // e.g. "jumpcloud.systeminsights"
	RunDate     string    `json:"run_date"`   // YYYY-MM-DD, the logical collection day
}

// Snapshot is the full canonical inventory for one run. It is written to
// local/current/snapshot.json and sharded per service so the digest can point
// Claude at a specific slice for drill-down.
type Snapshot struct {
	SchemaVersion   string           `json:"schema_version"`
	RunDate         string           `json:"run_date"`          // YYYY-MM-DD
	RunTimestamp    time.Time        `json:"run_timestamp_utc"` // exact UTC instant of the run
	JumpCloud       JumpCloudShard   `json:"jumpcloud"`
	Sophos          SophosShard      `json:"sophos"`
	GoogleWorkspace GWSShard         `json:"google_workspace"`
	PeopleForce     PeopleForceShard `json:"peopleforce,omitempty"`
	Provenance      Provenance       `json:"provenance"`
}

// Provenance records the concrete API query templates each service collector
// issued for this run — the endpoint shapes (method + path, dynamic segments as
// {placeholders}) actually exercised. Each list is deduplicated and sorted, so
// identical collector behaviour yields a byte-identical block.
type Provenance struct {
	JumpCloud       []string `json:"jumpcloud,omitempty"`
	Sophos          []string `json:"sophos,omitempty"`
	GoogleWorkspace []string `json:"google_workspace,omitempty"`
	PeopleForce     []string `json:"peopleforce,omitempty"`
}

// JumpCloudShard groups the JumpCloud canonical records.
//
// Devices and Identity are classified entities (run through the drift engine).
// PolicyEnforcement, SaaS, and Software are kept for dashboard/drill-down; they
// are not classified in this version.
type JumpCloudShard struct {
	Devices           []JCDevice            `json:"devices"`
	Identity          []JCUser              `json:"identity"`
	PolicyEnforcement []JCPolicyEnforcement `json:"policy_enforcement"`
	SaaS              []SaaSApp             `json:"saas,omitempty"`
	Software          []JCPersonSoftware    `json:"software,omitempty"`
}

// SophosShard groups the Sophos canonical records.
//
// AccountHealth is a tenant-level rollup (not a classified entity).
type SophosShard struct {
	Endpoints     []SophosEndpoint     `json:"endpoints"`
	AccountHealth *SophosAccountHealth `json:"account_health,omitempty"`
}

// GWSShard groups the Google Workspace canonical records.
//
// Identity is classified; Devices is a snapshot shard for drill-down only.
type GWSShard struct {
	Identity []GWSUser   `json:"identity"`
	Devices  []GWSDevice `json:"devices"`
}

// ── JumpCloud ────────────────────────────────────────────────────────────────

// JCDevice is one JumpCloud-managed endpoint in canonical form.
type JCDevice struct {
	Meta Meta `json:"meta"`

	SystemID string `json:"system_id" drift:"identity"`
	Hostname string `json:"hostname"  drift:"identity"`
	Serial   string `json:"serial,omitempty" drift:"identity"`

	DisplayName string `json:"display_name,omitempty"`
	OSType      string `json:"os_type,omitempty"`
	OSFamily    string `json:"os_family,omitempty" drift:"identity"` // darwin|windows|linux
	OSVersion   string `json:"os_version,omitempty"`
	OSCodename  string `json:"os_codename,omitempty"`

	Manufacturer  string `json:"manufacturer,omitempty"`
	HardwareModel string `json:"hardware_model,omitempty"`

	OwnerEmail string `json:"owner_email,omitempty" drift:"identity"`

	// Posture (monitored). nil = not collected → DATA_GAP.
	DiskEncrypted *bool `json:"disk_encrypted" drift:"monitored,sev=crit"`
	MDMEnrolled   *bool `json:"mdm_enrolled"   drift:"monitored,sev=med"`

	EncryptionType string `json:"encryption_type,omitempty"`
	MDMVendor      string `json:"mdm_vendor,omitempty"`

	// State (volatile).
	Active       *bool      `json:"active"                 drift:"volatile"`
	LastContact  *time.Time `json:"last_contact,omitempty" drift:"volatile"`
	RemoteIP     string     `json:"remote_ip,omitempty"    drift:"volatile"`
	AgentVersion string     `json:"agent_version,omitempty"`

	MACAddresses []string `json:"mac_addresses,omitempty"`
}

// JCUser is a JumpCloud directory user in canonical form.
type JCUser struct {
	Meta Meta `json:"meta"`

	UserID   string `json:"user_id"  drift:"identity"`
	Email    string `json:"email"    drift:"identity"`
	Username string `json:"username" drift:"identity"`
	FullName string `json:"full_name,omitempty"`

	// Posture (monitored).
	MFAEnabled           *bool `json:"mfa_enabled"            drift:"monitored,sev=crit"`
	PasswordNeverExpires *bool `json:"password_never_expires" drift:"monitored,sev=med"`
	JumpCloudGoEnabled   *bool `json:"jumpcloud_go_enabled"  drift:"monitored,sev=low"`

	TOTPEnabled *bool `json:"totp_enabled,omitempty"`
	MFARequired *bool `json:"mfa_required,omitempty"`

	// State (volatile / descriptive).
	PasswordExpiration *time.Time `json:"password_expiration_date,omitempty" drift:"volatile"`
	PasswordExpired    *bool      `json:"password_expired,omitempty"`
	Suspended          *bool      `json:"suspended,omitempty"`
	AccountLocked      *bool      `json:"account_locked,omitempty"`
	Activated          *bool      `json:"activated,omitempty"`
}

// JCPolicyEnforcement is a per-policy rollup across all systems. PolicyID holds
// the policy name (JumpCloud's per-system status carries no policy UUID).
//
// This entity is stored for dashboard/drill-down and is not classified in this
// version; the monitored AppliedCount tag lets a future baseline express
// expected enforcement breadth without a schema change.
type JCPolicyEnforcement struct {
	Meta Meta `json:"meta"`

	PolicyID string `json:"policy_id" drift:"identity"`

	AppliedCount *int `json:"applied_count" drift:"monitored,sev=high"`
	FailedCount  int  `json:"failed_count"  drift:"volatile"`
	PendingCount int  `json:"pending_count" drift:"volatile"`
}

// SaaSApp is one application discovered by JumpCloud AI & SaaS Management, with
// its owner accounts, license/contract economics, SSO connections, and usage
// rolled into a single record.
//
// This entity is stored for the SaaS dashboard tab and drill-down only; it is
// not classified in this version (like JCPolicyEnforcement). Status
// (NEWLY_DISCOVERED / UNAPPROVED / …) is a strong shadow-IT signal left for a
// future drift wiring.
//
// Category is a derived heuristic (the public API exposes no category taxonomy).
type SaaSApp struct {
	Meta Meta `json:"meta"`

	AppID string `json:"app_id" drift:"identity"`
	Name  string `json:"name"   drift:"identity"`

	CatalogAppID string   `json:"catalog_app_id,omitempty"`
	Category     string   `json:"category,omitempty"` // derived, not from the API
	Description  string   `json:"description,omitempty"`
	Domains      []string `json:"domains,omitempty"`
	LogoURL      string   `json:"logo_url,omitempty"`

	Status            string `json:"status,omitempty"`             // NEWLY_DISCOVERED|APPROVED|UNAPPROVED|IGNORED
	AccessRestriction string `json:"access_restriction,omitempty"` // NO_ACTION|WARNING|BLOCK|…

	OwnerUserID  string     `json:"owner_user_id,omitempty"`
	OwnerEmail   string     `json:"owner_email,omitempty"`
	DiscoveredAt *time.Time `json:"discovered_at,omitempty" drift:"volatile"`

	DiscoverySources []string     `json:"discovery_sources,omitempty"` // BROWSER_EXTENSION|JUMPCLOUD_SSO|CONNECTOR|…
	SSOConnected     bool         `json:"sso_connected"`
	SSOApps          []SaaSSSOApp `json:"sso_apps,omitempty"`

	Accounts []SaaSAccount `json:"accounts,omitempty"`
	Licenses []SaaSLicense `json:"licenses,omitempty"`
	Contract *SaaSContract `json:"contract,omitempty"`

	// Rollups derived from the nested data.
	AccountCount      int        `json:"account_count"`
	LicenseTotal      int        `json:"license_total"`
	LicenseAssigned   int        `json:"license_assigned"`
	LicenseUnassigned int        `json:"license_unassigned"`
	LatestUsedAt      *time.Time `json:"latest_used_at,omitempty" drift:"volatile"`
}

// SaaSAccount is one user account found inside a SaaS application — the
// license-owner identity. UserID links to a JumpCloud directory user when the
// account was matched; Email/Username are the account's own credentials.
// DeviceOwner is the fallback attribution (the owner of the device on which a
// device-agent account with no own identity was discovered).
type SaaSAccount struct {
	AccountID    string     `json:"account_id"`
	UserID       string     `json:"user_id,omitempty"`
	Email        string     `json:"email,omitempty"`
	Username     string     `json:"username,omitempty"`
	DeviceOwner  string     `json:"device_owner,omitempty"`
	LatestUsedAt *time.Time `json:"latest_used_at,omitempty"`
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
	Cost        float64 `json:"cost,omitempty"` // total yearly contract cost
	Currency    string  `json:"currency,omitempty"`
	Term        string  `json:"term,omitempty"` // MONTHLY_TERM|YEARLY_TERM|FREE_TERM
	RenewalDate string  `json:"renewal_date,omitempty"`
	Notes       string  `json:"notes,omitempty"`
}

// SaaSSSOApp is an SSO connection associated with a SaaS application.
type SaaSSSOApp struct {
	ID           string `json:"id"`
	AppName      string `json:"app_name,omitempty"`
	DisplayLabel string `json:"display_label,omitempty"`
	TemplateName string `json:"template_name,omitempty"`
	Status       string `json:"status,omitempty"` // CONNECTED|NOT_CONNECTED
}

// JCPersonSoftware is the per-person software footprint on JumpCloud, anchored by
// email (the person) and joined through device ownership (the device). It folds
// the person's SaaS-app memberships and every app/extension found on the devices
// they own into a single record — the "JumpCloud Software (SaaS)" view.
//
// Store-only: it carries no monitored fields and is never classified. What
// surfaces in the drift view is decided at output time by the allowlist
// (package allowlist), not by the drift engine, so the FindingKind set stays
// closed at six. Devices, Apps, Extensions, and SaaS are all sorted
// deterministically by the assembler.
type JCPersonSoftware struct {
	Meta Meta `json:"meta"`

	OwnerEmail string   `json:"owner_email" drift:"identity"`
	Devices    []string `json:"devices,omitempty"` // hostnames of the person's JumpCloud devices

	SaaS       []JCSaaSMembership `json:"saas,omitempty"`       // SaaS apps this person has an account in
	Apps       []JCSoftwareItem   `json:"apps,omitempty"`       // native apps (macOS/Windows/Linux)
	Extensions []JCSoftwareItem   `json:"extensions,omitempty"` // browser extensions

	// Rollups derived from the nested data.
	SaaSCount      int `json:"saas_count"`
	AppCount       int `json:"app_count"`
	ExtensionCount int `json:"extension_count"`
}

// JCSaaSMembership is one SaaS application a person has an account in, folded into
// their software footprint. It is a thin reference back to the full SaaSApp
// record in JumpCloudShard.SaaS, which keeps the license/contract economics.
type JCSaaSMembership struct {
	AppID  string `json:"app_id"`
	Name   string `json:"name"`
	Status string `json:"status,omitempty"` // NEWLY_DISCOVERED|APPROVED|UNAPPROVED|…
}

// JCSoftwareItem is one installed application or browser extension found on a
// person's device. Source records the origin (OS package set or browser);
// DeviceHostname records which device, so the same app on two machines is two
// items.
type JCSoftwareItem struct {
	Name           string `json:"name"`
	Version        string `json:"version,omitempty"`
	Source         string `json:"source"` // macos|windows|deb|rpm|chrome|firefox|safari|plugin
	DeviceHostname string `json:"device_hostname,omitempty"`
}

// ── Sophos ───────────────────────────────────────────────────────────────────

// SophosEndpoint is one Sophos-managed device in canonical form.
type SophosEndpoint struct {
	Meta Meta `json:"meta"`

	EndpointID string `json:"endpoint_id" drift:"identity"`
	Hostname   string `json:"hostname"    drift:"identity"`
	Serial     string `json:"serial,omitempty" drift:"identity"`

	OSPlatform string `json:"os_platform,omitempty" drift:"identity"` // windows|macOS|linux
	OSName     string `json:"os_name,omitempty"`
	OSVersion  string `json:"os_version,omitempty"`

	OwnerEmail string `json:"owner_email,omitempty" drift:"identity"`
	OwnerLogin string `json:"owner_login,omitempty"` // raw SSO/AD login (may be bare username)
	OwnerName  string `json:"owner_name,omitempty"`

	// Posture (monitored).
	TamperProtection *bool `json:"tamper_protection" drift:"monitored,sev=crit"`

	HealthOverall  string `json:"health_overall,omitempty"` // good|suspicious|bad|unknown
	HealthThreats  string `json:"health_threats,omitempty"`
	HealthServices string `json:"health_services,omitempty"`

	AssignedProducts []string `json:"assigned_products,omitempty"`
	Policies         []string `json:"policies,omitempty"`

	// State (volatile).
	Online            *bool      `json:"online"                 drift:"volatile"`
	LastSeenAt        *time.Time `json:"last_seen_at,omitempty" drift:"volatile"`
	AlertCount        int        `json:"alert_count"            drift:"volatile"`
	DetectionCount30d int        `json:"detection_count_30d"    drift:"volatile"`
	FetchError        bool       `json:"fetch_error"`

	MACAddresses  []string `json:"mac_addresses,omitempty"`
	IPv4Addresses []string `json:"ipv4_addresses,omitempty"`
}

// SophosAccountHealth is a tenant-level rollup for the dashboard. It is derived
// from the endpoint set, not collected, and is not a classified entity.
type SophosAccountHealth struct {
	Meta Meta `json:"meta"`

	EndpointsTotal   int `json:"endpoints_total"`
	HealthGood       int `json:"health_good"`
	HealthSuspicious int `json:"health_suspicious"`
	HealthBad        int `json:"health_bad"`
	HealthUnknown    int `json:"health_unknown"`
	TamperOffCount   int `json:"tamper_off_count"`
	TotalAlerts      int `json:"total_alerts"`
}

// ── Google Workspace ─────────────────────────────────────────────────────────

// GWSUser is a Google Workspace user in canonical form.
type GWSUser struct {
	Meta Meta `json:"meta"`

	Email       string `json:"email"                   drift:"identity"`
	FullName    string `json:"full_name,omitempty"`
	OrgUnitPath string `json:"org_unit_path,omitempty" drift:"identity"`

	// Posture (monitored).
	MFAEnabled       *bool `json:"mfa_enabled"        drift:"monitored,sev=crit"` // 2SV enrolled
	MFAEnforced      *bool `json:"mfa_enforced"       drift:"monitored,sev=high"` // 2SV enforced
	IsAdmin          *bool `json:"is_admin"           drift:"monitored,sev=high"`
	ASPCount         *int  `json:"asp_count"          drift:"monitored,sev=high"` // app-specific passwords (MFA bypass)
	BackupCodeCount  *int  `json:"backup_code_count"  drift:"monitored,sev=med"`
	ThirdPartyTokens *int  `json:"third_party_tokens" drift:"monitored,sev=med"` // OAuth-connected apps

	// Descriptive / state.
	Suspended     *bool      `json:"suspended,omitempty"`
	IsArchived    *bool      `json:"is_archived,omitempty"`
	RecoveryEmail string     `json:"recovery_email,omitempty"`
	CreatedAt     *time.Time `json:"created_at,omitempty"`

	// State (volatile).
	LastLoginTime    *time.Time `json:"last_login_time,omitempty"  drift:"volatile"`
	LastLoginIP      string     `json:"last_login_ip,omitempty"    drift:"volatile"`
	SuccessfulLogins int        `json:"successful_login_count"     drift:"volatile"`
	FailedLogins     int        `json:"failed_login_count"         drift:"volatile"`
	SuspiciousLogins int        `json:"suspicious_login_count"     drift:"volatile"`
}

// GWSDevice is an enrolled Google Workspace mobile/endpoint device. It is a
// snapshot shard for drill-down; it has no monitored posture and is not
// classified in this version.
type GWSDevice struct {
	Meta Meta `json:"meta"`

	DeviceID   string `json:"device_id"             drift:"identity"`
	DeviceKind string `json:"device_kind,omitempty" drift:"identity"`
	OwnerEmail string `json:"owner_email,omitempty" drift:"identity"`
	Serial     string `json:"serial,omitempty"      drift:"identity"`

	Model        string `json:"model,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	OSType       string `json:"os_type,omitempty"`
	OSVersion    string `json:"os_version,omitempty"`
	Status       string `json:"status,omitempty"`

	LastSync     *time.Time `json:"last_sync,omitempty" drift:"volatile"`
	MACAddresses []string   `json:"mac_addresses,omitempty"`
}

// ── PeopleForce ──────────────────────────────────────────────────────────────

// PeopleForceShard groups the PeopleForce canonical records.
//
// Assets is stored for the Assets dashboard tab and snapshot drill-down. It is
// not classified in this version — PFAsset carries no monitored fields.
type PeopleForceShard struct {
	Assets []PFAsset `json:"assets"`
}

// PFAsset is one physical asset from PeopleForce Asset Management in canonical
// form. It records the current assignment (assignee email, issued date) resolved
// from the assignment history and the employee directory.
//
// All fields are volatile or identity — no monitored posture fields exist in
// this version. Promoting assignment state into findings would require an
// explicit decision to extend FindingKind or add monitored pointer fields.
type PFAsset struct {
	Meta Meta `json:"meta"`

	AssetID      string `json:"asset_id"                drift:"identity"`
	Name         string `json:"name"                    drift:"identity"`
	Code         string `json:"code,omitempty"          drift:"identity"`
	SerialNumber string `json:"serial_number,omitempty" drift:"identity"`

	Category    string `json:"category,omitempty"`
	Description string `json:"description,omitempty"`

	// Current active assignment (most recent non-returned record).
	AssignedToEmail string `json:"assigned_to_email,omitempty" drift:"volatile"`
	AssignedToName  string `json:"assigned_to_name,omitempty"  drift:"volatile"`
	AssignedToID    int    `json:"assigned_to_id,omitempty"    drift:"volatile"`
	IssuedOn        string `json:"issued_on,omitempty"         drift:"volatile"` // ISO date

	// Employee context (resolved from the assignee's directory record).
	Department string `json:"department,omitempty" drift:"volatile"`
	Position   string `json:"position,omitempty"   drift:"volatile"`
	Location   string `json:"location,omitempty"   drift:"volatile"`

	IsAssigned bool `json:"is_assigned" drift:"volatile"`

	CreatedAt *time.Time `json:"created_at,omitempty" drift:"volatile"`
	UpdatedAt *time.Time `json:"updated_at,omitempty" drift:"volatile"`
}
