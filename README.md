# gogo-assets

A security asset-inventory CLI written in Go. On each run it collects asset data
from **Google Workspace**, **JumpCloud**, and **Sophos Central**, correlates it
into a single people-and-devices view, assembles a canonical snapshot, runs a
drift engine against an approved baseline, and publishes the results to tiered
local storage and Google Sheets.

The whole thing is one binary, one run, no daemon: collect → correlate →
assemble → detect drift → persist → publish.

---

## Contents

- [What it does](#what-it-does)
- [Quick start](#quick-start)
- [The pipeline](#the-pipeline)
- [Data model — the global structures](#data-model--the-global-structures)
- [Cross-source correlation](#cross-source-correlation)
- [Sheets tabs — which structure builds which table](#sheets-tabs--which-structure-builds-which-table)
- [Configuration](#configuration)
- [Drift engine reference](#drift-engine-reference)
- [Storage tiers](#storage-tiers)
- [Development](#development)
- [Architecture](#architecture)

---

## What it does

1. **Collect** — pulls users, devices, login activity, and OAuth tokens (Google
   Workspace); systems, directory users, and policy statuses (JumpCloud); and
   endpoints, tamper protection, health, alerts, and detections (Sophos Central).
2. **Correlate** — merges everything into one **email-keyed** view of people and
   joins each physical device's JumpCloud and Sophos records together.
3. **Assemble** — normalises the raw collector output into a single canonical
   `model.Snapshot`, with every entity slice sorted by identity key for
   byte-stable, diffable output.
4. **Detect drift** — classifies each entity against a hand-authored baseline,
   compares monitored fields, and diffs the live entity set against the approved
   census.
5. **Persist** — writes to tiered local storage (current / daily / archive) via
   atomic writes.
6. **Publish** — writes five Google Sheets tabs and a compact, findings-first
   `digest.json` sized to fit a downstream analyst's context budget.

---

## Quick start

Requires **Go 1.25+** and a Google Workspace service-account JSON with
Domain-Wide Delegation.

```bash
cp .env.example .env        # fill in credentials
make build                  # compiles bin/inventory
./bin/inventory all         # full run: collect, correlate, drift, sheets
```

Common variations:

```bash
./bin/inventory all --no-sheets        # collect + drift, skip Sheets
./bin/inventory gw                      # collect Google Workspace only
./bin/inventory all --json --no-sheets  # print the canonical snapshot to stdout
./bin/inventory all --approve-baseline  # anchor the census for NEW/GONE detection
```

Targets are `gw | jc | sp | all` (default `all`). The drift engine runs **only**
on `all` — a partial run would false-positive the census diff by flagging every
uncollected entity as gone. Targets and flags are order-independent.

---

## The pipeline

The single most important thing to understand: **the raw collector output fans
out into two independent views**, built for two different consumers.

```
                         ┌─────────────────────────────────────────────┐
   Google Workspace ─┐   │            raw collector output             │
   JumpCloud ────────┼──▶│  gworkspace.UserRecord · jumpcloud.System   │
   Sophos Central ───┘   │  jumpcloud.User · sophos.Endpoint           │
                         └───────────────┬───────────────┬─────────────┘
                                         │               │
                  ingest + Finalize()    │               │  assemble.Build()
                                         ▼               ▼
                        ┌────────────────────────┐  ┌──────────────────────────┐
                        │  inventory.            │  │  model.Snapshot          │
                        │  AssetInventory        │  │  (canonical, sorted,     │
                        │  (email-keyed people + │  │   drift-tagged shards)   │
                        │   device join)         │  │                          │
                        └───────────┬────────────┘  └────────────┬─────────────┘
                                    │                             │
                       drives       │                             │  drives
                       Sheets       ▼                             ▼
                  ┌──────────────────────────┐      ┌──────────────────────────────┐
                  │ GoogleWorkspace · JumpCloud │   │ classify → drift → digest     │
                  │ Sophos · UsersAll          │    │ snapshot.json · digest.json   │
                  └──────────────────────────┘      │ Findings tab                  │
                                                     └──────────────────────────────┘
```

- The **inventory view** (`inventory.AssetInventory`) is **people-centric**:
  one record per email, with each person's devices joined across JumpCloud and
  Sophos. It powers the human-facing Sheets tabs.
- The **canonical view** (`model.Snapshot`) is **entity-centric and flat**: sorted
  slices of drift-tagged devices/users/endpoints. It powers the offline drift
  engine, the persisted snapshot, and the digest.

Both are derived from the same collector run in `collect()`
([`cmd/inventory/main.go`](cmd/inventory/main.go)) and never depend on each other.

### 1. Collect

`collect()` runs the selected per-source collectors concurrently-per-source and
feeds each result into **both** sinks:

| Source | Collector | Raw type(s) | Fed into |
|---|---|---|---|
| Google Workspace | `gworkspace.Collector.CollectAll` | `map[email]*UserRecord` | `inv.AddGoogle` + `src.GWS` |
| JumpCloud | `jumpcloud.Collector.CollectAll` | `[]System`, `map[email]User` | `inv.AddJC` + `src.JCSystems/JCUsers` |
| Sophos | `sophos.Collector.CollectAll` | `[]Endpoint` | `inv.AddSophos` + `src.Endpoints` |

A collector with absent credentials is **skipped silently**, not failed — its
shard is simply empty downstream.

### 2. Unify (inventory)

After all sources are ingested, `inv.Finalize()`
([`internal/inventory/finalize.go`](internal/inventory/finalize.go)) runs the
cross-source correlation: the bare-username heuristic, the JC↔Sophos device join,
and per-user attribution. This is the step that produces
`UnifiedUserRecord.Devices` and the identity-level Sophos coverage — see
[Cross-source correlation](#cross-source-correlation) below.

### 3. Assemble (canonical snapshot)

`assemble.Build(src, runTimestamp, runDate)`
([`internal/assemble/assemble.go`](internal/assemble/assemble.go)) is the **single
seam** where raw collector types meet the canonical `model`. Each collector's
`to_model.go` converter is invoked here, the entity slices are sorted by identity
key, and a single provenance `Meta` (run timestamp + date) is stamped on every
entity. Output is deterministic: identical input → byte-identical snapshot.

### 4. Persist

`store.WriteSnapshot(snap)` writes the canonical snapshot to both
`current/snapshot.json` (live working copy) and `daily/<run_date>/snapshot.json`
(retained history), each via atomic temp-file + rename.

### 5. Drift engine

Gated on `target == "all"`. `runDrift()`
([`cmd/inventory/drift.go`](cmd/inventory/drift.go)) runs three phases on the
canonical snapshot:

1. **classify** (`classify.Run`) — assign each entity to a baseline class; emit
   `UNCLASSIFIED` / `CLASS_CONFLICT` coverage findings. Writes
   `current/classification.json`.
2. **drift** (`drift.Run`) — compare each monitored field against the class's
   expectation and diff the entity set against the census, emitting
   `BASELINE_DRIFT` / `DATA_GAP` / `NEW_ENTITY` / `ENTITY_DISAPPEARED`.
3. **digest** (`digest.Build`) — roll all findings into the Claude-facing
   `digest.json`, carrying `first_seen` forward from the previous digest and
   truncating by severity to fit `DIGEST_MAX_BYTES`.

`--approve-baseline` replaces phases 1–3 with a census write: it anchors the
current entity set as the baseline for future NEW/GONE detection.

### 6. Publish (Sheets)

`writeSheets()` writes five tabs (gated by target + data availability). Four are
built from the **inventory view**; the Findings tab is built from the **drift
findings**. See the [tab mapping](#sheets-tabs--which-structure-builds-which-table).

---

## Data model — the global structures

This section lists the global structures and what is assembled from each. There
are four layers: **raw collector types** → **unified inventory** (Sheets) and
**canonical snapshot** (engine) → **findings & digest**.

### Raw collector types

The fetch/normalise logic for each source lives in its own package; these are the
"source of truth" structs everything else is derived from.

| Type | Package | Granularity | Key fields |
|---|---|---|---|
| `gworkspace.UserRecord` | `internal/gworkspace` | one Google user | `Identity`, `Auth`, `LoginActivity`, `OAuthGrants`, `ConnectedApps`, `Devices` |
| `jumpcloud.System` | `internal/jumpcloud` | one managed endpoint | `SystemID`, `Hostname`, `SerialNumber`, `OwnerEmail`, `DiskEncrypted`, `MDMEnrolled`, `LastContact`, policies, software, hardware |
| `jumpcloud.User` | `internal/jumpcloud` | one directory user | `Email`, `MFAConfigured`, `TOTPEnabled`, password/account state, `SSHKeys` |
| `sophos.Endpoint` | `internal/sophos` | one Sophos device | `EndpointID`, `Hostname`, `SerialNumber`, `OwnerLogin` (raw SSO/AD login), `OwnerEmail`, `HealthOverall`, `TamperProtected`, `AlertCount`, `LastSeenAt` |

> **Ownership note.** `jumpcloud.System.OwnerEmail` is reliable. `sophos.Endpoint`
> ownership is not: `OwnerLogin` is the raw `associatedPerson.viaLogin` — often a
> bare username, a UPN, or a `HOST\user` local login — and `OwnerEmail` is
> frequently empty. This asymmetry is the whole reason the device join exists.

### The unified inventory (Sheets-facing)

[`internal/inventory/model.go`](internal/inventory/model.go) — the email-keyed
people view that drives the Sheets tabs.

```go
// Top-level result of one run's unification.
type AssetInventory struct {
    Users           map[string]*UnifiedUserRecord // keyed by email — the merged view
    JCSystems       []jumpcloud.System            // raw, for the JumpCloud tab
    JCUsers         map[string]jumpcloud.User      // raw, joined into the JumpCloud tab
    SophosEndpoints []sophos.Endpoint             // raw, for the Sophos tab
    UnownedDevices  []DevicePair                  // devices with no resolvable owner
    MatchStats      map[string]int                // paired / jc_only / sophos_only / unowned / …
    CollectedAt     time.Time
}

// One person, the primary unit of the inventory.
type UnifiedUserRecord struct {
    Email     string
    Google    *gworkspace.UserRecord // nil if absent from GWS
    JumpCloud *JCSlice               // .Systems + the matching directory .User
    Sophos    *SophosSlice           // .Endpoints attributed to this person
    Devices   []DevicePair           // physical devices, JC ↔ Sophos joined
}

// One physical device, with its two source records and how they were matched.
type DevicePair struct {
    JC       *jumpcloud.System // nil ⇒ Sophos-only device
    Sophos   *sophos.Endpoint  // nil ⇒ JC-only device
    MatchKey string            // "serial" | "hostname" | "mac" | "jc-only" | "sophos-only"
}
```

The Sheets `Match` column reads from `Devices` (`P` = paired, `JC` = JC-only,
`SP` = Sophos-only); the `SP` / `Health` / `Open` columns read from
`Sophos.Endpoints`. Keeping those two in agreement is what the device-join
backfill guarantees.

### The canonical snapshot (engine-facing)

[`internal/model/model.go`](internal/model/model.go) — the flat, sorted, drift-
tagged schema (`SchemaVersion = "2.0"`). The drift engine operates **purely** on
this package and never imports a collector.

```go
type Snapshot struct {
    SchemaVersion   string
    RunDate         string         // YYYY-MM-DD
    RunTimestamp    time.Time      // exact UTC instant
    JumpCloud       JumpCloudShard // Devices[] · Identity[] · PolicyEnforcement[]
    Sophos          SophosShard    // Endpoints[] · AccountHealth
    GoogleWorkspace GWSShard       // Identity[] · Devices[]
}
```

| Canonical entity | Classified? | Notes |
|---|---|---|
| `JCDevice` | ✅ | monitored: `DiskEncrypted` (crit), `MDMEnrolled` (med) |
| `JCUser` | ✅ | monitored: `MFAEnabled` (crit), `PasswordNeverExpires` (med), `JumpCloudGoEnabled` (low) |
| `JCPolicyEnforcement` | — | per-policy rollup, dashboard only (not classified) |
| `SophosEndpoint` | ✅ | monitored: `TamperProtection` (crit) |
| `SophosAccountHealth` | — | tenant-level rollup, derived not collected |
| `GWSUser` | ✅ | monitored: `MFAEnabled` (crit), `MFAEnforced`/`IsAdmin`/`ASPCount` (high), backup codes / 3p tokens (med) |
| `GWSDevice` | — | drill-down shard only (not classified) |

**Two structural rules** enforced by [`internal/drifttag`](internal/drifttag) at
startup:

- **Drift tags.** Every field carries a `drift:"…"` tag: `monitored,sev=…`
  (compared to baseline), `volatile` (stored for context, never compared), or
  `identity` (a match key — serial, email — stored, not compared).
- **The pointer rule.** Every *monitored* field is a pointer (`*bool` / `*int` /
  `*time.Time`). `nil` means **not collected** → `DATA_GAP` (a data-quality issue);
  `*false` means **collected and off** → `BASELINE_DRIFT` (a real finding). A
  monitored field that is not a pointer panics at startup.

### Findings & digest

[`internal/model/finding.go`](internal/model/finding.go) and
[`internal/digest/digest.go`](internal/digest/digest.go).

```go
type Finding struct {
    Kind     FindingKind // one of exactly six kinds (closed set)
    Severity Severity    // CRIT | HIGH | MED | LOW
    Service  []Service
    Entity   Entity      // self-contained: type, id, hostname, owner
    Field, Was, Now string
    ClassID, Summary string
    DetectedAt, FirstSeen time.Time
}

type Digest struct {
    SchemaVersion, RunDate, BaselineVersion string
    Counts        Counts            // totals + findings-by-severity / -by-kind
    Findings      []model.Finding   // severity-ordered, truncated to fit budget
    ShardPointers map[string]string // where to drill down in the snapshot
    Truncated     bool
}
```

---

## Cross-source correlation

All correlation runs in `inventory.Finalize()`, exactly once after ingest. The
steps, in order:

1. **Bare-username heuristic.** A Sophos endpoint whose `owner_login` has no `@`
   (e.g. `alice`) is attached to `<login>@<primary-domain>` if that user exists.
   The primary domain is inferred as the most common email domain in the run.
2. **Sophos owner index.** Build `endpoint_id → owning email` from whatever is
   already attached at the identity level (by `AddSophos` and step 1).
3. **Device join.** Pair each `jumpcloud.System` with a `sophos.Endpoint` by, in
   priority order: **serial → hostname → MAC** (all normalised; hostname is
   lower-cased and trimmed to the first label). Each side is used at most once.
4. **Attribute + backfill.** Attach each pair to its owner — **JumpCloud's
   `OwnerEmail` wins** on conflict (owner mismatches are counted, not silenced).
   Then **backfill identity-level Sophos coverage**: the paired Sophos endpoint is
   added to that owner's `Sophos.Endpoints` (deduped by `EndpointID`).

### Why the backfill matters

Without step 4's backfill, a device could show `JC+SP` in the `UsersAll` →
`Detail` column while the same row's `SP` coverage column showed `—`. That happened
whenever a Sophos endpoint's `owner_login` was a `HOST\user` local login (e.g.
`UA-L-M-JV9PR2D57J\aliana`) that resolved to no email: the **device-level** join
still matched it to the right JumpCloud system (by hostname/serial), but the
**identity-level** attribution missed it. Since the `SP`, `Health`, and `Open`
columns all read `Sophos.Endpoints`, they under-reported coverage.

The backfill closes that gap by treating **JumpCloud's device ownership as
authoritative**: if a Sophos endpoint is physically the same device as a JumpCloud
system owned by a person, that endpoint counts as that person's Sophos coverage —
regardless of what Sophos thinks the login is.

`MatchStats` records the outcome of every run (`paired`, `jc_only`,
`sophos_only`, `unowned`, `owner_mismatch`, `bare_username_matched`,
`bare_username_unmatched`) and is logged at the end of the collect phase.

---

## Sheets tabs — which structure builds which table

`writeSheets()` ([`cmd/inventory/main.go`](cmd/inventory/main.go)) writes five
tabs. The first column lists the default tab name (overridable via env).

| Tab (default name) | Built from | Row unit | Writer |
|---|---|---|---|
| **GoogleWorkspace** | `[]*UnifiedUserRecord` | one user | `WriteGWS` |
| **JumpCloud** | `JCSystemRow{System, User}` from `JCSystems` + `JCUsers` | one system (+ owner) | `WriteJC` |
| **Sophos** | `[]sophos.Endpoint` from `SophosEndpoints` | one endpoint | `WriteSophos` |
| **UsersAll** | `[]*UnifiedUserRecord` | one user (cross-source merged) | `WriteMerged` |
| **Findings** | `[]model.Finding` (drift output) | one finding | `WriteFindings` |

Every tab uses the same layout engine
([`internal/sheets/writer.go`](internal/sheets/writer.go)): a merged group-header
row, a column-header row, frozen headers, an "Updated" footer, and per-cell
red/yellow alert colouring driven by each column's `AlertRed` / `AlertYellow`
predicate. Each tab is fully delete-and-recreated on every write.

### UsersAll column groups

The flagship cross-source summary, one row per person (people with at least one
device float to the top):

| Group | Columns | Source |
|---|---|---|
| Identity | Email · Full Name · Org Unit · **Admin** | `Google.Identity` |
| Coverage | GWS · JC · **SP** | presence of `Google` / `JumpCloud.Systems` / `Sophos.Endpoints` |
| GWS | Suspended · 2SV · Last Login | `Google.Identity` / `Google.Auth` |
| Devices | Count · Match · **OS** · **Enc** · **MDM** · **Last Seen** · Detail | rollups over `Devices` (the JC↔Sophos pairs) |
| Alerts | Health · Open | worst `Sophos.Endpoints[].HealthOverall` / sum of `AlertCount` |

The `Devices` group rollups (`OS`, `Enc`, `MDM`, `Last Seen`) summarise per-device
facts that the wide `Detail` cell spells out line-by-line, so they're sortable and
filterable without opening the cell. `Enc = ✗` (red) when any device reports an
unencrypted disk; `MDM = ✗` (yellow) when any JC device is not MDM-enrolled.

---

## Configuration

All configuration is via environment variables or a `.env` file (OS environment
takes precedence). See [`.env.example`](.env.example) for the full list. Missing
**required** variables abort at startup; optional collectors are skipped silently
when their credentials are absent.

| Variable | Default | Purpose |
|---|---|---|
| `GWS_SA_JSON_PATH`, `GWS_ADMIN_EMAIL` | — (required) | Google SA + delegated admin |
| `GWS_CUSTOMER_ID` | `my_customer` | Google Workspace customer |
| `JC_API_KEY`, `JC_ORG_ID` | — | JumpCloud (skipped if unset) |
| `SOPHOS_CLIENT_ID`, `SOPHOS_CLIENT_SECRET` | — | Sophos (skipped if unset) |
| `SHEETS_SPREADSHEET_ID` | — | target spreadsheet (skipped if unset) |
| `SHEETS_GW_WORKSHEET` | `GoogleWorkspace` | per-source tab names |
| `SHEETS_JC_WORKSHEET` | `JumpCloud` | |
| `SHEETS_SP_WORKSHEET` | `Sophos` | |
| `SHEETS_MERGED_WORKSHEET` | `UsersAll` | cross-source summary tab |
| `SHEETS_FINDINGS_WORKSHEET` | `Findings` | drift-findings tab |
| `LOCAL_DIR`, `BASELINE_DIR` | `./local`, `./local/baseline` | storage roots |
| `DIGEST_MAX_BYTES` | — | digest size budget (truncates by severity) |
| `LOG_LEVEL` | `INFO` | structured-log level |

---

## Drift engine reference

### Finding kinds

| Kind | Default severity | Meaning |
|---|---|---|
| `BASELINE_DRIFT` | from drift tag | monitored field diverged from baseline |
| `DATA_GAP` | MED | monitored field was nil (not collected) |
| `NEW_ENTITY` | MED | present now, absent from baseline census |
| `ENTITY_DISAPPEARED` | HIGH | in census, absent from this snapshot |
| `UNCLASSIFIED` | MED | no class matched the entity |
| `CLASS_CONFLICT` | LOW | multiple classes matched; resolved deterministically |

The set is **closed** — the digest schema and the Claude-facing contract depend on
it. Severity ranks CRIT(4) → HIGH(3) → MED(2) → LOW(1) for deterministic ordering
and truncation.

### Baseline classes

Classes live in `local/baseline/classes.json` and are pure configuration — edit
freely, no recompile. A class matches entities by canonical JSON field name
(AND-ed conditions) and pins the monitored fields it expects:

```json
{
  "id": "jc-device-macos",
  "priority": 10,
  "match": { "os_family": "darwin" },
  "expected": {
    "disk_encrypted": "true",
    "mdm_enrolled": { "value": "true", "severity": "crit" }
  }
}
```

`baseline.validate()` catches typos in field names at startup.

---

## Storage tiers

| Tier | Path | Contents | Retention |
|---|---|---|---|
| baseline | `local/baseline/` | `classes.json`, `baseline.meta.json` | permanent (hand-authored, tracked) |
| current | `local/current/` | `snapshot.json`, `classification.json`, `digest.json` | overwritten each run |
| daily | `local/daily/YYYY-MM-DD/` | full `snapshot.json` | 30 days |
| archive | `local/archive/` | `<run_date>_digest.json` | 180 days |

`--prune` (default on) deletes expired daily/archive entries after each run. All
writes are atomic (temp-file + rename).

---

## Development

```bash
make test          # unit tests
make race          # tests under the race detector (required before any PR)
make vet           # static analysis
make lint          # golangci-lint
make check         # the full pre-PR gate: fmt-check + vet + race
```

The drift engine (`model`, `drifttag`, `classify`, `drift`, `digest`, `baseline`,
`assemble`, `snapshot`) depends only on the standard library and is fully testable
offline — `cmd/inventory/pipeline_test.go` runs the whole pipeline end-to-end in a
temp directory with no network calls.

---

## Architecture

```
cmd/inventory/        CLI entrypoint (flag parsing, orchestration, drift wiring)
internal/
  model/              canonical snapshot schema + Finding types (single source of truth)
  config/             env/.env loader
  logging/            structured slog handler
  gworkspace/         Google Workspace collector  (+ to_model converter)
  jumpcloud/          JumpCloud collector          (+ to_model converter)
  sophos/             Sophos Central collector     (+ to_model converter)
  inventory/          cross-source unification (email-keyed people + device join)
  assemble/           raw collector output → model.Snapshot (the one seam)
  baseline/           load classes.json + baseline.meta.json
  drifttag/           reflection-based drift-tag parser (enforces the pointer rule)
  classify/           phase 1: assign entities to baseline classes
  drift/              phase 2: field comparison + census diff
  digest/             build the findings-first digest
  snapshot/           tiered atomic storage
  sheets/             Google Sheets writer (one file per tab + shared layout engine)
local/
  baseline/           classes.json, baseline.meta.json (hand-authored policy, tracked)
  current/            live snapshot, classification, digest (runtime, gitignored)
  daily/              dated snapshot history (runtime, gitignored)
```

**Key invariant:** `internal/model` never imports a collector package, and the
drift engine imports only `model`. The `assemble` package is the single place that
knows both the raw collector types and the canonical model. This keeps the engine
testable offline with hand-crafted snapshots, and keeps the two views
([inventory](#the-unified-inventory-sheets-facing) and
[canonical](#the-canonical-snapshot-engine-facing)) cleanly decoupled.
