// Package jumpcloud talks to the JumpCloud v1, v2, and System Insights APIs
// and produces normalised system/user models.
package jumpcloud

import "time"

// PolicyStatus is one applied policy with its last-reported status.
type PolicyStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "success" | "failed" | "pending" | "error"
}

// App is an installed application or browser extension, normalised across all OSes.
type App struct {
	Name       string `json:"name"`
	Version    string `json:"version,omitempty"`
	LastOpened string `json:"last_opened,omitempty"` // free-form date string
}

// SSHKey is an SSH public key registered on a JumpCloud user.
type SSHKey struct {
	Name            string    `json:"name"`
	PublicKeyPrefix string    `json:"public_key_prefix,omitempty"` // first 40 chars
	CreatedAt       time.Time `json:"created_at,omitempty"`
}

// User is a JumpCloud system user — identity, MFA, password, and key posture.
type User struct {
	UserID   string `json:"user_id"`
	Email    string `json:"email"`
	Username string `json:"username"`
	FullName string `json:"full_name"`

	// MFA.
	MFAConfigured bool `json:"mfa_configured"`
	TOTPEnabled   bool `json:"totp_enabled"`
	MFARequired   bool `json:"mfa_required"` // enable_user_portal_multifactor

	// Password.
	PasswordExpirationDate time.Time `json:"password_expiration_date,omitempty"`
	PasswordExpired        bool      `json:"password_expired"`
	PasswordNeverExpires   bool      `json:"password_never_expires"`

	// Account state.
	AccountLocked bool `json:"account_locked"`
	Activated     bool `json:"activated"`
	Suspended     bool `json:"suspended"`

	// JumpCloud Go feature state at the org level. Nil = unknown.
	JCGoEligible *bool `json:"jc_go_eligible,omitempty"`

	SSHKeys []SSHKey `json:"ssh_keys,omitempty"`
}

// System is one JumpCloud-managed endpoint — fully enriched from v1, v2, and SI.
type System struct {
	SystemID    string `json:"system_id"`
	Hostname    string `json:"hostname"`
	DisplayName string `json:"display_name,omitempty"`

	// OS.
	OSType     string `json:"os_type,omitempty"`   // "Mac OS X", "Windows", "Linux"
	OSFamily   string `json:"os_family,omitempty"` // "darwin", "windows", "linux"
	OSVersion  string `json:"os_version,omitempty"`
	OSCodename string `json:"os_codename,omitempty"` // "Sonoma", "Ventura", "focal", etc.
	OSBuild    string `json:"os_build,omitempty"`

	SerialNumber string    `json:"serial_number,omitempty"`
	LastContact  time.Time `json:"last_contact,omitempty"`
	Active       bool      `json:"active"`
	AgentVersion string    `json:"agent_version,omitempty"`
	RemoteIP     string    `json:"remote_ip,omitempty"`

	OwnerEmail string `json:"owner_email,omitempty"`

	// Hardware (from System Insights system_info — nil pointers when SI unavailable).
	Manufacturer     string `json:"manufacturer,omitempty"`
	HardwareModel    string `json:"hardware_model,omitempty"`
	HardwareUUID     string `json:"hardware_uuid,omitempty"`
	CPUBrand         string `json:"cpu_brand,omitempty"`
	CPUPhysicalCores *int   `json:"cpu_physical_cores,omitempty"`
	CPULogicalCores  *int   `json:"cpu_logical_cores,omitempty"`
	RAMBytes         *int64 `json:"ram_bytes,omitempty"`
	DiskSizeBytes    *int64 `json:"disk_size_bytes,omitempty"`

	MACAddresses []string `json:"mac_addresses,omitempty"`

	// MDM.
	MDMEnrolled       bool   `json:"mdm_enrolled"`
	MDMVendor         string `json:"mdm_vendor,omitempty"`
	MDMDEP            bool   `json:"mdm_dep"`
	MDMUserApproved   bool   `json:"mdm_user_approved"`
	MDMEnrollmentType string `json:"mdm_enrollment_type,omitempty"`

	// Encryption.
	DiskEncrypted   *bool  `json:"disk_encrypted,omitempty"`   // nil = unknown
	EncryptionType  string `json:"encryption_type,omitempty"`  // "AES-XTS" / "BitLocker" / "LUKS"
	FileVaultStatus string `json:"filevault_status,omitempty"` // "On" / "Off"

	// Policies.
	PolicyStats     map[string]int `json:"policy_stats,omitempty"`
	FailedPolicies  []string       `json:"failed_policies,omitempty"`
	PendingPolicies []string       `json:"pending_policies,omitempty"`
	PolicyStatuses  []PolicyStatus `json:"policy_statuses,omitempty"`
	AppliedPolicies []string       `json:"applied_policies,omitempty"`

	// Local OS users.
	LocalUsers           []string `json:"local_users,omitempty"`
	UnexpectedLocalUsers []string `json:"unexpected_local_users,omitempty"`

	USBDevices []string `json:"usb_devices,omitempty"`

	// Software (OS-specific, from System Insights).
	Apps        []App `json:"apps,omitempty"`         // macOS
	Programs    []App `json:"programs,omitempty"`     // Windows
	DEBPackages []App `json:"deb_packages,omitempty"` // Linux deb
	RPMPackages []App `json:"rpm_packages,omitempty"` // Linux rpm

	// Browser extensions.
	BrowserPlugins   []App `json:"browser_plugins,omitempty"`
	ChromeExtensions []App `json:"chrome_extensions,omitempty"`
	FirefoxAddons    []App `json:"firefox_addons,omitempty"`
	SafariExtensions []App `json:"safari_extensions,omitempty"`

	// Network config.
	EtcHosts []string `json:"etc_hosts,omitempty"`
}
