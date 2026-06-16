// Package sophos talks to the Sophos Central API and exposes a normalised
// endpoint model.
package sophos

import "time"

// Endpoint is one Sophos-managed device.
//
// OwnerLogin is associatedPerson.viaLogin from the endpoint API — the raw
// SSO/AD login string, which may be a bare username, UPN, or domain\user.
//
// OwnerEmail is resolved separately from the Sophos directory using
// associatedPerson.id. It is empty when the endpoint has no associated
// user or the directory call failed.
//
// DetectionCount30d is 0 and FetchError is true when the Detections API is
// unavailable (XDR/MTR license required) — the sheet writer surfaces this
// as "?" rather than "0".
type Endpoint struct {
	EndpointID   string    `json:"endpoint_id"`
	Hostname     string    `json:"hostname"`
	OSPlatform   string    `json:"os_platform,omitempty"` // "windows", "macOS", "linux"
	OSName       string    `json:"os_name,omitempty"`
	OSVersion    string    `json:"os_version,omitempty"`
	SerialNumber string    `json:"serial_number,omitempty"`
	Online       bool      `json:"online"`
	LastSeenAt   time.Time `json:"last_seen_at,omitempty"`

	// Health: "good" | "suspicious" | "bad" | "unknown"
	HealthOverall   string `json:"health_overall,omitempty"`
	HealthThreats   string `json:"health_threats,omitempty"`
	HealthServices  string `json:"health_services,omitempty"`
	TamperProtected bool   `json:"tamper_protected"`

	// Assigned products (e.g. "endpointProtection", "interceptX").
	AssignedProducts []string `json:"assigned_products,omitempty"`

	// Ownership.
	OwnerLogin string `json:"owner_login,omitempty"`
	OwnerName  string `json:"owner_name,omitempty"`
	OwnerEmail string `json:"owner_email,omitempty"`

	// Names of applied Sophos configuration policies.
	Policies []string `json:"policies,omitempty"`

	// Network.
	MACAddresses  []string `json:"mac_addresses,omitempty"`
	IPv4Addresses []string `json:"ipv4_addresses,omitempty"`

	// Aggregated from /common/v1/alerts and /detections/v1/queries/detections.
	AlertCount        int  `json:"alert_count"`
	DetectionCount30d int  `json:"detection_count_30d"`
	FetchError        bool `json:"fetch_error"`
}
