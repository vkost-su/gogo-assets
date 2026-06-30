# IT Admin API Endpoint Map & Go Types
## Services: Sophos · Google Workspace · Atlassian · JumpCloud · PeopleForce

> **Scope**: read-only GET endpoints useful for security analysis, inventory, event processing.  
> **Confidence tags**: `HIGH` = verbatim from official spec · `MED` = cross-referenced, path may be inferred · `LOW` = known to exist, exact params unclear.  
> **Pointer rule**: monitored fields are always `*T` — `nil` means data gap, `*false` means collected & off.

---

## 1. SOPHOS CENTRAL

### Auth flow (always first)
```
GET  https://api.central.sophos.com/whoami/v1
  → returns: id (tenantId), idType, apiHosts.dataRegion
  
All subsequent calls: base = apiHosts.dataRegion
Headers: Authorization: Bearer {token}  +  X-Tenant-ID: {tenantId}
Pagination: ?pageFromKey={nextKey}  (cursor, not offset)
```

### 1.1 endpoint/v1  HIGH
| Method | Path | Key params | Returns |
|--------|------|-----------|---------|
| GET | `/endpoint/v1/endpoints` | `healthStatus`, `type` (computer\|server\|mobile), `tamperProtectionEnabled`, `lockdownStatus`, `lastSeenBefore`, `lastSeenAfter`, `ids`, `fields`, `view` (summary\|full), `sort`, `pageFromKey`, `pageSize` | list of endpoints |
| GET | `/endpoint/v1/endpoints/{id}` | `fields`, `view` | single endpoint |
| GET | `/endpoint/v1/endpoints/{id}/tamper-protection` | — | TP status + password |
| GET | `/endpoint/v1/endpoints/{id}/isolation` | — | isolation status |
| GET | `/endpoint/v1/settings/tamper-protection` | — | global TP toggle |
| GET | `/endpoint/v1/policies` | `pageFromKey`, `pageSize`, `type` | policies list |
| GET | `/endpoint/v1/policies/{id}` | — | policy detail |
| GET | `/endpoint/v1/endpoint-groups` | `pageFromKey`, `pageSize` | endpoint groups |
| GET | `/endpoint/v1/endpoint-groups/{id}` | — | group detail |

**Key response fields** (`/endpoints` item):
```
id, hostname, type (computer|server|mobile)
health.overall (good|suspicious|bad|unknown)
health.threats.status, health.services.status
os.platform (windows|macOS|linux), os.name, os.majorVersion
ipv4Addresses[], macAddresses[]
tamperProtectionEnabled
lockdownStatus (creatingWhitelist|installing|locked|notInstalled|registering|starting|stopping|unlocked|unavailable)
associatedPerson.viaLogin, associatedPerson.id (→ use for user lookup)
assignedProducts[].code (endpointProtection|interceptX|coreAgent|…)
lastSeenAt
online
```

### 1.2 account-health-check/v1  HIGH
| Method | Path | Key params | Returns |
|--------|------|-----------|---------|
| GET | `/account-health-check/v1/health-check` | `checks` (protection,policy,exclusions,tamperProtection), `products` (endpoint) | health report |
| GET | `/account-health-check/v1/health-check/scores/historical` | `startDate`, `endDate` | daily scores over range |
| GET | `/account-health-check/v1/health-check/scores/regional` | — | peer-comparison scores by estate size |

**Key response fields** (`/health-check`):
```
overall.score (0-100)
endpoint.protection.computer.{total, notFullyProtected}
endpoint.protection.server.{total, notFullyProtected}
endpoint.policy.{total, notConfigured}
endpoint.exclusions.{total, dangerous}
endpoint.tamperProtection.computer.{total, turnedOff}
endpoint.tamperProtection.server.{total, turnedOff}
```

### 1.3 siem/v1  HIGH (24h window max)
| Method | Path | Key params | Returns |
|--------|------|-----------|---------|
| GET | `/siem/v1/alerts` | `limit` (≤1000), `cursor`, `from_date` (unix ts) | security alerts |
| GET | `/siem/v1/events` | `limit`, `cursor`, `from_date`, `exclude_types` | security events |

**Alert fields**: `id`, `created_at`, `severity` (low\|medium\|high\|critical), `threat`, `location`, `endpoint_id`, `endpoint_type`, `source_info.ip`, `when`, `customer_id`, `data`  
**Event fields**: `id`, `type`, `created_at`, `severity`, `source_info.ip`, `endpoint_id`, `name`, `group`, `user_id`, `customer_id`

### 1.4 audit-events-v1  HIGH
| Method | Path | Key params | Returns |
|--------|------|-----------|---------|
| GET | `/audit/v1/events` | `from`, `to` (ISO 8601), `pageFromKey`, `pageSize`, `type` (filter by event type) | audit log entries |
| GET | `/audit/v1/events/{id}` | — | single event |

**Event fields**: `id`, `type`, `triggeredAt`, `userId`, `userName`, `userEmail`, `source` (API\|UI), `description`, `resourceType`, `resourceId`, `siteName`

### 1.5 detections/v1  HIGH (XDR/MTR license required)
> This is an async query API: POST to start run → GET results when ready.

| Method | Path | Key params | Returns |
|--------|------|-----------|---------|
| POST | `/detections/v1/queries/detections` | body: `{from, to}` | creates query run |
| GET | `/detections/v1/queries/detections/{id}` | — | run status (pending\|succeeded\|failed) |
| GET | `/detections/v1/queries/detections/{id}/results` | `page`, `pageSize` | detection items |
| POST | `/detections/v1/queries/detection-groups` | body: `{from, to}` | creates grouped run |
| GET | `/detections/v1/queries/detection-groups/{id}/results` | `page`, `pageSize` | grouped detections |

**Detection fields**: `id`, `attackType`, `detectionRule`, `severity` (1-10), `sensorGeneratedAt`, `device.id`, `device.type`, `device.entity` (hostname), `sensor.id`

### 1.6 common/v1 (Directory)  HIGH
| Method | Path | Key params | Returns |
|--------|------|-----------|---------|
| GET | `/common/v1/directory/users` | `search` (email/name), `pageFromKey`, `pageSize`, `groupId` | user list |
| GET | `/common/v1/directory/users/{id}` | — | user detail |
| GET | `/common/v1/directory/user-groups` | `pageFromKey`, `pageSize` | groups |
| GET | `/common/v1/directory/user-groups/{id}` | — | group detail |
| GET | `/common/v1/directory/user-groups/{id}/users` | `pageFromKey`, `pageSize` | group members |

### 1.7 accounts-v1  MED (account/tenant mgmt — MSP/partner use)
| Method | Path | Key params | Returns |
|--------|------|-----------|---------|
| GET | `/accounts/v1/customers` | `pageFromKey`, `pageSize` | managed tenants list |
| GET | `/accounts/v1/customers/{id}` | — | tenant detail |

---

## 2. GOOGLE WORKSPACE

### Auth
```
OAuth 2.0 service account with domain-wide delegation.
Scopes listed per endpoint below.
Pagination: pageToken in query param, nextPageToken in response.
```
*(Full endpoint table already exists in api_endpoint_map.json — 52 endpoints. Summary of key groups below.)*

### 2.1 Directory API v1  `admin.googleapis.com`  HIGH
| Group | Key GET endpoints | Scope |
|-------|------------------|-------|
| Users | `/admin/directory/v1/users`, `/…/{userKey}`, `/…/{userKey}/aliases`, `/…/{userKey}/tokens`, `/…/{userKey}/asps`, `/…/{userKey}/verificationCodes` | `admin.directory.user.readonly` |
| Groups | `/admin/directory/v1/groups`, `/…/{groupKey}`, `/…/{groupKey}/members`, `/…/{groupKey}/hasMember/{memberKey}` | `admin.directory.group.readonly` |
| OrgUnits | `/admin/directory/v1/customer/{customerId}/orgunits`, `/…/{orgunitsId}` | `admin.directory.orgunit.readonly` |
| Chrome/Mobile devices | `/…/devices/chromeos`, `/…/devices/mobile`, `/…/devices/mobile/{resourceId}` | `admin.directory.device.chromeos.readonly` |
| Roles & Assignments | `/…/roles`, `/…/roleassignments`, `/…/privileges` | `admin.directory.rolemanagement.readonly` |
| Schemas | `/…/schemas`, `/…/schemas/{schemaKey}` | `admin.directory.userschema.readonly` |
| Domains | `/…/domains`, `/…/domainaliases` | `admin.directory.domain.readonly` |

**Key User fields**: `primaryEmail`, `name.fullName`, `orgUnitPath`, `suspended`, `archived`, `isAdmin`, `isDelegatedAdmin`, `agreedToTerms`, `creationTime`, `lastLoginTime`, `isEnrolledIn2Sv`, `isEnforcedIn2Sv`, `changePasswordAtNextLogin`, `customerId`, `thumbnailPhotoUrl`, `recoveryEmail`, `aliases[]`

**Key ASP fields** (app-specific passwords = MFA bypass risk): `codeId`, `name`, `creationTime`, `lastTimeUsed`

**Key Token fields** (OAuth3rd party): `clientId`, `displayText`, `anonymous`, `nativeApp`, `scopes[]`, `userKey`

### 2.2 Reports API v1  HIGH
| Method | Path | Key params |
|--------|------|-----------|
| GET | `/admin/reports/v1/activity/users/{userKey}/applications/{appName}` | `appName`: login, token, admin, drive, mobile, saml… · `eventName`, `filters`, `startTime`, `endTime`, `maxResults`, `orgUnitID`, `groupIdFilter` |
| GET | `/admin/reports/v1/usage/dates/{date}` | `customerId`, `parameters`, `pageToken` |
| GET | `/admin/reports/v1/usage/users/{userKey}/dates/{date}` | `filters`, `orgUnitID`, `parameters`, `maxResults` |

**Login event names**: `login_success`, `login_failure`, `login_verification`, `logout`, `account_disabled_spamming_through_relay`, `suspicious_login`, `suspicious_login_less_secure_app`

**Token event names**: `authorize`, `revoke`, `activity` (+ `app_name`, `client_id`, `scope_data` params)

---

## 3. ATLASSIAN (Guard + Jira + Confluence)

### Auth
```
API key (scopeless) or OAuth 2.0.
Organization endpoints: https://api.atlassian.com/admin/v1/...
Jira endpoints: https://{site}.atlassian.net/rest/api/3/...
Confluence: https://{site}.atlassian.net/wiki/rest/api/...
SCIM: https://api.atlassian.com/scim/directory/{directoryId}/...
```

### 3.1 Atlassian Admin — Organizations API  HIGH
| Method | Path | Key params | Notes |
|--------|------|-----------|-------|
| GET | `/admin/v1/orgs` | `cursor` | list orgs |
| GET | `/admin/v1/orgs/{orgId}` | — | org detail |
| GET | `/admin/v1/orgs/{orgId}/users` | `cursor` | managed accounts in org |
| GET | `/admin/v1/orgs/{orgId}/domains` | `cursor` | verified domains |
| GET | `/admin/v1/orgs/{orgId}/events` | `action`, `from` (ms epoch), `to`, `cursor`, `q` (free-text search) | audit log — filtered, Guard Standard required |
| GET | `/admin/v1/orgs/{orgId}/events-stream` | `from` (ms epoch), `to`, `cursor` | audit log — simple paginated stream |
| GET | `/admin/v1/orgs/{orgId}/events/{eventId}` | — | single event |
| GET | `/admin/v1/orgs/{orgId}/event-actions` | — | list of available event action types |
| GET | `/admin/v1/orgs/{orgId}/policies` | `cursor`, `type` (ip-allowlist\|password-policy\|…) | org policies |
| GET | `/admin/v1/orgs/{orgId}/policies/{policyId}` | — | policy detail |
| GET | `/admin/v1/orgs/{orgId}/api-keys` | `cursor` | org API keys |

**Audit event fields**: `id`, `type`, `attributes.time`, `attributes.action`, `attributes.actor.id`, `attributes.actor.name`, `attributes.actor.email`, `attributes.context[]` (resources changed), `attributes.container[]` (org/site), `attributes.location.ip`, `attributes.location.geo.countryName`

**Key event actions** (for filtering): `user_login`, `user_sso_login`, `user_login_failed`, `user_added_to_group`, `user_removed_from_group`, `user_suspended`, `user_reactivated`, `user_api_token_created`, `user_api_token_revoked`, `policy_created`, `policy_updated`, `product_access_granted`, `product_access_revoked`, `org_admin_added`, `org_admin_removed`

### 3.2 Atlassian Admin — User Management API  HIGH
| Method | Path | Key params | Notes |
|--------|------|-----------|-------|
| GET | `/admin/v1/users/{accountId}/manage` | — | user profile (manage scope) |
| GET | `/admin/v1/users/{accountId}/manage/profile` | — | extended profile fields |
| GET | `/admin/v1/users/{accountId}/manage/api-tokens` | — | user's API tokens |

**User fields**: `account_id`, `email`, `name`, `picture`, `account_status` (active\|inactive\|closed), `account_type` (atlassian\|customer\|app), `locale`, `extended_profile.department`, `extended_profile.organization`, `extended_profile.jobTitle`, `extended_profile.location`

**API Token fields**: `id`, `label`, `created_at`, `last_authenticated`, `expires_at`, `status`

### 3.3 SCIM 2.0 — User Provisioning API  HIGH
Base: `https://api.atlassian.com/scim/directory/{directoryId}`  
Requires Guard Standard.

| Method | Path | Key params |
|--------|------|-----------|
| GET | `/Users` | `filter` (SCIM filter), `startIndex`, `count`, `attributes`, `excludedAttributes` |
| GET | `/Users/{id}` | — |
| GET | `/Groups` | `filter`, `startIndex`, `count` |
| GET | `/Groups/{id}` | — |
| GET | `/Schemas` | — |
| GET | `/ServiceProviderConfig` | — |

**User SCIM fields**: `id`, `externalId`, `userName` (email), `name.formatted`, `name.givenName`, `name.familyName`, `displayName`, `emails[]`, `active`, `title`, `department`, `organization`, `timezone`, `locale`, `groups[]`

### 3.4 Jira REST API v3  HIGH
Base: `https://{site}.atlassian.net/rest/api/3`

| Method | Path | Key params | Notes |
|--------|------|-----------|-------|
| GET | `/users/search` | `query`, `startAt`, `maxResults` (≤200) | all users — filter `accountType=atlassian` |
| GET | `/user` | `accountId`, `expand` (groups,applicationRoles) | single user |
| GET | `/user/groups` | `accountId` | user's groups |
| GET | `/groups/picker` | `query`, `maxResults` | search groups |
| GET | `/group/member` | `groupId`, `startAt`, `maxResults`, `includeInactiveUsers` | group members |
| GET | `/project` | `startAt`, `maxResults`, `expand`, `status`, `typeKey` | projects list |
| GET | `/project/{projectId}` | `expand` | project detail |
| GET | `/project/{projectId}/role` | — | roles in project |
| GET | `/project/{projectId}/role/{id}` | — | role members |
| GET | `/project/{projectId}/statuses` | — | workflow statuses |
| GET | `/permissionscheme` | `expand` | permission schemes |
| GET | `/applicationrole` | — | product access roles |
| GET | `/myself` | `expand` | current user context |

**User fields**: `accountId`, `accountType`, `active`, `displayName`, `emailAddress` (may be null for privacy), `avatarUrls`, `groups.items[]`, `applicationRoles.items[]`, `locale`, `timeZone`

### 3.5 Confluence REST API v1  HIGH
Base: `https://{site}.atlassian.net/wiki/rest/api`

| Method | Path | Key params |
|--------|------|-----------|
| GET | `/user` | `accountId`, `expand` (operations,personalSpace,details.personal) |
| GET | `/user/list` | `start`, `limit` |
| GET | `/group` | `start`, `limit` |
| GET | `/group/{id}/member` | `start`, `limit` |
| GET | `/space` | `start`, `limit`, `type` (global\|personal), `status`, `expand` |
| GET | `/space/{spaceKey}` | `expand` |
| GET | `/space/{spaceKey}/permission` | — |
| GET | `/audit` | `startDate`, `endDate`, `searchString`, `start`, `limit` |

---

## 4. JUMPCLOUD

*(Full endpoint list: 189 in JC API 2.0 + 13 in API 1.0 + 2 in Directory Insights — all in api_endpoint_map.json)*

### Key endpoint groups for IT admin use:

| Group | Key endpoints | Notes |
|-------|-------------|-------|
| Systems (devices) | `GET /api/systems`, `/api/systems/{id}` | OS, agent, MDM, serial |
| Users | `GET /api/systemusers`, `/api/systemusers/{id}`, `/…/{id}/sshkeys`, `/…/{id}/totpinfo` | Identity, MFA, SSH |
| System ↔ User bindings | `GET /api/v2/systems/{id}/users`, `/api/v2/users/{id}/systems` | Who has access to what |
| Groups | `GET /api/v2/systemgroups`, `/api/v2/usergroups` | Group membership |
| Policies | `GET /api/v2/policies/{id}`, `/…/policyresults`, `/…/policystatuses` | Compliance state |
| System Insights | `GET /api/v2/systeminsights/*` | ~50 tables: apps, programs, disk_encryption, bitlocker_info, users, certificates, etc. |
| Auth policies | `GET /api/v2/authn/policies` | MFA and auth rules |
| Apple MDM | `GET /api/v2/applemdms`, `/…/{id}/devices` | MDM device inventory |
| Software apps | `GET /api/v2/softwareapps`, `/…/{id}/statuses` | Deployment status |
| Directory Insights | `GET /insights/directory/v1/reports` (at `api.jumpcloud.com`) | Report artifacts |

**System Insights tables especially useful** (filter by `system_id`):
- `disk_encryption` — `encryption_status`, `type`, `name`, `uid`
- `bitlocker_info` — `protection_status`, `drive_letter`, `encryption_method`
- `users` — `username`, `uid`, `gid`, `shell`, `directory`
- `certificates` — `common_name`, `not_valid_after`, `sha1`, `store` (win)
- `os_version` — `name`, `platform`, `version`, `arch`
- `apps` / `programs` / `linux_packages` — installed software
- `chrome_extensions` / `firefox_addons` / `safari_extensions`
- `logged_in_users` — `user`, `host`, `time`, `tty`
- `authorized_keys` — `uid`, `key`, `key_file`
- `secureboot` — `secure_boot_state`
- `tpm_info` — `spec_version`, `status`
- `wifi_networks` — `ssid`, `security_type`, `last_connected`
- `usb_devices` — `vendor_id`, `model`, `serial`, `class`

---

## 5. PEOPLEFORCE

### Auth
```
Header: X-API-KEY: {company_api_key}
Base: https://api.peopleforce.io
Pagination: page, per_page params
```

### 5.1 Core Employee Endpoints  HIGH
| Method | Path | Key params | Returns |
|--------|------|-----------|---------|
| GET | `/employees` | `page`, `per_page`, `ids[]`, `status` (active\|inactive), `department_id`, `location_id`, `employment_type_id` | employees list |
| GET | `/employees/{id}` | — | full employee profile |
| GET | `/employees/terminated` | `page`, `per_page`, `terminated_from`, `terminated_to` | offboarded employees |
| GET | `/employees/{id}/positions` | — | position history |
| GET | `/employees/{id}/salaries` | — | compensation history |
| GET | `/employees/{id}/employment_statuses` | — | employment status history |
| GET | `/employees/{id}/leave_balances` | — | leave balances per policy |
| GET | `/employees/{id}/assets` | — | assigned hardware assets |
| GET | `/employees/{id}/documents` | — | documents list |
| GET | `/employees/{id}/notes` | — | notes |
| GET | `/employees/{id}/tasks` | — | tasks assigned to employee |
| GET | `/employees/{id}/educations` | — | education history |
| GET | `/employees/{id}/dependents` | — | dependents |
| GET | `/employees/{id}/emergency_contacts` | — | emergency contacts |
| GET | `/employees/{id}/holidays` | — | applicable holidays |
| GET | `/employees/{id}/employee_leave_types` | — | assigned leave policies |
| GET | `/employees/birthdays` | `from`, `to` | birthday list |
| GET | `/employees/anniversaries` | `from`, `to` | work anniversaries |

**Employee fields**: `id`, `first_name`, `last_name`, `email`, `phone`, `personal_email`, `date_of_birth`, `gender`, `hire_date`, `termination_date`, `status` (active\|inactive\|terminated), `employee_number`, `department.id/name`, `division.id/name`, `location.id/name`, `position.id/name`, `manager.id/name/email`, `employment_type.name`, `avatar_url`, `custom_fields{}` (arbitrary per company)

### 5.2 Assets  HIGH
| Method | Path | Key params | Returns |
|--------|------|-----------|---------|
| GET | `/assets` | `page`, `per_page`, `category_id`, `employee_id`, `status` (assigned\|unassigned) | all assets |
| GET | `/assets/{id}` | — | single asset detail |
| GET | `/asset_categories` | — | asset category list |

**Asset fields**: `id`, `name`, `code`, `serial_number`, `description`, `category.id/name`, `status` (assigned\|unassigned), `assigned_to.id/name/email/department/location`, `issued_on`, `created_at`, `updated_at`

### 5.3 Org Structure  HIGH
| Method | Path | Returns |
|--------|------|---------|
| GET | `/departments` | departments with hierarchy |
| GET | `/divisions` | divisions |
| GET | `/positions` | position titles |
| GET | `/locations` | office locations |
| GET | `/employment_types` | employment type list |
| GET | `/employee_fields` | custom field definitions (with `internal_name`) |
| GET | `/leave_types` | leave type catalog |
| GET | `/leave_policies` | leave policy rules |
| GET | `/job_profiles` | job profiles (level/function matrix) |
| GET | `/job_groups` | job groups |
| GET | `/job_levels` | job levels |

### 5.4 Leave & Attendance  HIGH
| Method | Path | Key params | Returns |
|--------|------|-----------|---------|
| GET | `/leave_requests` | `employee_id`, `from`, `to`, `status` (pending\|approved\|rejected), `leave_type_id`, `page` | leave requests |
| GET | `/leave_requests/{id}` | — | single request |
| GET | `/leave_requests/pending` | `page` | pending approvals |

### 5.5 Audit & Misc  HIGH
| Method | Path | Key params | Returns |
|--------|------|-----------|---------|
| GET | `/audits` | `page`, `per_page` | audit log (account actions) |
| GET | `/calendars` | `from`, `to` | company calendar events |
| GET | `/tasks` | `page`, `per_page`, `employee_id`, `status` | all tasks |
| GET | `/external_users` | `page` | non-employee external users |
| GET | `/competencies` | — | competency framework |

---

## Go Types

### sophos/types.go

```go
package sophos

import "time"

// Endpoint — already defined in existing package.
// Additional types:

// Alert is one item from GET /siem/v1/alerts.
type Alert struct {
	ID           string    `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	CustomerID   string    `json:"customer_id"`
	Severity     string    `json:"severity"`     // low|medium|high|critical
	Type         string    `json:"type"`
	Threat       string    `json:"threat,omitempty"`
	Location     string    `json:"location,omitempty"`
	EndpointID   string    `json:"endpoint_id,omitempty"`
	EndpointType string    `json:"endpoint_type,omitempty"`
	SourceIP     string    `json:"source_ip,omitempty"`
	Name         string    `json:"name,omitempty"`
	When         time.Time `json:"when,omitempty"`
	// Data is product-specific; kept raw for LLM analysis.
	Data map[string]any `json:"data,omitempty"`
}

// Event is one item from GET /siem/v1/events.
type Event struct {
	ID           string    `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	CustomerID   string    `json:"customer_id"`
	Severity     string    `json:"severity"` // none|low|medium|high|critical
	Type         string    `json:"type"`
	Name         string    `json:"name,omitempty"`
	Group        string    `json:"group,omitempty"`
	UserID       string    `json:"user_id,omitempty"`
	EndpointID   string    `json:"endpoint_id,omitempty"`
	EndpointType string    `json:"endpoint_type,omitempty"`
	SourceIP     string    `json:"source_ip,omitempty"`
	Origin       string    `json:"origin,omitempty"`
}

// AuditEvent is one item from GET /audit/v1/events.
type AuditEvent struct {
	ID            string    `json:"id"`
	Type          string    `json:"type"`
	TriggeredAt   time.Time `json:"triggered_at"`
	UserID        string    `json:"user_id,omitempty"`
	UserName      string    `json:"user_name,omitempty"`
	UserEmail     string    `json:"user_email,omitempty"`
	Source        string    `json:"source,omitempty"` // API|UI
	Description   string    `json:"description,omitempty"`
	ResourceType  string    `json:"resource_type,omitempty"`
	ResourceID    string    `json:"resource_id,omitempty"`
	SiteName      string    `json:"site_name,omitempty"`
}

// Detection is one item from GET /detections/v1/queries/detections/{id}/results.
// Requires XDR or MTR license.
type Detection struct {
	ID                   string    `json:"id"`
	DetectionRule        string    `json:"detection_rule"`
	AttackType           string    `json:"attack_type,omitempty"`
	SensorGeneratedAt    time.Time `json:"sensor_generated_at,omitempty"`
	DeviceID             string    `json:"device_id,omitempty"`
	DeviceType           string    `json:"device_type,omitempty"`  // computer|server
	DeviceHostname       string    `json:"device_hostname,omitempty"`
	// Severity is 1-10; 10 = most severe.
	Severity             int       `json:"severity,omitempty"`
	DetectionAttack      string    `json:"detection_attack,omitempty"` // MITRE tactic
	DetectionLicenses    string    `json:"detection_licenses,omitempty"` // raw JSON array string
	PublicIP             string    `json:"public_ip,omitempty"`
}

// DetectionGroup is one item from detection-groups results.
type DetectionGroup struct {
	DetectionRule string    `json:"detection_rule"`
	Count         int       `json:"count"`
	Severity      int       `json:"severity,omitempty"`
	DeviceID      string    `json:"device_id,omitempty"`
	DeviceHostname string   `json:"device_hostname,omitempty"`
	Time          time.Time `json:"time,omitempty"`
}

// DirectoryUser is from GET /common/v1/directory/users.
type DirectoryUser struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
}

// HealthCheck is the full response from GET /account-health-check/v1/health-check.
type HealthCheck struct {
	Overall struct {
		Score int `json:"score"`
	} `json:"overall"`
	Endpoint struct {
		Protection struct {
			Computer ProtectionStat `json:"computer"`
			Server   ProtectionStat `json:"server"`
		} `json:"protection"`
		Policy struct {
			Total         int `json:"total"`
			NotConfigured int `json:"notConfigured"`
		} `json:"policy"`
		Exclusions struct {
			Total     int `json:"total"`
			Dangerous int `json:"dangerous"`
		} `json:"exclusions"`
		TamperProtection struct {
			Computer ProtectionStat `json:"computer"`
			Server   ProtectionStat `json:"server"`
		} `json:"tamperProtection"`
	} `json:"endpoint"`
}

type ProtectionStat struct {
	Total             int `json:"total"`
	NotFullyProtected int `json:"notFullyProtected"`
	TurnedOff         int `json:"turnedOff,omitempty"` // tamper protection variant
}
```

### atlassian/types.go

```go
package atlassian

import "time"

// OrgUser is a managed account from GET /admin/v1/orgs/{orgId}/users.
type OrgUser struct {
	AccountID     string `json:"account_id"`
	Email         string `json:"email"`
	Name          string `json:"name"`
	AccountType   string `json:"account_type"`   // atlassian|customer|app
	AccountStatus string `json:"account_status"` // active|inactive|closed
	AccessBillable bool  `json:"access_billable"`
	LastActive    *time.Time `json:"last_active,omitempty"`
	// ProductAccess is populated by a second call to Jira/Confluence if needed.
	ProductAccess []string `json:"product_access,omitempty"`
}

// AuditEvent is from GET /admin/v1/orgs/{orgId}/events.
// Guard Standard required; Guard Premium for full user-activity events.
type AuditEvent struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// All payload lives in Attributes.
	Attributes AuditAttributes `json:"attributes"`
}

type AuditAttributes struct {
	Time      time.Time  `json:"time"`
	Action    string     `json:"action"`   // e.g. "user_login", "policy_updated"
	Actor     AuditActor `json:"actor"`
	Context   []AuditResource `json:"context,omitempty"`
	Container []AuditContainer `json:"container,omitempty"`
	Location  *AuditLocation `json:"location,omitempty"`
	// ChangedValues holds before/after for policy/settings changes.
	ChangedValues []ChangedValue `json:"changed_values,omitempty"`
}

type AuditActor struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	// Links contains the actor's admin URL.
	Links map[string]string `json:"links,omitempty"`
}

type AuditResource struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Name  string `json:"name,omitempty"`
}

type AuditContainer struct {
	ID   string `json:"id"`
	Type string `json:"type"` // org|site
	Name string `json:"name,omitempty"`
}

type AuditLocation struct {
	IP          string `json:"ip,omitempty"`
	GeoCountry  string `json:"geo_country,omitempty"`
	GeoCity     string `json:"geo_city,omitempty"`
}

type ChangedValue struct {
	FieldName string `json:"field_name"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
}

// UserDetail is from GET /admin/v1/users/{accountId}/manage/profile.
type UserDetail struct {
	AccountID  string `json:"account_id"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	Picture    string `json:"picture,omitempty"`
	Locale     string `json:"locale,omitempty"`
	// Extended is only populated with the `profile` sub-endpoint.
	Extended *ExtendedProfile `json:"extended_profile,omitempty"`
}

type ExtendedProfile struct {
	Department   string `json:"department,omitempty"`
	Organization string `json:"organization,omitempty"`
	JobTitle     string `json:"job_title,omitempty"`
	Location     string `json:"location,omitempty"`
}

// APIToken is from GET /admin/v1/users/{accountId}/manage/api-tokens.
// API tokens are a significant security risk: they bypass SSO and MFA.
type APIToken struct {
	ID              string     `json:"id"`
	Label           string     `json:"label"`
	CreatedAt       time.Time  `json:"created_at"`
	LastAuthenticAt *time.Time `json:"last_authenticated_at,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"` // nil = never
	Status          string     `json:"status,omitempty"`
}

// SCIMUser is from GET /scim/directory/{id}/Users.
type SCIMUser struct {
	ID         string `json:"id"`
	ExternalID string `json:"externalId,omitempty"`
	UserName   string `json:"userName"` // email
	Active     bool   `json:"active"`
	Name       struct {
		Formatted  string `json:"formatted,omitempty"`
		GivenName  string `json:"givenName,omitempty"`
		FamilyName string `json:"familyName,omitempty"`
	} `json:"name,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Title       string `json:"title,omitempty"`
	Department  string `json:"department,omitempty"`
	// Groups is populated in full view.
	Groups []struct {
		Value string `json:"value"`
		Display string `json:"display,omitempty"`
	} `json:"groups,omitempty"`
}

// JiraUser is from GET /rest/api/3/users/search.
type JiraUser struct {
	AccountID    string `json:"accountId"`
	AccountType  string `json:"accountType"`  // atlassian|customer|app
	Active       bool   `json:"active"`
	DisplayName  string `json:"displayName"`
	// EmailAddress may be empty due to Jira privacy settings even for admins.
	EmailAddress string `json:"emailAddress,omitempty"`
	// Groups populated via expand=groups or separate call.
	Groups *struct {
		Items []struct {
			Name    string `json:"name"`
			GroupID string `json:"groupId"`
		} `json:"items"`
	} `json:"groups,omitempty"`
	// ApplicationRoles populated via expand=applicationRoles.
	ApplicationRoles *struct {
		Items []struct {
			Key  string `json:"key"`
			Name string `json:"name"`
		} `json:"items"`
	} `json:"applicationRoles,omitempty"`
}

// OrgPolicy is from GET /admin/v1/orgs/{orgId}/policies.
type OrgPolicy struct {
	ID         string `json:"id"`
	Type       string `json:"type"` // ip-allowlist|data-residency|password-policy|mobile-app-policy|…
	Status     string `json:"status"` // active|inactive
	Name       string `json:"name"`
	// Resources is the list of sites/users this policy applies to.
	Resources []struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"resources,omitempty"`
}
```

### peopleforce/types.go

```go
package peopleforce

import "time"

// Employee is from GET /employees/{id} — the full profile.
type Employee struct {
	ID             int    `json:"id"`
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name"`
	Email          string `json:"email"`
	PersonalEmail  string `json:"personal_email,omitempty"`
	Phone          string `json:"phone,omitempty"`
	EmployeeNumber string `json:"employee_number,omitempty"`

	// Identity
	DateOfBirth *time.Time `json:"date_of_birth,omitempty"`
	Gender      string     `json:"gender,omitempty"`
	Nationality string     `json:"nationality,omitempty"`

	// Employment
	HireDate        *time.Time `json:"hire_date,omitempty"`
	TerminationDate *time.Time `json:"termination_date,omitempty"`
	// Status: active|inactive|terminated|on_leave
	Status         string `json:"status"`
	EmploymentType struct {
		ID   int    `json:"id"`
		Name string `json:"name"` // Full-time|Part-time|Contractor|…
	} `json:"employment_type,omitempty"`

	// Org structure — always populated on active employees.
	Department struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"department,omitempty"`
	Division struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"division,omitempty"`
	Location struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"location,omitempty"`
	// Position is the current role title (latest position record).
	Position struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"position,omitempty"`
	// Manager is the direct manager.
	Manager *struct {
		ID        int    `json:"id"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Email     string `json:"email"`
	} `json:"manager,omitempty"`

	// Custom fields — keys are internal_names from /employee_fields.
	// Typed as map[string]any because values can be string, int, array.
	CustomFields map[string]any `json:"custom_fields,omitempty"`

	AvatarURL string `json:"avatar_url,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TerminatedEmployee is from GET /employees/terminated.
type TerminatedEmployee struct {
	ID              int        `json:"id"`
	FirstName       string     `json:"first_name"`
	LastName        string     `json:"last_name"`
	Email           string     `json:"email"`
	TerminationDate *time.Time `json:"termination_date,omitempty"`
	TerminationNote string     `json:"termination_note,omitempty"`
	Department      struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"department,omitempty"`
}

// Position is one record from GET /employees/{id}/positions.
// Multiple records exist over time (history).
type Position struct {
	ID           int        `json:"id"`
	EmployeeID   int        `json:"employee_id"`
	Title        string     `json:"title,omitempty"`
	DepartmentID int        `json:"department_id,omitempty"`
	DivisionID   int        `json:"division_id,omitempty"`
	LocationID   int        `json:"location_id,omitempty"`
	ManagerID    int        `json:"manager_id,omitempty"`
	EffectiveAt  *time.Time `json:"effective_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// Salary is one record from GET /employees/{id}/salaries.
type Salary struct {
	ID             int        `json:"id"`
	EmployeeID     int        `json:"employee_id"`
	Amount         float64    `json:"amount"`
	Currency       string     `json:"currency"`
	PaySchedule    string     `json:"pay_schedule,omitempty"` // monthly|biweekly|…
	EffectiveAt    *time.Time `json:"effective_at,omitempty"`
	TerminatedAt   *time.Time `json:"terminated_at,omitempty"`
}

// EmploymentStatus is from GET /employees/{id}/employment_statuses.
type EmploymentStatus struct {
	ID          int        `json:"id"`
	EmployeeID  int        `json:"employee_id"`
	Status      string     `json:"status"` // active|on_leave|terminated|…
	StartDate   *time.Time `json:"start_date,omitempty"`
	EndDate     *time.Time `json:"end_date,omitempty"`
	Note        string     `json:"note,omitempty"`
}

// LeaveBalance is from GET /employees/{id}/leave_balances.
type LeaveBalance struct {
	EmployeeID    int     `json:"employee_id"`
	LeaveTypeID   int     `json:"leave_type_id"`
	LeaveTypeName string  `json:"leave_type_name"`
	Balance       float64 `json:"balance"`
	Used          float64 `json:"used"`
	Available     float64 `json:"available"`
	Unit          string  `json:"unit"` // days|hours
}

// Asset — already defined as PFAsset in model package.
// This richer type is the raw API response before normalisation.
type Asset struct {
	ID           int        `json:"id"`
	Name         string     `json:"name"`
	Code         string     `json:"code,omitempty"`
	SerialNumber string     `json:"serial_number,omitempty"`
	Description  string     `json:"description,omitempty"`
	Status       string     `json:"status"` // assigned|unassigned
	Category     struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"category,omitempty"`
	// AssignedTo is the current active assignment.
	AssignedTo *AssetAssignee `json:"assigned_to,omitempty"`
	IssuedOn   string         `json:"issued_on,omitempty"` // ISO date string
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

// AssetAssignee is the employee a given asset is assigned to.
type AssetAssignee struct {
	ID         int    `json:"id"`
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
	Email      string `json:"email"`
	Department struct {
		Name string `json:"name,omitempty"`
	} `json:"department,omitempty"`
	Position struct {
		Name string `json:"name,omitempty"`
	} `json:"position,omitempty"`
	Location struct {
		Name string `json:"name,omitempty"`
	} `json:"location,omitempty"`
}

// Department is from GET /departments.
type Department struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	ParentID *int   `json:"parent_id,omitempty"`
	HeadID   *int   `json:"head_id,omitempty"` // employee id of dept head
}

// EmployeeField defines a custom field from GET /employee_fields.
// internal_name is used as the key in Employee.CustomFields.
type EmployeeField struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`         // display label
	InternalName string `json:"internal_name"` // API key (auto-generated slug)
	FieldType    string `json:"field_type"`   // text|number|date|select|multi_select|…
	Required     bool   `json:"required"`
}

// LeaveRequest is from GET /leave_requests.
type LeaveRequest struct {
	ID             int        `json:"id"`
	EmployeeID     int        `json:"employee_id"`
	EmployeeName   string     `json:"employee_name,omitempty"`
	LeaveTypeID    int        `json:"leave_type_id"`
	LeaveTypeName  string     `json:"leave_type_name,omitempty"`
	StartDate      *time.Time `json:"start_date"`
	EndDate        *time.Time `json:"end_date"`
	DaysCount      float64    `json:"days_count"`
	Status         string     `json:"status"` // pending|approved|rejected|cancelled
	Note           string     `json:"note,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// AuditEntry is from GET /audits.
type AuditEntry struct {
	ID          int        `json:"id"`
	Action      string     `json:"action"`       // human-readable action name
	Description string     `json:"description,omitempty"`
	UserID      int        `json:"user_id,omitempty"`
	UserName    string     `json:"user_name,omitempty"`
	ResourceID  int        `json:"resource_id,omitempty"`
	ResourceType string    `json:"resource_type,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}
```

---

## Confidence & Gaps Summary

| Service | Endpoint source confidence | Notes |
|---------|---------------------------|-------|
| JumpCloud v1/v2 | HIGH — official OpenAPI spec | 189+13 endpoints already mapped |
| JumpCloud DI | HIGH — official spec | 2 endpoints |
| Google Workspace | HIGH — official discovery docs | 52 endpoints already mapped |
| Sophos endpoint/v1 | HIGH — official spec + Postman | core 9 GET endpoints |
| Sophos siem/v1 | HIGH — official + GitHub source | 2 endpoints, 24h window limit |
| Sophos audit/v1 | HIGH — official link provided by user | newer API, details from official docs |
| Sophos health-check/v1 | HIGH (main) / MED (scores sub-routes) | score route paths inferred |
| Sophos detections/v1 | HIGH — XDR license required | async pattern |
| Sophos common/v1 | HIGH — confirmed via stitchflow + docs | |
| Atlassian org admin API | HIGH — official dev docs | Guard Standard required for audit |
| Atlassian SCIM | HIGH — official dev docs | Guard Standard required |
| Atlassian Jira REST v3 | HIGH — official docs | email may be null (privacy) |
| Atlassian Confluence REST | HIGH — official docs | |
| PeopleForce | HIGH — official llms.txt + API ref | Company API key gives full access |

### Known limitations & gotchas
- **Sophos detections/v1**: async — must poll until `succeeded`. XDR/MTR license required; FetchError flag already in existing Sophos struct.
- **Sophos siem/v1**: max 24-hour window per call; requires cursor-based polling for continuous collection.
- **Atlassian email privacy**: `emailAddress` in Jira REST API may be `null` even for site admins. Only Guard/admin export gives reliable email coverage.
- **JumpCloud System Insights**: only available for systems with JumpCloud agent installed AND SI feature enabled. Some tables macOS-only (`apps`, `alf`, `launchd`), some Windows-only (`programs`, `bitlocker_info`, `appcompat_shims`).
- **PeopleForce custom fields**: keys change per company (internal_name slug). Must call `GET /employee_fields` first to build the field map.
- **Atlassian audit log retention**: 180 days — must export to own storage regularly.
- **Sophos account-health regional scores**: confirms peer comparison exists; exact path inferred from official guide phrasing (MEDIUM confidence — verify at runtime).
