package jumpcloud

import "gogo-assets/internal/model"

// ToSaaSApp converts a collected SaaSApp into the canonical model.SaaSApp,
// flattening the rollups (account count, license totals, latest usage) and the
// nested accounts/licenses/contract/SSO slices.
//
// This entity is stored for the SaaS tab and snapshot drill-down only; it is not
// classified in this version, so no monitored fields are populated.
func ToSaaSApp(a SaaSApp, meta model.Meta) model.SaaSApp {
	meta.SourceAPI = "jumpcloud.saas"

	total, assigned, unassigned := a.LicenseTotals()

	out := model.SaaSApp{
		Meta:              meta,
		AppID:             a.AppID,
		Name:              a.Name,
		CatalogAppID:      a.CatalogAppID,
		Category:          a.Category,
		Description:       a.Description,
		Domains:           a.Domains,
		LogoURL:           a.LogoURL,
		Status:            a.Status,
		AccessRestriction: a.AccessRestriction,
		OwnerUserID:       a.OwnerUserID,
		OwnerEmail:        a.OwnerEmail,
		DiscoveredAt:      ptrTime(a.DiscoveredAt),
		DiscoverySources:  a.DiscoverySources,
		SSOConnected:      a.SSOConnected(),
		AccountCount:      a.AccountCount(),
		LicenseTotal:      total,
		LicenseAssigned:   assigned,
		LicenseUnassigned: unassigned,
		LatestUsedAt:      ptrTime(a.LatestUsedAt()),
	}

	for _, s := range a.SSOApps {
		out.SSOApps = append(out.SSOApps, model.SaaSSSOApp{
			ID:           s.ID,
			AppName:      s.AppName,
			DisplayLabel: s.DisplayLabel,
			TemplateName: s.TemplateName,
			Status:       s.Status,
		})
	}
	for _, acc := range a.Accounts {
		out.Accounts = append(out.Accounts, model.SaaSAccount{
			AccountID:    acc.AccountID,
			UserID:       acc.UserID,
			Email:        acc.Email,
			Username:     acc.Username,
			DeviceOwner:  acc.DeviceOwner,
			LatestUsedAt: ptrTime(acc.LatestUsedAt),
		})
	}
	for _, l := range a.Licenses {
		out.Licenses = append(out.Licenses, model.SaaSLicense{
			LicenseID:      l.LicenseID,
			Name:           l.Name,
			Count:          l.Count,
			Assigned:       l.Assigned,
			Unassigned:     l.Unassigned,
			CostPerLicense: l.CostPerLicense,
			IsUnlimited:    l.IsUnlimited,
		})
	}
	if a.Contract != nil {
		out.Contract = &model.SaaSContract{
			Cost:        a.Contract.Cost,
			Currency:    a.Contract.Currency,
			Term:        a.Contract.Term,
			RenewalDate: a.Contract.RenewalDate,
			Notes:       a.Contract.Notes,
		}
	}
	return out
}
