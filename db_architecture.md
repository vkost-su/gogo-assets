# Database Architecture — IT Admin Identity Graph
## SQLite · Identity Resolution · Security Posture Tracking

---

## Концептуальна основа

### Три аксіоми дизайну

**1. Email — єдиний природній ключ.** Кожен сервіс знає email користувача. Він стабільний, людиночитаємий і дозволяє JOIN без lookup-таблиць. Auto-increment `id` — зручний для реляційних баз, але тут він лише додав би зайвий рівень індирекції. Виняток: `devices`, `assets`, `events` — там email є foreign key, а PK — це `(source, service_id)`.

**2. Pointer rule скрізь.** Кожне поле, що представляє стан безпеки, зберігається як `INTEGER` з трьома можливими значеннями: `NULL` = не зібрано (data gap), `0` = зібрано і вимкнено (реальна знахідка), `1` = зібрано і ввімкнено (норма). Це дозволяє відрізняти "агент не відповів" від "шифрування вимкнено".

**3. Rebuild vs append.** Таблиці стану (`persons`, `devices`, `assets`, `person_posture`) повністю перебудовуються при кожному зборі — вони описують поточну реальність. Таблиця `events` — тільки append, ніколи не видаляється. `raw_snapshots` — append per run_date.

### Ієрархія достовірності

```
PeopleForce          → хто є активний співробітник (джерело правди)
JumpCloud users      → identity + MFA стан (trusted directory)
Google Workspace     → identity + security posture (email = primary)
Atlassian            → product access + audit trail
Sophos               → device health (email resolved через JC)
```

---

## Схема бази даних

### 1. `collection_runs` — метадані кожного запуску

Кожен збір даних — це одна транзакція. Усі інші таблиці посилаються на `run_date` з цієї таблиці.

```sql
CREATE TABLE collection_runs (
    run_id    TEXT PRIMARY KEY,           -- UUID v4, генерується при старті
    run_date  TEXT NOT NULL UNIQUE,       -- 'YYYY-MM-DD', logical collection day
    started_at   TEXT NOT NULL,           -- ISO 8601 UTC
    finished_at  TEXT,                    -- NULL якщо ще виконується або впав
    status    TEXT NOT NULL DEFAULT 'running'
              CHECK (status IN ('running','success','partial','failed')),

    -- Які сервіси пройшли успішно, які впали
    services_ok     TEXT NOT NULL DEFAULT '[]',  -- JSON array: ["peopleforce","jumpcloud",…]
    services_failed TEXT NOT NULL DEFAULT '[]',

    -- Підсумкові лічильники (для швидкого огляду без повних запитів)
    total_persons          INTEGER NOT NULL DEFAULT 0,
    total_devices          INTEGER NOT NULL DEFAULT 0,
    total_assets           INTEGER NOT NULL DEFAULT 0,
    total_service_accounts INTEGER NOT NULL DEFAULT 0,
    total_events_collected INTEGER NOT NULL DEFAULT 0,

    -- Ким і чим запущено (audit trail для самого збору)
    triggered_by  TEXT,                   -- 'cron' | 'manual' | 'api'
    collector_version TEXT                -- SemVer рядок
);

CREATE INDEX idx_runs_date ON collection_runs(run_date);
```

---

### 2. `persons` — канонічна ідентичність

Одна людина = один рядок. Зберігає cross-service IDs і поточний стан identity-полів. **Повністю перебудовується при кожному зборі.** Попередні стани — у `person_posture` (один рядок на день).

```sql
CREATE TABLE persons (
    -- Anchor
    email        TEXT PRIMARY KEY,        -- canonical email (з PeopleForce або JC)
    display_name TEXT NOT NULL DEFAULT '',
    account_type TEXT NOT NULL DEFAULT 'employee'
                 CHECK (account_type IN ('employee','contractor','external','unknown')),

    -- ── PeopleForce ──────────────────────────────────────────────────────────
    pf_employee_id   INTEGER UNIQUE,      -- NULL якщо не знайдено в PF
    pf_status        TEXT,                -- 'active'|'on_leave'|NULL
    pf_department    TEXT,
    pf_division      TEXT,
    pf_location      TEXT,
    pf_position      TEXT,                -- job title
    pf_manager_email TEXT REFERENCES persons(email) ON DELETE SET NULL,
    pf_hire_date     TEXT,                -- ISO date string
    pf_employment_type TEXT,              -- 'Full-time'|'Contractor'|…

    -- ── JumpCloud ─────────────────────────────────────────────────────────────
    jc_user_id    TEXT UNIQUE,
    jc_username   TEXT,
    jc_activated  INTEGER,               -- pointer: NULL|0|1
    jc_suspended  INTEGER,               -- pointer: NULL|0|1

    -- ── Google Workspace ──────────────────────────────────────────────────────
    gws_account_id TEXT UNIQUE,          -- Google's immutable ID
    gws_org_unit   TEXT,
    gws_is_admin   INTEGER,              -- pointer: NULL|0|1
    gws_suspended  INTEGER,              -- pointer: NULL|0|1
    gws_archived   INTEGER,              -- pointer: NULL|0|1

    -- ── Atlassian ─────────────────────────────────────────────────────────────
    atlassian_account_id TEXT UNIQUE,
    atlassian_status     TEXT,           -- 'active'|'inactive'|'closed'

    -- ── Sophos ────────────────────────────────────────────────────────────────
    sophos_user_id TEXT UNIQUE,          -- з /common/v1/directory/users; може бути NULL

    -- ── Meta ──────────────────────────────────────────────────────────────────
    first_seen_date TEXT NOT NULL,        -- коли вперше з'явився в системі
    run_date TEXT NOT NULL
             REFERENCES collection_runs(run_date)
);

CREATE INDEX idx_persons_pf_id         ON persons(pf_employee_id);
CREATE INDEX idx_persons_jc_id         ON persons(jc_user_id);
CREATE INDEX idx_persons_gws_id        ON persons(gws_account_id);
CREATE INDEX idx_persons_atlassian_id  ON persons(atlassian_account_id);
CREATE INDEX idx_persons_manager       ON persons(pf_manager_email);
CREATE INDEX idx_persons_department    ON persons(pf_department);
CREATE INDEX idx_persons_run_date      ON persons(run_date);
```

---

### 3. `service_accounts` — не-людські акаунти

Будь-який акаунт, знайдений у сервісах, для якого немає відповідного активного рядка в `persons`. Зберігається окремо — не змішується з людьми, але повністю аудитується.

```sql
CREATE TABLE service_accounts (
    email        TEXT PRIMARY KEY,
    account_subtype TEXT NOT NULL DEFAULT 'service'
                    CHECK (account_subtype IN (
                        'service',    -- технічний акаунт (*-svc, *-bot)
                        'shared',     -- shared inbox / team account
                        'admin',      -- dedicated admin account без PF-запису
                        'external',   -- підрядник / вендор поза PF
                        'unknown'
                    )),

    -- Де знайдено (може бути в кількох сервісах одночасно)
    detected_in    TEXT NOT NULL DEFAULT '[]', -- JSON array

    -- Який патерн спрацював при класифікації
    detection_pattern TEXT,           -- 'no_pf_record'|'svc_prefix'|'bot_suffix'|…
    detection_note    TEXT,           -- людський опис

    -- IDs у сервісах (можуть бути NULL якщо не знайдено)
    jc_user_id         TEXT UNIQUE,
    gws_account_id     TEXT UNIQUE,
    atlassian_account_id TEXT UNIQUE,

    -- Чи є підключені девайси (важливо для безпеки)
    has_active_devices INTEGER NOT NULL DEFAULT 0,

    first_seen_date TEXT NOT NULL,
    run_date TEXT NOT NULL
             REFERENCES collection_runs(run_date)
);

CREATE INDEX idx_sa_subtype  ON service_accounts(account_subtype);
CREATE INDEX idx_sa_run_date ON service_accounts(run_date);
```

---

### 4. `login_aliases` — таблиця розв'язання login-рядків

Критично для Sophos: `associatedPerson.viaLogin` часто повертає `CORP\username` або просто `username`, а не email. Ця таблиця є persistent mapping між raw login strings і canonical emails.

```sql
CREATE TABLE login_aliases (
    alias          TEXT PRIMARY KEY,  -- 'CORP\john.doe' | 'jdoe' | 'john.doe@corp.com'
    resolved_email TEXT,              -- FK → persons.email OR service_accounts.email
    entity_type    TEXT NOT NULL DEFAULT 'person'
                   CHECK (entity_type IN ('person','service_account','unresolved')),
    source         TEXT NOT NULL,     -- 'sophos'|'jumpcloud'|'google_workspace'
    -- Наскільки впевнені у відповідності
    confidence     INTEGER NOT NULL DEFAULT 100
                   CHECK (confidence BETWEEN 0 AND 100),
    -- Як саме знайшли відповідність
    resolution_method TEXT NOT NULL DEFAULT 'exact_email'
                      CHECK (resolution_method IN (
                          'exact_email',     -- alias IS email
                          'jc_username',     -- matched via JC user.username
                          'gws_email',       -- matched via GWS primaryEmail
                          'manual',          -- вручну вказано адміном
                          'unresolved'
                      )),
    created_at TEXT NOT NULL,
    last_seen  TEXT NOT NULL
);

CREATE INDEX idx_aliases_email ON login_aliases(resolved_email);
```

---

### 5. `devices` — всі девайси з усіх джерел

Один рядок на одне фізичне або logical device з одного сервісу. Якщо один ноутбук є і в JumpCloud, і в Sophos — це два рядки, пов'язані через `serial` або `hostname`. Дедуплікація — через view `v_devices_merged`.

```sql
CREATE TABLE devices (
    -- Surrogate PK: (source, service_id) унікально ідентифікує пристрій
    source      TEXT NOT NULL CHECK (source IN ('jumpcloud','sophos','google_workspace')),
    service_id  TEXT NOT NULL,   -- JC: system_id; Sophos: endpoint_id; GWS: deviceId
    PRIMARY KEY (source, service_id),

    -- Власник (NULL = не розв'язано)
    owner_email TEXT REFERENCES persons(email) ON DELETE SET NULL,

    -- Базова ідентифікація
    hostname      TEXT,
    serial        TEXT,          -- для cross-source дедуплікації
    mac_addresses TEXT,          -- JSON array
    os_platform   TEXT,          -- 'windows'|'macos'|'linux'|'ios'|'android'
    os_version    TEXT,
    device_type   TEXT,          -- 'computer'|'server'|'mobile'

    -- ── Posture (pointer rule: NULL=невідомо, 0=вимк, 1=увімк) ──────────────
    disk_encrypted     INTEGER,  -- JC: systeminsights.disk_encryption; Sophos: -
    mdm_enrolled       INTEGER,  -- JC: mdmEnrolled; GWS: -
    tamper_protection  INTEGER,  -- Sophos: tamperProtectionEnabled
    secure_boot        INTEGER,  -- JC: systeminsights.secureboot
    
    -- Sophos-specific health (TEXT, не pointer — це enum)
    health_overall   TEXT,       -- 'good'|'suspicious'|'bad'|'unknown'|NULL
    health_threats   TEXT,
    health_services  TEXT,

    -- JC-specific
    jc_agent_version  TEXT,
    jc_last_contact   TEXT,      -- ISO 8601
    jc_active         INTEGER,   -- pointer

    -- Sophos-specific
    sophos_online       INTEGER, -- pointer
    sophos_last_seen    TEXT,
    sophos_assigned_products TEXT, -- JSON array ['endpointProtection','interceptX']

    -- GWS mobile-specific
    gws_device_status   TEXT,    -- 'APPROVED'|'PENDING'|…
    gws_sync_first_time TEXT,
    gws_sync_last_time  TEXT,

    -- Meta
    run_date TEXT NOT NULL REFERENCES collection_runs(run_date)
);

CREATE INDEX idx_devices_owner    ON devices(owner_email);
CREATE INDEX idx_devices_serial   ON devices(serial);
CREATE INDEX idx_devices_hostname ON devices(hostname);
CREATE INDEX idx_devices_platform ON devices(os_platform);
CREATE INDEX idx_devices_encrypted ON devices(disk_encrypted);
CREATE INDEX idx_devices_run_date ON devices(run_date);
```

---

### 6. `assets` — фізичні активи

Дані з PeopleForce Asset Management (і опційно з JumpCloud SoftwareApps для tracking).

```sql
CREATE TABLE assets (
    source    TEXT NOT NULL CHECK (source IN ('peopleforce','jumpcloud')),
    asset_id  TEXT NOT NULL,
    PRIMARY KEY (source, asset_id),

    owner_email TEXT REFERENCES persons(email) ON DELETE SET NULL,

    -- Ідентифікація
    asset_name    TEXT NOT NULL,
    serial_number TEXT,
    code          TEXT,            -- PF internal asset code
    category      TEXT,            -- 'Laptop'|'Monitor'|'Phone'|…
    description   TEXT,

    -- Призначення
    is_assigned   INTEGER NOT NULL DEFAULT 0,
    issued_on     TEXT,            -- ISO date
    issued_to_name TEXT,           -- snapshot імені (може змінитись)

    -- Org context на момент видачі (denormalized для зручності)
    assignee_department TEXT,
    assignee_location   TEXT,

    run_date TEXT NOT NULL REFERENCES collection_runs(run_date)
);

CREATE INDEX idx_assets_owner  ON assets(owner_email);
CREATE INDEX idx_assets_serial ON assets(serial_number);
CREATE INDEX idx_assets_category ON assets(category);
```

---

### 7. `person_posture` — history безпеки-стану по людині

Один рядок на `(email, run_date)`. Це "знімок" безпекового стану за день. Не перебудовується — тільки INSERT нових рядків.

```sql
CREATE TABLE person_posture (
    email    TEXT NOT NULL REFERENCES persons(email) ON DELETE CASCADE,
    run_date TEXT NOT NULL REFERENCES collection_runs(run_date),
    PRIMARY KEY (email, run_date),

    -- ── JumpCloud MFA ─────────────────────────────────────────────────────────
    jc_mfa_configured        INTEGER,   -- pointer: TOTP або будь-який MFA
    jc_totp_enabled          INTEGER,   -- pointer: конкретно TOTP
    jc_mfa_required          INTEGER,   -- pointer: enforce на рівні org
    jc_password_never_expires INTEGER,  -- pointer (0=хороше, 1=поганий знак)
    jc_password_expired      INTEGER,   -- pointer
    jc_account_locked        INTEGER,   -- pointer
    jc_activated             INTEGER,   -- pointer

    -- ── Google Workspace ──────────────────────────────────────────────────────
    gws_mfa_enrolled         INTEGER,   -- pointer: isEnrolledIn2Sv
    gws_mfa_enforced         INTEGER,   -- pointer: isEnforcedIn2Sv
    gws_is_admin             INTEGER,   -- pointer
    gws_asp_count            INTEGER,   -- кількість app-specific passwords (MFA bypass!)
    gws_oauth_token_count    INTEGER,   -- кількість виданих OAuth токенів 3rd-party apps
    gws_backup_code_count    INTEGER,   -- verification codes
    gws_suspended            INTEGER,   -- pointer

    -- ── Atlassian ─────────────────────────────────────────────────────────────
    atlassian_status         TEXT,      -- 'active'|'inactive'|'closed'
    atlassian_api_token_count INTEGER,  -- кількість активних API-токенів (security risk!)
    atlassian_mfa_policy     TEXT,      -- назва застосованої policy

    -- ── Sophos (агреговано по всіх девайсах цієї людини) ─────────────────────
    sophos_device_count      INTEGER NOT NULL DEFAULT 0,
    sophos_unprotected_count INTEGER NOT NULL DEFAULT 0,  -- health != 'good'
    sophos_tamper_off_count  INTEGER NOT NULL DEFAULT 0,

    -- ── Пристрої (агреговано з devices по owner_email) ────────────────────────
    total_device_count          INTEGER NOT NULL DEFAULT 0,
    unencrypted_device_count    INTEGER NOT NULL DEFAULT 0,  -- disk_encrypted = 0
    no_mdm_device_count         INTEGER NOT NULL DEFAULT 0,
    sophos_no_tamper_count      INTEGER NOT NULL DEFAULT 0,

    -- ── Ризик-скор (0-100, вираховується програмно) ──────────────────────────
    -- Формула: зважена сума порушень. Визначається в коді, не в БД.
    risk_score INTEGER,

    collected_at TEXT NOT NULL         -- timestamp збору
);

CREATE INDEX idx_posture_run_date  ON person_posture(run_date);
CREATE INDEX idx_posture_risk      ON person_posture(risk_score);
CREATE INDEX idx_posture_gws_asp   ON person_posture(gws_asp_count);
CREATE INDEX idx_posture_at_tokens ON person_posture(atlassian_api_token_count);
```

---

### 8. `person_groups` — членство в групах

Many-to-many: людина може бути в багатьох групах в кожному сервісі.

```sql
CREATE TABLE person_groups (
    email      TEXT NOT NULL REFERENCES persons(email) ON DELETE CASCADE,
    source     TEXT NOT NULL CHECK (source IN ('jumpcloud','google_workspace','atlassian')),
    group_id   TEXT NOT NULL,
    group_name TEXT NOT NULL,
    role       TEXT,               -- 'MEMBER'|'MANAGER'|'OWNER' (GWS) | тощо
    run_date   TEXT NOT NULL REFERENCES collection_runs(run_date),
    PRIMARY KEY (email, source, group_id)
);

CREATE INDEX idx_groups_group_id ON person_groups(source, group_id);
CREATE INDEX idx_groups_run_date ON person_groups(run_date);
```

---

### 9. `person_products` — доступ до Atlassian продуктів

```sql
CREATE TABLE person_products (
    email        TEXT NOT NULL REFERENCES persons(email) ON DELETE CASCADE,
    product_key  TEXT NOT NULL,  -- 'jira-software'|'confluence'|'jira-service-management'|…
    product_name TEXT NOT NULL,
    site_id      TEXT,           -- Atlassian cloud site ID
    run_date     TEXT NOT NULL REFERENCES collection_runs(run_date),
    PRIMARY KEY (email, product_key, site_id)
);
```

---

### 10. `events` — append-only security event log

Ніколи не видаляється. Сюди потрапляють: Sophos SIEM alerts/events, GWS Reports (login, token), Atlassian audit log, JumpCloud Directory Insights.

```sql
CREATE TABLE events (
    event_id     TEXT PRIMARY KEY,  -- '{source}:{original_id}' або UUID якщо ID немає
    source       TEXT NOT NULL CHECK (source IN (
                     'sophos_alert','sophos_event','sophos_audit',
                     'gws_login','gws_token','gws_admin',
                     'atlassian_audit',
                     'jc_directory_insights'
                 )),
    event_type   TEXT NOT NULL,     -- наприклад 'login_success', 'user_login_failed', тощо
    severity     TEXT CHECK (severity IN ('info','low','medium','high','critical',NULL)),

    -- Ким зроблено (може бути NULL для системних подій)
    person_email TEXT REFERENCES persons(email) ON DELETE SET NULL,
    -- Де стався (може бути NULL)
    device_source TEXT,
    device_id     TEXT,
    -- FOREIGN KEY (device_source, device_id) REFERENCES devices(source, service_id)
    -- (not a real FK через nullable composite — enforce в коді)

    -- Де відбулось
    source_ip  TEXT,
    location   TEXT,               -- country/city якщо доступно

    -- Деталі
    action_description TEXT,       -- human-readable
    threat_name        TEXT,       -- Sophos: threat name
    raw_json           TEXT,       -- повний raw event для LLM-аналізу

    occurred_at  TEXT NOT NULL,    -- ISO 8601, час події
    collected_at TEXT NOT NULL     -- ISO 8601, час збору
);

CREATE INDEX idx_events_source      ON events(source);
CREATE INDEX idx_events_type        ON events(event_type);
CREATE INDEX idx_events_person      ON events(person_email);
CREATE INDEX idx_events_occurred    ON events(occurred_at);
CREATE INDEX idx_events_severity    ON events(severity);
CREATE INDEX idx_events_device      ON events(device_source, device_id);

-- Партиціонування через partial indexes для швидких recent-запитів
CREATE INDEX idx_events_recent ON events(occurred_at)
    WHERE occurred_at >= date('now', '-30 days');
```

---

### 11. `raw_snapshots` — повні JSON-відповіді від API

Зберігається для LLM drill-down і відтворення стану. Append-only per `run_date`.

```sql
CREATE TABLE raw_snapshots (
    run_date    TEXT NOT NULL REFERENCES collection_runs(run_date),
    service     TEXT NOT NULL,      -- 'peopleforce'|'jumpcloud'|'sophos'|'google_workspace'|'atlassian'
    entity_type TEXT NOT NULL,      -- 'employee'|'system'|'endpoint'|'user'|…
    entity_id   TEXT NOT NULL,      -- native ID від сервісу
    data        TEXT NOT NULL,      -- JSON (стиснутий або raw)
    collected_at TEXT NOT NULL,
    PRIMARY KEY (run_date, service, entity_type, entity_id)
);

CREATE INDEX idx_raw_service      ON raw_snapshots(service, entity_type);
CREATE INDEX idx_raw_run_date     ON raw_snapshots(run_date);
```

---

## Views — корисні зрізи без JOIN'ів у коді

```sql
-- Люди з критичними посторовими проблемами (для daily digest)
CREATE VIEW v_risky_persons AS
SELECT
    p.email,
    p.display_name,
    p.pf_department,
    pp.risk_score,
    pp.gws_asp_count,
    pp.atlassian_api_token_count,
    pp.jc_mfa_configured,
    pp.gws_mfa_enrolled,
    pp.unencrypted_device_count,
    pp.sophos_unprotected_count
FROM persons p
JOIN person_posture pp ON pp.email = p.email
WHERE pp.run_date = (SELECT MAX(run_date) FROM collection_runs WHERE status = 'success')
  AND (
      pp.jc_mfa_configured = 0
   OR pp.gws_mfa_enrolled = 0
   OR pp.gws_asp_count > 0
   OR pp.atlassian_api_token_count > 2
   OR pp.unencrypted_device_count > 0
  );

-- Девайси без прив'язаного власника (orphans)
CREATE VIEW v_orphan_devices AS
SELECT d.source, d.service_id, d.hostname, d.serial, d.os_platform, d.run_date
FROM devices d
WHERE d.owner_email IS NULL
  AND d.run_date = (SELECT MAX(run_date) FROM collection_runs WHERE status = 'success');

-- Cross-source device deduplication (один ноутбук в JC і Sophos)
CREATE VIEW v_devices_merged AS
SELECT
    COALESCE(jc.serial, s.serial) AS serial,
    jc.hostname      AS jc_hostname,
    s.hostname       AS sophos_hostname,
    COALESCE(jc.owner_email, s.owner_email) AS owner_email,
    jc.disk_encrypted,
    jc.mdm_enrolled,
    s.tamper_protection,
    s.health_overall,
    jc.os_platform,
    jc.os_version    AS jc_os_version,
    s.os_version     AS sophos_os_version,
    jc.service_id    AS jc_system_id,
    s.service_id     AS sophos_endpoint_id
FROM devices jc
FULL OUTER JOIN devices s
    ON s.source = 'sophos'
    AND (
        (jc.serial IS NOT NULL AND jc.serial = s.serial)
     OR (LOWER(jc.hostname) = LOWER(s.hostname))
    )
WHERE jc.source = 'jumpcloud'
  AND jc.run_date = (SELECT MAX(run_date) FROM collection_runs WHERE status = 'success')
  AND (s.run_date = jc.run_date OR s.run_date IS NULL);

-- Повний профіль людини (для LLM контексту)
CREATE VIEW v_person_full AS
SELECT
    p.*,
    pp.risk_score,
    pp.jc_mfa_configured,
    pp.gws_mfa_enrolled,
    pp.gws_asp_count,
    pp.atlassian_api_token_count,
    pp.unencrypted_device_count,
    pp.total_device_count,
    (SELECT COUNT(*) FROM assets a WHERE a.owner_email = p.email
        AND a.run_date = p.run_date) AS asset_count,
    (SELECT GROUP_CONCAT(pg.group_name, ', ')
     FROM person_groups pg WHERE pg.email = p.email AND pg.source = 'jumpcloud'
        AND pg.run_date = p.run_date) AS jc_groups
FROM persons p
LEFT JOIN person_posture pp ON pp.email = p.email AND pp.run_date = p.run_date;
```

---

## Identity Resolution — алгоритм

```
INPUT: raw snapshots від усіх сервісів за сьогодні
OUTPUT: заповнені таблиці persons, service_accounts, login_aliases, devices, assets

Step 1: Collect all emails
    pf_emails    = {e.email for e in pf.employees if e.status == 'active'}
    jc_emails    = {u.email for u in jc.systemusers}
    gws_emails   = {u.primaryEmail for u in gws.users}
    atlassian_emails = {u.email for u in atlassian.org_users}

Step 2: Build candidate set
    all_emails = pf_emails ∪ jc_emails ∪ gws_emails ∪ atlassian_emails

Step 3: Classify each email
    for email in all_emails:
        if email in pf_emails:
            INSERT INTO persons (account_type = 'employee' or 'contractor')
        else if is_service_pattern(email):      # *-svc, *-bot, noreply@, …
            INSERT INTO service_accounts (account_subtype = 'service')
        else:
            INSERT INTO service_accounts (account_subtype = 'unknown')

Step 4: Resolve Sophos viaLogin → email
    for endpoint in sophos.endpoints:
        login = endpoint.associatedPerson.viaLogin   # 'CORP\jdoe' або 'jdoe@co.com'
        if '@' in login:
            alias = login.lower()
            # вже email → шукаємо в persons/service_accounts
        else:
            username = strip_domain(login)           # 'CORP\jdoe' → 'jdoe'
            # шукаємо JC user де username = username
            jc_user = find_jc_by_username(username)
            alias = jc_user.email if jc_user else None
        
        INSERT OR IGNORE INTO login_aliases (alias, resolved_email, source='sophos', …)
        # потім через login_aliases заповнюємо devices.owner_email для Sophos

Step 5: Enrich devices.owner_email
    -- JC: devices.owner_email = persons.jc_user_id → persons.email (вже є)
    -- Sophos: devices.owner_email через login_aliases
    -- GWS mobile: devices.owner_email = gws_user.primaryEmail

Step 6: Aggregate person_posture
    for person in persons:
        INSERT INTO person_posture SELECT aggregated posture fields …
        UPDATE person_posture SET risk_score = calculate_risk(…)
```

---

## Правила ризик-скору

```
risk_score = sum(weight * is_violated) з cap 100

jc_mfa_configured = 0            → +35
gws_mfa_enrolled = 0             → +30
gws_asp_count > 0                → +25 (ASP = MFA bypass!)
atlassian_api_token_count > 0    → +10 per token (cap +30)
unencrypted_device_count > 0     → +20 per device (cap +40)
gws_is_admin = 1 AND gws_mfa_enrolled = 0 → +50 (admin без MFA)
jc_account_locked = 1            → +5 (informational)
sophos_unprotected_count > 0     → +15 per device (cap +30)
jc_password_never_expires = 1    → +10
```

---

## Partition strategy і retention

```sql
-- Events: зберігати 90 днів у "гарячій" таблиці, решта → архів
-- Реалізація через щоквартальне DELETE:

DELETE FROM events
WHERE occurred_at < date('now', '-90 days');

-- raw_snapshots: 30 днів за замовчуванням
DELETE FROM raw_snapshots
WHERE run_date < date('now', '-30 days');

-- person_posture: зберігати forever (невелика таблиця ~365 rows/person/year)
-- При 1000 людях і 365 днях = 365,000 рядків → ~15MB, тривіально

-- Vacuum після великих DELETE:
PRAGMA auto_vacuum = INCREMENTAL;
```

---

## Розміри і продуктивність (оцінка для 1000 співробітників)

| Таблиця | Рядків | Розмір | Основні запити |
|---------|--------|--------|----------------|
| `persons` | ~1,000 | ~200KB | за email, за department |
| `devices` | ~3,000 | ~1MB | за owner, за disk_encrypted |
| `assets` | ~2,000 | ~500KB | за owner, за category |
| `person_posture` | ~365K/рік | ~50MB/рік | за run_date, за risk_score |
| `events` (90d) | ~500K | ~200MB | за occurred_at, за person |
| `raw_snapshots` (30d) | ~90K | ~2GB | за run_date, за service |

SQLite без проблем обробляє такі обсяги. Час типового аналітичного запиту `v_risky_persons` — <10ms.

---

## SQLite pragmas для production

```sql
PRAGMA journal_mode = WAL;        -- concurrent reads під час writes
PRAGMA foreign_keys = ON;         -- enforce FK constraints
PRAGMA synchronous = NORMAL;      -- баланс між безпекою і швидкістю
PRAGMA cache_size = -32768;       -- 32MB page cache
PRAGMA temp_store = MEMORY;       -- temporary tables in RAM
PRAGMA mmap_size = 268435456;     -- 256MB memory-mapped I/O
```
