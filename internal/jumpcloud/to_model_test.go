package jumpcloud

import (
	"testing"

	"gogo-assets/internal/model"
)

func TestToPersonSoftware(t *testing.T) {
	systems := []System{
		{Hostname: "alice-mac", OwnerEmail: "Alice@x.com", // mixed case → normalised
			Apps:             []App{{Name: "Slack", Version: "4"}, {Name: "Zoom"}},
			ChromeExtensions: []App{{Name: "uBlock"}}},
		{Hostname: "alice-win", OwnerEmail: "alice@x.com",
			Programs: []App{{Name: "7-Zip"}}},
		{Hostname: "bob-mac", OwnerEmail: "bob@x.com",
			Apps: []App{{Name: "Docker"}}},
		{Hostname: "orphan", OwnerEmail: "", // no owner → contributes nothing
			Apps: []App{{Name: "Ghost"}}},
	}
	saas := []SaaSApp{
		{AppID: "app-figma", Name: "Figma", Status: "APPROVED",
			Accounts: []SaaSAccount{{Email: "alice@x.com"}}},
		{AppID: "app-notion", Name: "Notion", Status: "UNAPPROVED",
			Accounts: []SaaSAccount{{DeviceOwner: "bob@x.com"}}}, // device-agent fallback
	}

	got := ToPersonSoftware(systems, saas, model.Meta{RunDate: "2026-05-05"})

	if len(got) != 2 {
		t.Fatalf("people = %d, want 2 (alice, bob); orphan dropped", len(got))
	}
	// Sorted by email: alice, bob.
	alice, bob := got[0], got[1]
	if alice.OwnerEmail != "alice@x.com" || bob.OwnerEmail != "bob@x.com" {
		t.Fatalf("emails = %q,%q want alice@x.com,bob@x.com", alice.OwnerEmail, bob.OwnerEmail)
	}
	// alice: two devices, three native apps (Slack,Zoom,7-Zip), one extension, one SaaS.
	if len(alice.Devices) != 2 || alice.Devices[0] != "alice-mac" || alice.Devices[1] != "alice-win" {
		t.Errorf("alice devices = %v, want [alice-mac alice-win]", alice.Devices)
	}
	if alice.AppCount != 3 || alice.ExtensionCount != 1 || alice.SaaSCount != 1 {
		t.Errorf("alice counts = app %d ext %d saas %d, want 3/1/1", alice.AppCount, alice.ExtensionCount, alice.SaaSCount)
	}
	if alice.Apps[0].Name != "7-Zip" || alice.Apps[0].Source != "windows" { // sorted by name
		t.Errorf("alice.Apps[0] = %+v, want 7-Zip/windows first", alice.Apps[0])
	}
	if alice.SaaS[0].Name != "Figma" {
		t.Errorf("alice.SaaS = %+v, want Figma", alice.SaaS)
	}
	if alice.Meta.SourceAPI != "jumpcloud.software" {
		t.Errorf("meta.SourceAPI = %q, want jumpcloud.software", alice.Meta.SourceAPI)
	}
	// bob: SaaS resolved via DeviceOwner fallback.
	if bob.SaaSCount != 1 || bob.SaaS[0].Name != "Notion" {
		t.Errorf("bob.SaaS = %+v, want Notion via DeviceOwner", bob.SaaS)
	}

	// Determinism: identical input ⇒ identical output.
	got2 := ToPersonSoftware(systems, saas, model.Meta{RunDate: "2026-05-05"})
	if len(got2) != len(got) || got2[0].OwnerEmail != got[0].OwnerEmail {
		t.Errorf("non-deterministic aggregation")
	}
}

func TestToDevicePointerDiscipline(t *testing.T) {
	enc := false

	t.Run("disk_encrypted nil stays nil (DATA_GAP)", func(t *testing.T) {
		d := ToDevice(System{SystemID: "s1"}, model.Meta{})
		if d.DiskEncrypted != nil {
			t.Errorf("DiskEncrypted = %v, want nil (SI absent → DATA_GAP)", *d.DiskEncrypted)
		}
	})

	t.Run("disk_encrypted *false stays *false (drift, not gap)", func(t *testing.T) {
		d := ToDevice(System{SystemID: "s1", DiskEncrypted: &enc}, model.Meta{})
		if d.DiskEncrypted == nil || *d.DiskEncrypted != false {
			t.Errorf("DiskEncrypted = %v, want *false (collected & off)", d.DiskEncrypted)
		}
	})

	t.Run("mdm_enrolled false → non-nil *false", func(t *testing.T) {
		d := ToDevice(System{SystemID: "s1", MDMEnrolled: false}, model.Meta{})
		if d.MDMEnrolled == nil || *d.MDMEnrolled != false {
			t.Errorf("MDMEnrolled = %v, want non-nil *false", d.MDMEnrolled)
		}
	})
}

func TestToUserPointerDiscipline(t *testing.T) {
	t.Run("mfa false → non-nil *false", func(t *testing.T) {
		u := ToUser(User{UserID: "u1", MFAConfigured: false}, model.Meta{})
		if u.MFAEnabled == nil || *u.MFAEnabled != false {
			t.Errorf("MFAEnabled = %v, want non-nil *false", u.MFAEnabled)
		}
	})

	t.Run("jumpcloud_go nil stays nil", func(t *testing.T) {
		u := ToUser(User{UserID: "u1"}, model.Meta{}) // JCGoEligible nil
		if u.JumpCloudGoEnabled != nil {
			t.Errorf("JumpCloudGoEnabled = %v, want nil", *u.JumpCloudGoEnabled)
		}
	})
}

func TestToPolicyEnforcement(t *testing.T) {
	systems := []System{
		{SystemID: "s1", PolicyStatuses: []PolicyStatus{{Name: "FileVault", Status: "success"}}},
		{SystemID: "s2", PolicyStatuses: []PolicyStatus{{Name: "FileVault", Status: "failed"}}},
		{SystemID: "s3", PolicyStatuses: []PolicyStatus{{Name: "AppLock", Status: "pending"}}},
	}
	got := ToPolicyEnforcement(systems, model.Meta{})
	if len(got) != 2 {
		t.Fatalf("got %d policies, want 2: %+v", len(got), got)
	}
	// Sorted by PolicyID: "AppLock" before "FileVault".
	if got[0].PolicyID != "AppLock" || got[1].PolicyID != "FileVault" {
		t.Fatalf("policies not sorted by id: %q, %q", got[0].PolicyID, got[1].PolicyID)
	}
	fv := got[1]
	if fv.AppliedCount == nil || *fv.AppliedCount != 1 || fv.FailedCount != 1 {
		t.Errorf("FileVault rollup = applied %v failed %d, want applied 1 failed 1", fv.AppliedCount, fv.FailedCount)
	}
}
