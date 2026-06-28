package jumpcloud

import "testing"

func TestMapSaaSAppListEntry(t *testing.T) {
	raw := map[string]any{
		"id":                     "app1",
		"catalog_app_id":         "cat1",
		"owner_user_id":          "u1",
		"status":                 "UNAPPROVED",
		"access_restriction":     "BLOCK",
		"discovered_at":          "2026-05-01T10:00:00Z",
		"discovery_source_types": []any{"JUMPCLOUD_SSO", "CONNECTOR"},
	}
	a := mapSaaSAppListEntry(raw)
	if a.AppID != "app1" || a.CatalogAppID != "cat1" || a.OwnerUserID != "u1" {
		t.Fatalf("ids wrong: %+v", a)
	}
	if a.Status != "UNAPPROVED" || a.AccessRestriction != "BLOCK" {
		t.Errorf("status/restriction wrong: %q %q", a.Status, a.AccessRestriction)
	}
	if a.DiscoveredAt.IsZero() {
		t.Error("discovered_at not parsed")
	}
	if len(a.DiscoverySources) != 2 {
		t.Errorf("discovery sources = %v", a.DiscoverySources)
	}
}

func TestApplySaaSAppDetail(t *testing.T) {
	a := SaaSApp{AppID: "app1"}
	applySaaSAppDetail(&a, map[string]any{
		"name": "Figma",
		"sso_apps": []any{
			map[string]any{"id": "s1", "template_name": "figma", "status": "CONNECTED"},
			map[string]any{"id": "s2", "status": "NOT_CONNECTED"},
		},
	})
	if a.Name != "Figma" {
		t.Errorf("name = %q", a.Name)
	}
	if len(a.SSOApps) != 2 {
		t.Fatalf("sso apps = %d", len(a.SSOApps))
	}
	if !a.SSOConnected() {
		t.Error("SSOConnected() = false, want true (one connection is CONNECTED)")
	}
}

func TestMapSaaSAccountsJoinsUsage(t *testing.T) {
	accounts := []map[string]any{
		{"id": "acc1", "user_id": "u1", "email": "a@x.com", "username": "a"},
		{"id": "acc2", "email": "b@x.com"},
	}
	usage := []map[string]any{
		{"account_id": "acc1", "latest_used_at": "2026-06-01T12:00:00Z"},
		// a later second row for the same account must win
		{"account_id": "acc1", "latest_used_at": "2026-06-10T12:00:00Z"},
	}
	got, dropped := mapSaaSAccounts(accounts, usage, nil)
	if len(got) != 2 {
		t.Fatalf("accounts = %d", len(got))
	}
	if len(dropped) != 0 {
		t.Errorf("dropped = %d, want 0 (both accounts have identity)", len(dropped))
	}
	if got[0].AccountID != "acc1" || got[0].Email != "a@x.com" {
		t.Errorf("acc1 wrong: %+v", got[0])
	}
	if got[0].LatestUsedAt.IsZero() || got[0].LatestUsedAt.Format("2006-01-02") != "2026-06-10" {
		t.Errorf("acc1 usage not joined/latest: %v", got[0].LatestUsedAt)
	}
	if !got[1].LatestUsedAt.IsZero() {
		t.Errorf("acc2 should have no usage: %v", got[1].LatestUsedAt)
	}
}

func TestMapSaaSAccountsAttributesOrDropsDeviceAgentAccounts(t *testing.T) {
	owners := map[string]string{"u-owner": "owner@x.com"}
	accounts := []map[string]any{
		{"id": "acc1", "email": "real@x.com", "username": "real"},
		// device-agent account with a resolvable owner → kept, attributed.
		{"id": "6a31d22952c3e10001285cdb", "user_id": "u-owner"},
		// device-agent account with no resolvable owner → dropped.
		{"id": "6b42e33063d4f20002396dec", "user_id": "u-unknown"},
		// device-agent account with no user_id at all → dropped.
		{"id": "6c53f44174e5g30003407efd"},
		// username-only account is a real owner and must be kept.
		{"id": "acc3", "username": "svc-bot"},
	}
	got, dropped := mapSaaSAccounts(accounts, nil, owners)
	if len(dropped) != 2 {
		t.Errorf("dropped = %d, want 2", len(dropped))
	}
	if len(got) != 3 {
		t.Fatalf("kept %d accounts, want 3: %+v", len(got), got)
	}

	byID := make(map[string]SaaSAccount, len(got))
	for _, a := range got {
		byID[a.AccountID] = a
	}
	attributed, ok := byID["6a31d22952c3e10001285cdb"]
	if !ok {
		t.Fatal("device-agent account with resolvable owner was dropped, want kept")
	}
	if attributed.DeviceOwner != "owner@x.com" {
		t.Errorf("DeviceOwner = %q, want owner@x.com", attributed.DeviceOwner)
	}
	if attributed.Email != "" || attributed.Username != "" {
		t.Errorf("attributed account should have no own identity: %+v", attributed)
	}
	if _, leaked := byID["6c53f44174e5g30003407efd"]; leaked {
		t.Error("unattributable account leaked through")
	}
}

func TestLicenseTotalsAndLatestUsed(t *testing.T) {
	a := SaaSApp{
		Licenses: []SaaSLicense{
			{Count: 20, Assigned: 12, Unassigned: 8},
			{Count: 5, Assigned: 5, Unassigned: 0, IsUnlimited: true},
		},
	}
	total, assigned, unassigned := a.LicenseTotals()
	// finite tier contributes Count (20); unlimited tier contributes Assigned (5).
	if total != 25 || assigned != 17 || unassigned != 8 {
		t.Errorf("totals = %d/%d/%d, want 25/17/8", total, assigned, unassigned)
	}
}

func TestMapSaaSContractNilWhenEmpty(t *testing.T) {
	if c := mapSaaSContract(nil); c != nil {
		t.Errorf("nil raw → want nil contract, got %+v", c)
	}
	if c := mapSaaSContract(map[string]any{}); c != nil {
		t.Errorf("empty raw → want nil contract, got %+v", c)
	}
	c := mapSaaSContract(map[string]any{"cost": 1200.0, "currency": "USD", "term": "YEARLY_TERM"})
	if c == nil || c.Cost != 1200 || c.Currency != "USD" {
		t.Errorf("contract = %+v", c)
	}
}
