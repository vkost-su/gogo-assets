package inventory

import (
	"sort"
	"strings"

	"gogo-assets/internal/jumpcloud"
	"gogo-assets/internal/sophos"
)

// Finalize runs the cross-source correlation. It must be called exactly once
// after all Add*() methods — repeated calls would duplicate device pairs.
//
// Steps:
//
//  1. Bare-username heuristic — attach Sophos endpoints whose owner_login
//     lacks "@" to <login>@<primary_domain>.
//  2. Build endpoint_id → owning email index from current Sophos slots.
//  3. Pair JC systems with Sophos endpoints by serial → hostname → MAC.
//  4. Attribute each pair to its owning user (JC wins on conflict) and
//     backfill the owner's identity-level Sophos slice from the pair.
//
// Populates Users[...].Devices, UnownedDevices, and MatchStats.
func (a *AssetInventory) Finalize() {
	bareMatched, bareUnmatched := a.matchBareUsernameEndpoints()
	sophosOwner := a.buildSophosOwnerIndex()
	pairs := a.buildDevicePairs()
	ownerMismatches := a.distributePairs(pairs, sophosOwner)

	paired, jcOnly, spOnly := 0, 0, 0
	for _, p := range pairs {
		switch {
		case p.JC != nil && p.Sophos != nil:
			paired++
		case p.JC != nil && p.Sophos == nil:
			jcOnly++
		case p.JC == nil && p.Sophos != nil:
			spOnly++
		}
	}

	a.MatchStats = map[string]int{
		"paired":                  paired,
		"jc_only":                 jcOnly,
		"sophos_only":             spOnly,
		"unowned":                 len(a.UnownedDevices),
		"owner_mismatch":          ownerMismatches,
		"bare_username_matched":   bareMatched,
		"bare_username_unmatched": bareUnmatched,
	}
}

func (a *AssetInventory) primaryDomain() string {
	counts := make(map[string]int)
	for email := range a.Users {
		if i := strings.IndexByte(email, '@'); i > 0 {
			counts[strings.ToLower(email[i+1:])]++
		}
	}
	var best string
	bestCount := 0
	for d, c := range counts {
		if c > bestCount {
			best, bestCount = d, c
		}
	}
	return best
}

func (a *AssetInventory) matchBareUsernameEndpoints() (matched, unmatched int) {
	domain := a.primaryDomain()
	if domain == "" {
		return 0, 0
	}

	attached := make(map[string]struct{})
	for _, u := range a.Users {
		if u.Sophos == nil {
			continue
		}
		for _, ep := range u.Sophos.Endpoints {
			attached[ep.EndpointID] = struct{}{}
		}
	}

	for i := range a.SophosEndpoints {
		ep := a.SophosEndpoints[i]
		if _, ok := attached[ep.EndpointID]; ok {
			continue
		}
		if ep.OwnerLogin == "" || strings.Contains(ep.OwnerLogin, "@") {
			continue
		}
		candidate := strings.ToLower(strings.TrimSpace(ep.OwnerLogin)) + "@" + domain
		slot, ok := a.Users[candidate]
		if !ok {
			unmatched++
			continue
		}
		if slot.Sophos == nil {
			slot.Sophos = &SophosSlice{}
		}
		slot.Sophos.Endpoints = append(slot.Sophos.Endpoints, ep)
		matched++
	}
	return matched, unmatched
}

func (a *AssetInventory) buildSophosOwnerIndex() map[string]string {
	out := make(map[string]string)
	for email, u := range a.Users {
		if u.Sophos == nil {
			continue
		}
		for _, ep := range u.Sophos.Endpoints {
			out[ep.EndpointID] = email
		}
	}
	return out
}

// buildDevicePairs joins JC systems to Sophos endpoints by serial → hostname → MAC.
func (a *AssetInventory) buildDevicePairs() []DevicePair {
	jcBySerial := make(map[string]*jumpcloud.System)
	jcByHostname := make(map[string]*jumpcloud.System)
	jcByMAC := make(map[string]*jumpcloud.System)

	for i := range a.JCSystems {
		s := &a.JCSystems[i]
		if s.SerialNumber != "" {
			jcBySerial[strings.ToUpper(strings.TrimSpace(s.SerialNumber))] = s
		}
		if s.Hostname != "" {
			jcByHostname[normalizeHostname(s.Hostname)] = s
		}
		for _, m := range s.MACAddresses {
			if k := normalizeMAC(m); k != "" {
				jcByMAC[k] = s
			}
		}
	}

	pairs := make([]DevicePair, 0, len(a.SophosEndpoints)+len(a.JCSystems))
	used := make(map[string]struct{})

	for i := range a.SophosEndpoints {
		ep := &a.SophosEndpoints[i]

		var match *jumpcloud.System
		matchKey := ""

		if ep.SerialNumber != "" {
			if cand, ok := jcBySerial[strings.ToUpper(strings.TrimSpace(ep.SerialNumber))]; ok {
				if _, taken := used[cand.SystemID]; !taken {
					match, matchKey = cand, "serial"
				}
			}
		}
		if match == nil && ep.Hostname != "" {
			if cand, ok := jcByHostname[normalizeHostname(ep.Hostname)]; ok {
				if _, taken := used[cand.SystemID]; !taken {
					match, matchKey = cand, "hostname"
				}
			}
		}
		if match == nil {
			for _, m := range ep.MACAddresses {
				if cand, ok := jcByMAC[normalizeMAC(m)]; ok {
					if _, taken := used[cand.SystemID]; !taken {
						match, matchKey = cand, "mac"
						break
					}
				}
			}
		}

		if match != nil {
			pairs = append(pairs, DevicePair{JC: match, Sophos: ep, MatchKey: matchKey})
			used[match.SystemID] = struct{}{}
		} else {
			pairs = append(pairs, DevicePair{Sophos: ep, MatchKey: "sophos-only"})
		}
	}

	for i := range a.JCSystems {
		s := &a.JCSystems[i]
		if _, ok := used[s.SystemID]; ok {
			continue
		}
		pairs = append(pairs, DevicePair{JC: s, MatchKey: "jc-only"})
	}

	// Stable ordering helps testing.
	sort.SliceStable(pairs, func(i, j int) bool {
		return pairKey(pairs[i]) < pairKey(pairs[j])
	})
	return pairs
}

// distributePairs attaches each pair to its owning user (JC wins on conflict)
// and counts owner-mismatch incidents.
func (a *AssetInventory) distributePairs(pairs []DevicePair, sophosOwner map[string]string) int {
	mismatches := 0
	for _, p := range pairs {
		var jcOwner, spOwner string
		if p.JC != nil {
			jcOwner = p.JC.OwnerEmail
		}
		if p.Sophos != nil {
			spOwner = sophosOwner[p.Sophos.EndpointID]
		}
		if jcOwner != "" && spOwner != "" && jcOwner != spOwner {
			mismatches++
		}
		owner := jcOwner
		if owner == "" {
			owner = spOwner
		}
		if owner != "" {
			if u, ok := a.Users[owner]; ok {
				u.Devices = append(u.Devices, p)
				// Backfill identity-level Sophos coverage. A Sophos endpoint
				// paired to this user's JumpCloud device (by serial/hostname/MAC)
				// belongs to the same user even when its own owner_login never
				// resolved to an email — e.g. a "HOST\user" local login that the
				// AddSophos and bare-username heuristics both skip. Without this
				// the SP / Health / Open columns (which read u.Sophos.Endpoints)
				// would show "—" for a device Detail already lists as "JC+SP".
				if p.Sophos != nil {
					a.attachSophosEndpoint(u, p.Sophos)
				}
				continue
			}
		}
		a.UnownedDevices = append(a.UnownedDevices, p)
	}
	return mismatches
}

// attachSophosEndpoint adds ep to the user's identity-level Sophos slice unless
// an endpoint with the same EndpointID is already present (it may have been
// attached earlier by AddSophos or the bare-username heuristic).
func (a *AssetInventory) attachSophosEndpoint(u *UnifiedUserRecord, ep *sophos.Endpoint) {
	if u.Sophos == nil {
		u.Sophos = &SophosSlice{}
	}
	for i := range u.Sophos.Endpoints {
		if u.Sophos.Endpoints[i].EndpointID == ep.EndpointID {
			return
		}
	}
	u.Sophos.Endpoints = append(u.Sophos.Endpoints, *ep)
}

func pairKey(p DevicePair) string {
	switch {
	case p.JC != nil:
		return p.JC.SystemID
	case p.Sophos != nil:
		return p.Sophos.EndpointID
	}
	return ""
}

func normalizeHostname(h string) string {
	if h == "" {
		return ""
	}
	h = strings.ToLower(strings.TrimSpace(h))
	if i := strings.IndexByte(h, '.'); i >= 0 {
		return h[:i]
	}
	return h
}

func normalizeMAC(m string) string {
	if m == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range m {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
	}
	return strings.ToLower(b.String())
}
