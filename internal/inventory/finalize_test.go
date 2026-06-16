package inventory

import (
	"testing"

	"gogo-assets/internal/gworkspace"
	"gogo-assets/internal/jumpcloud"
	"gogo-assets/internal/sophos"
)

func gwsUser(email string) *gworkspace.UserRecord {
	return &gworkspace.UserRecord{Identity: gworkspace.Identity{Email: email}}
}

// TestFinalize_PairBySerial pairs a JC system and a Sophos endpoint by serial
// (case-insensitive), attributes the pair to the shared owner, and counts it.
func TestFinalize_PairBySerial(t *testing.T) {
	inv := New()
	inv.AddGoogle(map[string]*gworkspace.UserRecord{"a@x.com": gwsUser("a@x.com")})
	inv.AddJC(
		[]jumpcloud.System{{SystemID: "s1", Hostname: "h1", SerialNumber: "ABC", OwnerEmail: "a@x.com"}},
		map[string]jumpcloud.User{"a@x.com": {UserID: "u1", Email: "a@x.com"}},
	)
	inv.AddSophos([]sophos.Endpoint{{EndpointID: "e1", Hostname: "h1", SerialNumber: "abc", OwnerEmail: "a@x.com"}})
	inv.Finalize()

	u := inv.Users["a@x.com"]
	if u == nil || len(u.Devices) != 1 {
		t.Fatalf("want 1 device for a@x.com, got %v", u)
	}
	d := u.Devices[0]
	if d.MatchKey != "serial" || d.JC == nil || d.Sophos == nil {
		t.Errorf("want serial-paired device with both sides, got %+v", d)
	}
	if inv.MatchStats["paired"] != 1 {
		t.Errorf("paired = %d, want 1", inv.MatchStats["paired"])
	}
}

// TestFinalize_PairByHostnameFallback pairs by normalised hostname when neither
// side carries a serial.
func TestFinalize_PairByHostnameFallback(t *testing.T) {
	inv := New()
	inv.AddGoogle(map[string]*gworkspace.UserRecord{"a@x.com": gwsUser("a@x.com")})
	inv.AddJC(
		[]jumpcloud.System{{SystemID: "s1", Hostname: "host1.local", OwnerEmail: "a@x.com"}},
		map[string]jumpcloud.User{},
	)
	inv.AddSophos([]sophos.Endpoint{{EndpointID: "e1", Hostname: "HOST1", OwnerEmail: "a@x.com"}})
	inv.Finalize()

	u := inv.Users["a@x.com"]
	if u == nil || len(u.Devices) != 1 || u.Devices[0].MatchKey != "hostname" {
		t.Fatalf("want 1 hostname-paired device, got %+v", u)
	}
	if inv.MatchStats["paired"] != 1 {
		t.Errorf("paired = %d, want 1", inv.MatchStats["paired"])
	}
}

// TestFinalize_OwnerMismatchJCWins records an owner mismatch and attributes the
// pair to the JC owner.
func TestFinalize_OwnerMismatchJCWins(t *testing.T) {
	inv := New()
	inv.AddGoogle(map[string]*gworkspace.UserRecord{
		"a@x.com": gwsUser("a@x.com"),
		"b@x.com": gwsUser("b@x.com"),
	})
	inv.AddJC(
		[]jumpcloud.System{{SystemID: "s1", SerialNumber: "ABC", OwnerEmail: "a@x.com"}},
		map[string]jumpcloud.User{},
	)
	inv.AddSophos([]sophos.Endpoint{{EndpointID: "e1", SerialNumber: "ABC", OwnerEmail: "b@x.com"}})
	inv.Finalize()

	if inv.MatchStats["owner_mismatch"] != 1 {
		t.Errorf("owner_mismatch = %d, want 1", inv.MatchStats["owner_mismatch"])
	}
	if a := inv.Users["a@x.com"]; a == nil || len(a.Devices) != 1 {
		t.Errorf("JC owner a@x.com should hold the pair, got %+v", a)
	}
	if b := inv.Users["b@x.com"]; b != nil && len(b.Devices) != 0 {
		t.Errorf("sophos owner b@x.com should not hold the pair, got %+v", b.Devices)
	}
}

// TestFinalize_BareUsernameHeuristic attaches a Sophos endpoint whose owner is a
// bare login (no "@") to <login>@<primary-domain>.
func TestFinalize_BareUsernameHeuristic(t *testing.T) {
	inv := New()
	inv.AddGoogle(map[string]*gworkspace.UserRecord{
		"alice@x.com": gwsUser("alice@x.com"),
		"bob@x.com":   gwsUser("bob@x.com"),
	})
	inv.AddJC([]jumpcloud.System{}, map[string]jumpcloud.User{})
	inv.AddSophos([]sophos.Endpoint{{EndpointID: "e1", Hostname: "h1", OwnerLogin: "alice"}})
	inv.Finalize()

	if inv.MatchStats["bare_username_matched"] != 1 {
		t.Errorf("bare_username_matched = %d, want 1", inv.MatchStats["bare_username_matched"])
	}
	u := inv.Users["alice@x.com"]
	if u == nil || u.Sophos == nil || len(u.Sophos.Endpoints) != 1 {
		t.Fatalf("endpoint should attach to alice@x.com, got %+v", u)
	}
}

// TestFinalize_SophosCoverageFromDeviceJoin reproduces the real-world case where
// a Sophos endpoint's owner_login is a "HOST\user" local login that resolves to
// no email. The endpoint must still count as identity-level Sophos coverage for
// the user JumpCloud says owns the paired device.
func TestFinalize_SophosCoverageFromDeviceJoin(t *testing.T) {
	inv := New()
	inv.AddGoogle(map[string]*gworkspace.UserRecord{"aliana@superunlimited.com": gwsUser("aliana@superunlimited.com")})
	inv.AddJC(
		[]jumpcloud.System{{
			SystemID:   "s1",
			Hostname:   "UA-L-M-JV9PR2D57J",
			OwnerEmail: "aliana@superunlimited.com",
		}},
		map[string]jumpcloud.User{},
	)
	// owner_login is a HOST\user local login: no "@", not a bare username that
	// maps to <login>@domain, and OwnerEmail is empty.
	inv.AddSophos([]sophos.Endpoint{{
		EndpointID: "e1",
		Hostname:   "UA-L-M-JV9PR2D57J",
		OwnerLogin: `UA-L-M-JV9PR2D57J\aliana`,
	}})
	inv.Finalize()

	u := inv.Users["aliana@superunlimited.com"]
	if u == nil || len(u.Devices) != 1 || u.Devices[0].MatchKey != "hostname" {
		t.Fatalf("want 1 hostname-paired device, got %+v", u)
	}
	if u.Sophos == nil || len(u.Sophos.Endpoints) != 1 {
		t.Fatalf("Sophos coverage should be backfilled from the device join, got %+v", u.Sophos)
	}
	if u.Sophos.Endpoints[0].EndpointID != "e1" {
		t.Errorf("backfilled endpoint = %q, want e1", u.Sophos.Endpoints[0].EndpointID)
	}
}

// TestFinalize_NoDuplicateSophosBackfill ensures the device-join backfill does
// not duplicate an endpoint already attached by AddSophos.
func TestFinalize_NoDuplicateSophosBackfill(t *testing.T) {
	inv := New()
	inv.AddGoogle(map[string]*gworkspace.UserRecord{"a@x.com": gwsUser("a@x.com")})
	inv.AddJC(
		[]jumpcloud.System{{SystemID: "s1", SerialNumber: "ABC", OwnerEmail: "a@x.com"}},
		map[string]jumpcloud.User{},
	)
	inv.AddSophos([]sophos.Endpoint{{EndpointID: "e1", SerialNumber: "ABC", OwnerEmail: "a@x.com"}})
	inv.Finalize()

	u := inv.Users["a@x.com"]
	if u == nil || u.Sophos == nil || len(u.Sophos.Endpoints) != 1 {
		t.Fatalf("endpoint should appear exactly once, got %+v", u.Sophos)
	}
}

// TestFinalize_UnownedDevice routes a device with no resolvable owner to
// UnownedDevices.
func TestFinalize_UnownedDevice(t *testing.T) {
	inv := New()
	inv.AddJC(
		[]jumpcloud.System{{SystemID: "s1", Hostname: "orphan"}}, // no OwnerEmail
		map[string]jumpcloud.User{},
	)
	inv.AddSophos([]sophos.Endpoint{})
	inv.Finalize()

	if inv.MatchStats["jc_only"] != 1 {
		t.Errorf("jc_only = %d, want 1", inv.MatchStats["jc_only"])
	}
	if inv.MatchStats["unowned"] != 1 || len(inv.UnownedDevices) != 1 {
		t.Errorf("want 1 unowned device, got stats=%d slice=%d",
			inv.MatchStats["unowned"], len(inv.UnownedDevices))
	}
}
