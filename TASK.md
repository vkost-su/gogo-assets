# TASK — Unified whitelist filters (early purge)

## North star

Every hand-authored whitelist lives in plain-text `*.filter` files under
`local/baseline/` (or paths overridden via env). **Listed entries are
known-good and are removed from all downstream data as early as possible** —
raw collector shapes, `inventory.AssetInventory`, `assemble.Sources`, canonical
`snapshot.json`, `inventory.json`, `saas.json`, run-folder JSON, and Sheets tabs
all see the same already-filtered world.

Concrete end state:

| Surface | Filter file (default) | Match key | Effect |
|---|---|---|---|
| JumpCloud devices → **All Users** | `jc-localusers-macos.filter` / `jc-localusers-windows.filter` | local username | OS-specific allowlist strips known system accounts; **survivors only** persist |
| JumpCloud devices → **Unexpected Users** | *(removed)* | — | Delete `UnexpectedLocalUsers` field + hardcoded `isExpectedUser`; no duplicate concept |
| JumpCloud Software → app/extension names | `jc-apps.filter` + `jc-system.filter` (merged) | app/extension name | Allowlisted names **purged** from device software slices before assemble |
| JumpCloud SaaS → owner accounts | `jc-saas-owner.filter` | email domain | Accounts on listed domains removed from `SaaSApp.Accounts` |
| Google Workspace → Connected Apps | `gw-apps.filter` | `ConnectedApp.DisplayText` | Allowlisted apps removed from `UserRecord.ConnectedApps`; `ThirdPartyTokens` reflects survivors |

Explicitly **out of scope**:

- New `FindingKind` values or monitored drift fields for SaaS status, OAuth events, SSH keys, login thresholds (see model intent-markers — owner decision required).
- Linux local-user allowlist (no file until requested; Linux devices pass through unfiltered).
- Renaming the per-app **SaaS** economics tab or consolidating it with JumpCloud Software.
- External `report-*.md` generation.
- CI credential wiring.

**Post-purge note:** JumpCloud Software **(Drift)** companion becomes redundant when
the full shard already contains only non-allowlisted software. After early purge,
either skip the companion when it would duplicate the full tab, or document that
the full tab *is* the actionable view — do not re-introduce a second allowlist pass
at the Sheets layer.

---

## Hard constraints

1. **Closed FindingKind (6)** — no 7th kind; SaaS/software stay store-only for the drift engine.
2. **Pointer rule** — do not add non-pointer monitored fields; `drifttag` must still pass at init.
3. **Determinism** — filter passes preserve stable sort order; identical input → byte-identical output.
4. **Collector isolation** — collectors stay dumb; filtering happens **once** after `service.Collect`, before `inv.Finalize()` and `assemble.Build()`. No filter logic inside Sheets writers.
5. **SchemaVersion** — removing `unexpected_local_users` from `model.JCDevice` is an intentional, documented bump (target `2.2`); call it out in PROGRESS.md changelog.
6. **Surgical edits** — every changed line traces to a checklist item below.
7. **Verification gate** — after every batch: `go build ./... && go vet ./... && go test ./...`. Run `make check` (fmt + vet + race) before closing the final phase.
8. **Architectural forks** — if you hit an unlisted fork (e.g. match `DeviceOwner` emails in SaaS filter, Linux OS file, drift companion behaviour), **STOP**, write it under **Open questions for human** in `PROGRESS.md`, do not guess.
9. **Code quality** — follow existing package conventions; for Go style and test discipline defer to the **go-programmer** skill rather than restating it here.

---

## Green baseline (recorded 2026-07-02)

```
go build ./...  ✅
go test ./...   ✅  (all packages ok)
```

Repo already has partial whitelist work (`internal/allowlist`, local-user + software
paths, sheet-time local-user filter). This task **replaces** `.txt` allowlist names,
moves filtering earlier, and adds SaaS-domain + GWS-app filters per owner decisions.

---

## The loop

Each working session:

1. Open `PROGRESS.md`. If missing, recreate from its template section.
2. Pick the next **unchecked** item in phase order — **one item at a time**.
3. Before editing: write a one-line plan + verify step in the changelog stub.
4. Make a surgical change; keep the tree buildable.
5. Verify (build/vet/test; `make check` on phase close). Green → mark `[x]` + one-line note. Red → fix before moving on.
6. Append a short entry to `PROGRESS.md` changelog.
7. Repeat until all phases checked, then run the full gate, update docs, write final summary in `PROGRESS.md`, **stop**.

**Rule:** current code beats memory and stale docs. Re-read files on demand; do not reconstruct state from the chat transcript.

---

## Phase 0 — Audit (read-only)

- [ ] Map every read/write of `UnexpectedLocalUsers`, `isExpectedUser`, `LocalUsers` (grep + note call sites). **Verify:** written inventory in PROGRESS.md.
- [ ] Map `ConnectedApps`, `ThirdPartyTokens`, SaaS `Accounts` / `saasAccountsDetail` consumers. **Verify:** table in PROGRESS.md.
- [ ] List current allowlist load sites (`main.go`, `publish.go`, `allowlist.LoadSet`). **Verify:** noted.
- [ ] Confirm green baseline (`go build ./... && go test ./...`). **Verify:** recorded in PROGRESS.md.

---

## Phase 1 — `*.filter` files + loader

Owner decision: **rename** to `*.filter`; remove old `*_allowlist.txt` names (no dual fallback).

- [ ] Rename baseline files and update headers:
  - `jc-apps.filter` ← software apps/extensions
  - `jc-system.filter` ← OS/system tools (merged with apps at load)
  - `jc-localusers-macos.filter`, `jc-localusers-windows.filter`
  - `jc-saas-owner.filter` ← domain lines (`domain.com`, `@domain.com`, `*@domain.com` equivalent)
  - `gw-apps.filter` ← GWS connected app display names
  **Verify:** old `.txt` names gone from `local/baseline/`; git tracks new files.
- [ ] Extend `internal/allowlist`:
  - Update file-name constants.
  - Add `DomainList` (or `List.AllowedDomain(email string) bool`) — suffix match on `@domain`, case-insensitive.
  - Keep existing exact + `prefix*` name matching for apps/users.
  - `LoadSet(baselineDir)` → load all six paths; missing file = empty list.
  **Verify:** `go test ./internal/allowlist/...` green; table tests for domain lines + `Google*` prefix.
- [ ] Add `config.Filters` struct + env vars with defaults under `BaselineDir`:
  - `FILTER_JC_APPS`, `FILTER_JC_SYSTEM`, `FILTER_JC_LOCALUSERS_MACOS`, `FILTER_JC_LOCALUSERS_WINDOWS`, `FILTER_JC_SAAS_OWNER`, `FILTER_GW_APPS`
  **Verify:** `config` tests or smoke load; paths expand `~/`.

---

## Phase 2 — Early filter pass (single seam)

New functions live in `internal/filter` (stdlib + allowlist + collector types only — **not** `internal/model`).

- [ ] Implement `filter.Apply(inv *inventory.AssetInventory, src *assemble.Sources, f allowlist.Set)`:
  1. **JC local users** — per device OS (`darwin` → mac list, `windows` → win list), replace `LocalUsers` with survivors (`allowlist.Unresolved`). Mutate both `inv.JCSystems` and `src.JCSystems` (same slice elements if shared — prefer filtering once on the canonical slice referenced by both).
  2. **JC software** — strip allowlisted names from `Apps`, `Programs`, `DEBPackages`, `RPMPackages`, `BrowserPlugins`, `ChromeExtensions`, `FirefoxAddons`, `SafariExtensions` on each `jumpcloud.System`.
  3. **SaaS accounts** — drop accounts whose `Email` (and `DeviceOwner` when it looks like an email) matches `jc-saas-owner.filter` domains.
  4. **GWS connected apps** — drop allowlisted `DisplayText` from each `gworkspace.UserRecord.ConnectedApps` in `src.GWS` and matching `inv.Users[..].Google`.
  **Verify:** unit tests in `internal/filter/filter_test.go` with fixture structs; no network.
- [ ] Wire in `cmd/inventory/main.go`: load filters from config → `filter.Apply` **after** `collect()`, **before** `inv.Finalize()` and `assemble.Build()`.
  **Verify:** `pipeline_test.go` extended — allowlisted app/user/app/domain absent from snapshot + inventory fixtures.

---

## Phase 3 — Remove legacy unexpected-user path

- [ ] Delete `isExpectedUser`, `_knownExpectedUsers`, `_systemUserRE` and `UnexpectedLocalUsers` computation from `internal/jumpcloud/mapper.go`.
- [ ] Remove `UnexpectedLocalUsers` from `jumpcloud.System`, `model.JCDevice`, and `jumpcloud/to_model.go`.
- [ ] Bump `model.SchemaVersion` to `2.2`; note in PROGRESS changelog.
- [ ] Simplify `internal/sheets/tabs_jc.go`: `JCSystemRow.LocalUsers` comes straight from already-filtered `sys.LocalUsers`; delete `unexpectedLocalUsers()` and drop `allowlist.Set` param from `WriteJC` if no longer needed.
- [ ] Update `cmd/inventory/sheets.go`, `publish.go`, `main.go` call sites for simplified JC tab signature.
  **Verify:** `go test ./...` green; grep shows zero `UnexpectedLocalUsers` / `isExpectedUser`.

---

## Phase 4 — Simplify output-layer allowlist (software drift)

Early purge makes output-time software allowlist redundant.

- [ ] `serviceview.SoftwareDrift` — either pass `nil` allowlist everywhere or simplify to identity copy when input is pre-filtered; **do not** double-filter.
- [ ] JumpCloud Software **(Drift)** Sheets companion — skip when full tab already contains only survivors (or when drift slice equals full slice). Document behaviour in code comment.
- [ ] Remove allowlist reload from sheet-only paths if `inventory.json` is already filtered (republish path reads stored inventory — **no second filter** on `sheets` command).
  **Verify:** `serviceview_test.go`, `pipeline_test.go`, `sheets` drift tests updated.

---

## Phase 5 — Docs + `.env.example`

- [ ] `.env.example` — document all six `FILTER_*` vars with default filenames and comment syntax (`#`, blank lines, `prefix*` for names, domain lines for SaaS).
- [ ] `README.md` — replace `The whitelist (allowlist)` section: new filenames, early-purge semantics, env overrides, removed `UnexpectedLocalUsers`.
- [ ] `AGENT_GUIDE.md` — update allowlist/filter section, config table, codebase map (`internal/filter`), storage tier file list.
  **Verify:** docs match code; no references to old `*_allowlist.txt`.

---

## Phase 6 — Final gate

- [ ] `make check` green.
- [ ] Definition of done (below) all `[x]`.
- [ ] Final summary in `PROGRESS.md`.

---

## Definition of done

- [ ] Six `*.filter` files tracked under `local/baseline/` with documented syntax.
- [ ] `FILTER_*` env vars in config + `.env.example`; defaults resolve under `BASELINE_DIR`.
- [ ] `filter.Apply` runs once post-collect; allowlisted entities absent from inventory, snapshot, saas export, and Sheets inputs.
- [ ] `UnexpectedLocalUsers` + `isExpectedUser` removed; `SchemaVersion` bumped and noted.
- [ ] No redundant allowlist pass at Sheets/serviceview layer for pre-purged software.
- [ ] FindingKind set still 6; pointer rule intact; determinism tests pass.
- [ ] README + AGENT_GUIDE + PROGRESS.md updated.
- [ ] Anything else → **Proposed follow-ups** in PROGRESS.md (not implemented).

---

## External memory

`PROGRESS.md` is the source of truth for what's done. Update it every session;
do not rely on the chat transcript.
