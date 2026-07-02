package jumpcloud

import (
	"sort"
	"strings"
	"time"

	"gogo-assets/internal/model"
)

// ToDevice converts a collected System into the canonical JCDevice.
//
// meta carries CollectedAt/RunDate from the run; SourceAPI is set here.
//
// Pointer discipline (ТЗ §11): DiskEncrypted is copied as-is — it is already a
// *bool that is nil when System Insights did not report encryption, which the
// engine reads as DATA_GAP. MDMEnrolled/Active come from the system object that
// was definitely fetched, so they convert to a non-nil pointer.
func ToDevice(s System, meta model.Meta) model.JCDevice {
	meta.SourceAPI = "jumpcloud.systems"
	return model.JCDevice{
		Meta:           meta,
		SystemID:       s.SystemID,
		Hostname:       s.Hostname,
		Serial:         s.SerialNumber,
		DisplayName:    s.DisplayName,
		OSType:         s.OSType,
		OSFamily:       s.OSFamily,
		OSVersion:      s.OSVersion,
		OSCodename:     s.OSCodename,
		Manufacturer:   s.Manufacturer,
		HardwareModel:  s.HardwareModel,
		OwnerEmail:     s.OwnerEmail,
		DiskEncrypted:  s.DiskEncrypted, // already *bool; nil = SI absent → DATA_GAP
		MDMEnrolled:    ptrBool(s.MDMEnrolled),
		EncryptionType: s.EncryptionType,
		MDMVendor:      s.MDMVendor,
		Active:         ptrBool(s.Active),
		LastContact:    ptrTime(s.LastContact),
		RemoteIP:       s.RemoteIP,
		AgentVersion:   s.AgentVersion,
		MACAddresses:   s.MACAddresses,
	}
}

// ToUser converts a collected User into the canonical JCUser.
//
// MFAConfigured/Password*/account-state come from the user object and convert
// to non-nil pointers. JCGoEligible is already a *bool and is copied as-is
// (nil = the org-level state was unknown → DATA_GAP).
func ToUser(u User, meta model.Meta) model.JCUser {
	meta.SourceAPI = "jumpcloud.users"
	return model.JCUser{
		Meta:                 meta,
		UserID:               u.UserID,
		Email:                u.Email,
		Username:             u.Username,
		FullName:             u.FullName,
		MFAEnabled:           ptrBool(u.MFAConfigured),
		PasswordNeverExpires: ptrBool(u.PasswordNeverExpires),
		JumpCloudGoEnabled:   u.JCGoEligible,
		TOTPEnabled:          ptrBool(u.TOTPEnabled),
		MFARequired:          ptrBool(u.MFARequired),
		PasswordExpiration:   ptrTime(u.PasswordExpirationDate),
		PasswordExpired:      ptrBool(u.PasswordExpired),
		Suspended:            ptrBool(u.Suspended),
		AccountLocked:        ptrBool(u.AccountLocked),
		Activated:            ptrBool(u.Activated),
	}
}

// ToPolicyEnforcement rolls up per-system policy statuses into one record per
// policy. JumpCloud's per-system status carries no policy UUID, so PolicyID is
// the policy name. Output is sorted by PolicyID for deterministic snapshots.
func ToPolicyEnforcement(systems []System, meta model.Meta) []model.JCPolicyEnforcement {
	meta.SourceAPI = "jumpcloud.systems.policies"

	type agg struct{ applied, failed, pending int }
	byPolicy := make(map[string]*agg)
	for _, s := range systems {
		for _, ps := range s.PolicyStatuses {
			a := byPolicy[ps.Name]
			if a == nil {
				a = &agg{}
				byPolicy[ps.Name] = a
			}
			switch ps.Status {
			case "success":
				a.applied++
			case "failed", "error":
				a.failed++
			case "pending":
				a.pending++
			}
		}
	}

	out := make([]model.JCPolicyEnforcement, 0, len(byPolicy))
	for name, a := range byPolicy {
		applied := a.applied
		out = append(out, model.JCPolicyEnforcement{
			Meta:         meta,
			PolicyID:     name,
			AppliedCount: &applied,
			FailedCount:  a.failed,
			PendingCount: a.pending,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PolicyID < out[j].PolicyID })
	return out
}

// ToPersonSoftware aggregates the per-person JumpCloud software footprint: every
// app/extension found on the devices a person owns, plus the SaaS apps they have
// an account in. The person is keyed by lower-cased email — the device
// OwnerEmail is authoritative; a SaaS account falls back to its own email and
// then to the device-owner attributed to a device-agent account. A device with
// no resolvable owner contributes nothing (there is nothing to anchor it to).
// Output, and every nested slice, is sorted for deterministic snapshots.
//
// The record is store-only: it carries no monitored fields and is never
// classified — the allowlist decides at output time what surfaces in the drift
// view, so the FindingKind set stays closed at six.
func ToPersonSoftware(systems []System, saas []SaaSApp, meta model.Meta) []model.JCPersonSoftware {
	meta.SourceAPI = "jumpcloud.software"

	type person struct {
		devices    map[string]struct{}
		apps       []model.JCSoftwareItem
		extensions []model.JCSoftwareItem
		saas       map[string]model.JCSaaSMembership // keyed by AppID (dedup)
	}
	byEmail := make(map[string]*person)
	get := func(email string) *person {
		p := byEmail[email]
		if p == nil {
			p = &person{devices: map[string]struct{}{}, saas: map[string]model.JCSaaSMembership{}}
			byEmail[email] = p
		}
		return p
	}

	// Device software → apps/extensions, attributed to the device owner.
	for _, s := range systems {
		email := normEmail(s.OwnerEmail)
		if email == "" {
			continue
		}
		p := get(email)
		if s.Hostname != "" {
			p.devices[s.Hostname] = struct{}{}
		}
		add := func(apps []App, source string, ext bool) {
			for _, a := range apps {
				if a.Name == "" {
					continue
				}
				item := model.JCSoftwareItem{Name: a.Name, Version: a.Version, Source: source, DeviceHostname: s.Hostname}
				if ext {
					p.extensions = append(p.extensions, item)
				} else {
					p.apps = append(p.apps, item)
				}
			}
		}
		add(s.Apps, "macos", false)
		add(s.Programs, "windows", false)
		add(s.DEBPackages, "deb", false)
		add(s.RPMPackages, "rpm", false)
		add(s.BrowserPlugins, "plugin", true)
		add(s.ChromeExtensions, "chrome", true)
		add(s.FirefoxAddons, "firefox", true)
		add(s.SafariExtensions, "safari", true)
	}

	// SaaS memberships → per person, deduped by AppID.
	for _, app := range saas {
		for _, acct := range app.Accounts {
			email := normEmail(acct.Email)
			if email == "" {
				email = normEmail(acct.DeviceOwner)
			}
			if email == "" {
				continue
			}
			p := get(email)
			if _, ok := p.saas[app.AppID]; !ok {
				p.saas[app.AppID] = model.JCSaaSMembership{AppID: app.AppID, Name: app.Name, Status: app.Status}
			}
		}
	}

	out := make([]model.JCPersonSoftware, 0, len(byEmail))
	for email, p := range byEmail {
		rec := model.JCPersonSoftware{
			Meta:       meta,
			OwnerEmail: email,
			Devices:    sortedStrings(p.devices),
			Apps:       sortSoftware(p.apps),
			Extensions: sortSoftware(p.extensions),
			SaaS:       sortMemberships(p.saas),
		}
		rec.AppCount = len(rec.Apps)
		rec.ExtensionCount = len(rec.Extensions)
		rec.SaaSCount = len(rec.SaaS)
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OwnerEmail < out[j].OwnerEmail })
	return out
}

func normEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func sortedStrings(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func sortSoftware(items []model.JCSoftwareItem) []model.JCSoftwareItem {
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.DeviceHostname != b.DeviceHostname {
			return a.DeviceHostname < b.DeviceHostname
		}
		return a.Version < b.Version
	})
	return items
}

func sortMemberships(set map[string]model.JCSaaSMembership) []model.JCSaaSMembership {
	if len(set) == 0 {
		return nil
	}
	out := make([]model.JCSaaSMembership, 0, len(set))
	for _, m := range set {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].AppID < out[j].AppID
	})
	return out
}

func ptrBool(b bool) *bool { return &b }

func ptrTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
