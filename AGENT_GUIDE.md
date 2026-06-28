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
    store.go       WriteSnapshot / WriteInventory / WriteSaaS / WriteCurrentJSON
    snapshot.go    tiered store paths (current / daily / archive); atomic writes
  sheets/
    writer.go      writeTab[T] generic — delete-recreate, values, format
    column.go      Column[T] struct + Bool/BoolValue helpers
    cellfmt.go     cell colour helpers
    format.go      batch format request builders
    tabs_gws.go    WriteGWS
    tabs_jc.go     WriteJC
    tabs_saas.go   WriteSaaS
    tabs_sophos.go WriteSophos
    tabs_pf.go     WritePeopleForce
    tabs_merged.go WriteMerged (UsersAll)
    tabs_findings.go WriteFindings
  servicecli/runner.go  shared runner for standalone service binaries
  httpstat/        shared HTTP counter (RoundTripper); logs status_200/429/500 totals
  logging/         slog handler
  apiquery/        query-template dedup helper used by collectors

local/
  baseline/        classes.json (policy config), baseline.meta.json (census anchor)
  current/         runtime files — snapshot.json, inventory.json, saas.json, etc.
  daily/           YYYY-MM-DD/ dated mirrors
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
                                  ▼                 ▼
               inventory.AssetInventory      model.Snapshot
               (email-keyed, Sheets view)    (flat, sorted, drift engine view)
                        │                          │
              writeSheets()                  runDrift()
              7 Google Sheets tabs           classify → drift → digest
                                             → findings.json, digest.json
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

### Step 2 — Call `WriteFoo` in `cmd/inventory/publish.go`

In `writeSheets()`, add a gate + call:
```go
if tabAllowed("foo", allowed) && len(inv.FooRecords) > 0 {
    if err := sheets.WriteFoo(ctx, svc, cfg.Sheets.FooWorksheet, inv.FooRecords); err != nil {
        return fmt.Errorf("write foo tab: %w", err)
    }
}
```

### Step 3 — Add config for tab name

In `config.go` `Sheets` struct:
```go
FooWorksheet string
```
In `LoadWithOptions`:
```go
FooWorksheet: optional("SHEETS_FOO_WORKSHEET", "Foo"),
```

### Step 4 — Add `foo` to `--tabs` key list

In `cmd/inventory/main.go`, update the `parseTabs` allowed set and the help text.

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
contract. SaaS Status findings were deliberately excluded for this reason.

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
| `SHEETS_*_WORKSHEET` | tab names | Override individual tab names (`SHEETS_PF_WORKSHEET` default `PeopleForce`) |
| `LOCAL_DIR` | `./local` | Root of storage tiers |
| `BASELINE_DIR` | `./local/baseline` | Baseline config + census |
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
| `baseline` | `local/baseline/` | `--approve-baseline` | permanent; git-tracked |
| `current` | `local/current/` | every run | overwritten each run |
| `daily` | `local/daily/YYYY-MM-DD/` | every run | 30 days |
| `archive` | `local/archive/` | every run | 180 days |

All writes are atomic (temp-file + rename via `snapshot.Store`).

`store.WriteSnapshot(snap)` writes `snapshot.json` (lean canonical).
`store.WriteInventory(inv)` writes `inventory.json` (rich Sheets source of truth).
`store.WriteSaaS(export)` writes `saas.json` (SaaS tab source; skipped if empty).
`store.WriteCurrentJSON(name, v)` writes any raw artifact to `current/<name>`.

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
| Add a new Sheets tab | `internal/sheets/tabs_foo.go`, `cmd/inventory/publish.go`, `config/config.go` |
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
