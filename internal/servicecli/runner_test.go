package servicecli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gogo-assets/internal/assemble"
	"gogo-assets/internal/config"
	"gogo-assets/internal/inventory"
	"gogo-assets/internal/model"
	"gogo-assets/internal/service"
)

type fakeModule struct{}

func (fakeModule) Key() service.Key                { return "fake" }
func (fakeModule) DisplayName() string             { return "Fake" }
func (fakeModule) ModelService() model.Service     { return "Fake" }
func (fakeModule) Required() bool                  { return true }
func (fakeModule) Configured(config.Settings) bool { return true }
func (fakeModule) MissingConfigMessage() string    { return "missing fake config" }
func (fakeModule) RawArtifactName() string         { return "fake_raw.json" }
func (fakeModule) IngestInventory(*inventory.AssetInventory, service.Result) error {
	return nil
}
func (fakeModule) AppendSources(*assemble.Sources, service.Result) error { return nil }
func (fakeModule) Collect(context.Context, service.Runtime) (service.Result, error) {
	return service.Result{
		Key:         "fake",
		Service:     "Fake",
		DisplayName: "Fake",
		Output:      map[string]any{"ok": true},
		Queries:     []string{"GET /fake/v1/things"},
		Counts:      map[string]int{"things": 1},
	}, nil
}

func TestRunJSONNoPersist(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var stdout bytes.Buffer
	if err := Run([]string{"--json", "--no-persist"}, Options{
		Name:   "fake",
		Module: fakeModule{},
		Stdout: &stdout,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var got map[string]bool
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout JSON = %q: %v", stdout.String(), err)
	}
	if !got["ok"] {
		t.Fatalf("stdout JSON = %v, want ok=true", got)
	}
	if _, err := os.Stat(filepath.Join("local", "current", "fake_raw.json")); !os.IsNotExist(err) {
		t.Fatalf("raw artifact should not exist with --no-persist, stat err=%v", err)
	}
}

func TestRunPersistsRawArtifact(t *testing.T) {
	root := t.TempDir()
	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LOCAL_DIR", root)

	if err := Run(nil, Options{Name: "fake", Module: fakeModule{}}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var got map[string]bool
	b, err := os.ReadFile(filepath.Join(root, "current", "fake_raw.json"))
	if err != nil {
		t.Fatalf("read raw artifact: %v", err)
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("raw artifact JSON = %q: %v", string(b), err)
	}
	if !got["ok"] {
		t.Fatalf("raw artifact = %v, want ok=true", got)
	}
}

func TestRunValidatesSelectedConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("JC_API_KEY", "")

	err := Run([]string{"--no-persist"}, Options{
		Name:        "fake",
		Module:      fakeModule{},
		LoadOptions: config.LoadOptions{RequireJumpCloud: true},
	})
	if err == nil {
		t.Fatal("Run should fail when selected config is missing")
	}
}
