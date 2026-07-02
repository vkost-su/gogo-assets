// Package gworkspace talks to the Google Admin SDK Directory and Reports APIs
// and produces a normalised per-user record.
package gworkspace

import "time"

// DeviceKind is the OS family inferred from a mobiledevices.list entry.
// "chromeos" is intentionally omitted — not used in this organisation.
type DeviceKind string

const (
	DeviceAndroid DeviceKind = "android"
	DeviceIOS     DeviceKind = "ios"
	DeviceMacOS   DeviceKind = "macos"
	DeviceWindows DeviceKind = "windows"
	DeviceOther   DeviceKind = "other"
)

// OAuthEventType narrows the event kinds we surface from Reports API tokens.
type OAuthEventType string

const (
	OAuthAuthorize OAuthEventType = "authorize"
	OAuthRevoke    OAuthEventType = "revoke"
	OAuthActivity  OAuthEventType = "activity"
)

// Identity is the subset of directory.users we treat as identity attributes.
type Identity struct {
	Email         string    `json:"email"`
	FullName      string    `json:"full_name"`
	OrgUnitPath   string    `json:"org_unit_path"`
	IsSuspended   bool      `json:"is_suspended"`
	IsArchived    bool      `json:"is_archived"`
	IsAdmin       bool      `json:"is_admin"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	RecoveryEmail string    `json:"recovery_email,omitempty"`
}

// AuthPosture captures the security flags Google stores on the user object.
//
// LastLoginTime here may lag behind the Reports API; treat
// LoginActivity.LastLoginIP as the authoritative recent value.
type AuthPosture struct {
	Is2SVEnrolled             bool      `json:"is_2sv_enrolled"` // true - expected
	Is2SVEnforced             bool      `json:"is_2sv_enforced"` // true - expected
	LastLoginTime             time.Time `json:"last_login_time,omitempty"`
	PasswordChangedAt         time.Time `json:"password_changed_at,omitempty"`
	ChangePasswordAtNextLogin bool      `json:"change_password_at_next_login"`
}

// LoginActivity aggregates login events from Reports API over the configured window.
type LoginActivity struct {
	KnownIPs             []string  `json:"known_ips,omitempty"`     // store it independet from scheduled runs
	LastLoginIP          string    `json:"last_login_ip,omitempty"` // check the IP location
	SuccessfulLoginCount int       `json:"successful_login_count"`
	FailedLoginCount     int       `json:"failed_login_count"`     // if > 5 - warining, not timing
	SuspiciousLoginCount int       `json:"suspicious_login_count"` // if > 5 - warining, not timing
	EventsWindowStart    time.Time `json:"events_window_start,omitempty"`
	EventsWindowEnd      time.Time `json:"events_window_end,omitempty"`
}

// OAuthGrant is one event from activities.list("token") — authorize, revoke,
// or activity. One user typically produces many.
type OAuthGrant struct { // flag all events
	EventTime  time.Time      `json:"event_time"`
	EventType  OAuthEventType `json:"event_type"`
	AppName    string         `json:"app_name"`
	ClientID   string         `json:"client_id,omitempty"`
	Scopes     []string       `json:"scopes,omitempty"`
	ClientType string         `json:"client_type,omitempty"`
}

// ConnectedApp is a third-party application currently authorized on the user,
// sourced from directory.tokens.list.
type ConnectedApp struct { // flag all events
	ClientID    string   `json:"client_id"`
	DisplayText string   `json:"display_text"`
	Scopes      []string `json:"scopes,omitempty"`
	IsAnonymous bool     `json:"is_anonymous"`
	IsNativeApp bool     `json:"is_native_app"`
}

// Device is an enrolled mobile/endpoint device from mobiledevices.list.
type Device struct { // store it independet from scheduled runs
	DeviceID     string     `json:"device_id"`
	DeviceKind   DeviceKind `json:"device_kind"`
	OwnerEmail   string     `json:"owner_email,omitempty"`
	Model        string     `json:"model,omitempty"`
	Manufacturer string     `json:"manufacturer,omitempty"`
	SerialNumber string     `json:"serial_number,omitempty"`
	OSType       string     `json:"os_type,omitempty"`
	OSVersion    string     `json:"os_version,omitempty"`
	LastSync     time.Time  `json:"last_sync,omitempty"`
	Status       string     `json:"status,omitempty"`
	MACAddresses []string   `json:"mac_addresses,omitempty"`
}

// UserRecord aggregates everything we know about one Google user.
// LoginActivity, OAuthGrants, ConnectedApps, Devices are populated by the
// enrichment stage; they may all be empty on a brand-new or suspended user.
type UserRecord struct {
	Identity      Identity       `json:"identity"`
	Auth          AuthPosture    `json:"auth"`
	LoginActivity *LoginActivity `json:"login_activity,omitempty"`
	OAuthGrants   []OAuthGrant   `json:"oauth_grants,omitempty"`
	ConnectedApps []ConnectedApp `json:"connected_apps,omitempty"`
	Devices       []Device       `json:"devices,omitempty"`
}
