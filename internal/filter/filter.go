// Package filter applies baseline whitelist filters once, immediately after
// collection and before inventory finalization and canonical assembly.
//
// It depends on collector packages and allowlist only — not internal/model.
package filter

import (
	"strings"

	"gogo-assets/internal/allowlist"
	"gogo-assets/internal/assemble"
	"gogo-assets/internal/gworkspace"
	"gogo-assets/internal/inventory"
	"gogo-assets/internal/jumpcloud"
)

// Apply purges allowlisted entries from inv and src in place and returns purge
// stats for logging. inv.JCSystems and src.JCSystems share the same underlying
// slice; SaaS and GWS records are similarly aliased between inventory and sources.
func Apply(inv *inventory.AssetInventory, src *assemble.Sources, f allowlist.Set) Stats {
	st := Stats{Loaded: f.Loaded()}
	if inv == nil {
		return st
	}
	total := len(inv.JCSystems)
	for i := range inv.JCSystems {
		sys := &inv.JCSystems[i]
		swBefore := jumpcloud.SoftwareCount(*sys)
		luBefore := len(sys.LocalUsers)
		st.SoftwareBefore += swBefore
		st.LocalUsersBefore += luBefore

		filterSystem(sys, f)

		swAfter := jumpcloud.SoftwareCount(*sys)
		luAfter := len(sys.LocalUsers)
		st.SoftwareAfter += swAfter
		st.LocalUsersAfter += luAfter

		if swBefore > swAfter || luBefore > luAfter {
			st.Devices = append(st.Devices, DeviceStats{
				Index:            i + 1,
				Total:            total,
				Hostname:         sys.Hostname,
				Owner:            sys.OwnerEmail,
				SoftwareBefore:   swBefore,
				SoftwareAfter:    swAfter,
				LocalUsersBefore: luBefore,
				LocalUsersAfter:  luAfter,
			})
		}
	}
	if len(inv.SaaSApps) > 0 {
		before, after := filterSaaSApps(inv.SaaSApps, f.SaaSOwner)
		st.SaaSAccountsBefore += before
		st.SaaSAccountsAfter += after
	}
	if src != nil && len(src.GWS) > 0 {
		before, after := filterGWS(src.GWS, f.GWApps)
		st.GWSAppsBefore += before
		st.GWSAppsAfter += after
	}
	return st
}

func filterSystem(s *jumpcloud.System, f allowlist.Set) {
	var userList *allowlist.List
	switch s.OSFamily {
	case "darwin":
		userList = f.LocalUsersMac
	case "windows":
		userList = f.LocalUsersWin
	}
	s.LocalUsers = allowlist.Unresolved(userList, s.LocalUsers, func(u string) string { return u })

	sw := f.Software
	s.Apps = purgeApps(s.Apps, sw)
	s.Programs = purgeApps(s.Programs, sw)
	s.DEBPackages = purgeApps(s.DEBPackages, sw)
	s.RPMPackages = purgeApps(s.RPMPackages, sw)
	s.BrowserPlugins = purgeApps(s.BrowserPlugins, sw)
	s.ChromeExtensions = purgeApps(s.ChromeExtensions, sw)
	s.FirefoxAddons = purgeApps(s.FirefoxAddons, sw)
	s.SafariExtensions = purgeApps(s.SafariExtensions, sw)
}

func purgeApps(apps []jumpcloud.App, l *allowlist.List) []jumpcloud.App {
	return allowlist.Unresolved(l, apps, func(a jumpcloud.App) string { return a.Name })
}

func filterSaaSApps(apps []jumpcloud.SaaSApp, domains *allowlist.DomainList) (before, after int) {
	for i := range apps {
		before += len(apps[i].Accounts)
		apps[i].Accounts = purgeSaaSAccounts(apps[i].Accounts, domains)
		after += len(apps[i].Accounts)
	}
	return before, after
}

func purgeSaaSAccounts(accts []jumpcloud.SaaSAccount, d *allowlist.DomainList) []jumpcloud.SaaSAccount {
	if d == nil || d.Empty() {
		return accts
	}
	out := make([]jumpcloud.SaaSAccount, 0, len(accts))
	for _, a := range accts {
		if saasAccountAllowed(a, d) {
			continue
		}
		out = append(out, a)
	}
	return out
}

func saasAccountAllowed(a jumpcloud.SaaSAccount, d *allowlist.DomainList) bool {
	if a.Email != "" && d.Allowed(a.Email) {
		return true
	}
	if strings.Contains(a.DeviceOwner, "@") && d.Allowed(a.DeviceOwner) {
		return true
	}
	return false
}

func filterGWS(users map[string]*gworkspace.UserRecord, apps *allowlist.List) (before, after int) {
	for _, rec := range users {
		if rec == nil {
			continue
		}
		before += len(rec.ConnectedApps)
		rec.ConnectedApps = allowlist.Unresolved(apps, rec.ConnectedApps,
			func(c gworkspace.ConnectedApp) string { return c.DisplayText })
		after += len(rec.ConnectedApps)
	}
	return before, after
}
