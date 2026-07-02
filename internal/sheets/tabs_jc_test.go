package sheets

import (
	"testing"

	"gogo-assets/internal/inventory"
	"gogo-assets/internal/jumpcloud"
)

func TestFmtJCHostname(t *testing.T) {
	tests := []struct {
		host, display, want string
	}{
		{"mac1", "mac1", "mac1"},
		{"mac1", "MacBook Pro", "mac1 (MacBook Pro)"},
		{"mac1", "", "mac1"},
		{"MAC1", "mac1", "MAC1"},
	}
	for _, tt := range tests {
		got := fmtJCHostname(jumpcloud.System{Hostname: tt.host, DisplayName: tt.display})
		if got != tt.want {
			t.Errorf("fmtJCHostname(%q, %q) = %q, want %q", tt.host, tt.display, got, tt.want)
		}
	}
}

func TestJCDeviceDriftIncludesOwnerUser(t *testing.T) {
	inv := &inventory.AssetInventory{
		JCSystems: []jumpcloud.System{
			{SystemID: "dev-clean", OwnerEmail: "clean@x.com"},
			{SystemID: "dev-bad", OwnerEmail: "bad@x.com"},
		},
	}
	deviceDrift := map[string]struct{}{"dev-clean": {}}
	userDrift := map[string]struct{}{"bad@x.com": {}}

	got := JCDeviceDrift(inv, deviceDrift, userDrift)
	for _, id := range []string{"dev-clean", "dev-bad"} {
		if _, ok := got[id]; !ok {
			t.Errorf("drift set missing %q: %v", id, got)
		}
	}
	if len(got) != 2 {
		t.Errorf("drift set = %v, want 2 entries", got)
	}
}
