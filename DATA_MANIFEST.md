# DATA_MANIFEST

This document captures the data architecture as it exists now. It is not an
implementation plan and does not change any code. Its goal is to make every data
structure, persisted artifact, and downstream consumer explicit before the next
architecture pass for local LLM analysis.

## 1. Current Data Flow

The program does not currently build one single master object and then slice it.
It collects raw data once and fans that data into two independent views:

```text
external APIs
  -> service.Collect()
      -> inventory.AssetInventory  (rich, people-centric, Sheets source)
      -> assemble.Sources          (raw collector bundle)
  -> assemble.Build()
      -> model.Snapshot            (lean, entity-centric, drift/digest source)
```

The important invariant is that `inventory.AssetInventory` and
`model.Snapshot` are siblings. Neither is derived from the other. They are both
derived from the same typed collector outputs in the same run.

## 2. Service Modules

Every integration follows the same service-module lifecycle:

1. Check whether the service is configured.
2. Collect typed raw output.
3. Ingest raw output into `inventory.AssetInventory`.
4. Append raw output into `assemble.Sources`.
5. Carry API query provenance into `model.Snapshot.Provenance`.

| Service | Target | Required | Raw output | Inventory ingest | Snapshot source append | Raw artifact name |
|---|---:|---:|---|---|---|---|
| Google Workspace | `gw` | yes | `*gworkspace.Output` | `inv.AddGoogle(out.Records)` | `src.GWS`, `src.GWSQueries` | `gws_raw.json` |
| JumpCloud | `jc` | no | `*jumpcloud.Output` | `inv.AddJC(out.Systems, out.Users)`, `inv.SaaSApps` | `src.JCSystems`, `src.JCUsers`, `src.JCSaaS`, `src.JCQueries` | `jc_raw.json` |
| Sophos Central | `sp` | no | `*sophos.Output` | `inv.AddSophos(out.Endpoints)` | `src.Endpoints`, `src.SophosQueries` | `sophos_raw.json` |
| PeopleForce | `pf` | no | `*peopleforce.Output` | `inv.AddPeopleForce(out.Assets)` | `src.PFAssets`, `src.PFQueries` | `pf_raw.json` |

Google Workspace is treated as the required identity spine. Optional services
are skipped when credentials are absent.

## 3. Raw Collector Outputs

Raw collector packages own API-specific models and mapping details. These are
the richest source records before cross-source joining or canonical reduction.

### Google Workspace

Package: `internal/gworkspace`

Top-level output:

```go
type Output struct {
    Records map[string]*UserRecord `json:"records"` // keyed by primary email
    Queries []string               `json:"queries"`
}
```

Primary raw structures:

| Type | Unit | Key fields / payload |
|---|---|---|
| `Identity` | one Google directory user identity block | `Email`, `FullName`, `OrgUnitPath`, `IsSuspended`, `IsArchived`, `IsAdmin`, `CreatedAt`, `RecoveryEmail` |
| `AuthPosture` | auth flags from the user object | `Is2SVEnrolled`, `Is2SVEnforced`, `LastLoginTime`, `PasswordChangedAt`, `ChangePasswordAtNextLogin` |
| `LoginActivity` | aggregated Reports API login activity | `KnownIPs`, `LastLoginIP`, `SuccessfulLoginCount`, `FailedLoginCount`, `SuspiciousLoginCount`, `EventsWindowStart`, `EventsWindowEnd` |
| `OAuthGrant` | one token activity event | `EventTime`, `EventType`, `AppName`, `ClientID`, `Scopes`, `ClientType` |
| `ConnectedApp` | one currently authorized app | `ClientID`, `DisplayText`, `Scopes`, `IsAnonymous`, `IsNativeApp` |
| `Device` | one Google enrolled mobile/endpoint device | `DeviceID`, `DeviceKind`, `OwnerEmail`, `Model`, `Manufacturer`, `SerialNumber`, `OSType`, `OSVersion`, `LastSync`, `Status`, `MACAddresses` |
| `UserRecord` | one enriched Google user | `Identity`, `Auth`, `LoginActivity`, `OAuthGrants`, `ConnectedApps`, `Devices` |

### JumpCloud

Package: `internal/jumpcloud`

Top-level output:

```go
type Output struct {
    Systems  []System        `json:"systems"`
    Users    map[string]User `json:"users"` // keyed by email
    SaaSApps []SaaSApp       `json:"saas_apps"`
    Queries  []string        `json:"queries"`
}
```

Primary raw structures:

| Type | Unit | Key fields / payload |
|---|---|---|
| `PolicyStatus` | one applied policy status on a system | `Name`, `Status` |
| `App` | one installed app/package/browser extension | `Name`, `Version`, `LastOpened` |
| `SSHKey` | one JumpCloud user SSH key | `Name`, `PublicKeyPrefix`, `CreatedAt` |
| `User` | one JumpCloud directory user | `UserID`, `Email`, `Username`, `FullName`, MFA flags, password state, account state, `JCGoEligible`, `SSHKeys` |
| `System` | one managed endpoint | identity, OS, serial, owner, activity, hardware, MDM, encryption, policies, local users, USB, software, browser extensions, network config |

`System` is intentionally rich. The canonical snapshot keeps a lean device
posture view, while `inventory.json` and the JumpCloud Sheet preserve the
broader hardware/software/policy context.

### JumpCloud SaaS

Package: `internal/jumpcloud`

SaaS is collected inside the JumpCloud collector and stored in
`jumpcloud.Output.SaaSApps`.

Primary raw structures:

| Type | Unit | Key fields / payload |
|---|---|---|
| `SaaSApp` | one discovered SaaS application | `AppID`, `Name`, `CatalogAppID`, derived `Category`, `Description`, `Domains`, `LogoURL`, status/access fields, owner, discovery sources, SSO apps, accounts, licenses, contract |
| `SaaSAccount` | one owner/user account inside an app | `AccountID`, `UserID`, `Email`, `Username`, `DeviceOwner`, `LatestUsedAt` |
| `SaaSLicense` | one license tier | `LicenseID`, `Name`, `Count`, `Assigned`, `Unassigned`, `CostPerLicense`, `IsUnlimited` |
| `SaaSContract` | app contract/cost summary | `Cost`, `Currency`, `Term`, `RenewalDate`, `Notes` |
| `SaaSSSOApp` | one associated SSO connection | `ID`, `AppName`, `DisplayLabel`, `TemplateName`, `Status` |
| `CatalogApp` | catalog metadata source | `ID`, `Name`, `Description`, `Domains`, `LogoURL` |

SaaS category is derived locally; the public JumpCloud API does not expose a
category taxonomy.

### Sophos Central

Package: `internal/sophos`

Top-level output:

```go
type Output struct {
    Endpoints []Endpoint `json:"endpoints"`
    Queries   []string   `json:"queries"`
}
```

Primary raw structures:

| Type | Unit | Key fields / payload |
|---|---|---|
| `Endpoint` | one Sophos managed device | `EndpointID`, `Hostname`, OS fields, serial, online/last seen, health, tamper protection, assigned products, owner login/name/email, policies, MAC/IP addresses, alert count, 30d detection count, fetch error |

`OwnerLogin` is raw Sophos login data and may be a bare username, UPN, or
`DOMAIN\user`. `OwnerEmail` is best-effort from the Sophos directory.

### PeopleForce

Package: `internal/peopleforce`

Top-level output:

```go
type Output struct {
    Assets  []Asset  `json:"assets"`
    Queries []string `json:"queries"`
}
```

Primary raw structures:

| Type | Unit | Key fields / payload |
|---|---|---|
| `Asset` | one physical asset | `ID`, `Name`, `Code`, `SerialNumber`, `Description`, location/category IDs, resolved `CategoryName`, assignment history, current assignment, employee context, `CreatedAt`, `UpdatedAt` |
| `AssetAssignment` | one historical asset assignment | `ID`, `UserID`, `AssetID`, `IssuedOn`, `ReturnedOn`, `CreatedAt`, `UpdatedAt` |
| `Employee` | one PeopleForce employee used for assignment resolution | `ID`, `FullName`, `Email`, `EmployeeNumber`, `Active`, `Department`, `Position`, `Location` |
| `AssetCategory` | one asset category | `ID`, `Name` |

The collector resolves the current active assignment into convenience fields on
`Asset`: assignee id/email/name, department, position, location, issued date,
and `IsAssigned`.

## 4. Unified Inventory: Rich Sheets Source

Package: `internal/inventory`

Persisted artifact:

| File | Shape | Purpose |
|---|---|---|
| `local/current/inventory.json` | `inventory.AssetInventory` | current rich source of truth for Sheets |
| `local/daily/<run_date>/inventory.json` | `inventory.AssetInventory` | dated mirror for republishing |

Top-level structure:

```go
type AssetInventory struct {
    Users           map[string]*UnifiedUserRecord `json:"users"`
    JCSystems       []jumpcloud.System            `json:"jc_systems,omitempty"`
    JCUsers         map[string]jumpcloud.User     `json:"jc_users,omitempty"`
    SaaSApps        []jumpcloud.SaaSApp           `json:"saas_apps,omitempty"`
    SophosEndpoints []sophos.Endpoint             `json:"sophos_endpoints,omitempty"`
    PFAssets        []peopleforce.Asset           `json:"pf_assets,omitempty"`
    UnownedDevices  []DevicePair                  `json:"unowned_devices,omitempty"`
    MatchStats      map[string]int                `json:"match_stats,omitempty"`
    CollectedAt     time.Time                     `json:"collected_at"`
}
```

Supporting structures:

| Type | Unit | Fields |
|---|---|---|
| `UnifiedUserRecord` | one email-keyed person | `Email`, optional `Google`, `JumpCloud`, `Sophos`, `PeopleForce`, joined `Devices` |
| `JCSlice` | JumpCloud data attached to a person | `Systems`, optional directory `User` |
| `SophosSlice` | Sophos data attached to a person | `Endpoints` |
| `PFSlice` | PeopleForce data attached to a person | `Assets` |
| `DevicePair` | one physical device join | optional `JC`, optional `Sophos`, `MatchKey` |

`DevicePair.MatchKey` values:

| Value | Meaning |
|---|---|
| `serial` | JumpCloud system and Sophos endpoint matched by serial |
| `hostname` | matched by normalized hostname |
| `mac` | matched by normalized MAC address |
| `jc-only` | JumpCloud system has no Sophos match |
| `sophos-only` | Sophos endpoint has no JumpCloud match |

`MatchStats` keys:

| Key | Meaning |
|---|---|
| `paired` | device pairs with both JC and Sophos records |
| `jc_only` | JumpCloud-only device pairs |
| `sophos_only` | Sophos-only device pairs |
| `unowned` | device pairs with no resolvable user owner |
| `owner_mismatch` | JC owner and Sophos owner disagree |
| `bare_username_matched` | Sophos bare username successfully mapped to primary-domain email |
| `bare_username_unmatched` | Sophos bare username could not be mapped |

Cross-source correlation is done exactly once in `AssetInventory.Finalize()`:

1. Infer primary email domain from known users.
2. Attach Sophos bare usernames as `<login>@<primary-domain>` when possible.
3. Pair JumpCloud systems and Sophos endpoints by serial, then hostname, then MAC.
4. Attribute a pair to a user; JumpCloud `OwnerEmail` wins on conflict.
5. Backfill paired Sophos endpoints into identity-level Sophos coverage.

## 5. Canonical Snapshot: Lean Drift/Digest Source

Package: `internal/model`

Persisted artifact:

| File | Shape | Purpose |
|---|---|---|
| `local/current/snapshot.json` | `model.Snapshot` | current canonical entity snapshot |
| `local/daily/<run_date>/snapshot.json` | `model.Snapshot` | dated canonical mirror |

Top-level structure:

```go
type Snapshot struct {
    SchemaVersion   string
    RunDate         string
    RunTimestamp    time.Time
    JumpCloud       JumpCloudShard
    Sophos          SophosShard
    GoogleWorkspace GWSShard
    PeopleForce     PeopleForceShard
    Provenance      Provenance
}
```

Common metadata:

```go
type Meta struct {
    CollectedAt time.Time `json:"collected_at"`
    SourceAPI   string    `json:"source_api"`
    RunDate     string    `json:"run_date"`
}
```

Provenance:

| Field | Meaning |
|---|---|
| `jumpcloud` | deduplicated JumpCloud API query templates issued this run |
| `sophos` | deduplicated Sophos API query templates issued this run |
| `google_workspace` | deduplicated Google API query templates issued this run |
| `peopleforce` | deduplicated PeopleForce API query templates issued this run |

### Snapshot Shards

| Shard | JSON path | Structures | Classified? | Notes |
|---|---|---|---:|---|
| JumpCloud devices | `jumpcloud.devices` | `[]JCDevice` | yes | device posture, owner, OS, hardware identifiers |
| JumpCloud identity | `jumpcloud.identity` | `[]JCUser` | yes | directory user security posture |
| JumpCloud policy enforcement | `jumpcloud.policy_enforcement` | `[]JCPolicyEnforcement` | no | stored rollup; currently not in classify set |
| JumpCloud SaaS | `jumpcloud.saas` | `[]SaaSApp` | no | store/dashboard/drill-down only |
| Sophos endpoints | `sophos.endpoints` | `[]SophosEndpoint` | yes | endpoint health/tamper posture |
| Sophos account health | `sophos.account_health` | `*SophosAccountHealth` | no | derived tenant-level rollup |
| Google Workspace identity | `google_workspace.identity` | `[]GWSUser` | yes | user MFA/admin/token posture |
| Google Workspace devices | `google_workspace.devices` | `[]GWSDevice` | no | drill-down only |
| PeopleForce assets | `peopleforce.assets` | `[]PFAsset` | no | store/dashboard/drill-down only |

### Drift Tags

Canonical fields use `drift` tags:

| Tag | Meaning |
|---|---|
| `drift:"identity"` | stable matching/key/context field |
| `drift:"monitored,sev=crit|high|med|low"` | compared against baseline |
| `drift:"volatile"` | stored but not compared |

All monitored fields must be pointers. `nil` means not collected and produces a
`DATA_GAP`. A non-nil false/zero value means the value was collected and can
produce `BASELINE_DRIFT`.

## 6. Canonical Entities

### `JCDevice`

JSON path: `jumpcloud.devices`

Identity fields:

| Field | JSON |
|---|---|
| `SystemID` | `system_id` |
| `Hostname` | `hostname` |
| `Serial` | `serial` |
| `OSFamily` | `os_family` |
| `OwnerEmail` | `owner_email` |

Monitored fields:

| Field | JSON | Severity |
|---|---|---|
| `DiskEncrypted *bool` | `disk_encrypted` | CRIT |
| `MDMEnrolled *bool` | `mdm_enrolled` | MED |

Volatile/context fields include display name, OS type/version/codename,
manufacturer, hardware model, encryption type, MDM vendor, active state, last
contact, remote IP, agent version, MAC addresses, and unexpected local users.

### `JCUser`

JSON path: `jumpcloud.identity`

Identity fields:

| Field | JSON |
|---|---|
| `UserID` | `user_id` |
| `Email` | `email` |
| `Username` | `username` |

Monitored fields:

| Field | JSON | Severity |
|---|---|---|
| `MFAEnabled *bool` | `mfa_enabled` | CRIT |
| `PasswordNeverExpires *bool` | `password_never_expires` | MED |
| `JumpCloudGoEnabled *bool` | `jumpcloud_go_enabled` | LOW |

Volatile/context fields include full name, TOTP/MFA-required flags, password
expiration, password expired, suspended, locked, and activated state.

### `JCPolicyEnforcement`

JSON path: `jumpcloud.policy_enforcement`

Fields:

| Field | JSON | Tag |
|---|---|---|
| `PolicyID` | `policy_id` | identity |
| `AppliedCount *int` | `applied_count` | monitored HIGH, but not classified today |
| `FailedCount` | `failed_count` | volatile |
| `PendingCount` | `pending_count` | volatile |

Important current-state note: although `AppliedCount` has a monitored tag, this
entity is not included in `classify.entities()`, so it does not currently create
drift findings.

### `SaaSApp`

JSON path: `jumpcloud.saas`

Fields:

| Group | Fields |
|---|---|
| Identity | `AppID`, `Name` |
| Catalog/service | `CatalogAppID`, `Category`, `Description`, `Domains`, `LogoURL` |
| Governance | `Status`, `AccessRestriction`, `OwnerUserID`, `OwnerEmail`, `DiscoveredAt`, `DiscoverySources` |
| SSO | `SSOConnected`, `SSOApps` |
| Nested data | `Accounts`, `Licenses`, `Contract` |
| Rollups | `AccountCount`, `LicenseTotal`, `LicenseAssigned`, `LicenseUnassigned`, `LatestUsedAt` |

Nested canonical structures:

| Type | Fields |
|---|---|
| `SaaSAccount` | `AccountID`, `UserID`, `Email`, `Username`, `DeviceOwner`, `LatestUsedAt` |
| `SaaSLicense` | `LicenseID`, `Name`, `Count`, `Assigned`, `Unassigned`, `CostPerLicense`, `IsUnlimited` |
| `SaaSContract` | `Cost`, `Currency`, `Term`, `RenewalDate`, `Notes` |
| `SaaSSSOApp` | `ID`, `AppName`, `DisplayLabel`, `TemplateName`, `Status` |

Current-state note: SaaS is not classified and has no monitored fields in
practice. Status values such as `NEWLY_DISCOVERED` or `UNAPPROVED` are surfaced
in Sheets, not as drift findings.

### `SophosEndpoint`

JSON path: `sophos.endpoints`

Identity fields:

| Field | JSON |
|---|---|
| `EndpointID` | `endpoint_id` |
| `Hostname` | `hostname` |
| `Serial` | `serial` |
| `OSPlatform` | `os_platform` |
| `OwnerEmail` | `owner_email` |

Monitored fields:

| Field | JSON | Severity |
|---|---|---|
| `TamperProtection *bool` | `tamper_protection` | CRIT |

Volatile/context fields include owner login/name, OS name/version, health,
assigned products, policies, online state, last seen time, alert count, 30-day
detection count, fetch error, MAC addresses, and IPv4 addresses.

### `SophosAccountHealth`

JSON path: `sophos.account_health`

Derived fields:

| Field | Meaning |
|---|---|
| `EndpointsTotal` | number of Sophos endpoints |
| `HealthGood` / `HealthSuspicious` / `HealthBad` / `HealthUnknown` | health distribution |
| `TamperOffCount` | endpoints with tamper protection off |
| `TotalAlerts` | sum of open alert counts |

This is a rollup, not a classified entity.

### `GWSUser`

JSON path: `google_workspace.identity`

Identity fields:

| Field | JSON |
|---|---|
| `Email` | `email` |
| `OrgUnitPath` | `org_unit_path` |

Monitored fields:

| Field | JSON | Severity |
|---|---|---|
| `MFAEnabled *bool` | `mfa_enabled` | CRIT |
| `MFAEnforced *bool` | `mfa_enforced` | HIGH |
| `IsAdmin *bool` | `is_admin` | HIGH |
| `ASPCount *int` | `asp_count` | HIGH |
| `BackupCodeCount *int` | `backup_code_count` | MED |
| `ThirdPartyTokens *int` | `third_party_tokens` | MED |

Current-state note: `ASPCount` and `BackupCodeCount` are intentionally left nil
by the converter because those endpoints are not fetched in this version. If a
baseline expects those fields, the engine reports `DATA_GAP`.

Volatile/context fields include full name, suspended/archive state, recovery
email, created time, last login time/IP, successful/failed/suspicious login
counts.

### `GWSDevice`

JSON path: `google_workspace.devices`

Fields include `DeviceID`, `DeviceKind`, `OwnerEmail`, `Serial`, `Model`,
`Manufacturer`, `OSType`, `OSVersion`, `Status`, `LastSync`, and
`MACAddresses`. This is a drill-down shard and is not classified.

### `PFAsset`

JSON path: `peopleforce.assets`

Identity fields:

| Field | JSON |
|---|---|
| `AssetID` | `asset_id` |
| `Name` | `name` |
| `Code` | `code` |
| `SerialNumber` | `serial_number` |

Volatile/context fields:

| Group | Fields |
|---|---|
| Asset | `Category`, `Description`, `CreatedAt`, `UpdatedAt` |
| Assignment | `AssignedToEmail`, `AssignedToName`, `AssignedToID`, `IssuedOn`, `IsAssigned` |
| Employee context | `Department`, `Position`, `Location` |

Current-state note: PeopleForce assets are stored for tab/dashboard/drill-down
use only. They are not classified and carry no monitored fields.

## 7. Assembly Rules

Package: `internal/assemble`

`assemble.Sources` is the typed bundle passed into `assemble.Build()`:

```go
type Sources struct {
    GWS       map[string]*gworkspace.UserRecord
    JCSystems []jumpcloud.System
    JCUsers   map[string]jumpcloud.User
    JCSaaS    []jumpcloud.SaaSApp
    Endpoints []sophos.Endpoint
    PFAssets  []peopleforce.Asset

    GWSQueries    []string
    JCQueries     []string
    SophosQueries []string
    PFQueries     []string
}
```

Converters used by assembly:

| Raw type | Converter | Canonical type | Source API |
|---|---|---|---|
| `jumpcloud.System` | `jumpcloud.ToDevice` | `model.JCDevice` | `jumpcloud.systems` |
| `jumpcloud.User` | `jumpcloud.ToUser` | `model.JCUser` | `jumpcloud.users` |
| `[]jumpcloud.System` | `jumpcloud.ToPolicyEnforcement` | `[]model.JCPolicyEnforcement` | `jumpcloud.systems.policies` |
| `jumpcloud.SaaSApp` | `jumpcloud.ToSaaSApp` | `model.SaaSApp` | `jumpcloud.saas` |
| `sophos.Endpoint` | `sophos.ToEndpoint` | `model.SophosEndpoint` | `sophos.endpoints` |
| `[]sophos.Endpoint` | `sophos.ToAccountHealth` | `*model.SophosAccountHealth` | `sophos.endpoints.rollup` |
| `gworkspace.UserRecord` | `gworkspace.ToUser` | `model.GWSUser` | `gworkspace.directory` |
| `gworkspace.Device` | `gworkspace.ToDevice` | `model.GWSDevice` | `gworkspace.mobiledevices` |
| `peopleforce.Asset` | `peopleforce.ToAsset` | `model.PFAsset` | `peopleforce.assets` |

Sort order for deterministic output:

| Slice | Sort key |
|---|---|
| `jumpcloud.devices` | `SystemID` |
| `jumpcloud.identity` | `Email` |
| `jumpcloud.policy_enforcement` | `PolicyID` |
| `jumpcloud.saas` | `Category`, then `Name`, then `AppID` |
| `sophos.endpoints` | `EndpointID` |
| `google_workspace.identity` | `Email` |
| `google_workspace.devices` | `DeviceID` |
| `peopleforce.assets` | `AssetID` |

## 8. Classification and Drift Data

Only these canonical entities are classified today:

| Entity | Evidence ref pattern |
|---|---|
| `model.JCDevice` | `local/current/snapshot.json#jumpcloud.devices[system_id=<id>]` |
| `model.JCUser` | `local/current/snapshot.json#jumpcloud.identity[email=<email>]` |
| `model.SophosEndpoint` | `local/current/snapshot.json#sophos.endpoints[endpoint_id=<id>]` |
| `model.GWSUser` | `local/current/snapshot.json#google_workspace.identity[email=<email>]` |

Excluded from classification today:

| Entity / shard | Reason |
|---|---|
| `JCPolicyEnforcement` | dashboard/drill-down rollup only in current classifier |
| `SaaSApp` | store-only; no drift findings for SaaS status |
| `SophosAccountHealth` | tenant rollup |
| `GWSDevice` | drill-down only |
| `PFAsset` | store-only; no monitored fields |

Baseline files:

| File | Shape | Purpose |
|---|---|---|
| `local/baseline/classes.json` | `{ "classes": [] }` | class taxonomy and expected monitored fields |
| `local/baseline/baseline.meta.json` | `baseline.MetaFile` | approved version, approver, timestamp, census |

Baseline structures:

| Type | Fields |
|---|---|
| `baseline.Class` | `ID`, `Priority`, `Match`, `Expected` |
| `baseline.Expectation` | `Value`, optional severity override |
| `baseline.Census` | `Devices`, `Users` |
| `baseline.MetaFile` | `Version`, `ApprovedBy`, `ApprovedAt`, `Census` |

Finding model:

```go
type Finding struct {
    Kind       FindingKind
    Severity   Severity
    Service    []Service
    Entity     Entity
    Field      string
    Was        string
    Now        string
    ClassID    string
    Summary    string
    DetectedAt time.Time
    FirstSeen  time.Time
    EvidenceRef string
}
```

Closed finding kinds:

| Kind | Meaning |
|---|---|
| `BASELINE_DRIFT` | collected monitored value differs from expected baseline |
| `DATA_GAP` | monitored field was nil / not collected |
| `NEW_ENTITY` | entity is present now but absent from approved census |
| `ENTITY_DISAPPEARED` | entity is in approved census but absent now |
| `UNCLASSIFIED` | no baseline class matched |
| `CLASS_CONFLICT` | multiple classes matched; classifier resolved deterministically |

## 9. Digest

Package: `internal/digest`

Persisted artifacts:

| File | Shape | Purpose |
|---|---|---|
| `local/current/digest.json` | `digest.Digest` | compact LLM/analyst-facing finding rollup |
| `local/archive/<run_date>_digest.json` | `digest.Digest` | retained digest archive |

Digest structure:

| Field | Meaning |
|---|---|
| `schema_version` | canonical schema version |
| `run_date` | logical run date |
| `run_timestamp_utc` | exact run timestamp |
| `baseline_version` | loaded baseline version |
| `counts` | totals and finding rollups |
| `drift_findings` | severity-ordered findings, truncated if needed |
| `shard_pointers` | pointers to full snapshot/classification shards |
| `truncated` | true when the byte budget dropped low-severity findings |

Current shard pointers:

| Pointer key | Target |
|---|---|
| `jumpcloud_devices` | `local/current/snapshot.json#jumpcloud.devices` |
| `jumpcloud_identity` | `local/current/snapshot.json#jumpcloud.identity` |
| `jumpcloud_policy_enforcement` | `local/current/snapshot.json#jumpcloud.policy_enforcement` |
| `sophos_endpoints` | `local/current/snapshot.json#sophos.endpoints` |
| `sophos_account_health` | `local/current/snapshot.json#sophos.account_health` |
| `gws_identity` | `local/current/snapshot.json#google_workspace.identity` |
| `gws_devices` | `local/current/snapshot.json#google_workspace.devices` |
| `classification` | `local/current/classification.json` |

Current-state note: the digest shard pointers do not yet include
`jumpcloud.saas` or `peopleforce.assets`.

## 10. Standalone SaaS Export

Package: `internal/jumpcloud`

Persisted artifacts:

| File | Shape | Purpose |
|---|---|---|
| `local/current/saas.json` | `jumpcloud.SaaSExport` | full nested SaaS source for the SaaS tab |
| `local/daily/<run_date>/saas.json` | `jumpcloud.SaaSExport` | dated SaaS mirror |

Structure:

```go
type SaaSExport struct {
    SchemaVersion string    `json:"schema_version"`
    RunDate       string    `json:"run_date"`
    RunTimestamp  time.Time `json:"run_timestamp"`
    Count         int       `json:"count"`
    Applications  []SaaSApp `json:"applications"`
}
```

The export is written only when SaaS apps were collected, so a partial or
unlicensed run does not clobber a previously populated file with an empty one.

## 11. Sheets Outputs

Sheets render from `inventory.AssetInventory` plus drift findings. The `sheets`
command republishes from persisted `inventory.json` and `findings.json`; it does
not recollect external APIs.

| Tab key | Default tab | Data source | Row unit | Writer |
|---|---|---|---|---|
| `gw` | `GoogleWorkspace` | `AssetInventory.Users` | one user | `WriteGWS` |
| `jc` | `JumpCloud` | `AssetInventory.JCSystems` + `JCUsers` | one system with owner | `WriteJC` |
| `saas` | `SaaS` | `AssetInventory.SaaSApps` | one SaaS app | `WriteSaaS` |
| `sophos` | `Sophos` | `AssetInventory.SophosEndpoints` | one endpoint | `WriteSophos` |
| `pf` | `PeopleForce` | `AssetInventory.PFAssets` | one asset | `WritePeopleForce` |
| `usersall` | `UsersAll` | `AssetInventory.Users` + joined devices | one person | `WriteMerged` |
| `findings` | `Findings` | `[]model.Finding` | one finding | `WriteFindings` |

Column groups by tab:

| Tab | Groups / columns |
|---|---|
| GoogleWorkspace | Sources; Identity: Email, Full Name, Org Unit, Admin, Suspended, Created, Recovery Email; Auth: 2SV Enrolled, 2SV Enforced, Last Login, Force PW Change; Activity (7d): Last Login IP, Known IPs, Logins OK, Logins Failed, Suspicious; Apps: Connected Apps; Devices: Devices |
| JumpCloud | User: Owner Email, Full Name, 2FA Status, TOTP Enrolled, Password, Acct Locked, SSH Keys; Endpoint: Hostname, Display Name, OS, OS Version, Active, Last Contact, Agent Version; Hardware: Device, Specs, Serial; Network: Remote IP; MDM; Encryption; Policies; Local Users; USB; Software; Network Config |
| SaaS | Service: Name, Category, Status, Domains, Discovery; Access: Restriction, SSO, Owner; Accounts: Count, Owner Accounts, Last Used; Licenses: Seats, Cost/yr, Renewal, Term |
| Sophos | Endpoint: Hostname, OS Platform, OS Version, Owner Email, Owner Login, Last Seen; Status: Online, Health, Threats, Services, Tamper, Alerts, Detections, Assigned Products; Policies |
| PeopleForce | Asset: Category, Name, Code, Serial, Description; Assignment: Status, Assigned To, Name, Issued On; Employee: Department, Position, Location; Meta: Asset ID, Created |
| UsersAll | Identity; Coverage: GWS, JC, SP, PF; GWS; Devices: Count, Match, OS, Enc, MDM, Last Seen, Detail; PF Assets; Alerts |
| Findings | Finding; Entity; Detail; Tracking |

## 12. Local Storage Tiers

| Tier | Path | Contents | Retention |
|---|---|---|---|
| baseline | `local/baseline/` | `classes.json`, `baseline.meta.json` | permanent / tracked |
| current | `local/current/` | `snapshot.json`, `inventory.json`, `saas.json`, `classification.json`, `digest.json`, `findings.json` | overwritten each run |
| daily | `local/daily/<YYYY-MM-DD>/` | `snapshot.json`, `inventory.json`, `saas.json` | 30 days |
| archive | `local/archive/` | `<run_date>_digest.json` | 180 days |

All store writes are atomic temp-file plus rename.

## 13. LLM-Oriented Observations

These are observations about the current state, not implemented changes.

1. The current architecture already has natural shards.
   `model.Snapshot` is already split into service/entity shards, and
   `digest.json` already points an analyst to several of them.

2. The cheapest default LLM entry point is `digest.json`.
   It is findings-first, size-limited, and carries pointers to deeper data.

3. The richest human/reporting entry point is `inventory.json`.
   It preserves raw collector details and the cross-source user/device join, but
   it is likely too large and too nested to feed directly to a local LLM on every
   prompt.

4. `snapshot.json` is the best canonical source for deterministic agent shards.
   It is sorted, leaner than `inventory.json`, and has stable JSON paths such as
   `jumpcloud.devices` and `sophos.endpoints`.

5. SaaS and PeopleForce currently need explicit LLM pointers.
   `saas.json` exists as a standalone artifact, but `digest.ShardPointers` does
   not include SaaS or PeopleForce. That means an LLM reading only the digest may
   not naturally discover those datasets.

6. A future LLM data layout can be created without changing collectors first.
   A reasonable next layer would read the existing artifacts and write derived
   shards such as:

| Proposed shard file | Source | Unit | Why it is useful |
|---|---|---|---|
| `llm/digest.json` | `current/digest.json` | findings summary | first context for agents |
| `llm/users.json` | `inventory.Users` or `snapshot.*.identity` | one user | identity/posture questions |
| `llm/devices.json` | `inventory.Users[].Devices` + `snapshot` devices | one physical device | cross-source device reasoning |
| `llm/jumpcloud-devices.json` | `snapshot.jumpcloud.devices` | one JC device | encryption/MDM drift drill-down |
| `llm/jumpcloud-users.json` | `snapshot.jumpcloud.identity` | one JC user | MFA/password posture |
| `llm/sophos-endpoints.json` | `snapshot.sophos.endpoints` | one endpoint | tamper/health/alerts |
| `llm/gws-users.json` | `snapshot.google_workspace.identity` | one user | MFA/admin/OAuth posture |
| `llm/gws-devices.json` | `snapshot.google_workspace.devices` | one GWS device | enrolled-device drill-down |
| `llm/saas-apps.json` | `current/saas.json` or `snapshot.jumpcloud.saas` | one app | SaaS status, accounts, licenses |
| `llm/peopleforce-assets.json` | `snapshot.peopleforce.assets` | one asset | asset ownership and assignment |
| `llm/findings.json` | `current/findings.json` | one finding | full untruncated finding list |

7. For cost and context size, prefer entity shards over column-per-file shards.
   A file per column would reduce token cost for one-column questions, but it
   would make multi-field reasoning expensive and fragile. The current data
   model is mostly entity-oriented; keeping one JSON object per user/device/app
   lets an agent retrieve compact records with enough local context.

8. If a column-level index is needed, generate it as a secondary index.
   Example: `indexes/by_owner_email.json`, `indexes/by_serial.json`,
   `indexes/by_service_status.json`, or `indexes/fields/<field>.json`. That
   preserves good entity records while still allowing cheap lookups.

## 14. Known Current Gaps / Design Decisions

| Area | Current state | Impact |
|---|---|---|
| `JCPolicyEnforcement` | has a monitored tag but is not classified | no findings today from policy rollups |
| SaaS | status and access restriction are visible in Sheets but not drift findings | shadow-IT signals are not in `digest.json` unless separately surfaced |
| PeopleForce | assets are store/dashboard-only | no drift findings for missing/incorrect assignments |
| GWS ASP / backup codes | canonical monitored fields exist but converter leaves them nil | baseline expectations produce `DATA_GAP` until collection is added |
| Digest pointers | no SaaS or PeopleForce pointers | LLM reading only digest may miss those shards |
| Inventory vs snapshot | two sibling views, not one master document | future LLM layer must choose source per question or build derived shards |

## 15. Architecture Review: Main Gaps

This section records recommended improvements based on the current manifest. It
is still analysis only, not an implemented design.

The current architecture is solid in three important ways:

1. Collectors are isolated behind service modules.
2. Canonical drift data is separated from rich Sheets data.
3. Storage writes are atomic and deterministic enough for stable snapshots.

The main gaps are not in basic collection. They are in run identity, immutable
history, artifact versioning, LLM-oriented indexing, and explicit data-quality
contracts.

| Area | Current state | Gap | Recommended direction |
|---|---|---|---|
| Run retention | `current/` is overwritten; `daily/<date>/` is overwritten per date | multiple runs on the same day are not preserved | add immutable per-run directories under `runs/<date>/<run_id>/` |
| Artifact versioning | `model.SchemaVersion` and `SaaSExportSchemaVersion` exist | no per-artifact manifest with schema, hashes, counts, build info | write a `run_manifest.json` for every run |
| LLM access | digest points to some snapshot shards | SaaS/PF missing, no compact entity indexes, no retrieval manifest | add `llm/` derived shards and `llm_manifest.json` |
| Data quality | join stats exist in `MatchStats` | no formal quality report for missing keys, duplicate IDs, weak joins | add `quality_report.json` |
| Partial runs | partial target writes current artifacts when persisted | hard for a reader to know completeness without reading logs | add explicit `collection_scope` and `completeness` metadata |
| Cross-source identity | users/devices are joined in inventory only | canonical snapshot has service shards but not a graph/index | add derived people/device/app/asset indexes |
| Security/PII | rich data is persisted as collected | LLM layer may include more sensitive fields than needed | define redacted/minimal LLM shards |

## 16. Run Identity and Immutable Local History

### Current Behavior

Current storage has these semantics:

| Path | Current semantics |
|---|---|
| `local/current/` | mutable latest run |
| `local/daily/<YYYY-MM-DD>/` | mutable latest run for that date |
| `local/archive/` | digest archives by date |

This is useful for "latest" workflows, but it does not satisfy strict
"preserve every run" history. A second run on the same day overwrites
`daily/<date>/snapshot.json`, `daily/<date>/inventory.json`, and
`daily/<date>/saas.json`.

### Recommended Target Layout

Keep `current/` for convenience, keep `daily/` as the latest successful run per
day, and add immutable `runs/` for every run:

```text
local/
  current/
    snapshot.json
    inventory.json
    findings.json
    digest.json
    saas.json
    run_manifest.json
    llm/
      llm_manifest.json
      digest.json
      users.jsonl
      devices.jsonl
      findings.jsonl
      indexes/

  daily/
    2026-06-28/
      latest.json                 # points to runs/2026-06-28/<run_id>
      snapshot.json               # optional latest mirror
      inventory.json              # optional latest mirror
      digest.json                 # optional latest mirror

  runs/
    2026-06-28/
      20260628T184501Z-a1b2c3d4/
        run_manifest.json
        raw/
          gws_raw.json
          jc_raw.json
          sophos_raw.json
          pf_raw.json
        canonical/
          snapshot.json
          classification.json
          findings.json
          digest.json
        inventory/
          inventory.json
          saas.json
        llm/
          llm_manifest.json
          digest.json
          users.jsonl
          devices.jsonl
          jumpcloud-devices.jsonl
          jumpcloud-users.jsonl
          sophos-endpoints.jsonl
          gws-users.jsonl
          gws-devices.jsonl
          saas-apps.jsonl
          peopleforce-assets.jsonl
          findings.jsonl
          indexes/
```

Recommended `run_id`:

```text
<UTC timestamp>-<short random or content hash>
example: 20260628T184501Z-a1b2c3d4
```

Rationale:

| Decision | Why |
|---|---|
| immutable `runs/<date>/<run_id>/` | preserves every execution, including repeated runs on one day |
| mutable `current/` | keeps existing operator workflow simple |
| mutable `daily/<date>/latest` | supports "show me that day's final state" without scanning all runs |
| UTC timestamp in run ID | stable ordering and timezone-safe comparisons |
| random/hash suffix | avoids collisions and helps identify exact artifacts |

## 17. Run Manifest and Artifact Versioning

Every persisted run should have a single manifest that describes exactly what
was collected, written, skipped, and verified.

Recommended file:

```text
local/runs/<date>/<run_id>/run_manifest.json
local/current/run_manifest.json
```

Recommended fields:

| Field | Purpose |
|---|---|
| `manifest_version` | version of the manifest shape |
| `run_id` | immutable run identifier |
| `run_date` | logical date |
| `run_timestamp_utc` | exact run timestamp |
| `command` | target and flags, for example `all --tabs usersall` |
| `collection_scope` | selected collectors and skipped collectors |
| `schema_versions` | versions for snapshot, inventory, SaaS export, LLM export |
| `build` | binary version, Go version, git revision, dirty flag if available |
| `config_fingerprint` | non-secret hash of relevant config choices |
| `artifacts` | path, type, schema, row count, bytes, sha256 for each file |
| `provenance` | API query templates per service |
| `quality` | summary of quality checks and warnings |
| `completeness` | whether the run is full, partial, or publish-only |
| `previous_run_id` | previous successful run used for drift/diff context |

Example artifact entry:

```json
{
  "path": "canonical/snapshot.json",
  "artifact": "snapshot",
  "schema_version": "2.0",
  "count": {
    "jumpcloud.devices": 120,
    "sophos.endpoints": 118,
    "google_workspace.identity": 150
  },
  "bytes": 392122,
  "sha256": "..."
}
```

Rationale:

| Benefit | Explanation |
|---|---|
| reproducibility | future agents know exactly which inputs produced an output |
| safe LLM context | agents can inspect manifest metadata before loading large files |
| integrity checks | hashes catch partial/corrupt writes |
| version control | schema changes become explicit per artifact, not tribal knowledge |
| partial-run safety | downstream readers can reject incomplete data for full-inventory tasks |

## 18. Storage Optimization

The current data is JSON and human-readable, which is good for debugging. For
larger history and LLM workflows, add derived optimized formats rather than
replacing the current files immediately.

Recommended storage layers:

| Layer | Format | Purpose |
|---|---|---|
| raw normalized outputs | JSON, optionally compressed | replay/debug collector behavior |
| canonical snapshot | indented JSON | stable source of truth for drift and diffs |
| LLM entity shards | JSONL | streaming, retrieval, smaller reads |
| indexes | compact JSON maps | cheap lookup by email, serial, hostname, service ID |
| summaries | compact JSON | first-pass context for agents |
| archive compression | `.json.zst` or `.json.gz` eventually | reduce long-term disk size |

JSONL is a good fit for local LLM work because one line equals one entity. An
agent or retriever can read only matching lines instead of loading a large JSON
array into memory.

Recommended indexes:

| Index | Maps from | Maps to |
|---|---|---|
| `by_email.json` | normalized email | user record IDs, device IDs, asset IDs, SaaS account refs |
| `by_serial.json` | normalized serial | JC device IDs, Sophos endpoint IDs, PF asset IDs, GWS device IDs |
| `by_hostname.json` | normalized hostname | device IDs |
| `by_mac.json` | normalized MAC | device IDs |
| `by_service_id.json` | service-specific ID | shard file and line/entity reference |
| `by_finding_kind.json` | finding kind | finding IDs |
| `by_severity.json` | severity | finding IDs |
| `by_saas_status.json` | SaaS status | SaaS app IDs |

Rationale:

| Decision | Why |
|---|---|
| keep canonical JSON | humans and tests can inspect it easily |
| add JSONL for LLM | cheap incremental reads and retrieval |
| add indexes separately | avoid destroying entity locality |
| compress old immutable runs | save disk without complicating current/latest workflows |

## 19. Collection and Persistence Efficiency

Recommended improvements for collection:

| Improvement | Why it helps |
|---|---|
| collector-level result metadata | each collector should report counts, skipped pages, rate limits, and partial failures |
| incremental fetch where APIs support it | reduces API calls and run time |
| ETag / modified-since caching where APIs support it | avoids downloading unchanged data |
| explicit retry budget per service | prevents one noisy API from making the whole run unpredictable |
| per-service timeout budget | makes long runs diagnosable |
| API query manifest per run | already partly present; should be written in `run_manifest.json` |
| raw artifact persistence for all service binaries | enables replay without re-hitting APIs |
| quality gates before publish | avoid publishing obviously incomplete full-run data |

Recommended persistence rules:

| Rule | Rationale |
|---|---|
| write into a temp run directory first | a run is invisible until complete |
| write `run_manifest.json` last | manifest marks the run as complete |
| update `current/` only after run completion | readers never see mixed old/new artifacts |
| update `daily/<date>/latest` only after run completion | daily latest never points to a partial run |
| never overwrite immutable `runs/` | every run remains auditable |

This keeps the current atomic-file safety and adds atomic-run safety.

## 20. LLM Data Layer

The LLM should not be pointed directly at the largest rich artifact by default.
It should start from a small manifest/digest, then retrieve entity shards and
indexes on demand.

Recommended access order:

1. Read `llm/llm_manifest.json`.
2. Read `llm/digest.json`.
3. Use indexes to find relevant entity IDs.
4. Load only matching JSONL lines/entities.
5. Fall back to `inventory.json` only when rich human-facing context is needed.

Recommended `llm_manifest.json` fields:

| Field | Purpose |
|---|---|
| `llm_schema_version` | version of the LLM export shape |
| `run_id` | links LLM shards to exact run |
| `source_artifacts` | hashes and paths of source files used to generate shards |
| `shards` | path, entity type, count, bytes, primary key, sort key |
| `indexes` | path, key type, target shards |
| `redaction_profile` | what was removed or masked |
| `recommended_entrypoints` | small files to read first |
| `token_estimates` | approximate token size per shard |

Recommended LLM shards:

| Shard | Unit | Source | Notes |
|---|---|---|---|
| `digest.json` | run summary | digest | smallest first context |
| `findings.jsonl` | one finding | findings | full untruncated list |
| `users.jsonl` | one person | inventory + canonical identity | merged identity/posture summary |
| `devices.jsonl` | one physical device | inventory device pairs + canonical devices | best shard for cross-source device questions |
| `jumpcloud-devices.jsonl` | one JC device | snapshot | service-specific posture |
| `jumpcloud-users.jsonl` | one JC user | snapshot | service-specific user posture |
| `sophos-endpoints.jsonl` | one Sophos endpoint | snapshot | tamper/health/alerts |
| `gws-users.jsonl` | one GWS user | snapshot | MFA/admin/token posture |
| `gws-devices.jsonl` | one GWS device | snapshot | enrolled devices |
| `saas-apps.jsonl` | one app | SaaS export or snapshot SaaS | status, accounts, license economics |
| `peopleforce-assets.jsonl` | one asset | snapshot PF assets | physical assignment context |
| `quality.json` | run quality summary | quality checks | tells LLM what not to overtrust |

Recommended entity IDs:

| Entity | Stable ID format |
|---|---|
| Google user | `gws:user:<email>` |
| Google device | `gws:device:<device_id>` |
| JumpCloud user | `jc:user:<email>` or `jc:user:<user_id>` |
| JumpCloud device | `jc:device:<system_id>` |
| Sophos endpoint | `sophos:endpoint:<endpoint_id>` |
| PeopleForce asset | `pf:asset:<asset_id>` |
| SaaS app | `jc:saas:<app_id>` |
| Merged person | `person:<email>` |
| Merged physical device | `device:<best_serial_or_jc_or_sophos_id>` |

Rationale:

| Decision | Why |
|---|---|
| entity JSONL over column files | preserves local context needed for reasoning |
| indexes for cheap lookup | avoids loading full shards for simple questions |
| merged `users` and `devices` shards | gives LLM the cross-source view it actually needs |
| service-specific shards | keeps deep drill-down precise and small |
| redaction profile | lets local LLM use minimum necessary data |

## 21. Data Quality Contracts

Recommended `quality_report.json` sections:

| Section | Checks |
|---|---|
| identity | duplicate emails, invalid emails, missing primary identity |
| devices | missing serials, duplicate serials, duplicate hostnames, missing owners |
| joins | match counts by serial/hostname/MAC, owner mismatches, unowned devices |
| service coverage | users missing JC/Sophos/PF/GWS coverage |
| monitored fields | nil monitored values by field and service |
| SaaS | unapproved/new apps, accounts without email or owner, unused licenses |
| PeopleForce | assigned assets without email, duplicate serials, unassigned assets |
| drift readiness | number of classified/unclassified entities and class conflicts |

This report should be consumed by both humans and LLM agents. It gives the agent
permission to say "this answer is uncertain because serials are missing" instead
of hallucinating a confident join.

Recommended severity for quality issues:

| Severity | Meaning |
|---|---|
| `blocker` | full-run data should not publish as trusted |
| `high` | major blind spot, for example many endpoints missing owners |
| `medium` | useful warning, for example duplicate hostnames |
| `low` | informational quality note |

## 22. Structural Improvements to Consider

### 22.1 Add a Derived Graph Layer

A local LLM will often ask graph-shaped questions:

| Question | Needs |
|---|---|
| "What belongs to this person?" | GWS user, JC user, devices, PF assets, SaaS accounts |
| "Is this laptop healthy?" | JC device, Sophos endpoint, PF asset, GWS device, findings |
| "Which apps are risky?" | SaaS app status, SSO state, owner accounts, recent usage |

Instead of forcing the LLM to join raw arrays every time, generate a derived
graph/index:

```text
llm/graph/
  nodes.jsonl
  edges.jsonl
```

Recommended node types:

| Node type | Examples |
|---|---|
| `person` | email-keyed merged user |
| `device` | physical device across JC/Sophos/GWS/PF |
| `service_record` | raw service-specific entity |
| `asset` | PeopleForce asset |
| `saas_app` | SaaS application |
| `finding` | drift finding |

Recommended edge types:

| Edge | Meaning |
|---|---|
| `owns` | person owns/uses device |
| `assigned` | PeopleForce asset assigned to person |
| `matches` | JC device matches Sophos endpoint |
| `has_account` | person/account relationship to SaaS app |
| `has_finding` | entity has drift finding |
| `observed_by` | physical device observed by a service |

### 22.2 Make Store-Only Entities Explicit

SaaS, PeopleForce, GWS devices, and Sophos account health are intentionally
store-only today. That should be encoded in metadata, not only comments.

Recommended metadata per shard:

| Field | Example |
|---|---|
| `classification_mode` | `classified`, `store_only`, `rollup_only` |
| `finding_policy` | `drift`, `dashboard_only`, `future_decision_required` |
| `primary_key` | `app_id`, `asset_id`, `endpoint_id` |
| `owner_key` | `owner_email`, `assigned_to_email` |

### 22.3 Decide Whether SaaS and PeopleForce Should Become Findings

Current decision: SaaS and PeopleForce do not generate drift findings.

Possible future options:

| Option | Pros | Cons |
|---|---|---|
| keep dashboard-only | simple, no digest schema change | LLM may miss important risk unless it reads SaaS/PF shards |
| map to existing finding kinds | keeps closed `FindingKind` set | may overload drift semantics |
| add new finding kinds | clearer semantics for SaaS/PF | breaks digest contract and downstream assumptions |
| add a separate `signals.json` | keeps drift clean and surfaces non-drift risk | adds another artifact and UI concept |

Recommended direction: add a separate `signals.json` for non-baseline risk such
as unapproved SaaS, unused licenses, unassigned assets, stale devices, or weak
joins. Keep drift findings for baseline posture and census changes.

### 22.4 Add Diff Artifacts

For every full run, generate diffs against the previous full run:

| File | Purpose |
|---|---|
| `diff/entities_added.jsonl` | new users/devices/assets/apps |
| `diff/entities_removed.jsonl` | disappeared users/devices/assets/apps |
| `diff/field_changes.jsonl` | changed monitored and important context fields |
| `diff/summary.json` | compact change summary |

This helps both humans and LLMs answer "what changed since yesterday?" without
loading two full snapshots.

### 22.5 Normalize Keys Once

The join logic already normalizes hostnames and MAC addresses. A future data
layer should persist normalized keys alongside raw values:

| Raw field | Normalized field |
|---|---|
| email | `email_norm` lowercased and trimmed |
| serial | `serial_norm` uppercased and trimmed |
| hostname | `hostname_norm` lowercased first label |
| MAC | `mac_norm` alphanumeric lowercase |
| SaaS domain | `domain_norm` lowercased |

Rationale: agents and indexes should not reimplement normalization differently.

## 23. Recommended Implementation Order

When implementation begins, the lowest-risk order is:

1. Add `run_id` and `run_manifest.json`.
2. Add immutable `local/runs/<date>/<run_id>/` while keeping `current/` and
   `daily/` behavior compatible.
3. Add artifact hashes/counts and completeness metadata.
4. Add `quality_report.json`.
5. Add `llm/llm_manifest.json` and JSONL shards derived from existing artifacts.
6. Add lookup indexes by email, serial, hostname, service ID, severity, and SaaS
   status.
7. Add digest pointers for SaaS, PeopleForce, and quality/LLM manifests.
8. Add diff artifacts between successful full runs.
9. Decide whether SaaS/PF risks become drift findings, separate signals, or
   dashboard-only data.

This order avoids changing collectors first. It improves version control,
history, and LLM usability by adding a derived data layer on top of the current
working pipeline.

## 24. Programmatic Control Evaluation

The local LLM should not spend tokens deciding facts that code can decide
deterministically. Checks such as MFA, disk encryption, MDM, Sophos tamper
protection, JumpCloud Go, policy status, SaaS approval, and asset assignment
should be evaluated before the LLM sees the data.

Recommended principle:

```text
raw API data
  -> canonical facts
  -> programmatic control results
  -> drift findings / non-drift signals
  -> compact LLM shards
  -> LLM analysis
```

The LLM should receive:

| Data | Purpose |
|---|---|
| normalized facts | exact source values, already cleaned |
| control results | PASS / FAIL / UNKNOWN / NOT_APPLICABLE decisions |
| evidence refs | where the decision came from |
| summaries | compact entity posture for first-pass reading |
| raw drill-down pointers | only when deeper inspection is needed |

This makes the LLM an analyst and explainer, not a parser for booleans and
policy lists.

### Why This Matters

| Problem if left to LLM | Programmatic solution |
|---|---|
| repeated token cost for simple checks | compute once per run |
| inconsistent interpretations | single rule definition per control |
| hidden uncertainty | explicit `UNKNOWN` / `DATA_GAP` |
| weak evidence | attach deterministic evidence refs |
| hard regression testing | unit-test control evaluators |
| harder auditing | persist control results with run ID and schema version |

## 25. Proposed Control Model

Add a deterministic control layer as a derived artifact. It can be generated
from the current `model.Snapshot` and `inventory.AssetInventory` without
changing collectors first.

Recommended artifacts:

| File | Shape | Purpose |
|---|---|---|
| `local/current/controls.json` | `ControlRun` | full control results for current run |
| `local/runs/<date>/<run_id>/canonical/controls.json` | `ControlRun` | immutable per-run control results |
| `local/current/llm/controls.jsonl` | one `ControlResult` per line | LLM-readable control facts |
| `local/current/llm/posture-summary.jsonl` | one entity summary per line | compact first-pass posture |

Recommended top-level shape:

```go
type ControlRun struct {
    SchemaVersion string          `json:"schema_version"`
    RunID         string          `json:"run_id"`
    RunDate       string          `json:"run_date"`
    EvaluatedAt   time.Time       `json:"evaluated_at"`
    Results       []ControlResult `json:"results"`
    Summaries     []PostureSummary `json:"summaries"`
}
```

Recommended control definition shape:

```go
type ControlDefinition struct {
    ControlID   string         `json:"control_id"`
    Title       string         `json:"title"`
    Domain      string         `json:"domain"`
    Service     model.Service  `json:"service"`
    EntityType  string         `json:"entity_type"`
    Severity    model.Severity `json:"severity"`
    Inputs      []string       `json:"inputs"`
    Description string         `json:"description,omitempty"`
}
```

Recommended control result shape:

```go
type ControlResult struct {
    ControlID    string        `json:"control_id"`
    Title        string        `json:"title"`
    Domain       string        `json:"domain"`
    Service      model.Service `json:"service"`
    Entity       model.Entity  `json:"entity"`
    Status       string        `json:"status"` // PASS | FAIL | UNKNOWN | NOT_APPLICABLE
    Severity     model.Severity `json:"severity"`
    Expected     string        `json:"expected,omitempty"`
    Actual       string        `json:"actual,omitempty"`
    Reason       string        `json:"reason"`
    EvidenceRefs []string      `json:"evidence_refs,omitempty"`
    Inputs       []FieldInput  `json:"inputs,omitempty"`
}
```

Recommended field input shape:

```go
type FieldInput struct {
    Field       string `json:"field"`
    Value       string `json:"value,omitempty"`
    Present     bool   `json:"present"`
    EvidenceRef string `json:"evidence_ref,omitempty"`
}
```

Recommended entity posture summary:

```go
type PostureSummary struct {
    Entity          model.Entity `json:"entity"`
    ControlsTotal   int          `json:"controls_total"`
    PassCount       int          `json:"pass_count"`
    FailCount       int          `json:"fail_count"`
    UnknownCount    int          `json:"unknown_count"`
    HighestSeverity model.Severity `json:"highest_severity,omitempty"`
    Labels          []string     `json:"labels,omitempty"`
    TopReasons      []string     `json:"top_reasons,omitempty"`
}
```

### Control Status Semantics

| Status | Meaning | Drift mapping |
|---|---|---|
| `PASS` | required condition is satisfied | no finding |
| `FAIL` | required condition is known and not satisfied | `BASELINE_DRIFT` when expected by class |
| `UNKNOWN` | required input was not collected or cannot be trusted | `DATA_GAP` when expected by class |
| `NOT_APPLICABLE` | control does not apply to this entity/class | no finding |

The important distinction is `FAIL` vs `UNKNOWN`. For example, disk encryption
known to be off is a security failure; disk encryption not collected is a data
quality gap.

## 26. Drift Model Extension

### Current Drift Model

The current drift engine compares baseline expectations against monitored fields
on classified canonical entities:

```text
classified entity
  -> expected monitored fields from baseline class
  -> drifttag.Value(field)
  -> BASELINE_DRIFT or DATA_GAP
```

This is good for direct fields such as:

| Entity | Direct monitored field |
|---|---|
| `GWSUser` | `mfa_enabled`, `mfa_enforced`, `is_admin`, token counts |
| `JCUser` | `mfa_enabled`, `password_never_expires`, `jumpcloud_go_enabled` |
| `JCDevice` | `disk_encrypted`, `mdm_enrolled` |
| `SophosEndpoint` | `tamper_protection` |

### Recommended Extended Drift Model

Add programmatic controls as first-class evaluated facts. The drift engine can
then compare baseline expectations against either raw monitored fields or
control results.

```text
snapshot
  -> classify entities
  -> evaluate controls for each entity
  -> resolve baseline expected controls
  -> compare expected control status
  -> findings
```

Baseline classes can evolve from field-only expectations:

```json
{
  "expected": {
    "disk_encrypted": "true",
    "mdm_enrolled": "true"
  }
}
```

to a mixed field/control model:

```json
{
  "expected_controls": {
    "jc.device.disk_encryption": "PASS",
    "jc.device.mdm_enrollment": "PASS",
    "sophos.endpoint.tamper_protection": "PASS"
  }
}
```

Recommended compatibility rule:

| Baseline key | Meaning |
|---|---|
| `expected` | existing monitored field expectations |
| `expected_controls` | new evaluated control expectations |

This preserves the current baseline format while allowing a better control
model to grow next to it.

### Finding Generation from Controls

| Control result | Expected control | Finding |
|---|---|---|
| `PASS` | `PASS` | none |
| `FAIL` | `PASS` | `BASELINE_DRIFT` |
| `UNKNOWN` | `PASS` | `DATA_GAP` |
| `NOT_APPLICABLE` | no expectation | none |
| missing control result | expected | `DATA_GAP` |

The `Finding.Field` can use the control ID, for example:

```text
jc.device.disk_encryption
gws.user.mfa_enforced
sophos.endpoint.tamper_protection
```

The `Finding.EvidenceRef` should point to the control result first, and the
control result should point to its source fields.