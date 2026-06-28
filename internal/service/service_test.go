package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"gogo-assets/internal/assemble"
	"gogo-assets/internal/config"
	"gogo-assets/internal/gworkspace"
	"gogo-assets/internal/inventory"
	"gogo-assets/internal/jumpcloud"
	"gogo-assets/internal/model"
	"gogo-assets/internal/sophos"
)

type fakeModule struct {
	key        Key
	required   bool
	configured bool
	err        error
}

func (m fakeModule) Key() Key                        { return m.key }
func (m fakeModule) DisplayName() string             { return string(m.key) }
func (m fakeModule) ModelService() model.Service     { return model.Service(m.key) }
func (m fakeModule) Required() bool                  { return m.required }
func (m fakeModule) Configured(config.Settings) bool { return m.configured }
func (m fakeModule) MissingConfigMessage() string    { return "missing " + string(m.key) }
func (m fakeModule) RawArtifactName() string         { return string(m.key) + ".json" }
func (m fakeModule) Collect(context.Context, Runtime) (Result, error) {
	if m.err != nil {
		return Result{}, m.err
	}
	return Result{Key: m.key, Output: m.key, Counts: map[string]int{"ok": 1}}, nil
}
func (m fakeModule) IngestInventory(*inventory.AssetInventory, Result) error { return nil }
func (m fakeModule) AppendSources(*assemble.Sources, Result) error           { return nil }

func TestRegistryForTarget(t *testing.T) {
	reg := Registry{
		fakeModule{key: KeyGoogleWorkspace},
		fakeModule{key: KeyJumpCloud},
		fakeModule{key: KeySophos},
	}

	all, err := reg.ForTarget("all")
	if err != nil {
		t.Fatalf("ForTarget(all): %v", err)
	}
	if len(all) != 3 || all[0].Key() != KeyGoogleWorkspace || all[1].Key() != KeyJumpCloud || all[2].Key() != KeySophos {
		t.Fatalf("ForTarget(all) = %#v, want registry order", all)
	}

	jc, err := reg.ForTarget("jc")
	if err != nil {
		t.Fatalf("ForTarget(jc): %v", err)
	}
	if len(jc) != 1 || jc[0].Key() != KeyJumpCloud {
		t.Fatalf("ForTarget(jc) = %#v, want only JumpCloud", jc)
	}

	if _, err := reg.ForTarget("jira"); err == nil {
		t.Fatal("ForTarget(jira) should error until a Jira module is registered")
	}
}

func TestCollectSkipAndErrorPolicy(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("skips unconfigured optional modules", func(t *testing.T) {
		reg := Registry{
			fakeModule{key: KeyGoogleWorkspace, required: true, configured: true},
			fakeModule{key: KeyJumpCloud, configured: false},
			fakeModule{key: KeySophos, configured: true},
		}
		_, _, results, err := Collect(context.Background(), reg, Runtime{Log: log}, "all")
		if err != nil {
			t.Fatalf("Collect: %v", err)
		}
		if len(results) != 2 || results[0].Key != KeyGoogleWorkspace || results[1].Key != KeySophos {
			t.Fatalf("results = %#v, want gws + sophos only", results)
		}
	})

	t.Run("errors on unconfigured required modules", func(t *testing.T) {
		reg := Registry{fakeModule{key: KeyGoogleWorkspace, required: true, configured: false}}
		if _, _, _, err := Collect(context.Background(), reg, Runtime{Log: log}, "all"); err == nil {
			t.Fatal("Collect should error for an unconfigured required module")
		}
	})

	t.Run("wraps collection errors with service key", func(t *testing.T) {
		reg := Registry{fakeModule{key: KeyJumpCloud, configured: true, err: errors.New("boom")}}
		if _, _, _, err := Collect(context.Background(), reg, Runtime{Log: log}, "jc"); err == nil || !errors.Is(err, reg[0].(fakeModule).err) {
			t.Fatalf("Collect error = %v, want wrapped boom", err)
		}
	})
}

func TestConcreteModuleAdapters(t *testing.T) {
	t.Run("google workspace", func(t *testing.T) {
		m := GoogleWorkspaceModule{}
		out := &gworkspace.Output{
			Records: map[string]*gworkspace.UserRecord{
				"a@example.com": {Identity: gworkspace.Identity{Email: "a@example.com"}},
			},
			Queries: []string{"GET /admin/directory/v1/users"},
		}
		r := Result{Output: out}
		inv := inventory.New()
		var src assemble.Sources

		if err := m.IngestInventory(inv, r); err != nil {
			t.Fatal(err)
		}
		if err := m.AppendSources(&src, r); err != nil {
			t.Fatal(err)
		}
		if inv.Users["a@example.com"] == nil || src.GWS["a@example.com"] == nil || len(src.GWSQueries) != 1 {
			t.Fatalf("GWS adapter failed: inv=%+v src=%+v", inv.Users, src)
		}
	})

	t.Run("jumpcloud", func(t *testing.T) {
		m := JumpCloudModule{}
		out := &jumpcloud.Output{
			Systems:  []jumpcloud.System{{SystemID: "s1", Hostname: "mac1", OwnerEmail: "a@example.com"}},
			Users:    map[string]jumpcloud.User{"a@example.com": {Email: "a@example.com"}},
			SaaSApps: []jumpcloud.SaaSApp{{AppID: "app1", Name: "Figma"}},
			Queries:  []string{"GET /api/systems"},
		}
		r := Result{Output: out}
		inv := inventory.New()
		var src assemble.Sources

		if err := m.IngestInventory(inv, r); err != nil {
			t.Fatal(err)
		}
		if err := m.AppendSources(&src, r); err != nil {
			t.Fatal(err)
		}
		if len(inv.JCSystems) != 1 || len(inv.SaaSApps) != 1 || len(src.JCSystems) != 1 || len(src.JCSaaS) != 1 || len(src.JCQueries) != 1 {
			t.Fatalf("JumpCloud adapter failed: inv=%+v src=%+v", inv, src)
		}
	})

	t.Run("sophos", func(t *testing.T) {
		m := SophosModule{}
		out := &sophos.Output{
			Endpoints: []sophos.Endpoint{{EndpointID: "e1", Hostname: "mac1", OwnerEmail: "a@example.com"}},
			Queries:   []string{"GET /endpoint/v1/endpoints"},
		}
		r := Result{Output: out}
		inv := inventory.New()
		var src assemble.Sources

		if err := m.IngestInventory(inv, r); err != nil {
			t.Fatal(err)
		}
		if err := m.AppendSources(&src, r); err != nil {
			t.Fatal(err)
		}
		if len(inv.SophosEndpoints) != 1 || inv.Users["a@example.com"] == nil || len(src.Endpoints) != 1 || len(src.SophosQueries) != 1 {
			t.Fatalf("Sophos adapter failed: inv=%+v src=%+v", inv, src)
		}
	})
}
