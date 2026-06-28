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
	APIKey        string
	OrgID         string  // optional; required only for MSP / multi-tenant accounts
	SaaSUsageDays int     // trailing usage window for SaaS App Management (1..90)
	MaxRPS        float64 // steady request-rate cap across all JC collectors (0 ⇒ client default)
}

// Sophos holds Sophos Central API credentials.
// Both fields must be set for the Sophos collector to run.
type Sophos struct {
	ClientID     string
	ClientSecret string
}

// PeopleForce holds PeopleForce API credentials.
// APIKey may be empty — the collector is then skipped.
type PeopleForce struct {
	APIKey  string
	BaseURL string  // optional override; defaults to the production endpoint
	MaxRPS  float64 // steady request-rate cap (0 ⇒ client default)
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
	SaaSWorksheet     string // JumpCloud SaaS App Management tab
	SophosWorksheet   string
	MergedWorksheet   string
	FindingsWorksheet string // drift-engine findings tab
	PFWorksheet       string // PeopleForce assets tab
}

// Settings is the top-level configuration consumed by main.
// Collectors pull the slice they need.
type Settings struct {
	JumpCloud        JumpCloud
	Sophos           Sophos
	Google           Google
	PeopleForce      PeopleForce
	Sheets           Sheets
	LocalDir         string // root of the storage tiers (baseline/current/daily/archive)
	BaselineDir      string // approved baseline anchor; defaults to LocalDir/baseline
	DigestMaxBytes   int    // hard budget for current/digest.json
	EnrichDelay      time.Duration
	RecentlySeenDays int
	LogLevel         string
	PersistLocal     bool // write the local storage tiers; false ⇒ Sheets-only (ephemeral)
}

// LoadOptions controls which service-specific credentials are required. The
// loader always parses every known setting so defaults stay consistent across
// binaries; these flags only decide which missing credentials are fatal.
type LoadOptions struct {
	RequireGoogle      bool
	RequireJumpCloud   bool
	RequireSophos      bool
	RequirePeopleForce bool
}

// Load reads .env (if present) and the OS environment, then assembles Settings.
// OS environment values override .env values.
//
// When envPath is empty, Load looks for ./.env in the working directory, and if
// that is absent falls back to the XDG config location
// $XDG_CONFIG_HOME/gogo-assets/.env (default ~/.config/gogo-assets/.env) — see
// defaultEnvPath.
func Load(envPath string) (Settings, error) {
	return LoadWithOptions(envPath, LoadOptions{RequireGoogle: true})
}

// LoadWithOptions is the shared loader for both the full orchestrator and
// standalone service binaries. Use Load for the normal inventory binary, which
// requires Google Workspace; use this when a service binary should validate only
// its own credentials while still sharing defaults, XDG .env discovery, local
// storage settings, logging, and rate-limit parsing.
func LoadWithOptions(envPath string, opts LoadOptions) (Settings, error) {
	if envPath == "" {
		envPath = defaultEnvPath()
	}
	get := EnvLookup(envPath)

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

	saasDays, err := strconv.Atoi(optional("JC_SAAS_USAGE_DAYS", "30"))
	if err != nil {
		return Settings{}, fmt.Errorf("JC_SAAS_USAGE_DAYS not an int: %w", err)
	}
	if saasDays < 1 {
		saasDays = 1
	}
	if saasDays > 90 {
		saasDays = 90
	}

	// 0 means "use the client's built-in default"; the loader keeps it at 0 when
	// JC_MAX_RPS is unset rather than baking the default in two places.
	maxRPS, err := strconv.ParseFloat(optional("JC_MAX_RPS", "0"), 64)
	if err != nil || maxRPS < 0 {
		return Settings{}, fmt.Errorf("JC_MAX_RPS must be a non-negative number: %q", get("JC_MAX_RPS"))
	}

	localDir := expandUser(optional("LOCAL_DIR", "./local"))
	baselineDir := expandUser(optional("BASELINE_DIR", filepath.Join(localDir, "baseline")))

	google, err := loadGoogle(required, optional, opts.RequireGoogle)
	if err != nil {
		return Settings{}, err
	}
	jc, err := loadJumpCloud(optional, opts.RequireJumpCloud, saasDays, maxRPS)
	if err != nil {
		return Settings{}, err
	}
	sp, err := loadSophos(optional, opts.RequireSophos)
	if err != nil {
		return Settings{}, err
	}
	pf, err := loadPeopleForce(optional, opts.RequirePeopleForce)
	if err != nil {
		return Settings{}, err
	}

	return Settings{
		JumpCloud:   jc,
		Sophos:      sp,
		Google:      google,
		PeopleForce: pf,
		Sheets: Sheets{
			SpreadsheetID:     optional("SHEETS_SPREADSHEET_ID", ""),
			Worksheet:         optional("SHEETS_GW_WORKSHEET", "GoogleWorkspace"),
			JCWorksheet:       optional("SHEETS_JC_WORKSHEET", "JumpCloud"),
			SaaSWorksheet:     optional("SHEETS_SAAS_WORKSHEET", "SaaS"),
			SophosWorksheet:   optional("SHEETS_SP_WORKSHEET", "Sophos"),
			MergedWorksheet:   optional("SHEETS_MERGED_WORKSHEET", "UsersAll"),
			FindingsWorksheet: optional("SHEETS_FINDINGS_WORKSHEET", "Findings"),
			PFWorksheet:       optional("SHEETS_PF_WORKSHEET", "PeopleForce"),
		},
		LocalDir:         localDir,
		BaselineDir:      baselineDir,
		DigestMaxBytes:   digestMax,
		EnrichDelay:      time.Duration(enrichSecs) * time.Second,
		RecentlySeenDays: days,
		LogLevel:         strings.ToUpper(optional("LOG_LEVEL", "INFO")),
		PersistLocal:     persistLocal(get),
	}, nil
}

func loadGoogle(
	required func(string) (string, error),
	optional func(string, string) string,
	require bool,
) (Google, error) {
	saPath := optional("GWS_SA_JSON_PATH", "")
	adminEmail := optional("GWS_ADMIN_EMAIL", "")
	if require {
		var err error
		saPath, err = required("GWS_SA_JSON_PATH")
		if err != nil {
			return Google{}, err
		}
		adminEmail, err = required("GWS_ADMIN_EMAIL")
		if err != nil {
			return Google{}, err
		}
	}

	if saPath != "" {
		saPath = expandUser(saPath)
		if require {
			if _, err := os.Stat(saPath); err != nil {
				return Google{}, fmt.Errorf("GWS_SA_JSON_PATH not accessible: %w", err)
			}
		}
	}

	return Google{
		SAJSONPath: saPath,
		AdminEmail: adminEmail,
		CustomerID: optional("GWS_CUSTOMER_ID", "my_customer"),
	}, nil
}

func loadJumpCloud(
	optional func(string, string) string,
	require bool,
	saasDays int,
	maxRPS float64,
) (JumpCloud, error) {
	apiKey := optional("JC_API_KEY", "")
	if require && apiKey == "" {
		return JumpCloud{}, fmt.Errorf("%w: JC_API_KEY", ErrMissing)
	}
	return JumpCloud{
		APIKey:        apiKey,
		OrgID:         optional("JC_ORG_ID", ""),
		SaaSUsageDays: saasDays,
		MaxRPS:        maxRPS,
	}, nil
}

func loadSophos(
	optional func(string, string) string,
	require bool,
) (Sophos, error) {
	clientID := optional("SOPHOS_CLIENT_ID", "")
	clientSecret := optional("SOPHOS_CLIENT_SECRET", "")
	if require {
		if clientID == "" {
			return Sophos{}, fmt.Errorf("%w: SOPHOS_CLIENT_ID", ErrMissing)
		}
		if clientSecret == "" {
			return Sophos{}, fmt.Errorf("%w: SOPHOS_CLIENT_SECRET", ErrMissing)
		}
	}
	return Sophos{ClientID: clientID, ClientSecret: clientSecret}, nil
}

func loadPeopleForce(
	optional func(string, string) string,
	require bool,
) (PeopleForce, error) {
	apiKey := optional("PF_API_KEY", "")
	if require && apiKey == "" {
		return PeopleForce{}, fmt.Errorf("%w: PF_API_KEY", ErrMissing)
	}
	pfMaxRPS, err := strconv.ParseFloat(optional("PF_MAX_RPS", "0"), 64)
	if err != nil || pfMaxRPS < 0 {
		return PeopleForce{}, fmt.Errorf("PF_MAX_RPS must be a non-negative number: %q", optional("PF_MAX_RPS", "0"))
	}
	return PeopleForce{
		APIKey:  apiKey,
		BaseURL: optional("PF_BASE_URL", ""),
		MaxRPS:  pfMaxRPS,
	}, nil
}

// EnvLookup performs the same .env discovery as Load — ./.env in the working
// directory, else $XDG_CONFIG_HOME/gogo-assets/.env — and returns a lookup that
// resolves a name from the OS environment first, then the file. An empty
// envPath triggers the default discovery.
//
// It requires no variables, so standalone collector binaries (cmd/jc, cmd/sophos)
// can honour the same credential file as the orchestrator while validating only
// the variables they actually need.
func EnvLookup(envPath string) func(name string) string {
	if envPath == "" {
		envPath = defaultEnvPath()
	}
	// godotenv.Read returns the file's variables WITHOUT exporting them to
	// os.Environ, keeping the two sources independent.
	fileVars, _ := godotenv.Read(envPath) // missing file → nil map, no error
	return func(name string) string {
		if v, ok := os.LookupEnv(name); ok && v != "" {
			return v
		}
		return fileVars[name]
	}
}

// ExpandUser replaces a leading "~/" (or a bare "~") with $HOME. Exposed so
// standalone binaries expand path-valued env vars (LOCAL_DIR, key paths) the
// same way Load does.
func ExpandUser(p string) string { return expandUser(p) }

// persistLocal decides whether the run writes the local storage tiers (history +
// the source of truth the `sheets` command republishes from) or runs ephemerally
// and publishes to Google Sheets only.
//
// An explicit LOCAL_PERSIST (true/false) overrides everything. Otherwise the run
// is ephemeral only on a GitHub-hosted runner — RUNNER_ENVIRONMENT=github-hosted,
// whose filesystem is discarded when the job ends, so only Sheets survives. A
// self-hosted runner or a local machine persists.
func persistLocal(get func(string) string) bool {
	if v := get("LOCAL_PERSIST"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return !strings.EqualFold(get("RUNNER_ENVIRONMENT"), "github-hosted")
}

// defaultEnvPath picks the .env file Load reads when no explicit path is given:
// ./.env in the working directory when it exists, otherwise the XDG config
// location $XDG_CONFIG_HOME/gogo-assets/.env (falling back to
// ~/.config/gogo-assets/.env). This lets credentials live outside the repo
// tree. OS environment variables still override whatever the file provides, so
// this only controls where file-level defaults are read from.
//
// Note: this deliberately uses the XDG ~/.config convention rather than
// os.UserConfigDir(), which on macOS resolves to ~/Library/Application Support.
func defaultEnvPath() string {
	const local = ".env"
	if _, err := os.Stat(local); err == nil {
		return local
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return local
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "gogo-assets", ".env")
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
