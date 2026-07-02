package filter

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gogo-assets/internal/allowlist"
	"gogo-assets/internal/assemble"
	"gogo-assets/internal/gworkspace"
	"gogo-assets/internal/inventory"
	"gogo-assets/internal/jumpcloud"
)

func TestApplyStats(t *testing.T) {
	inv := inventory.New()
	inv.JCSystems = []jumpcloud.System{{
		SystemID:   "s1",
		OSFamily:   "darwin",
		OwnerEmail: "a@x.com",
		Hostname:   "mac1",
		LocalUsers: []string{"_spotlight", "alice"},
		Apps: []jumpcloud.App{
			{Name: "Google Chrome"},
			{Name: "Sketchy Tool"},
		},
	}}
	src := assemble.Sources{JCSystems: inv.JCSystems}

	sw, _ := allowlist.Load(writeList(t, "Google Chrome\n"))
	mac, _ := allowlist.Load(writeList(t, "_*\n"))
	st := Apply(inv, &src, allowlist.Set{Software: sw, LocalUsersMac: mac})

	if !st.Loaded || !st.Purged() {
		t.Fatalf("stats = %+v, want loaded and purged", st)
	}
	if st.SoftwareBefore != 2 || st.SoftwareAfter != 1 {
		t.Errorf("software before/after = %d/%d, want 2/1", st.SoftwareBefore, st.SoftwareAfter)
	}
	if len(st.Devices) != 1 {
		t.Fatalf("devices = %d, want 1", len(st.Devices))
	}
	d := st.Devices[0]
	if d.SoftwareAfter != 1 || d.SoftwareBefore != 2 {
		t.Errorf("device software = %d collected %d", d.SoftwareAfter, d.SoftwareBefore)
	}
	if d.LocalUsersAfter != 1 || d.LocalUsersBefore != 2 {
		t.Errorf("device local users = %d collected %d", d.LocalUsersAfter, d.LocalUsersBefore)
	}
}

func TestApplyStatsEmptyFilters(t *testing.T) {
	inv := inventory.New()
	inv.JCSystems = []jumpcloud.System{{Apps: []jumpcloud.App{{Name: "A"}}}}
	st := Apply(inv, nil, allowlist.Set{})
	if st.Loaded || st.Purged() || len(st.Devices) != 0 {
		t.Errorf("empty filters: %+v", st)
	}
}

func TestApplyJCLocalUsersAndSoftware(t *testing.T) {
	inv := inventory.New()
	inv.JCSystems = []jumpcloud.System{{
		SystemID:   "s1",
		OSFamily:   "darwin",
		LocalUsers: []string{"_spotlight", "alice", "root"},
		Apps: []jumpcloud.App{
			{Name: "Google Chrome"},
			{Name: "Sketchy Tool"},
		},
	}}
	src := assemble.Sources{JCSystems: inv.JCSystems}

	sw, _ := allowlist.Load(writeList(t, "Google Chrome\n"))
	mac, _ := allowlist.Load(writeList(t, "_*\nroot\n"))
	Apply(inv, &src, allowlist.Set{Software: sw, LocalUsersMac: mac})

	sys := inv.JCSystems[0]
	if !reflect.DeepEqual(sys.LocalUsers, []string{"alice"}) {
		t.Errorf("LocalUsers = %v, want [alice]", sys.LocalUsers)
	}
	if len(sys.Apps) != 1 || sys.Apps[0].Name != "Sketchy Tool" {
		t.Errorf("Apps = %+v, want only Sketchy Tool", sys.Apps)
	}
}

func TestApplySaaSOwnerDomains(t *testing.T) {
	inv := inventory.New()
	inv.SaaSApps = []jumpcloud.SaaSApp{{
		AppID: "a1",
		Accounts: []jumpcloud.SaaSAccount{
			{Email: "user@superunlimited.com"},
			{Email: "other@example.com"},
			{DeviceOwner: "via@superunlimited.com"},
		},
	}}
	dom, err := allowlist.LoadDomains(writeList(t, "superunlimited.com\n@corp.io\n"))
	if err != nil {
		t.Fatal(err)
	}
	st := Apply(inv, nil, allowlist.Set{SaaSOwner: dom})

	got := inv.SaaSApps[0].Accounts
	if len(got) != 1 || got[0].Email != "other@example.com" {
		t.Errorf("accounts = %+v, want only other@example.com", got)
	}
	if st.SaaSAccountsBefore != 3 || st.SaaSAccountsAfter != 1 {
		t.Errorf("saas stats = %d/%d, want 3/1", st.SaaSAccountsBefore, st.SaaSAccountsAfter)
	}
}

func TestApplyGWSConnectedApps(t *testing.T) {
	rec := &gworkspace.UserRecord{
		Identity: gworkspace.Identity{Email: "a@x.com"},
		ConnectedApps: []gworkspace.ConnectedApp{
			{DisplayText: "Google Drive"},
			{DisplayText: "Unknown App"},
		},
	}
	inv := inventory.New()
	inv.AddGoogle(map[string]*gworkspace.UserRecord{"a@x.com": rec})
	src := assemble.Sources{GWS: map[string]*gworkspace.UserRecord{"a@x.com": rec}}

	gw, err := allowlist.Load(writeList(t, "Google*\n"))
	if err != nil {
		t.Fatal(err)
	}
	st := Apply(inv, &src, allowlist.Set{GWApps: gw})

	want := []gworkspace.ConnectedApp{{DisplayText: "Unknown App"}}
	if !reflect.DeepEqual(rec.ConnectedApps, want) {
		t.Errorf("ConnectedApps = %+v, want %+v", rec.ConnectedApps, want)
	}
	if st.GWSAppsBefore != 2 || st.GWSAppsAfter != 1 {
		t.Errorf("gws stats = %d/%d, want 2/1", st.GWSAppsBefore, st.GWSAppsAfter)
	}
}

func writeList(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "list.filter")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
