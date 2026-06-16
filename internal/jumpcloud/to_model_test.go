package jumpcloud

import (
	"testing"

	"gogo-assets/internal/model"
)

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
