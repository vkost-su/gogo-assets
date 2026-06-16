package assemble

import (
	"reflect"
	"testing"
	"time"

	"gogo-assets/internal/gworkspace"
	"gogo-assets/internal/jumpcloud"
	"gogo-assets/internal/model"
	"gogo-assets/internal/sophos"
)

func ptr[T any](v T) *T { return &v }

// sample builds a small but representative Sources covering every shard.
func sample() Sources {
	return Sources{
		JCSystems: []jumpcloud.System{
			// Deliberately out of SystemID order to prove Build sorts.
			{
				SystemID:      "sys-b",
				Hostname:      "beta",
				SerialNumber:  "SNB",
				OSFamily:      "windows",
				MDMEnrolled:   true,
				DiskEncrypted: nil, // SI absent → must stay nil → DATA_GAP
				OwnerEmail:    "b@example.com",
				PolicyStatuses: []jumpcloud.PolicyStatus{
					{Name: "disk-policy", Status: "success"},
				},
			},
			{
				SystemID:      "sys-a",
				Hostname:      "alpha",
				SerialNumber:  "SNA",
				OSFamily:      "darwin",
				MDMEnrolled:   true,
				DiskEncrypted: ptr(true),
				OwnerEmail:    "a@example.com",
			},
		},
		JCUsers: map[string]jumpcloud.User{
			"a@example.com": {UserID: "u1", Email: "a@example.com", Username: "a", MFAConfigured: true},
		},
		Endpoints: []sophos.Endpoint{
			{EndpointID: "ep-1", Hostname: "alpha", TamperProtected: true, HealthOverall: "good", AlertCount: 2},
		},
		GWS: map[string]*gworkspace.UserRecord{
			"a@example.com": {
				Identity: gworkspace.Identity{Email: "a@example.com", FullName: "A", IsAdmin: true},
				Auth:     gworkspace.AuthPosture{Is2SVEnrolled: true, Is2SVEnforced: true},
				Devices: []gworkspace.Device{
					{DeviceID: "dev-1", DeviceKind: gworkspace.DeviceMacOS, OwnerEmail: "a@example.com"},
				},
			},
		},
	}
}

func TestBuild_StampsAndCounts(t *testing.T) {
	ts := time.Date(2026, 6, 12, 10, 30, 0, 0, time.UTC)
	snap := Build(sample(), ts, "2026-06-12")

	if snap.SchemaVersion != model.SchemaVersion {
		t.Errorf("schema_version = %q, want %q", snap.SchemaVersion, model.SchemaVersion)
	}
	if snap.RunDate != "2026-06-12" {
		t.Errorf("run_date = %q, want 2026-06-12", snap.RunDate)
	}
	if !snap.RunTimestamp.Equal(ts) {
		t.Errorf("run_timestamp = %v, want %v", snap.RunTimestamp, ts)
	}

	if got := len(snap.JumpCloud.Devices); got != 2 {
		t.Errorf("jc devices = %d, want 2", got)
	}
	if got := len(snap.JumpCloud.Identity); got != 1 {
		t.Errorf("jc identity = %d, want 1", got)
	}
	if got := len(snap.JumpCloud.PolicyEnforcement); got != 1 {
		t.Errorf("jc policy enforcement = %d, want 1", got)
	}
	if got := len(snap.Sophos.Endpoints); got != 1 {
		t.Errorf("sophos endpoints = %d, want 1", got)
	}
	if got := len(snap.GoogleWorkspace.Identity); got != 1 {
		t.Errorf("gws identity = %d, want 1", got)
	}
	if got := len(snap.GoogleWorkspace.Devices); got != 1 {
		t.Errorf("gws devices = %d, want 1", got)
	}
}

func TestBuild_SortsDevicesByIdentity(t *testing.T) {
	snap := Build(sample(), time.Unix(0, 0).UTC(), "2026-06-12")
	if snap.JumpCloud.Devices[0].SystemID != "sys-a" || snap.JumpCloud.Devices[1].SystemID != "sys-b" {
		t.Errorf("devices not sorted by SystemID: %q, %q",
			snap.JumpCloud.Devices[0].SystemID, snap.JumpCloud.Devices[1].SystemID)
	}
}

// TestBuild_PreservesPointerRule guards ТЗ §11: a nil monitored pointer must
// survive conversion as nil (DATA_GAP), while a collected value becomes non-nil.
func TestBuild_PreservesPointerRule(t *testing.T) {
	snap := Build(sample(), time.Unix(0, 0).UTC(), "2026-06-12")

	byID := map[string]model.JCDevice{}
	for _, d := range snap.JumpCloud.Devices {
		byID[d.SystemID] = d
	}
	if byID["sys-b"].DiskEncrypted != nil {
		t.Error("sys-b DiskEncrypted should stay nil (DATA_GAP), got non-nil")
	}
	if byID["sys-a"].DiskEncrypted == nil || *byID["sys-a"].DiskEncrypted != true {
		t.Error("sys-a DiskEncrypted should be *true")
	}
	// MDMEnrolled comes from the always-fetched system object → never nil.
	if byID["sys-a"].MDMEnrolled == nil {
		t.Error("sys-a MDMEnrolled should be non-nil")
	}
}

func TestBuild_StampsProvenance(t *testing.T) {
	ts := time.Date(2026, 6, 12, 10, 30, 0, 0, time.UTC)
	snap := Build(sample(), ts, "2026-06-12")

	d := snap.JumpCloud.Devices[0]
	if d.Meta.SourceAPI != "jumpcloud.systems" {
		t.Errorf("device SourceAPI = %q, want jumpcloud.systems", d.Meta.SourceAPI)
	}
	if !d.Meta.CollectedAt.Equal(ts) || d.Meta.RunDate != "2026-06-12" {
		t.Errorf("device meta stamp wrong: %+v", d.Meta)
	}
	if snap.Sophos.Endpoints[0].Meta.SourceAPI != "sophos.endpoints" {
		t.Errorf("endpoint SourceAPI = %q", snap.Sophos.Endpoints[0].Meta.SourceAPI)
	}
	if snap.GoogleWorkspace.Identity[0].Meta.SourceAPI != "gworkspace.directory" {
		t.Errorf("gws user SourceAPI = %q", snap.GoogleWorkspace.Identity[0].Meta.SourceAPI)
	}
}

func TestBuild_AccountHealthDerived(t *testing.T) {
	snap := Build(sample(), time.Unix(0, 0).UTC(), "2026-06-12")
	h := snap.Sophos.AccountHealth
	if h == nil {
		t.Fatal("account health should be derived from endpoints")
	}
	if h.EndpointsTotal != 1 || h.HealthGood != 1 || h.TotalAlerts != 2 {
		t.Errorf("account health rollup wrong: %+v", h)
	}
}

// TestBuild_Deterministic proves that map iteration order cannot leak into the
// output: building the same Sources twice yields identical snapshots.
func TestBuild_Deterministic(t *testing.T) {
	ts := time.Date(2026, 6, 12, 10, 30, 0, 0, time.UTC)
	a := Build(sample(), ts, "2026-06-12")
	b := Build(sample(), ts, "2026-06-12")
	if !reflect.DeepEqual(a, b) {
		t.Error("Build is not deterministic for identical input")
	}
}

func TestBuild_EmptySourcesProducesEmptyShards(t *testing.T) {
	snap := Build(Sources{}, time.Unix(0, 0).UTC(), "2026-06-12")
	if len(snap.JumpCloud.Devices) != 0 || len(snap.GoogleWorkspace.Identity) != 0 || len(snap.Sophos.Endpoints) != 0 {
		t.Error("empty sources should produce empty shards")
	}
	if snap.Sophos.AccountHealth != nil {
		t.Error("no endpoints → account health should be nil")
	}
}
