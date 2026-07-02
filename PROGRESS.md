# PROGRESS.md — Unified whitelist filters (early purge)

**Task contract:** [`TASK.md`](TASK.md)  
**Status:** complete (2026-07-02)

---

## Owner decisions (locked)

| Fork | Decision |
|---|---|
| JC Software semantics | **Purge all** — allowlisted apps/extensions removed from every persisted/view structure |
| File naming | **Rename** to `*.filter`; remove old `*_allowlist.txt` (no dual fallback) |
| GWS Connected Apps match key | **DisplayText** — exact + `prefix*` wildcard |
| Linux local users | Unfiltered until `jc-localusers-linux.filter` is requested |
| JumpCloud Software (Drift) tab | Skipped when empty — full tab is the actionable view after early purge |

---

## Phase checklist

### Phase 0 — Audit (read-only)
- [x] Map `UnexpectedLocalUsers` / `isExpectedUser` / `LocalUsers` call sites
- [x] Map `ConnectedApps` / `ThirdPartyTokens` / SaaS `Accounts` consumers
- [x] List allowlist load sites today
- [x] Confirm green baseline

### Phase 1 — `*.filter` files + loader
- [x] Rename baseline files; remove old `.txt` names
- [x] Extend `internal/allowlist` (domain matcher + new constants)
- [x] Add `config.Filters` + `FILTER_*` env vars

### Phase 2 — Early filter pass
- [x] Implement `internal/filter.Apply`
- [x] Wire into `cmd/inventory/main.go` post-collect
- [x] Unit + pipeline tests

### Phase 3 — Remove legacy unexpected-user path
- [x] Delete mapper hardcode + model fields
- [x] SchemaVersion → `2.2`
- [x] Simplify JC Sheets tab (no sheet-time local-user filter)

### Phase 4 — Simplify output-layer software allowlist
- [x] Stop double-filtering in `serviceview.SoftwareDrift` / Sheets drift companion
- [x] Republish path uses already-filtered `inventory.json`

### Phase 5 — Docs + `.env.example`
- [x] `.env.example` FILTER vars
- [x] README + AGENT_GUIDE updates

### Phase 6 — Final gate
- [x] `make check` green
- [x] Definition of done in TASK.md all checked
- [x] Final summary below

---

## Definition of done

- [x] Six `*.filter` files tracked under `local/baseline/` with documented syntax.
- [x] `FILTER_*` env vars in config + `.env.example`; defaults resolve under `BASELINE_DIR`.
- [x] `filter.Apply` runs once post-collect; allowlisted entities absent from inventory, snapshot, saas export, and Sheets inputs.
- [x] `UnexpectedLocalUsers` + `isExpectedUser` removed; `SchemaVersion` bumped to `2.2`.
- [x] No redundant allowlist pass at Sheets/serviceview layer for pre-purged software.
- [x] FindingKind set still 6; pointer rule intact; determinism tests pass.
- [x] README + AGENT_GUIDE + PROGRESS.md updated.

---

## Open questions for human

*(none — Linux unfiltered and Drift companion skip confirmed by TASK.md owner notes.)*

---

## Proposed follow-ups (out of scope)

- 7th FindingKind / monitored fields for SaaS status, OAuth, SSH, login thresholds.
- `jc-localusers-linux.filter`.
- Consolidate per-app SaaS tab with JumpCloud Software tab.

---

## Changelog

- 2026-07-02 — Architect session: TASK.md + PROGRESS reset; green baseline recorded.
- 2026-07-02 — Implementation: `*.filter` files; `allowlist` + `DomainList`; `config.Filters`;
  `filter.Apply` wired post-collect before `Finalize`; removed `UnexpectedLocalUsers` /
  `isExpectedUser`; `SchemaVersion` `2.2`; simplified Sheets/serviceview/publish paths;
  docs + `.env.example`; `make check` green.

---

## Final summary

Unified whitelist filters now purge known-good entries **once**, immediately after
`service.Collect` and before `inv.Finalize()` / `assemble.Build()`. Six tracked
`*.filter` files under `local/baseline/` cover JC software, macOS/Windows local
users, SaaS owner domains, and GWS connected apps; paths are overridable via
`FILTER_*` env vars.

Legacy `UnexpectedLocalUsers` / hardcoded `isExpectedUser` are gone — local-user
filtering uses only the OS-specific filter files. Canonical schema bumped to
`2.2` (removed `unexpected_local_users` from `JCDevice`). JumpCloud Software
`(Drift)` companion and `jc-saas-drift.json` are explicit empty views after early
purge; the full software tab is the actionable surface.
