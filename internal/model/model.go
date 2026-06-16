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
const SchemaVersion = "2.0"

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
	SchemaVersion   string         `json:"schema_version"`
	RunDate         string         `json:"run_date"`          // YYYY-MM-DD
	RunTimestamp    time.Time      `json:"run_timestamp_utc"` // exact UTC instant of the run
	JumpCloud       JumpCloudShard `json:"jumpcloud"`
	Sophos          SophosShard    `json:"sophos"`
	GoogleWorkspace GWSShard       `json:"google_workspace"`
}

// JumpCloudShard groups the JumpCloud canonical records.
//
// Devices and Identity are classified entities (run through the drift engine).
// PolicyEnforcement is a per-policy rollup kept for dashboard/drill-down; it is
// not classified in this version.
type JumpCloudShard struct {
	Devices           []JCDevice            `json:"devices"`
	Identity          []JCUser              `json:"identity"`
	PolicyEnforcement []JCPolicyEnforcement `json:"policy_enforcement"`
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

	MACAddresses         []string `json:"mac_addresses,omitempty"`
	UnexpectedLocalUsers []string `json:"unexpected_local_users,omitempty"`
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
