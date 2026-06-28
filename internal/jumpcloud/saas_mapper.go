package jumpcloud

import (
	"sort"
	"time"
)

// mapSaaSAppListEntry builds the base SaaSApp from one /applications list entry.
// The list entry carries no name (only catalog_app_id) — Name is filled later
// from the catalog or the app detail.
func mapSaaSAppListEntry(raw map[string]any) SaaSApp {
	return SaaSApp{
		AppID:             asString(raw["id"]),
		CatalogAppID:      asString(raw["catalog_app_id"]),
		OwnerUserID:       asString(raw["owner_user_id"]),
		Status:            asString(raw["status"]),
		AccessRestriction: asString(raw["access_restriction"]),
		DiscoveredAt:      parseTime(asString(raw["discovered_at"])),
		DiscoverySources:  asStringSlice(raw["discovery_source_types"]),
	}
}

// applySaaSAppDetail merges the /applications/{id} detail into an app: the
// authoritative name and the SSO connections (when expanded).
func applySaaSAppDetail(app *SaaSApp, detail map[string]any) {
	if detail == nil {
		return
	}
	if name := asString(detail["name"]); name != "" {
		app.Name = name
	}
	for _, raw := range asSlice(detail["sso_apps"]) {
		m := asMap(raw)
		if m == nil {
			continue
		}
		app.SSOApps = append(app.SSOApps, SaaSSSOApp{
			ID:           asString(m["id"]),
			AppName:      asString(m["app_name"]),
			DisplayLabel: asString(m["display_label"]),
			TemplateName: asString(m["template_name"]),
			Status:       asString(m["status"]),
		})
	}
}

// mapSaaSAccounts builds owner accounts and joins the usage timestamps in. It
// returns the kept accounts plus the raw records it could not attribute at all.
//
// Accounts with neither an email nor a username are JumpCloud device-agent /
// service accounts: the API surfaces them as a bare ObjectID with no human
// identity (e.g. "6a31d22952c3e10001285cdb"). Rather than show that ObjectID, we
// fall back to the device owner — the SaaS usage was discovered on a managed
// device, so we attribute the account to that device's owner by resolving its
// user_id through the directory (ownerByUserID). Such an account is kept with
// DeviceOwner set and no Email/Username.
//
// Only accounts that have no own identity AND no resolvable owner are dropped;
// their raw records are returned so the caller can log what the API actually
// carried (to discover any further attribution field).
func mapSaaSAccounts(accountsRaw, usageRaw []map[string]any, ownerByUserID map[string]string) (accounts []SaaSAccount, dropped []map[string]any) {
	usageByAccount := make(map[string]time.Time, len(usageRaw))
	for _, u := range usageRaw {
		id := asString(u["account_id"])
		if id == "" {
			continue
		}
		if t := parseTime(asString(u["latest_used_at"])); !t.IsZero() {
			if t.After(usageByAccount[id]) {
				usageByAccount[id] = t
			}
		}
	}

	out := make([]SaaSAccount, 0, len(accountsRaw))
	for _, raw := range accountsRaw {
		id := asString(raw["id"])
		userID := asString(raw["user_id"])
		email := asString(raw["email"])
		username := asString(raw["username"])

		acc := SaaSAccount{
			AccountID:    id,
			UserID:       userID,
			Email:        email,
			Username:     username,
			LatestUsedAt: usageByAccount[id],
		}

		// No own identity → attribute to the device owner via user_id, or drop
		// when even that can't be resolved.
		if email == "" && username == "" {
			owner := ownerByUserID[userID]
			if owner == "" {
				dropped = append(dropped, raw)
				continue
			}
			acc.DeviceOwner = owner
		}
		out = append(out, acc)
	}
	return out, dropped
}

// mapSaaSLicenses builds the license tiers for an application.
func mapSaaSLicenses(rows []map[string]any) []SaaSLicense {
	out := make([]SaaSLicense, 0, len(rows))
	for _, raw := range rows {
		out = append(out, SaaSLicense{
			LicenseID:      asString(raw["id"]),
			Name:           asString(raw["name"]),
			Count:          asInt(raw["count"]),
			Assigned:       asInt(raw["assigned"]),
			Unassigned:     asInt(raw["unassigned"]),
			CostPerLicense: asFloat(raw["cost_per_license"]),
			IsUnlimited:    asBool(raw["is_unlimited"]),
		})
	}
	return out
}

// mapSaaSContract builds the contract summary, or nil when no contract data is
// present.
func mapSaaSContract(raw map[string]any) *SaaSContract {
	if raw == nil {
		return nil
	}
	c := SaaSContract{
		Cost:        asFloat(raw["cost"]),
		Currency:    asString(raw["currency"]),
		Term:        asString(raw["term"]),
		RenewalDate: asString(raw["renewal_date"]),
		Notes:       asString(raw["notes"]),
	}
	if c == (SaaSContract{}) {
		return nil
	}
	return &c
}

// mapCatalogApp builds the catalog service-info entry.
func mapCatalogApp(raw map[string]any) CatalogApp {
	return CatalogApp{
		ID:          asString(raw["id"]),
		Name:        asString(raw["name"]),
		Description: asString(raw["description"]),
		Domains:     asStringSlice(raw["domains"]),
		LogoURL:     asString(raw["logo_url"]),
	}
}

// rawKeys returns the sorted top-level keys of a decoded JSON object, for
// stable diagnostic logging of an unexpected record shape.
func rawKeys(raw map[string]any) []string {
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// asFloat converts a JSON-decoded number/string into float64 (best-effort).
func asFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	}
	return 0
}
