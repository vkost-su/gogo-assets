package inventory

import (
	"strings"

	"gogo-assets/internal/gworkspace"
	"gogo-assets/internal/jumpcloud"
	"gogo-assets/internal/sophos"
)

// AddGoogle attaches GWS records keyed by primary email.
func (a *AssetInventory) AddGoogle(records map[string]*gworkspace.UserRecord) {
	for email, rec := range records {
		slot := a.userSlot(email)
		slot.Google = rec
	}
}

// AddJC stores JC results and attaches each system to its owner user.
//
// owner_email priority follows Python: every system owner is linked even
// if the user isn't yet known (GWS may add them later); the matching
// JCUser is then attached when the email also exists in `users`.
func (a *AssetInventory) AddJC(systems []jumpcloud.System, users map[string]jumpcloud.User) {
	a.JCSystems = systems
	a.JCUsers = users

	for _, s := range systems {
		if s.OwnerEmail == "" {
			continue
		}
		slot := a.userSlot(s.OwnerEmail)
		if slot.JumpCloud == nil {
			slot.JumpCloud = &JCSlice{}
		}
		slot.JumpCloud.Systems = append(slot.JumpCloud.Systems, s)
	}
	// Now attach the matching JCUser into the slot's jumpcloud sub-record.
	for email, u := range users {
		u := u
		slot, ok := a.Users[email]
		if !ok || slot.JumpCloud == nil {
			continue
		}
		slot.JumpCloud.User = &u
	}
}

// AddSophos stores Sophos endpoints and attaches each to its owner — either by
// OwnerEmail (preferred) or by OwnerLogin if it looks like an email.
func (a *AssetInventory) AddSophos(endpoints []sophos.Endpoint) {
	a.SophosEndpoints = endpoints
	for i := range endpoints {
		ep := endpoints[i]
		email := ep.OwnerEmail
		if email == "" && strings.Contains(ep.OwnerLogin, "@") {
			email = ep.OwnerLogin
		}
		if email == "" {
			continue
		}
		slot := a.userSlot(email)
		if slot.Sophos == nil {
			slot.Sophos = &SophosSlice{}
		}
		slot.Sophos.Endpoints = append(slot.Sophos.Endpoints, ep)
	}
}
