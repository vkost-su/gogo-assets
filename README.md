# gogo-assets

A security asset-inventory CLI written in Go. On each run it collects asset data
from **Google Workspace**, **JumpCloud**, **Sophos Central**, and **PeopleForce**, correlates it
into a single people-and-devices view, assembles a canonical snapshot, runs a
drift engine against an approved baseline, and publishes the results to tiered
local storage and Google Sheets.

The main orchestrator is one binary, one run, no daemon: collect → correlate →
assemble → detect drift → persist → publish. Each external integration is also
wrapped as a uniform service module, so standalone service binaries and future
connectors use the same lifecycle instead of growing one-off code paths.

---

## Contents

- [What it does](#what-it-does)
- [Quick start](#quick-start)
- [Guide](#guide)
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
   Workspace); systems, directory users, policy statuses, and **SaaS applications**
   (owner accounts, licenses, usage — JumpCloud AI & SaaS Management); endpoints,
   tamper protection, health, alerts, and detections (Sophos Central); and hardware
   assets assigned to employees (PeopleForce).
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
6. **Publish** — writes the Google Sheets tabs (each service tab paired with a
   `(Drift)` companion of just the problem rows), plus per-service full/drift JSON
   into a dated run folder and a compact, findings-first `digest.json` sized to
   fit a downstream analyst's context budget.

---

## Quick start

Requires **Go 1.25+** and a Google Workspace service-account JSON with
Domain-Wide Delegation.

```bash
cp .env.example .env        # fill in credentials (repo-root .env), or use ~/.config (below)
make build                  # compiles bin/inventory
./bin/inventory all         # full run: collect, correlate, drift, sheets
```

**Where credentials live.** The binary looks for `.env` in the working directory
first, and if absent falls back to `~/.config/gogo-assets/.env`
(`$XDG_CONFIG_HOME/gogo-assets/.env`). The recommended setup keeps **both** the
`.env` and the service-account key outside the repo tree:

```bash
mkdir -p ~/.config/gogo-assets
cp .env.example ~/.config/gogo-assets/.env       # fill in credentials
cp /path/to/your-sa.json ~/.config/gogo-assets/sa.json
# in that .env:  GWS_SA_JSON_PATH=~/.config/gogo-assets/sa.json
./bin/inventory all                               # run from anywhere; .env is found via XDG
```

> **Never commit the Google service-account JSON** — it carries a private key.
> Keeping it in `~/.config/gogo-assets/` (outside the repo) is safest. If you do
> keep credentials inside the tree, the `.gitignore` safety nets catch them
> (`.env`, `*-sa.json`, `*service-account*.json`, `*-sdk-*.json`, and any
> `*.json` under `cmd/inventory/`). Live snapshots under `local/current`,
> `local/daily`, and `local/archive` are runtime data and are gitignored too —
> only the hand-authored `local/baseline/` is tracked.

Common variations:

```bash
./bin/inventory all --no-sheets        # collect + drift, skip Sheets
./bin/inventory gw                      # collect Google Workspace only
./bin/inventory all --json --no-sheets  # print the canonical snapshot to stdout
./bin/inventory all --approve-baseline  # anchor the census for NEW/GONE detection
./bin/inventory help                    # full usage: targets, commands, flags, --tabs keys
./bin/inventory version                 # build revision/time from the embedded build info
```

### Publishing to Sheets on demand

Every run persists the full collected data to `local/current/inventory.json` —
the **source of truth** the Sheets tabs render from. The `sheets` command
republishes from that file at any time, **without collecting**:

```bash
./bin/inventory sheets                       # republish every populated tab from the last run
./bin/inventory sheets --tabs jc,saas        # rewrite only those two tabs; others untouched
./bin/inventory sheets --run-date 2026-06-15 # publish a specific dated snapshot, not the latest
./bin/inventory sheets --dry-run             # log which tabs would be written; touch no API
./bin/inventory all --tabs usersall          # collect everything, but write only the UsersAll tab
```

`--tabs` takes a comma list of `gw, jc, saas, jcsoft, sophos, pf, usersall,
findings` (or `all`); it filters both the `sheets` command and the auto-write
after a normal run. Each service key also writes that tab's `(Drift)` companion.
A tab with no data is **skipped, never recreated empty**, so a partial run or
selective publish never clobbers a populated tab.

Every run also mirrors its inventory into the dated run folder,
`local/daily/<run-folder>/inventory.json` (readable name, e.g. `may05-2026`).
`--run-date <YYYY-MM-DD>` republishes from that dated mirror instead of
`current/` — handy for re-pushing an earlier day's snapshot (you pass the
`YYYY-MM-DD` date; the loader maps it to the folder). `--dry-run` walks
every gate (target, `--tabs` selection, data availability) and logs the tabs it
*would* write without opening the Google API — a safe way to verify a
`--tabs`/`--run-date` combination. Both flags are valid only with the `sheets`
command.

Targets/commands are `gw | jc | sp | all | sheets | help | version` (default
`all`). The drift engine runs **only** on `all` — a partial run would
false-positive the census diff by flagging every uncollected entity as gone.
Targets and flags are order-independent; run `inventory help` for the full
reference.

---

## Guide

A practical, end-to-end walkthrough: get credentials in place, run the binary,
and use every command the way it's meant to be used. For the *why* behind each
stage, follow the cross-links into [The pipeline](#the-pipeline) and the
[Drift engine reference](#drift-engine-reference).

### 1. Prerequisites

| Need | Why | Required? |
|---|---|---|
| **Go 1.25+** | builds the single binary | yes |
| **Google Workspace SA + DWD** | the only required collector; also authorises the Sheets write | yes |
| **A target spreadsheet** | where the tabs (+ their `(Drift)` companions) are written | only to publish |
| **JumpCloud API key** | systems, directory users, **SaaS apps** | optional collector |
| **Sophos Central client id/secret** | endpoints, health, alerts | optional collector |
| **PeopleForce API key** | hardware assets assigned to employees | optional collector |

Any optional collector with no credentials is **skipped silently** — its tab is
simply not written. So you can start with just Google Workspace and add the rest
later.

### 2. One-time setup

**a. Create the Google service account.** In Google Cloud, create a service
account, enable Domain-Wide Delegation, and download its JSON key. In the Admin
console, authorise the SA client ID for these scopes (one comma-separated line,
exactly as in [`.env.example`](.env.example)):

```
https://www.googleapis.com/auth/admin.directory.user.readonly,
https://www.googleapis.com/auth/admin.directory.user.security,
https://www.googleapis.com/auth/admin.directory.device.mobile.readonly,
https://www.googleapis.com/auth/admin.reports.audit.readonly,
https://www.googleapis.com/auth/spreadsheets
```

**b. Put credentials where the loader finds them.** The `.env` is read from the
working directory first, else `~/.config/gogo-assets/.env`. Keeping both the
`.env` and the key **outside the repo** is the recommended setup:

```bash
mkdir -p ~/.config/gogo-assets
cp .env.example ~/.config/gogo-assets/.env       # then fill it in
cp /path/to/sa.json ~/.config/gogo-assets/sa.json
# in that .env:
#   GWS_SA_JSON_PATH=~/.config/gogo-assets/sa.json
#   GWS_ADMIN_EMAIL=admin@yourdomain.com
#   SHEETS_SPREADSHEET_ID=<id from the sheet URL>
```

OS environment variables always override file values — that's how CI injects
secrets without a file.

**c. Share the spreadsheet.** Add the **SA's email** as an **Editor** on the
target spreadsheet, or the Sheets write returns a 403.

**d. Build and smoke-test.**

```bash
make build                       # → bin/inventory
./bin/inventory version          # prints build revision/time
./bin/inventory gw --no-sheets   # GWS-only, no writes — fastest way to prove creds work
```

**e. Anchor the drift baseline.** The first full run has nothing to diff against.
Once a run looks right, capture it as the approved census so future runs can flag
NEW / GONE entities:

```bash
./bin/inventory all --approve-baseline   # writes local/baseline/baseline.meta.json
```

Edit `local/baseline/classes.json` to pin the security posture you expect
(disk encryption, MFA, tamper protection …) — see
[Tuning the baseline](#6-tuning-the-drift-baseline) below.

### 3. Commands & targets

```
inventory [target|command] [flags]
```

| Invocation | What it does |
|---|---|
| `inventory all` | **default** — collect every source, correlate, run drift, publish all tabs |
| `inventory gw` | collect Google Workspace only (+ its tab) |
| `inventory jc` | collect JumpCloud only, **incl. SaaS** (+ JumpCloud devices, SaaS & JumpCloud Software tabs) |
| `inventory sp` | collect Sophos only (+ its tab) |
| `inventory pf` | collect PeopleForce only (+ PeopleForce tab) |
| `inventory sheets` | re-publish tabs from the **last persisted run** — no collection |
| `inventory help` | full usage reference |
| `inventory version` | build provenance from the embedded VCS info |

Targets and flags are **order-independent** (`inventory --json all` ==
`inventory all --json`). Drift runs **only** on `all`.

### 4. Flags reference

| Flag | Applies to | Effect |
|---|---|---|
| `--no-sheets` | collection | collect + drift, skip the Sheets write |
| `--json` | collection | also print the canonical snapshot JSON to stdout |
| `--tabs <list>` | collection & `sheets` | comma list of `gw,jc,saas,jcsoft,sophos,pf,usersall,findings` (or `all`); write only those (+ their `(Drift)` companions) |
| `--approve-baseline` | `all` | anchor the current entity set as the census, skip drift |
| `--approve-from-current` | — | re-approve the census from the **on-disk** snapshot, then exit (no collection) |
| `--run-date <YYYY-MM-DD>` | `sheets` | publish a dated `daily/` mirror instead of `current/` |
| `--dry-run` | `sheets` | log which tabs *would* be written; touch no Google API |
| `--prune` | collection | prune expired daily/archive tiers after the run (default `true`; `--prune=false` to keep) |

### 5. Advanced usage cookbook

**Publish without re-collecting.** Every run persists `current/inventory.json`;
the `sheets` command renders from it at any time:

```bash
./bin/inventory sheets                       # re-push every populated tab
./bin/inventory sheets --tabs saas           # rewrite just the SaaS tab
./bin/inventory sheets --tabs pf             # rewrite just the PeopleForce tab
./bin/inventory sheets --run-date 2026-06-15 # re-push a specific day's snapshot
./bin/inventory sheets --dry-run             # verify a --tabs/--run-date combo, no API
```

**Collect everything but write one tab.** `--tabs` also gates the auto-write
after a normal run:

```bash
./bin/inventory all --tabs usersall          # full collect+drift, publish only UsersAll
```

A tab with no data is **skipped, never recreated empty**, so a partial run never
clobbers a populated tab.

**Inspect the snapshot offline.** Pipe the canonical JSON into `jq` without
touching Sheets:

```bash
./bin/inventory all --json --no-sheets | jq '.jumpcloud.saas[] | {name, status, category}'
```

**Read the persisted artifacts.** After a persistent run, everything lives under
`local/current/`:

```bash
jq '.applications[] | select(.status=="UNAPPROVED") | .name' local/current/saas.json
jq '.counts'   local/current/digest.json     # findings by severity / kind
jq '.[].kind'  local/current/findings.json   # the raw findings feeding the tab
```

**Tune SaaS collection.** `JC_SAAS_USAGE_DAYS` (1–90, default 30) sets the
trailing window for the per-account *last-used* join. SaaS device-agent accounts
with no own identity are attributed to the device owner or dropped — set
`LOG_LEVEL=DEBUG` to see the `skipped_accounts` count and the raw fields of any
dropped record.

**Speed up a slow SaaS run.** Collecting hundreds of apps makes thousands of
JumpCloud calls; firing them in a burst trips JumpCloud's rate limit, and the
429-driven exponential backoff is what turns a ~2-minute job into ~30 minutes.
The client holds itself to a steady `JC_MAX_RPS` (default 8 req/s) shared across
all collectors so the limit is rarely tripped. If logs still show repeated 429s,
lower it (`JC_MAX_RPS=5`); if your org's quota is higher and collection feels
slow, raise it (`JC_MAX_RPS=15`).

Every run logs the HTTP volume to gauge this — once right after collection and
again in the final summary, as `http requests total=… status_200=…
status_429=… status_500=…` (one `status_<code>` per code seen, plus `errors` for
transport failures). A non-trivial `status_429` count means you're still being
throttled — lower `JC_MAX_RPS`.

Whenever a run gets any 404s, a companion `http 404 endpoints` line lists **which**
endpoints returned Not Found, grouped by template (id-like path segments collapse
to `{id}`) with a count each, ordered by frequency — e.g.
`GET console.jumpcloud.com/api/v2/systeminsights/{id}/{table} ×300`. Many of these
are expected (a device simply does not report a given System Insights table), but
the breakdown makes an unexpected 404 (a renamed or unlicensed endpoint) visible
instead of hiding inside a bare count.

**Throttle / quieten a run.** `ENRICH_DELAY_S` adds a per-user delay to the GWS
enrichment (use if you hit rate limits); `LOG_LEVEL=DEBUG|INFO|WARN|ERROR`
controls verbosity; `DIGEST_MAX_BYTES` caps `digest.json` (it truncates by
severity to fit).

**Choose where output lands.** The run auto-detects ephemeral vs persistent (see
[Running in CI](#running-in-ci--github-actions)); force it with
`LOCAL_PERSIST=true|false`:

```bash
LOCAL_PERSIST=false ./bin/inventory all      # Sheets only, write nothing locally
LOCAL_PERSIST=true  ./bin/inventory all      # force-persist even from a hosted runner
```

### 6. Tuning the drift baseline

`local/baseline/classes.json` is **pure configuration** — edit it freely, no
recompile. A class matches entities by canonical field name (AND-ed) and pins the
monitored fields it expects:

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

`baseline.validate()` catches a typo'd field name **at startup**, so a broken
class fails fast rather than silently mis-classifying. After changing the
*set* of entities you consider approved (not their posture), re-anchor the census
with `--approve-baseline` or `--approve-from-current`. Full field list and finding
kinds: [Drift engine reference](#drift-engine-reference).

### 7. Typical workflows

```bash
# Daily operator run (local, persistent)
./bin/inventory all

# Iterate on the SaaS tab after tweaking a category mapping — no re-collect
./bin/inventory sheets --tabs saas

# Re-approve the baseline from yesterday's snapshot without hitting any API
./bin/inventory --approve-from-current

# CI (GitHub-hosted): collect + publish, nothing persisted — see the workflow file
go run ./cmd/inventory all
```

### 8. Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `missing required env var: GWS_SA_JSON_PATH` | `.env` not found or var unset — check the working dir / `~/.config/gogo-assets/.env` |
| `GWS_SA_JSON_PATH not accessible` | path wrong or `~` not expanded by your shell — use an absolute path |
| Sheets write 403 | share the spreadsheet with the **SA email** as Editor; confirm the `spreadsheets` scope is authorised |
| SaaS tab never appears | JumpCloud **AI & SaaS Management** not licensed (calls fold to empty) or `JC_API_KEY` unset |
| Findings tab empty | drift runs only on `all`, and only with a `classes.json` baseline present |
| Every entity flagged `NEW_ENTITY` | no census yet — run `--approve-baseline` once |
| Nothing written anywhere in CI | ephemeral run with no `SHEETS_SPREADSHEET_ID` (a `WARN` says so) |

Run `make check` (fmt-check + vet + race) before any PR.

---

## The pipeline

The single most important thing to understand: **the raw collector output fans
out into two independent views**, built for two different consumers.

```
                         ┌──────────────────────────────────────────────────┐
   Google Workspace ─┐   │              raw collector output                │
   JumpCloud ────────┼──▶│  gworkspace.UserRecord · jumpcloud.System        │
   Sophos Central ───┤   │  jumpcloud.User · sophos.Endpoint                │
   PeopleForce ──────┘   │  peopleforce.Asset                               │
                         └───────────────┬───────────────┬──────────────────┘
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
                  ┌────────────────────────────┐    ┌──────────────────────────────┐
                  │ GoogleWorkspace · JumpCloud │    │ classify → drift → digest     │
                  │ Sophos · PeopleForce        │    │ snapshot.json · digest.json   │
                  │ UsersAll                    │    │ Findings tab                  │
                  └────────────────────────────┘    └──────────────────────────────┘
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

`collect()` runs the selected service modules from the registry and feeds each
result into **both** sinks:

| Source | Module / collector | Raw type(s) | Fed into |
|---|---|---|---|
| Google Workspace | `service.GoogleWorkspaceModule` → `gworkspace.Collector.CollectAll` | `map[email]*UserRecord` | `inv.AddGoogle` + `src.GWS` |
| JumpCloud | `service.JumpCloudModule` → `jumpcloud.Collector.CollectAll` | `[]System`, `map[email]User` | `inv.AddJC` + `src.JCSystems/JCUsers` |
| JumpCloud SaaS | inside `service.JumpCloudModule` → `jumpcloud.SaaSCollector.CollectAll` | `[]SaaSApp` | `inv.SaaSApps` + `src.JCSaaS` |
| Sophos | `service.SophosModule` → `sophos.Collector.CollectAll` | `[]Endpoint` | `inv.AddSophos` + `src.Endpoints` |
| PeopleForce | `service.PeopleForceModule` → `peopleforce.Collector.CollectAll` | `[]Asset` | `inv.AddPeopleForce` + `src.PFAssets` |

A collector with absent credentials is **skipped silently**, not failed — its
shard is simply empty downstream. The SaaS collector reuses the JumpCloud client
and runs only when `JC_API_KEY` is set; if **AI & SaaS Management** is not
licensed its calls fold to empty (like System Insights) and no SaaS tab is written.
Google Workspace is the required identity spine; optional modules such as
JumpCloud and Sophos are skipped when unconfigured.

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

Each run writes a live working set under `current/` and a **dated, human-readable
run folder** under `daily/` (e.g. `daily/may05-2026/` — see
[The run folder](#the-run-folder)). All writes are atomic (temp-file + rename)
and deterministic (identical input → byte-identical output).

`store.WriteSnapshot(snap)` writes the canonical (lean) snapshot to
`current/snapshot.json` (plain live copy — used by `--json`, baseline approval,
ad-hoc `jq`) and to `daily/<run-folder>/snapshot.tar.gz` (the retained history,
gzip-tarred).

`store.WriteInventory(inv)` persists the **rich** `inventory.AssetInventory` to
`current/inventory.json` (+ a daily mirror). This is the **source of truth the
Sheets tabs render from** — a superset of the lean snapshot that keeps everything
the canonical model drops (JC hardware/policies/SSH/USB, GWS connected-apps, and
the cross-source device join). The `sheets` command
([Publishing to Sheets on demand](#publishing-to-sheets-on-demand)) republishes
the tabs purely from this file, with no collection.

When SaaS apps were collected, `store.WriteSaaS(export)` also writes a
**standalone** `current/saas.json` (+ a daily mirror) — the full nested
`jumpcloud.SaaSApp` structures (owner accounts, license tiers, contract, SSO
connections), wrapped with run provenance (`schema_version`, `run_date`,
`run_timestamp`, `count`). It is skipped on an unlicensed/partial run so an empty
file never clobbers a populated one.

Finally, `writeServiceOutputs` emits the **per-service full/drift files** into the
run folder — see [The run folder](#the-run-folder) below.

### The run folder

Every run fills a dated folder `daily/<run-folder>/` (readable name, e.g.
`may05-2026`; the `YYYY-MM-DD` form is still stored inside each file as
`run_date`, and the prune parser reads the readable name back). One generic
mechanism (`internal/serviceview`) produces, per service, a **full** JSON (the
whole service shard) and a **drift** JSON (only records with ≥1 drift — clean
records are omitted). Each file is self-describing (`schema_version`, `service`,
`view`, `run_date`, `run_timestamp_utc`, `count`) and byte-stable.

```
daily/may05-2026/
  snapshot.tar.gz     # the whole canonical snapshot (jc+sophos+gw+pf), gzip-tarred
  jc.json             # JumpCloud DEVICES — full
  jc-drift.json       # JumpCloud DEVICES — only devices the engine flagged
  jc-saas.json        # JumpCloud SOFTWARE — per-person (device software + SaaS), full
  jc-saas-drift.json  # JumpCloud SOFTWARE — explicit empty view (software pre-filtered at collect)
  gw.json             # Google Workspace users — full
  gw-drift.json       # Google Workspace users — drift only
  sp.json             # Sophos endpoints — full
  sp-drift.json       # Sophos endpoints — drift only
  inventory.json      # rich Sheets source of truth (daily mirror)
  saas.json           # SaaS economics (daily mirror)
```

**Skip-empty:** a service that collected nothing writes no files (never an empty
clobber). A service that ran clean still writes its `*-drift.json` with `count: 0`
so the external report step always has its input.

**Report step is external.** This program writes only data; it guarantees every
`*-drift.json` for the run exists, self-describing and byte-stable. Turning those
drift files into `report-<run-folder>.md` is a separate tool (no AI client, API
key, or `.md` generation lives here).

### Whitelist filters (early purge)

Every hand-authored whitelist lives in plain-text `*.filter` files under
`local/baseline/` (tracked). **Listed entries are known-good and are removed
from all downstream data as early as possible** — right after collection,
before inventory finalization and canonical assembly. Inventory, snapshot,
`saas.json`, run-folder JSON, and Sheets all see the same already-filtered
world.

| Surface | Default file | Match key |
|---|---|---|
| JumpCloud device software | `jc-apps.filter` + `jc-system.filter` (merged) | app/extension name |
| JumpCloud local users (macOS) | `jc-localusers-macos.filter` | local username |
| JumpCloud local users (Windows) | `jc-localusers-windows.filter` | local username |
| JumpCloud SaaS owner accounts | `jc-saas-owner.filter` | email domain |
| Google Workspace connected apps | `gw-apps.filter` | `ConnectedApp.DisplayText` |

**Shared syntax (all `*.filter` files):**

| Pattern | Meaning |
|---|---|
| `entry` | exact match (case-insensitive, trimmed) |
| `entry*` | prefix match — `Google*`, `_*`, `com.apple.*` |

`jc-saas-owner.filter` applies rules to the **domain part** of an email. Exact
domain entries also match subdomains (`user@mail.corp.com` matches `corp.com`).
`@corp.com` and `*@corp.com` are equivalent to `corp.com` for exact rules.
Prefix rules (`super*`) use the same `*` semantics on the domain label.

Linux local users pass through unfiltered until a dedicated file is added.

Override paths with `FILTER_JC_APPS`, `FILTER_JC_SYSTEM`, `FILTER_JC_LOCALUSERS_MACOS`,
`FILTER_JC_LOCALUSERS_WINDOWS`, `FILTER_JC_SAAS_OWNER`, and `FILTER_GW_APPS`
(defaults resolve under `BASELINE_DIR`). Empty files ⇒ nothing filtered.

After early purge, the JumpCloud Software full tab is the actionable software
view — its `(Drift)` companion is skipped when it would duplicate the full tab.

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

`writeSheets()` writes the tabs (gated by target + data availability). Most are
built from the **inventory view**; the **JumpCloud Software** tab is built from
the canonical software shard, and the **Findings** tab from the **drift
findings**. Every service tab (GoogleWorkspace, JumpCloud, Sophos, JumpCloud
Software) is paired with a `(Drift)` companion of just the problem rows. See the
[tab mapping](#sheets-tabs--which-structure-builds-which-table).

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
| `peopleforce.Asset` | `internal/peopleforce` | one hardware asset | `ID`, `Name`, `Code`, `SerialNumber`, `CategoryName`, `AssignedEmail`, `AssignedName`, `Department`, `Position`, `Location`, `IssuedOn`, `IsAssigned` |

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
    PFAssets        []peopleforce.Asset            // raw, for the PeopleForce tab
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
    PeopleForce *PFSlice             // .Assets assigned to this person
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
tagged schema (`SchemaVersion = "2.1"`). The drift engine operates **purely** on
this package and never imports a collector.

```go
type Snapshot struct {
    SchemaVersion   string
    RunDate         string         // YYYY-MM-DD
    RunTimestamp    time.Time      // exact UTC instant
    JumpCloud       JumpCloudShard // Devices[] · Identity[] · PolicyEnforcement[] · SaaS[] · Software[]
    Sophos          SophosShard    // Endpoints[] · AccountHealth
    GoogleWorkspace GWSShard       // Identity[] · Devices[]
}
```

| Canonical entity | Classified? | Notes |
|---|---|---|
| `JCDevice` | ✅ | monitored: `DiskEncrypted` (crit), `MDMEnrolled` (med) |
| `JCUser` | ✅ | monitored: `MFAEnabled` (crit), `PasswordNeverExpires` (med), `JumpCloudGoEnabled` (low) |
| `JCPolicyEnforcement` | — | per-policy rollup, dashboard only (not classified) |
| `SaaSApp` | — | JumpCloud SaaS app + nested accounts / licenses / contract; store-only (not classified) |
| `JCPersonSoftware` | — | per-person device software/extensions + SaaS memberships (email→device); store-only, whitelist-purged at collect (not classified) |
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

`writeSheets()` ([`cmd/inventory/sheets.go`](cmd/inventory/sheets.go)) writes the
tabs below (each service tab also emits a `(Drift)` companion). The first column
lists the default tab name (overridable via env).

| Tab (default name) | Built from | Row unit | Writer |
|---|---|---|---|
| **GoogleWorkspace** (+ **(Drift)**) | `[]*UnifiedUserRecord` | one user | `WriteGWS` |
| **JumpCloud** (+ **(Drift)**) | `JCSystemRow{System, User}` — **devices only, software columns removed** | one device (+ owner) | `WriteJC` |
| **SaaS** | `[]jumpcloud.SaaSApp` from `SaaSApps` — per-app licence/cost economics | one SaaS application | `WriteSaaS` |
| **JumpCloud Software** (+ **(Drift)**) | `[]model.JCPersonSoftware` (canonical shard) — device software/extensions + SaaS memberships, **email→device** | one person | `WriteJCSoftware` |
| **Sophos** (+ **(Drift)**) | `[]sophos.Endpoint` from `SophosEndpoints` | one endpoint | `WriteSophos` |
| **PeopleForce** | `[]peopleforce.Asset` from `PFAssets` | one hardware asset | `WritePeopleForce` |
| **UsersAll** | `[]*UnifiedUserRecord` | one user (cross-source merged) | `WriteMerged` |
| **Findings** | `[]model.Finding` (drift output) | one finding | `WriteFindings` |

Each `(Drift)` companion has the **same columns** as its full tab and holds only
the problematic rows — the ones the drift engine flagged (GoogleWorkspace,
JumpCloud, Sophos). JumpCloud Software is pre-filtered at collect time, so its
`(Drift)` companion is skipped when empty (the full tab is the actionable view).
A companion with no drifting rows is skipped, never recreated empty. One generic
writer (`sheets.writeFullAndDrift`) backs the finding-based companions. The
`(Drift)` tab names are configurable (`SHEETS_*_DRIFT_WORKSHEET`); companions
ride their parent's `--tabs` key.

#### SaaS column groups

One row per discovered application, grouped by derived **Category** then name:

| Group | Columns | Source |
|---|---|---|
| Service | Name · **Category** · Status · Domains · Discovery | catalog + app status (yellow=`Newly Discovered`, red=`Unapproved`) |
| Access | Restriction · SSO · Owner | access restriction, SSO connection state, resolved owner email |
| Accounts | Count · **Owner Accounts** · Last Used | owner accounts (emails + last-used in a wrapped detail cell); device-agent accounts dropped |
| Licenses | Seats (Assigned/Total) · Cost/yr · Renewal · Term | per-app license tiers + contract (yellow when seats sit unassigned) |

**Category is a derived heuristic** — the JumpCloud public SaaS API exposes no
category taxonomy, so `deriveCategory` ([`internal/jumpcloud/saas_category.go`](internal/jumpcloud/saas_category.go))
maps each app to a coarse purpose bucket from its name/domains; unmatched apps
fall through to `Other`.

**Device-agent accounts are attributed to the device owner, not shown as a bare
ID.** The accounts endpoint surfaces JumpCloud device-agent accounts as a bare
ObjectID with no email or username (e.g. `6a31d22952c3e10001285cdb`) — the SaaS
usage was discovered *on a managed device*. `mapSaaSAccounts`
([`internal/jumpcloud/saas_mapper.go`](internal/jumpcloud/saas_mapper.go)) resolves
such an account's `user_id` through the directory to the device owner and keeps
it with `device_owner` set (shown as `owner@… (via device)` in the **Owner
Accounts** cell). Only accounts with no own identity *and* no resolvable owner
are dropped; the count is logged as `skipped_accounts`, and the raw fields of one
dropped record are logged at `DEBUG` so any further attribution handle the API
offers can be wired in.

Beyond the tab, every run writes a standalone `local/current/saas.json`
(+ a daily mirror) with the full nested SaaS structures — see
[Persist](#4-persist).

**SaaS is store-only — a deliberate scope decision, not a gap.** App **Status**
(`NEWLY_DISCOVERED` / `UNAPPROVED`) is a strong shadow-IT signal, surfaced as a
red/yellow cell on the tab rather than a drift finding. SaaS apps are *not*
classified: `model.SaaSApp` carries no monitored (`drift:"monitored"`) fields,
and `ToSaaSApp` populates none. This keeps the `FindingKind` set closed at six
and the `digest` contract untouched. Promoting SaaS Status into findings would
mean either a new monitored pointer field + census entry within the closed set,
or extending `FindingKind` (which breaks the digest schema) — both are out of
scope here and require an explicit decision before wiring.

Every tab uses the same layout engine
([`internal/sheets/writer.go`](internal/sheets/writer.go)): a merged group-header
row, a column-header row, frozen headers, an "Updated" footer, and per-cell
red/yellow alert colouring driven by each column's `AlertRed` / `AlertYellow`
predicate. Each tab is fully delete-and-recreated on every write. Any single cell
that would exceed Google's 50 000-character limit (e.g. a device with hundreds of
installed apps in the JumpCloud Software tab) is truncated with a
`… [truncated — see run-folder JSON]` marker; the full, untruncated data always
remains in the run folder's `*.json`.

### UsersAll column groups

The flagship cross-source summary, one row per person (people with at least one
device float to the top):

| Group | Columns | Source |
|---|---|---|
| Identity | Email · Full Name · Org Unit · **Admin** | `Google.Identity` |
| Coverage | GWS · JC · **SP** · PF | presence of `Google` / `JumpCloud.Systems` / `Sophos.Endpoints` / `PeopleForce.Assets` |
| GWS | Suspended · 2SV · Last Login | `Google.Identity` / `Google.Auth` |
| Devices | Count · Match · **OS** · **Enc** · **MDM** · **Last Seen** · Detail | rollups over `Devices` (the JC↔Sophos pairs) |
| Alerts | Health · Open | worst `Sophos.Endpoints[].HealthOverall` / sum of `AlertCount` |
| PF Assets | Count · Detail | number of PeopleForce assets; one line per asset (`Category: Name (serial)`) |

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

**`.env` discovery.** With no explicit path, the loader reads `./.env` from the
working directory if present, otherwise `$XDG_CONFIG_HOME/gogo-assets/.env`
(default `~/.config/gogo-assets/.env`). This lets you keep the `.env` and the
service-account JSON outside the repo — e.g. `~/.config/gogo-assets/.env` +
`~/.config/gogo-assets/sa.json` with `GWS_SA_JSON_PATH=~/.config/gogo-assets/sa.json`
(`~/` is expanded). OS environment variables still override file values.

**Service-specific validation.** The full `inventory` orchestrator uses
`config.Load`, which requires Google Workspace because it is the identity spine
and authorises Sheets. Standalone service binaries use `config.LoadWithOptions`
instead, so `jc` validates `JC_API_KEY` without requiring Google credentials, and
`sophos` validates only Sophos credentials. New service binaries should follow
that pattern rather than adding their own env parser.

| Variable | Default | Purpose |
|---|---|---|
| `GWS_SA_JSON_PATH`, `GWS_ADMIN_EMAIL` | — (required) | Google SA + delegated admin |
| `GWS_CUSTOMER_ID` | `my_customer` | Google Workspace customer |
| `JC_API_KEY`, `JC_ORG_ID` | — | JumpCloud (skipped if unset) |
| `JC_SAAS_USAGE_DAYS` | `30` | SaaS usage window in days (1–90) |
| `JC_MAX_RPS` | `8` | steady JumpCloud request-rate cap (req/s); smooths bursts so 429s/backoff are rare |
| `SOPHOS_CLIENT_ID`, `SOPHOS_CLIENT_SECRET` | — | Sophos (skipped if unset) |
| `PF_API_KEY` | — | PeopleForce API key (skipped if unset) |
| `PF_BASE_URL` | `https://app.peopleforce.io/api/public/v3` | PeopleForce API base URL |
| `PF_MAX_RPS` | `5.0` | PeopleForce request-rate cap (req/s) |
| `SHEETS_SPREADSHEET_ID` | — | target spreadsheet (skipped if unset) |
| `SHEETS_GW_WORKSHEET` | `GoogleWorkspace` | per-source tab names |
| `SHEETS_GW_DRIFT_WORKSHEET` | `GoogleWorkspace (Drift)` | GWS drift companion |
| `SHEETS_JC_WORKSHEET` | `JumpCloud` | JumpCloud devices tab |
| `SHEETS_JC_DRIFT_WORKSHEET` | `JumpCloud (Drift)` | JumpCloud devices drift companion |
| `SHEETS_SAAS_WORKSHEET` | `SaaS` | JumpCloud SaaS apps (per-app economics) tab |
| `SHEETS_JC_SOFTWARE_WORKSHEET` | `JumpCloud Software` | per-person device software + SaaS |
| `SHEETS_JC_SOFTWARE_DRIFT_WORKSHEET` | `JumpCloud Software (Drift)` | skipped when empty (software pre-filtered at collect) |
| `SHEETS_SP_WORKSHEET` | `Sophos` | |
| `SHEETS_SP_DRIFT_WORKSHEET` | `Sophos (Drift)` | Sophos drift companion |
| `SHEETS_PF_WORKSHEET` | `PeopleForce` | PeopleForce assets tab |
| `SHEETS_MERGED_WORKSHEET` | `UsersAll` | cross-source summary tab |
| `SHEETS_FINDINGS_WORKSHEET` | `Findings` | drift-findings tab |
| `LOCAL_DIR`, `BASELINE_DIR` | `./local`, `./local/baseline` | storage roots |
| `FILTER_JC_APPS`, `FILTER_JC_SYSTEM`, `FILTER_JC_LOCALUSERS_MACOS`, `FILTER_JC_LOCALUSERS_WINDOWS`, `FILTER_JC_SAAS_OWNER`, `FILTER_GW_APPS` | under `BASELINE_DIR` | whitelist filter file paths |
| `LOCAL_PERSIST` | auto | force storage mode (`true`/`false`); auto ⇒ ephemeral on a GitHub-hosted runner, persist otherwise |
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
| baseline | `local/baseline/` | `classes.json`, `baseline.meta.json`, six `*.filter` whitelist files | permanent (hand-authored, tracked) |
| current | `local/current/` | `snapshot.json`, `inventory.json`, `saas.json`, `classification.json`, `digest.json`, `findings.json` | overwritten each run |
| daily | `local/daily/<run-folder>/` | `snapshot.tar.gz`, per-service full/drift (`jc*.json`, `gw*.json`, `sp*.json`, `jc-saas*.json`), `inventory.json`, `saas.json` | 30 days |
| archive | `local/archive/` | `<run_date>_digest.json` | 180 days |

The daily folder name is a readable date (e.g. `may05-2026`); `snapshot.RunFolder`
formats it and the prune parser reads it back. See
[The run folder](#the-run-folder).

`--prune` (default on) deletes expired daily/archive entries after each run. All
writes are atomic (temp-file + rename).

---

## Running in CI / GitHub Actions

The binary adapts to where it runs, so the same `inventory all` works locally and
in CI:

- **Ephemeral (Sheets-only).** On a **GitHub-hosted runner** the filesystem is
  thrown away when the job ends, so persisting the local tiers is pointless. The
  run detects this (`RUNNER_ENVIRONMENT=github-hosted`) and writes **only Google
  Sheets** — no `snapshot.json` / `inventory.json` / `saas.json` / `digest.json`,
  no prune. The drift engine still runs and feeds the Findings tab; its baseline
  travels with the repo (`local/baseline/`, tracked). `Sheets` rendering reads
  the in-memory inventory, so nothing on disk is needed.
- **Persistent.** A **self-hosted runner** or a **local machine** writes the full
  tiers as usual, and on startup `EnsureDirs` lays out
  `local/{baseline,current,daily,archive}` so a fresh checkout has the structure
  ready.

Set `LOCAL_PERSIST=true|false` to force either mode (it overrides the
auto-detection — handy to persist from a hosted runner that mounts a volume, or
to dry-run Sheets-only locally). `--approve-baseline` is ignored on an ephemeral
run, since the baseline write would not survive — approve on a persistent host
and commit `local/baseline/`.

A ready-to-edit workflow lives at
[`.github/workflows/inventory.yml`](.github/workflows/inventory.yml): it runs on
a schedule (and on demand), writes the service-account JSON from a secret, and
invokes `inventory all`. Populate the repository secrets it lists at the top.

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
cmd/gw/               standalone Google Workspace service binary
cmd/jc/               standalone JumpCloud service binary
cmd/sophos/           standalone Sophos service binary
cmd/pf/               standalone PeopleForce service binary
internal/
  model/              canonical snapshot schema + Finding types (single source of truth)
  config/             env/.env loader
  logging/            structured slog handler
  httpstat/           shared request-counting RoundTripper (HTTP totals + per-status report)
  service/            uniform service-module contract, registry, and collect runner
  servicecli/         shared runner for standalone service binaries
  gworkspace/         Google Workspace collector  (+ to_model converter)
  jumpcloud/          JumpCloud collector          (+ to_model converter)
                      + SaaS App Management (saas_client/collector/mapper/category/to_model)
                      + ToPersonSoftware (per-person software footprint, email→device)
  sophos/             Sophos Central collector     (+ to_model converter)
  peopleforce/        PeopleForce collector        (+ to_model converter)
                      (model.go, client.go, collector.go, mapper.go, to_model.go)
  inventory/          cross-source unification (email-keyed people + device join)
  assemble/           raw collector output → model.Snapshot (the one seam)
  allowlist/          file-driven whitelist (*.filter) loader
  filter/             early post-collect whitelist purge (single seam)
  serviceview/        generic per-service full/drift views (Split/Wrap/SoftwareDrift)
  baseline/           load classes.json + baseline.meta.json
  drifttag/           reflection-based drift-tag parser (enforces the pointer rule)
  classify/           phase 1: assign entities to baseline classes
  drift/              phase 2: field comparison + census diff
  digest/             build the findings-first digest
  snapshot/           tiered atomic storage (tar.gz run folder + per-service JSON)
  sheets/             Google Sheets writer (one file per tab + shared layout engine + drift companions)
local/
  baseline/           classes.json, baseline.meta.json, *.filter whitelist files (hand-authored, tracked)
  current/            live snapshot, classification, digest (runtime, gitignored)
  daily/              dated run folders <mmmDD-YYYY>/ (runtime, gitignored)
```

**Key invariant:** `internal/model` never imports a collector package, and the
drift engine imports only `model`. The `assemble` package is the single place that
knows both the raw collector types and the canonical model. This keeps the engine
testable offline with hand-crafted snapshots, and keeps the two views
([inventory](#the-unified-inventory-sheets-facing) and
[canonical](#the-canonical-snapshot-engine-facing)) cleanly decoupled.

**Service-module invariant:** `cmd/inventory` does not know how a provider talks
to its API. It asks `internal/service.Registry` for the modules selected by the
target (`gw`, `jc`, `sp`, or `all`), then each module follows the same lifecycle:
configuration gate → client → collector → typed raw output → inventory ingest →
canonical source append → API query provenance. Adding Jira or any other provider should follow that contract: create
`internal/<service>/` for the client, raw model, mapper, collector, and `to_model`
converter; add a module that satisfies `service.Module`; register it in
`service.DefaultRegistry`; and extend the canonical snapshot only where the new
service has real entities to store.
Standalone binaries should be thin wrappers around `servicecli.Run`, which owns
the common `--json` / `--no-persist` flags, config loading, signal handling,
HTTP/query logs, JSON output, and raw artifact persistence.
