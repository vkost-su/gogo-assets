// Package inventory unifies records from Google Workspace, JumpCloud, and
// Sophos Central. The primary key is email; cross-source correlation
// (bare-username heuristic + JC↔Sophos device-level join) runs in Finalize.
package inventory

import (
	"time"

	"gogo-assets/internal/gworkspace"
	"gogo-assets/internal/jumpcloud"
	"gogo-assets/internal/sophos"
)

// JCSlice is JumpCloud data attached to a user in the unified view.
type JCSlice struct {
	Systems []jumpcloud.System `json:"systems,omitempty"`
	User    *jumpcloud.User    `json:"user,omitempty"`
}

// SophosSlice is Sophos data attached to a user in the unified view.
type SophosSlice struct {
	Endpoints []sophos.Endpoint `json:"endpoints,omitempty"`
}

// DevicePair links one physical device to its JC and/or Sophos record.
//
// MatchKey records how the two sides were joined:
//
//	"serial" | "hostname" | "mac" | "jc-only" | "sophos-only"
type DevicePair struct {
	JC       *jumpcloud.System `json:"jc,omitempty"`
	Sophos   *sophos.Endpoint  `json:"sophos,omitempty"`
	MatchKey string            `json:"match_key,omitempty"`
}

// UnifiedUserRecord is one row of the inventory, keyed by email.
type UnifiedUserRecord struct {
	Email     string                 `json:"email"`
	Google    *gworkspace.UserRecord `json:"google,omitempty"`
	JumpCloud *JCSlice               `json:"jumpcloud,omitempty"`
	Sophos    *SophosSlice           `json:"sophos,omitempty"`
	Devices   []DevicePair           `json:"devices,omitempty"`
}

// AssetInventory is the top-level snapshot produced by Run.
//
// Users is the merged-by-email view. JCSystems, JCUsers, SophosEndpoints hold
// the raw per-source results so the per-source sheet tabs can be written
// without re-fetching. UnownedDevices captures pairs whose owner could not be
// resolved to any known email.
type AssetInventory struct {
	Users           map[string]*UnifiedUserRecord `json:"users"`
	JCSystems       []jumpcloud.System            `json:"jc_systems,omitempty"`
	JCUsers         map[string]jumpcloud.User     `json:"jc_users,omitempty"`
	SaaSApps        []jumpcloud.SaaSApp           `json:"saas_apps,omitempty"`
	SophosEndpoints []sophos.Endpoint             `json:"sophos_endpoints,omitempty"`
	UnownedDevices  []DevicePair                  `json:"unowned_devices,omitempty"`
	MatchStats      map[string]int                `json:"match_stats,omitempty"`
	CollectedAt     time.Time                     `json:"collected_at"`
}

// New returns an empty inventory with timestamps + maps initialised.
func New() *AssetInventory {
	return &AssetInventory{
		Users:       make(map[string]*UnifiedUserRecord),
		CollectedAt: time.Now().UTC(),
	}
}

// userSlot returns (creating if needed) the user record for email.
func (a *AssetInventory) userSlot(email string) *UnifiedUserRecord {
	if r, ok := a.Users[email]; ok {
		return r
	}
	r := &UnifiedUserRecord{Email: email}
	a.Users[email] = r
	return r
}
