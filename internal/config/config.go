// Package config loads settings from environment variables and an optional
// .env file. OS environment takes priority so CI/CD and Docker always win
// over the local .env.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// ErrMissing is returned when a required environment variable is unset.
var ErrMissing = errors.New("missing required env var")

// JumpCloud holds JumpCloud API credentials.
// APIKey may be empty — the collector is then skipped.
type JumpCloud struct {
	APIKey string
	OrgID  string // optional; required only for MSP / multi-tenant accounts
}

// Sophos holds Sophos Central API credentials.
// Both fields must be set for the Sophos collector to run.
type Sophos struct {
	ClientID     string
	ClientSecret string
}

// Google holds Google Workspace service-account credentials.
type Google struct {
	SAJSONPath string // absolute path; existence is verified by Load
	AdminEmail string // super-admin to impersonate via DWD
	CustomerID string // usually "my_customer"
}

// Sheets holds target spreadsheet identifiers.
// SpreadsheetID may be empty — sheets writes are then skipped.
type Sheets struct {
	SpreadsheetID     string
	Worksheet         string // GWS tab name
	JCWorksheet       string
	SophosWorksheet   string
	MergedWorksheet   string
	FindingsWorksheet string // drift-engine findings tab
}

// Settings is the top-level configuration consumed by main.
// Collectors pull the slice they need.
type Settings struct {
	JumpCloud        JumpCloud
	Sophos           Sophos
	Google           Google
	Sheets           Sheets
	LocalDir         string // root of the storage tiers (baseline/current/daily/archive)
	BaselineDir      string // approved baseline anchor; defaults to LocalDir/baseline
	DigestMaxBytes   int    // hard budget for current/digest.json
	EnrichDelay      time.Duration
	RecentlySeenDays int
	LogLevel         string
}

// Load reads .env (if present) and the OS environment, then assembles Settings.
// OS environment values override .env values.
//
// envPath defaults to ".env" in the current directory when empty.
func Load(envPath string) (Settings, error) {
	if envPath == "" {
		envPath = ".env"
	}
	// godotenv.Read returns the file's variables WITHOUT exporting them to
	// os.Environ, keeping the two sources independent.
	fileVars, _ := godotenv.Read(envPath) // missing file → nil map, no error

	get := func(name string) string {
		if v, ok := os.LookupEnv(name); ok && v != "" {
			return v
		}
		return fileVars[name]
	}

	required := func(name string) (string, error) {
		v := get(name)
		if v == "" {
			return "", fmt.Errorf("%w: %s", ErrMissing, name)
		}
		return v, nil
	}

	optional := func(name, fallback string) string {
		if v := get(name); v != "" {
			return v
		}
		return fallback
	}

	saPath, err := required("GWS_SA_JSON_PATH")
	if err != nil {
		return Settings{}, err
	}
	saPath = expandUser(saPath)
	if _, err := os.Stat(saPath); err != nil {
		return Settings{}, fmt.Errorf("GWS_SA_JSON_PATH not accessible: %w", err)
	}

	adminEmail, err := required("GWS_ADMIN_EMAIL")
	if err != nil {
		return Settings{}, err
	}

	days, err := strconv.Atoi(optional("RECENTLY_SEEN_DAYS", "14"))
	if err != nil {
		return Settings{}, fmt.Errorf("RECENTLY_SEEN_DAYS not an int: %w", err)
	}

	digestMax, err := strconv.Atoi(optional("DIGEST_MAX_BYTES", "51200")) // 50 KB
	if err != nil {
		return Settings{}, fmt.Errorf("DIGEST_MAX_BYTES not an int: %w", err)
	}
	enrichSecs, err := strconv.Atoi(optional("ENRICH_DELAY_S", "0"))
	if err != nil {
		return Settings{}, fmt.Errorf("ENRICH_DELAY_S not an int: %w", err)
	}

	localDir := expandUser(optional("LOCAL_DIR", "./local"))
	baselineDir := expandUser(optional("BASELINE_DIR", filepath.Join(localDir, "baseline")))

	return Settings{
		JumpCloud: JumpCloud{
			APIKey: optional("JC_API_KEY", ""),
			OrgID:  optional("JC_ORG_ID", ""),
		},
		Sophos: Sophos{
			ClientID:     optional("SOPHOS_CLIENT_ID", ""),
			ClientSecret: optional("SOPHOS_CLIENT_SECRET", ""),
		},
		Google: Google{
			SAJSONPath: saPath,
			AdminEmail: adminEmail,
			CustomerID: optional("GWS_CUSTOMER_ID", "my_customer"),
		},
		Sheets: Sheets{
			SpreadsheetID:     optional("SHEETS_SPREADSHEET_ID", ""),
			Worksheet:         optional("SHEETS_GW_WORKSHEET", "GoogleWorkspace"),
			JCWorksheet:       optional("SHEETS_JC_WORKSHEET", "JumpCloud"),
			SophosWorksheet:   optional("SHEETS_SP_WORKSHEET", "Sophos"),
			MergedWorksheet:   optional("SHEETS_MERGED_WORKSHEET", "UsersAll"),
			FindingsWorksheet: optional("SHEETS_FINDINGS_WORKSHEET", "Findings"),
		},
		LocalDir:         localDir,
		BaselineDir:      baselineDir,
		DigestMaxBytes:   digestMax,
		EnrichDelay:      time.Duration(enrichSecs) * time.Second,
		RecentlySeenDays: days,
		LogLevel:         strings.ToUpper(optional("LOG_LEVEL", "INFO")),
	}, nil
}

// expandUser mirrors Python's Path.expanduser — replaces a leading "~/" with $HOME.
func expandUser(p string) string {
	if !strings.HasPrefix(p, "~/") && p != "~" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}
