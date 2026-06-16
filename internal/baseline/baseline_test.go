package baseline

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gogo-assets/internal/model"
)

func TestExpectationUnmarshal(t *testing.T) {
	tests := []struct {
		give    string
		wantVal string
		wantSev model.Severity
	}{
		{give: `"true"`, wantVal: "true", wantSev: ""},
		{give: `{"value":"false","severity":"HIGH"}`, wantVal: "false", wantSev: model.SevHigh},
		{give: `{"value":"0"}`, wantVal: "0", wantSev: ""},
	}
	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			var e Expectation
			if err := json.Unmarshal([]byte(tt.give), &e); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if e.Value != tt.wantVal || e.Severity != tt.wantSev {
				t.Errorf("got {%q,%q}, want {%q,%q}", e.Value, e.Severity, tt.wantVal, tt.wantSev)
			}
		})
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadNoBaseline(t *testing.T) {
	if _, err := Load(t.TempDir()); !errors.Is(err, ErrNoBaseline) {
		t.Errorf("Load(empty dir) = %v, want ErrNoBaseline", err)
	}
}

func TestLoadValidatesExpectedKeys(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "classes.json"), `{
		"classes": [
			{"id": "bad", "match": {"os_family": "darwin"},
			 "expected": {"not_a_monitored_field": "true"}}
		]
	}`)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load accepted an Expected key that is not a monitored field")
	}
}

func TestLoadValid(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "classes.json"), `{
		"classes": [
			{"id": "macos", "priority": 1, "match": {"os_family": "darwin"},
			 "expected": {"disk_encrypted": "true", "mfa_enabled": {"value":"true","severity":"CRIT"}}}
		]
	}`)
	writeFile(t, filepath.Join(dir, "baseline.meta.json"), `{
		"version": "2026-06-01.1",
		"approved_by": "secops",
		"census": {"devices": ["s1"], "users": ["a@x.com"]}
	}`)

	b, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if b.Version != "2026-06-01.1" || b.ApprovedBy != "secops" {
		t.Errorf("meta not loaded: %+v", b)
	}
	if len(b.Census.Devices) != 1 || len(b.Census.Users) != 1 {
		t.Errorf("census not loaded: %+v", b.Census)
	}
	if got := b.Classes[0].Expected["disk_encrypted"].Value; got != "true" {
		t.Errorf("expectation value = %q, want true", got)
	}
}

func TestLoadRejectsDuplicateClassID(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "classes.json"), `{
		"classes": [
			{"id": "dup", "match": {"os_family": "darwin"}},
			{"id": "dup", "match": {"os_family": "windows"}}
		]
	}`)
	if _, err := Load(dir); err == nil {
		t.Error("Load accepted duplicate class id")
	}
}
