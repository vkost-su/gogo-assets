package jumpcloud

import (
	"testing"
	"time"

	"gogo-assets/internal/model"
)

func TestToSaaSApp(t *testing.T) {
	used := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	disc := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	a := SaaSApp{
		AppID:            "app1",
		Name:             "Figma",
		CatalogAppID:     "cat1",
		Category:         "Design",
		Domains:          []string{"figma.com"},
		Status:           "APPROVED",
		OwnerUserID:      "u1",
		OwnerEmail:       "owner@x.com",
		DiscoveredAt:     disc,
		DiscoverySources: []string{"JUMPCLOUD_SSO"},
		SSOApps:          []SaaSSSOApp{{ID: "s1", Status: "CONNECTED"}},
		Accounts:         []SaaSAccount{{AccountID: "acc1", Email: "a@x.com", LatestUsedAt: used}},
		Licenses:         []SaaSLicense{{LicenseID: "l1", Count: 10, Assigned: 7, Unassigned: 3}},
		Contract:         &SaaSContract{Cost: 1200, Currency: "USD", Term: "YEARLY_TERM"},
	}

	out := ToSaaSApp(a, model.Meta{})

	if out.Meta.SourceAPI != "jumpcloud.saas" {
		t.Errorf("SourceAPI = %q", out.Meta.SourceAPI)
	}
	if out.AppID != "app1" || out.Name != "Figma" || out.Category != "Design" {
		t.Errorf("identity/category wrong: %+v", out)
	}
	if !out.SSOConnected {
		t.Error("SSOConnected = false, want true")
	}
	if out.AccountCount != 1 {
		t.Errorf("AccountCount = %d, want 1", out.AccountCount)
	}
	if out.LicenseTotal != 10 || out.LicenseAssigned != 7 || out.LicenseUnassigned != 3 {
		t.Errorf("license rollups = %d/%d/%d, want 10/7/3", out.LicenseTotal, out.LicenseAssigned, out.LicenseUnassigned)
	}
	if out.LatestUsedAt == nil || !out.LatestUsedAt.Equal(used) {
		t.Errorf("LatestUsedAt = %v, want %v", out.LatestUsedAt, used)
	}
	if out.DiscoveredAt == nil || !out.DiscoveredAt.Equal(disc) {
		t.Errorf("DiscoveredAt = %v, want %v", out.DiscoveredAt, disc)
	}
	if out.Contract == nil || out.Contract.Cost != 1200 {
		t.Errorf("contract not mapped: %+v", out.Contract)
	}
	if len(out.Accounts) != 1 || out.Accounts[0].LatestUsedAt == nil {
		t.Errorf("nested account not mapped: %+v", out.Accounts)
	}
}

func TestToSaaSAppEmptyTimes(t *testing.T) {
	out := ToSaaSApp(SaaSApp{AppID: "app1", Name: "X"}, model.Meta{})
	if out.DiscoveredAt != nil {
		t.Errorf("zero DiscoveredAt → want nil, got %v", out.DiscoveredAt)
	}
	if out.LatestUsedAt != nil {
		t.Errorf("no usage → want nil LatestUsedAt, got %v", out.LatestUsedAt)
	}
	if out.Contract != nil {
		t.Errorf("no contract → want nil, got %+v", out.Contract)
	}
}
