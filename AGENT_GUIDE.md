# gogo-assets — Agent Developer Guide

This document is written for Claude agents. It is dense by design: skip prose,
read the tables and code patterns. Cross-references use `package/file.go:line`
style so you can jump straight to the source.

---

## 1. Codebase Map

```
cmd/
  inventory/       main orchestrator binary (collect → assemble → drift → persist → publish)
    main.go        flag parsing, target routing, collect() orchestration
    drift.go       runDrift() — classify → drift → digest phases
    approve.go     --approve-baseline / --approve-from-current
    publish.go     writeSheets() + store.Write* helpers
    sheets.go      sheets command handler
    pipeline_test.go  end-to-end test, no network
  gw/main.go       standalone GWS binary (thin wrapper around servicecli.Run)
  jc/main.go       standalone JC binary
  sophos/main.go   standalone Sophos binary
  pf/main.go       standalone PeopleForce binary (thin wrapper around servicecli.Run)

internal/
  model/           ← canonical schema; drift engine imports ONLY this
    model.go       Snapshot, all entity types, drift tags, Severity
    finding.go     Finding, FindingKind (closed set of 6), Entity
  config/config.go Settings loader; env + .env file; XDG discovery
  service/
    service.go     Module interface, Registry, Collect() orchestrator
    modules.go     GoogleWorkspaceModule, JumpCloudModule, SophosModule
  assemble/assemble.go  Sources → model.Snapshot (the ONLY seam collectors↔engine)
  allowlist/       file-driven whitelist (*.filter); Load/LoadFromPaths + Unresolved[T] + DomainList
  filter/          early post-collect whitelist purge (filter.Apply)
  serviceview/     generic per-service full/drift views (DriftedIDs/Split/Filter/Wrap, Export[T], SoftwareDrift)
  inventory/
    model.go       AssetInventory, UnifiedUserRecord, DevicePair (Sheets-facing)
    ingest.go      AddGoogle / AddJC / AddSophos
    finalize.go    Finalize() — bare-username heuristic, device join, backfill
  gworkspace/      GWS collector: client.go, collector.go, model.go, mapper.go, to_model.go
  jumpcloud/       JC collector + SaaS: client.go, collector.go, model.go, mapper.go, to_model.go
                   saas_client.go, saas_collector.go, saas_model.go, saas_mapper.go,
                   saas_to_model.go, saas_category.go, saas_export.go
  sophos/          Sophos collector: client.go, collector.go, model.go, mapper.go, to_model.go
  peopleforce/     PeopleForce collector: client.go, collector.go, model.go, mapper.go, to_model.go
  classify/
    classify.go    Run() — assign entities to baseline classes → []Result with ClassID
    entities.go    flattens Snapshot → []Result (which entity types are classified)
  drift/drift.go   Run() — field comparison + census diff → []model.Finding
  digest/digest.go Build() — roll findings into digest.json; carry first_seen forward
  baseline/
    baseline.go    load classes.json + baseline.meta.json; validate() at startup
    write.go       WriteBaseline() — census anchoring (--approve-baseline)
  drifttag/        reflection-based drift-tag parser; enforces pointer rule at init
  snapshot/
    store.go       WriteSnapshot(tar.gz) / WriteInventory / WriteSaaS / WriteDailyJSON /
                   WriteCurrentJSON / ReadSnapshot / RunFolder (readable daily name)
    snapshot.go    tiered store paths (current / daily / archive); atomic writes
  sheets/
    writer.go      writeTab[T] generic — delete-recreate, values, format; clampCell (50k/cell cap)
    drift.go       writeFullAndDrift[T] — full tab + (Drift) companion; driftSubset filter
    column.go      Column[T] struct + Bool/BoolValue helpers
    cellfmt.go     cell colour helpers
    format.go      batch format request builders
    tabs_gws.go    WriteGWS (+ drift companion)
    tabs_jc.go     WriteJC (devices only; software columns removed) (+ drift)
    tabs_saas.go   WriteSaaS (per-app economics)
    tabs_jcsoftware.go WriteJCSoftware — per-person software + SaaS (+ drift, skipped when empty)
    tabs_sophos.go WriteSophos (+ drift companion)
    tabs_pf.go     WritePeopleForce
    tabs_merged.go WriteMerged (UsersAll)
    tabs_findings.go WriteFindings
  servicecli/runner.go  shared runner for standalone service binaries
  httpstat/        shared HTTP counter (RoundTripper); per-status totals + 404-endpoint breakdown
  logging/         slog handler
  apiquery/        query-template dedup helper used by collectors

local/
  baseline/        classes.json, baseline.meta.json, six *.filter whitelist files
  current/         runtime files — snapshot.json, inventory.json, saas.json, findings.json, etc.
  daily/           <run-folder>/ (readable date, e.g. may05-2026): snapshot.tar.gz + per-service full/drift
  archive/         dated digest archives
```

---

## 2. Pipeline Data Flow

```
                  ┌──────────────────────────────────────────────────────┐
  GWS/JC/Sophos/  │                                                      │
  PeopleForce ────▶  service.Collect()                                   │
                  │    per module:  Collect → IngestInventory → AppendSources
                  └───────────────┬─────────────────┬────────────────────┘
                                  │                 │
            inv.Finalize()        │                 │ assemble.Build()
                                  ▲                 ▲
                       filter.Apply (post-collect)   │
                                  ▼                 ▼
               inventory.AssetInventory      model.Snapshot
               (email-keyed, Sheets view)    (flat, sorted, drift engine view)
                        │                          │
              writeSheets()                  runDrift() + writeServiceOutputs()
              service tabs + (Drift)         classify → drift → digest
              companions + JC Software       → findings.json, digest.json,
              (from canonical shard)           per-service full/drift JSON (run folder)
```

**Key invariants:**
- `internal/model` never imports a collector package. The drift engine imports only `model`.
- `assemble` is the **only** package that knows both raw collector types and `model`.
- `inventory.AssetInventory` and `model.Snapshot` are derived independently from the same run; neither depends on the other.

---

## 3. Adding a New Collector (Service Integration)

Follow this checklist exactly. JumpCloud (`internal/jumpcloud/`), Sophos
(`internal/sophos/`), and PeopleForce (`internal/peopleforce/`) are the reference
implementations.

### Step 1 — Create `internal/<service>/`

Minimum files:

| File | Purpose |
|---|---|
| `model.go` | Raw API types (`System`, `User`, etc.) and the `Output` struct |
| `client.go` | HTTP client; wrap with `httpstat.Counter` RoundTripper |
| `collector.go` | `Collector` + `CollectAll(ctx)` method; populates `*Output` |
| `mapper.go` | Parse raw API JSON → internal model types |
| `to_model.go` | Thin converters: `ToDevice(raw, meta) model.XxxEntity` etc. |

The `Output` struct is the typed boundary:
```go
type Output struct {
    Records  []*SomeRecord
    Queries  []string // apiquery.Template entries — the endpoint shapes
}
```

### Step 2 — Extend `model.Snapshot` if the service has classifiable entities

Edit `internal/model/model.go`:

1. Add a new `XxxShard` struct with the entity slice.
2. Add a field to `Snapshot`: `Foo FooShard json:"foo"`.
3. For each entity type that should be classified:
   - Add `drift:"identity"` tags to matching/key fields.
   - Use `*bool` / `*int` / `*time.Time` for **monitored** fields + tag `drift:"monitored,sev=crit|high|med|low"`.
   - Use `drift:"volatile"` for contextual fields that are stored but never compared.

The `drifttag` package validates the pointer rule at startup — a non-pointer
monitored field panics immediately, so you catch it before any real data.

### Step 3 — Add `to_model.go` converters

Each converter takes a raw type + `model.Meta` and returns the canonical entity:
```go
func ToThing(raw SomeThing, meta model.Meta) model.FooEntity {
    return model.FooEntity{
        Meta:       meta,
        ThingID:    raw.ID,
        SomeField:  ptr(raw.Value), // use ptr() helper for bool/int pointers
    }
}
```

### Step 4 — Wire into `assemble/assemble.go`

1. Add the new raw types to `Sources`:
   ```go
   FooRecords []foo.Record
   FooQueries []string
   ```
2. Add a `buildFoo(src Sources, meta model.Meta) model.FooShard` function (sort by identity key).
3. Call it in `Build()` and add `Foo: buildFoo(src, meta)` to the returned `Snapshot`.
4. If needed, add a `Foo []string` provenance field to `model.Provenance`.

### Step 5 — Wire into `classify/entities.go`

Add a loop over the new entity slice (if classifiable):
```go
for _, t := range snap.Foo.Things {
    out = append(out, Result{
        Entity:      model.Entity{Type: model.EntityDevice, ID: t.ThingID, ...},
        Service:     model.ServiceFoo,
        Val:         t,
        EvidenceRef: evidence("foo.things", "thing_id", t.ThingID),
    })
}
```

### Step 6 — Add the `service.Module` implementation

In `internal/service/modules.go`, add a `FooModule` struct implementing all 8
methods of `service.Module`. Key methods:

```go
func (m FooModule) Collect(ctx context.Context, rt Runtime) (Result, error) {
    client := foo.New(rt.Settings.Foo.APIKey, rt.HTTPCounter)
    out := &foo.Output{}
    c := foo.NewCollector(client, out)
    if err := c.CollectAll(ctx); err != nil {
        return Result{}, err
    }
    return Result{Key: m.Key(), Service: m.ModelService(), Output: out,
        Queries: out.Queries, Counts: map[string]int{"records": len(out.Records)}}, nil
}

func (m FooModule) IngestInventory(inv *inventory.AssetInventory, r Result) error {
    out, err := outputAs[foo.Output](m, r)
    if err != nil { return err }
    inv.AddFoo(out.Records)
    return nil
}

func (m FooModule) AppendSources(src *assemble.Sources, r Result) error {
    out, err := outputAs[foo.Output](m, r)
    if err != nil { return err }
    src.FooRecords = out.Records
    src.FooQueries = out.Queries
    return nil
}
```

### Step 7 — Register the module

In `service.DefaultRegistry()` in `internal/service/service.go`:
```go
func DefaultRegistry() Registry {
    return Registry{
        GoogleWorkspaceModule{},
        JumpCloudModule{},
        SophosModule{},
        FooModule{},  // add here
    }
}
```

### Step 8 — Add config fields

In `internal/config/config.go`:
1. Add a `Foo struct { APIKey string }` field.
2. Add `Foo Foo` to `Settings`.
3. Parse in `LoadWithOptions`.
4. Add `RequireFoo bool` to `LoadOptions` and handle in `loadFoo()`.

### Step 9 — Standalone binary (optional)

Create `cmd/foo/main.go` — a three-line wrapper:
```go
func run(args []string) error {
    return servicecli.Run(args, servicecli.Options{
        Name:        "foo",
        Module:      service.FooModule{},
        LoadOptions: config.LoadOptions{RequireFoo: true},
    })
}
```

`servicecli.Run` handles flags (`--json`, `--no-persist`), config loading,
signal handling, HTTP log, JSON output, and raw artifact persistence automatically.

### Step 10 — Add `inventory.AssetInventory` ingest

In `internal/inventory/`:
1. Add slice/map fields to `AssetInventory` in `model.go`.
2. Add `AddFoo(records []foo.Record)` in `ingest.go`.
3. Extend `Finalize()` in `finalize.go` if cross-source correlation is needed.

### Step 11 — Add a Sheets tab

See [Section 4 — Adding a New Sheets Tab](#4-adding-a-new-sheets-tab) below.

---

## 4. Adding a New Sheets Tab

Reference: `internal/sheets/tabs_jc.go` (simplest) or `tabs_merged.go` (complex).

### Step 1 — Create `internal/sheets/tabs_foo.go`

```go
package sheets

import (
    "context"
    "gogo-assets/internal/foo" // your new package
)

// WriteFoo writes the Foo tab to svc.
func WriteFoo(ctx context.Context, svc *Service, sheetName string, records []foo.Record) error {
    cols := []Column[foo.Record]{
        {Group: "Identity", Header: "Name",   Extract: func(r foo.Record) string { return r.Name }},
        {Group: "Identity", Header: "Status", Extract: func(r foo.Record) string { return r.Status },
            AlertRed: func(s string) bool { return s == "BAD" }},
        // ... more columns
    }
    return writeTab(ctx, svc, sheetName, cols, records, WriteOptions{})
}
```

Rules for `Column[T]`:
- `Extract` must never panic; return `""` for missing data.
- Use `Bool(*bool)` helper for boolean fields (returns `"Yes"` / `"No"` / `""`).
- `AlertRed` / `AlertYellow` receive the already-extracted string.
- `Wrap: true` for cells with multi-line content.

### Step 2 — Register the tab in `cmd/inventory/sheets.go`

`buildSheetTabs` is the single registry both the auto-write and `sheets` publish
paths go through. Add a `tabKey` const (+ `allTabKeys`, `targetTabs`, and the
`parseTabs` error message), then a `sheetTab` entry with a data-availability gate:
```go
{tabFoo, s.Sheets.FooWorksheet, len(inv.FooRecords) > 0, func() error {
    return sheets.WriteFoo(ctx, svc, s.Sheets.FooWorksheet, inv.FooRecords)
}},
```
For a tab with a `(Drift)` companion, use `sheets.writeFullAndDrift` inside the
writer (pass the drift key set from `serviceview.DriftedIDs`) — see `WriteGWS`.

### Step 3 — Add config for tab name

In `config.go` `Sheets` struct:
```go
FooWorksheet string
```
In `LoadWithOptions`:
```go
FooWorksheet: optional("SHEETS_FOO_WORKSHEET", "Foo"),
```

### Step 4 — Add `foo` to the `--tabs` key list

In `cmd/inventory/sheets.go`, add the key to `allTabKeys`/`targetTabs` (the
`parseTabs` validation derives from `allTabKeys`); then update the `--tabs` help
text in `cmd/inventory/main.go` (`printUsage` + the flag description).

---

## 5. Extending the Drift Engine

### Adding a monitored field to an existing entity

1. In `internal/model/model.go`, add the field with a pointer type and drift tag:
   ```go
   SomeNewCheck *bool `json:"some_new_check" drift:"monitored,sev=high"`
   ```
2. In the collector's `to_model.go`, populate it from the raw data.
3. In `local/baseline/classes.json`, add the field name to the relevant class's `expected` block:
   ```json
   "some_new_check": "true"
   ```
   Or with severity override:
   ```json
   "some_new_check": {"value": "true", "severity": "crit"}
   ```

`baseline.validate()` checks field names against the canonical model at startup — a typo fails fast.

### Adding a new baseline class

Edit `local/baseline/classes.json` only — no recompile:
```json
{
  "id": "my-new-class",
  "priority": 10,
  "match": { "os_family": "darwin", "is_admin": "true" },
  "expected": {
    "disk_encrypted": "true",
    "mdm_enrolled": { "value": "true", "severity": "crit" }
  }
}
```

- `match` keys are canonical JSON field names (AND-ed).
- `expected` keys must match `drift:"monitored"` field JSON names.
- Higher `priority` wins on conflict (tie → `CLASS_CONFLICT` finding, still resolved deterministically).
- After changing which entities are considered approved (new class, changed match), re-anchor census:
  ```bash
  ./bin/inventory all --approve-baseline
  ```

### The FindingKind set is CLOSED

There are exactly 6 kinds (`BASELINE_DRIFT`, `DATA_GAP`, `NEW_ENTITY`,
`ENTITY_DISAPPEARED`, `UNCLASSIFIED`, `CLASS_CONFLICT`). Do not add a 7th
without explicit discussion — it breaks the digest schema and the downstream
contract. SaaS Status findings were deliberately excluded for this reason, and
JumpCloud software is whitelist-purged at the **collection** layer
(`filter.Apply`); the FindingKind set stays six.

---

## 6. Key Contracts and Invariants

### Pointer rule (`drifttag`)

Every `drift:"monitored"` field **must** be a pointer (`*bool`, `*int`, `*time.Time`).

- `nil` → not collected → emits `DATA_GAP`
- `*false` → collected, off → emits `BASELINE_DRIFT`

The `drifttag` package enforces this with reflection at program startup. If you
add a non-pointer monitored field, the binary panics immediately with a clear message.

### Deterministic output

`assemble.Build()` sorts every entity slice by its identity key before returning.
Identical collector output must produce byte-identical `snapshot.json`. Do not
introduce map iteration into the assembly path.

### Collector isolation

No collector package imports `internal/model`. Raw types live in their own
package; `to_model.go` in that same package does the one-way translation.
`assemble` is the only package that imports both worlds.

### SaaS is store-only

`model.SaaSApp` has no `drift:"monitored"` fields. It is stored in
`JumpCloudShard.SaaS` for the tab and `saas.json`, but is never classified and
generates no drift findings. Do not add monitored fields to `SaaSApp` without
explicitly extending the FindingKind set.

### PeopleForce is store-only

`model.PFAsset` follows the same store-only precedent as SaaS. It has no
`drift:"monitored"` fields — all fields carry `identity` or `volatile` tags.
Assets are stored in `PeopleForceShard.Assets` for the tab, but are never
classified and generate no drift findings. The FindingKind set remains closed at
six. Do not add monitored fields to `PFAsset` without an explicit decision to
extend FindingKind.

### JumpCloud Software is store-only (early-purge, no drift companion)

`model.JCPersonSoftware` (`JumpCloudShard.Software`) has no `drift:"monitored"`
fields and is never classified. Known-good software is purged at collection time
via `filter.Apply`; `serviceview.SoftwareDrift` returns nil so the full tab is
the actionable view. Do not promote software into a monitored field or a 7th
FindingKind without an explicit decision.

---

## 7. Config Reference (quick)

| Env var | Default | Purpose |
|---|---|---|
| `GWS_SA_JSON_PATH` | — (required) | Path to Google SA JSON key |
| `GWS_ADMIN_EMAIL` | — (required) | Admin email for DWD impersonation |
| `GWS_CUSTOMER_ID` | `my_customer` | GWS customer ID |
| `JC_API_KEY` | — | JumpCloud API key (skipped if unset) |
| `JC_ORG_ID` | — | JumpCloud org ID (MSP only) |
| `JC_SAAS_USAGE_DAYS` | `30` | SaaS usage window (1–90) |
| `JC_MAX_RPS` | `8` | JumpCloud rate cap req/s |
| `SOPHOS_CLIENT_ID` / `_SECRET` | — | Sophos credentials (skipped if unset) |
| `PF_API_KEY` | — | PeopleForce API key (skipped if unset) |
| `PF_BASE_URL` | `https://app.peopleforce.io/api/public/v3` | PeopleForce API base URL |
| `PF_MAX_RPS` | `5.0` | PeopleForce request-rate cap (req/s) |
| `SHEETS_SPREADSHEET_ID` | — | Target spreadsheet (skipped if unset) |
| `SHEETS_*_WORKSHEET` | tab names | Override tab names, incl. `SHEETS_JC_SOFTWARE_WORKSHEET` and the `SHEETS_*_DRIFT_WORKSHEET` companions |
| `LOCAL_DIR` | `./local` | Root of storage tiers |
| `BASELINE_DIR` | `./local/baseline` | Baseline config + census + `*.filter` files |
| `FILTER_JC_APPS` … `FILTER_GW_APPS` | under `BASELINE_DIR` | whitelist filter file path overrides |
| `LOCAL_PERSIST` | auto | `true`/`false`; auto=ephemeral on github-hosted runner |
| `DIGEST_MAX_BYTES` | `51200` | Hard cap on digest.json size |
| `LOG_LEVEL` | `INFO` | `DEBUG`/`INFO`/`WARN`/`ERROR` |
| `ENRICH_DELAY_S` | `0` | Per-user delay in GWS enrichment (rate limit relief) |

`.env` discovery: reads `./.env` first, then `~/.config/gogo-assets/.env`.
OS environment always overrides file values.

---

## 8. Storage Tiers

| Tier | Path | Written by | Kept |
|---|---|---|---|
| `baseline` | `local/baseline/` | `--approve-baseline` + hand-authored | permanent; git-tracked |
| `current` | `local/current/` | every run | overwritten each run |
| `daily` | `local/daily/<run-folder>/` | every run | 30 days |
| `archive` | `local/archive/` | every run | 180 days |

`baseline/` holds `classes.json`, `baseline.meta.json`, and six `*.filter`
whitelist files. The daily folder
name is a readable date (`snapshot.RunFolder("2026-05-05") == "may05-2026"`); the
prune parser (`dirDate`, `Jan02-2006`) reads it back. All writes are atomic
(temp-file + rename via `snapshot.Store`).

`store.WriteSnapshot(snap)` → `current/snapshot.json` (plain) + `daily/<folder>/snapshot.tar.gz`.
`store.WriteInventory(inv)` → `inventory.json` (rich Sheets source of truth).
`store.WriteSaaS(export)` → `saas.json` (per-app SaaS economics; skipped if empty).
`store.WriteDailyJSON(runDate, name, v)` → `daily/<folder>/<name>` (per-service full/drift).
`store.WriteCurrentJSON(name, v)` → any raw artifact under `current/`.
`store.ReadSnapshot(runDate)` reads the snapshot back (plain current, or the daily tar.gz).

### Per-service full/drift outputs (`serviceview`)

`cmd/inventory/serviceoutputs.go` (`writeServiceOutputs`) emits, per service, a
**full** and a **drift** JSON into the run folder via one generic mechanism —
no per-service copy:

- `serviceview.DriftedIDs(findings, svc, etype)` → identity keys with ≥1 finding.
- `serviceview.Split(records, keyOf, drifted, …)` → `Export[T]` full + drift.
- `serviceview.Export[T]` is the self-describing wrapper (`schema_version`,
  `service`, `view`, `run_date`, `run_timestamp_utc`, `count`).

Emitted: `jc.json`/`jc-drift.json` (devices), `gw.json`/`gw-drift.json`,
`sp.json`/`sp-drift.json`, and `jc-saas.json`/`jc-saas-drift.json` (per-person
software). **Skip-empty:** a service with no records writes nothing; a clean
service still writes its `*-drift.json` with `count: 0` for the external report
step. This path rides `assemble.Build → shard → classify/drift` and **never**
touches `internal/inventory`.

### JumpCloud Software = per-person, email→device (`model.JCPersonSoftware`)

`jumpcloud.ToPersonSoftware(systems, saas, meta)` (invoked in `assemble`) folds,
per person keyed by lower-cased email, every app/extension on the devices they
own (device `OwnerEmail` is the anchor) plus their SaaS-app memberships (SaaS
account email, falling back to the device-agent `DeviceOwner`). Store-only: no
monitored fields, never classified.

### Whitelist filters (early purge)

`internal/allowlist` loads plain-text `*.filter` files; `internal/filter.Apply`
purges known-good entries once, immediately after `service.Collect` and before
`inv.Finalize()` / `assemble.Build()`. Listed names/domains are removed from
inventory, snapshot, SaaS export, and Sheets inputs — not filtered again at the
Sheets layer.

Files under `local/baseline/` (override via `FILTER_*` env vars):

- `jc-apps.filter` + `jc-system.filter` — software/extension names (merged)
- `jc-localusers-macos.filter` / `jc-localusers-windows.filter` — local usernames
- `jc-saas-owner.filter` — SaaS account email domains
- `gw-apps.filter` — GWS `ConnectedApp.DisplayText`

`serviceview.SoftwareDrift` returns nil after early purge; the JumpCloud Software
full tab is the actionable view. `(Drift)` companion is skipped when empty.

### Sheets (Drift) companions

`sheets.writeFullAndDrift[T]` writes a full tab plus, when a drift set is given,
a `<Name> (Drift)` companion (same columns, only drifting rows; skipped if empty).
Used by `WriteGWS`/`WriteJC`/`WriteSophos` (findings-keyed). `WriteJCSoftware`
writes the per-person full tab; its `(Drift)` companion is skipped when empty
after early purge. Tab names live
in `config.Sheets` (`*Worksheet` + `*DriftWorksheet`); `(Drift)` companions ride
their parent's `--tabs` key.

---

## 9. Testing

```bash
make test          # unit tests
make race          # tests under race detector (required before any PR)
make check         # full gate: fmt-check + vet + race
```

The end-to-end pipeline test at `cmd/inventory/pipeline_test.go` runs the
complete assemble → classify → drift → digest → store cycle in a temp directory
with no network calls. It is the reference for testing new features that touch
the engine.

Collector packages each have `to_model_test.go` for unit-testing conversions.

The drift engine packages (`classify`, `drift`, `digest`, `baseline`, `assemble`)
depend only on the standard library and are fully testable offline.

---

## 10. Common Tasks — Quick Reference

| Task | Files to touch |
|---|---|
| Add a monitored field to an existing entity | `internal/model/model.go`, collector's `to_model.go`, `local/baseline/classes.json` |
| Add a new baseline class | `local/baseline/classes.json` only |
| Add a new collector (full) | See Section 3 — 11 steps |
| Add a new Sheets tab | `internal/sheets/tabs_foo.go`, `cmd/inventory/sheets.go` (buildSheetTabs + tabKey), `config/config.go` |
| Add a `(Drift)` companion to a tab | `sheets.writeFullAndDrift` in the writer + `serviceview.DriftedIDs`; `*DriftWorksheet` in `config.go` |
| Tune whitelist filters | edit `local/baseline/*.filter` or set `FILTER_*` env vars (no recompile) |
| Add a new standalone binary | `cmd/foo/main.go` (3 lines), `config/config.go` if new creds needed |
| Change tab name default | `config/config.go` optional() fallback |
| Tune SaaS rate limit | `.env` → `JC_MAX_RPS` |
| Re-anchor baseline after adding entities | `./bin/inventory all --approve-baseline` |
| Re-push Sheets from stored data | `./bin/inventory sheets [--tabs x,y] [--run-date YYYY-MM-DD]` |

---

## 11. Files NOT to Modify Without Careful Thought

| File | Why |
|---|---|
| `internal/model/finding.go` — `FindingKind` constants | Digest schema + downstream contract depend on the closed set of 6 |
| `internal/drifttag/drifttag.go` | Enforces the pointer rule at startup; changes affect all entity types |
| `internal/assemble/assemble.go` — sort order | Breaks determinism and snapshot diffs |
| `local/baseline/` | Hand-authored policy config; git-tracked; wrong edits cause false positives |
