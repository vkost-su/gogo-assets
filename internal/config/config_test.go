package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gogo-assets/internal/allowlist"
)

// TestDefaultEnvPath covers the .env discovery order: a local ./.env wins, else
// the XDG config location is used.
func TestDefaultEnvPath(t *testing.T) {
	t.Run("local .env wins", func(t *testing.T) {
		wd := t.TempDir()
		t.Chdir(wd)
		if err := os.WriteFile(filepath.Join(wd, ".env"), []byte("X=1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		if got := defaultEnvPath(); got != ".env" {
			t.Errorf("defaultEnvPath() = %q, want .env", got)
		}
	})

	t.Run("xdg fallback when no local .env", func(t *testing.T) {
		t.Chdir(t.TempDir()) // working dir without a .env
		xdg := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", xdg)
		want := filepath.Join(xdg, "gogo-assets", ".env")
		if got := defaultEnvPath(); got != want {
			t.Errorf("defaultEnvPath() = %q, want %q", got, want)
		}
	})
}

// TestPersistLocal covers the run-mode detection: explicit LOCAL_PERSIST wins,
// otherwise a GitHub-hosted runner is ephemeral and everything else persists.
func TestPersistLocal(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"local dev (no CI env)", nil, true},
		{"github-hosted runner", map[string]string{"RUNNER_ENVIRONMENT": "github-hosted"}, false},
		{"self-hosted runner", map[string]string{"RUNNER_ENVIRONMENT": "self-hosted"}, true},
		{"explicit false overrides self-hosted", map[string]string{"RUNNER_ENVIRONMENT": "self-hosted", "LOCAL_PERSIST": "false"}, false},
		{"explicit true overrides github-hosted", map[string]string{"RUNNER_ENVIRONMENT": "github-hosted", "LOCAL_PERSIST": "true"}, true},
		{"unparseable LOCAL_PERSIST falls through to detection", map[string]string{"RUNNER_ENVIRONMENT": "github-hosted", "LOCAL_PERSIST": "maybe"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			get := func(name string) string { return c.env[name] }
			if got := persistLocal(get); got != c.want {
				t.Errorf("persistLocal() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestLoadParsesMaxRPS covers JC_MAX_RPS: unset ⇒ 0 (client default), a valid
// number is carried through, and a negative value is rejected.
func TestLoadParsesMaxRPS(t *testing.T) {
	xdg := t.TempDir()
	cfgDir := filepath.Join(xdg, "gogo-assets")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sa := filepath.Join(cfgDir, "sa.json")
	if err := os.WriteFile(sa, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	load := func(t *testing.T, rps string) (Settings, error) {
		t.Helper()
		t.Chdir(t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", xdg)
		t.Setenv("GWS_SA_JSON_PATH", sa)
		t.Setenv("GWS_ADMIN_EMAIL", "admin@example.com")
		t.Setenv("JC_MAX_RPS", rps)
		return Load("")
	}

	t.Run("unset ⇒ 0", func(t *testing.T) {
		s, err := load(t, "")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if s.JumpCloud.MaxRPS != 0 {
			t.Errorf("MaxRPS = %v, want 0 (client default)", s.JumpCloud.MaxRPS)
		}
	})

	t.Run("valid value", func(t *testing.T) {
		s, err := load(t, "12.5")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if s.JumpCloud.MaxRPS != 12.5 {
			t.Errorf("MaxRPS = %v, want 12.5", s.JumpCloud.MaxRPS)
		}
	})

	t.Run("negative rejected", func(t *testing.T) {
		if _, err := load(t, "-1"); err == nil {
			t.Error("want error for negative JC_MAX_RPS, got nil")
		}
	})
}

// TestLoadReadsXDGEnv confirms Load picks up the XDG-located .env when the
// working directory has none, so credentials kept in ~/.config/gogo-assets are
// honoured.
func TestLoadReadsXDGEnv(t *testing.T) {
	xdg := t.TempDir()
	cfgDir := filepath.Join(xdg, "gogo-assets")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sa := filepath.Join(cfgDir, "sa.json")
	if err := os.WriteFile(sa, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := "GWS_SA_JSON_PATH=" + sa + "\nGWS_ADMIN_EMAIL=admin@example.com\nLOG_LEVEL=debug\n"
	if err := os.WriteFile(filepath.Join(cfgDir, ".env"), []byte(env), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(t.TempDir()) // no ./.env here → XDG fallback
	t.Setenv("XDG_CONFIG_HOME", xdg)
	// Make sure the host's real environment can't shadow the file values.
	t.Setenv("GWS_SA_JSON_PATH", "")
	t.Setenv("GWS_ADMIN_EMAIL", "")
	t.Setenv("LOG_LEVEL", "")

	s, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Google.SAJSONPath != sa {
		t.Errorf("SAJSONPath = %q, want %q", s.Google.SAJSONPath, sa)
	}
	if s.Google.AdminEmail != "admin@example.com" {
		t.Errorf("AdminEmail = %q, want admin@example.com", s.Google.AdminEmail)
	}
	if s.LogLevel != "DEBUG" {
		t.Errorf("LogLevel = %q, want DEBUG", s.LogLevel)
	}
}

// TestLoadWithOptionsRequiresOnlySelectedServices confirms standalone service
// binaries can share the central loader without requiring unrelated credentials.
func TestLoadWithOptionsRequiresOnlySelectedServices(t *testing.T) {
	t.Run("jumpcloud without google", func(t *testing.T) {
		t.Chdir(t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		t.Setenv("GWS_SA_JSON_PATH", "")
		t.Setenv("GWS_ADMIN_EMAIL", "")
		t.Setenv("JC_API_KEY", "jc-secret")
		t.Setenv("JC_SAAS_USAGE_DAYS", "120") // clamped
		t.Setenv("JC_MAX_RPS", "9.5")

		s, err := LoadWithOptions("", LoadOptions{RequireJumpCloud: true})
		if err != nil {
			t.Fatalf("LoadWithOptions(jc): %v", err)
		}
		if s.JumpCloud.APIKey != "jc-secret" {
			t.Errorf("JC APIKey = %q, want jc-secret", s.JumpCloud.APIKey)
		}
		if s.JumpCloud.SaaSUsageDays != 90 {
			t.Errorf("SaaSUsageDays = %d, want clamp to 90", s.JumpCloud.SaaSUsageDays)
		}
		if s.JumpCloud.MaxRPS != 9.5 {
			t.Errorf("MaxRPS = %v, want 9.5", s.JumpCloud.MaxRPS)
		}
	})

	t.Run("jumpcloud missing api key", func(t *testing.T) {
		t.Chdir(t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		t.Setenv("JC_API_KEY", "")

		if _, err := LoadWithOptions("", LoadOptions{RequireJumpCloud: true}); err == nil {
			t.Fatal("want missing JC_API_KEY error")
		}
	})

	t.Run("sophos without google", func(t *testing.T) {
		t.Chdir(t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		t.Setenv("GWS_SA_JSON_PATH", "")
		t.Setenv("GWS_ADMIN_EMAIL", "")
		t.Setenv("SOPHOS_CLIENT_ID", "sp-id")
		t.Setenv("SOPHOS_CLIENT_SECRET", "sp-secret")

		s, err := LoadWithOptions("", LoadOptions{RequireSophos: true})
		if err != nil {
			t.Fatalf("LoadWithOptions(sophos): %v", err)
		}
		if s.Sophos.ClientID != "sp-id" || s.Sophos.ClientSecret != "sp-secret" {
			t.Errorf("Sophos = %+v, want configured credentials", s.Sophos)
		}
	})

	t.Run("optional google path is not statted", func(t *testing.T) {
		t.Chdir(t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		t.Setenv("GWS_SA_JSON_PATH", "/definitely/not/a/key.json")
		t.Setenv("GWS_ADMIN_EMAIL", "")
		t.Setenv("JC_API_KEY", "jc-secret")

		if _, err := LoadWithOptions("", LoadOptions{RequireJumpCloud: true}); err != nil {
			t.Fatalf("optional invalid Google path should not fail JC config: %v", err)
		}
	})
}

// TestLoadFilterPaths confirms FILTER_* env vars resolve under BASELINE_DIR with
// tilde expansion.
func TestLoadFilterPaths(t *testing.T) {
	xdg := t.TempDir()
	cfgDir := filepath.Join(xdg, "gogo-assets")
	baseline := filepath.Join(cfgDir, "baseline")
	if err := os.MkdirAll(baseline, 0o755); err != nil {
		t.Fatal(err)
	}
	sa := filepath.Join(cfgDir, "sa.json")
	if err := os.WriteFile(sa, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("GWS_SA_JSON_PATH", sa)
	t.Setenv("GWS_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("FILTER_JC_APPS", "~/custom/jc-apps.filter")

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "custom", "jc-apps.filter")

	s, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Filters.JCApps != want {
		t.Errorf("Filters.JCApps = %q, want %q", s.Filters.JCApps, want)
	}
	if !strings.HasSuffix(s.Filters.JCSystem, allowlist.JCSystemFile) {
		t.Errorf("Filters.JCSystem = %q, want default under baseline", s.Filters.JCSystem)
	}
}

// TestLoadStillRequiresGoogle preserves the orchestrator contract: Load is the
// full inventory loader and Google Workspace remains required there.
func TestLoadStillRequiresGoogle(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GWS_SA_JSON_PATH", "")
	t.Setenv("GWS_ADMIN_EMAIL", "")

	if _, err := Load(""); err == nil {
		t.Fatal("Load should still require Google credentials")
	}
}
