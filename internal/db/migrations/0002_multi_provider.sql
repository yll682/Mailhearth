-- 0002_multi_provider.sql
-- 多服务商与手动邮箱连接的数据结构。时间统一使用 RFC3339 UTC。

CREATE TABLE mail_connections (
  id                    INTEGER PRIMARY KEY,
  org_id                INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  provider_kind         TEXT NOT NULL CHECK(provider_kind IN ('purelymail','migadu','manual')),
  label                 TEXT NOT NULL,
  enabled               INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
  revision              INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0),
  api_base_url          TEXT,
  api_username          TEXT,
  api_credential_id     INTEGER REFERENCES credentials(id),
  domain_scope_json     TEXT NOT NULL,
  protocol_defaults_json TEXT NOT NULL,
  last_api_check_at     TEXT,
  last_api_check_status TEXT NOT NULL DEFAULT 'never' CHECK(last_api_check_status IN ('never','passed','failed')),
  last_api_error_code   TEXT,
  last_sync_at          TEXT,
  created_at            TEXT NOT NULL,
  updated_at            TEXT NOT NULL
);
CREATE INDEX idx_mail_connections_org ON mail_connections(org_id);

CREATE TABLE credentials (
  id             INTEGER PRIMARY KEY,
  org_id         INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  connection_id  INTEGER NOT NULL REFERENCES mail_connections(id) ON DELETE CASCADE,
  mailbox_id     INTEGER REFERENCES mailboxes(id) ON DELETE CASCADE,
  purpose        TEXT NOT NULL CHECK(purpose IN ('api','mail')),
  source         TEXT NOT NULL CHECK(source IN ('entered','purelymail_app_password','migadu_identity','mailbox_password')),
  secret_enc     TEXT NOT NULL,
  generation     INTEGER NOT NULL DEFAULT 1 CHECK(generation > 0),
  state          TEXT NOT NULL CHECK(state IN ('candidate','active','retired','revocation_pending','revoked','unknown')),
  hint           TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL,
  CHECK((purpose='api' AND mailbox_id IS NULL) OR (purpose='mail' AND mailbox_id IS NOT NULL))
);
CREATE INDEX idx_credentials_mailbox ON credentials(mailbox_id);
CREATE INDEX idx_credentials_connection ON credentials(connection_id);

CREATE TABLE domain_bindings (
  id                    INTEGER PRIMARY KEY,
  org_id                INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  domain_id             INTEGER NOT NULL REFERENCES domains(id),
  connection_id         INTEGER NOT NULL REFERENCES mail_connections(id),
  management_mode       TEXT NOT NULL CHECK(management_mode IN ('api','external')),
  remote_state          TEXT NOT NULL CHECK(remote_state IN ('present','missing','inaccessible','unknown','external')),
  provider_settings_json TEXT NOT NULL DEFAULT '{}',
  dns_status_json       TEXT NOT NULL DEFAULT '{}',
  dns_checked_at        TEXT,
  revision              INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0),
  created_at            TEXT NOT NULL,
  updated_at            TEXT NOT NULL,
  UNIQUE(domain_id, connection_id)
);
CREATE INDEX idx_domain_bindings_domain ON domain_bindings(domain_id);

CREATE TABLE discovery_snapshots (
  id                  INTEGER PRIMARY KEY,
  connection_id       INTEGER NOT NULL REFERENCES mail_connections(id) ON DELETE CASCADE,
  connection_revision INTEGER NOT NULL,
  scope_json          TEXT NOT NULL,
  resources_json      TEXT NOT NULL,
  complete            INTEGER NOT NULL CHECK(complete IN (0,1)),
  created_at          TEXT NOT NULL,
  expires_at          TEXT NOT NULL
);
CREATE INDEX idx_discovery_snapshots_connection ON discovery_snapshots(connection_id, created_at);

CREATE TABLE mailboxes_v2 (
  id INTEGER PRIMARY KEY,
  org_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK(kind IN ('personal','shared')),
  address TEXT NOT NULL,
  domain_id INTEGER REFERENCES domains(id) ON DELETE SET NULL,
  display_name TEXT NOT NULL DEFAULT '',
  owner_member_id INTEGER REFERENCES members(id) ON DELETE SET NULL,
  credential_enc TEXT NOT NULL DEFAULT '',
  credential_label TEXT NOT NULL DEFAULT '',
  credential_at TEXT,
  status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','suspended','archived')),
  imported INTEGER NOT NULL DEFAULT 0,
  settings_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  CHECK(kind!='shared' OR owner_member_id IS NULL)
);
INSERT INTO mailboxes_v2 SELECT id,org_id,kind,pm_user,domain_id,display_name,owner_member_id,credential_enc,credential_label,credential_at,status,imported,settings_json,created_at,updated_at FROM mailboxes;
DROP TABLE mailboxes;
ALTER TABLE mailboxes_v2 RENAME TO mailboxes;
CREATE INDEX idx_mailboxes_owner ON mailboxes(owner_member_id);
ALTER TABLE mailboxes ADD COLUMN connection_id INTEGER NOT NULL DEFAULT 0 REFERENCES mail_connections(id);
ALTER TABLE mailboxes ADD COLUMN address_key TEXT NOT NULL DEFAULT '';
ALTER TABLE mailboxes ADD COLUMN management_mode TEXT NOT NULL DEFAULT 'api' CHECK(management_mode IN ('api','external'));
ALTER TABLE mailboxes ADD COLUMN remote_state TEXT NOT NULL DEFAULT 'unknown' CHECK(remote_state IN ('present','missing','inaccessible','unknown','external'));
ALTER TABLE mailboxes ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0);
ALTER TABLE mailboxes ADD COLUMN access_revision INTEGER NOT NULL DEFAULT 1 CHECK(access_revision > 0);
ALTER TABLE mailboxes ADD COLUMN sent_copy_mode TEXT NOT NULL DEFAULT 'append' CHECK(sent_copy_mode IN ('append','server'));
ALTER TABLE mailboxes ADD COLUMN folder_mapping_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE mailboxes ADD COLUMN domain_binding_id INTEGER REFERENCES domain_bindings(id);


CREATE TABLE mailbox_endpoints (
  id                   INTEGER PRIMARY KEY,
  mailbox_id           INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
  protocol             TEXT NOT NULL CHECK(protocol IN ('imap','smtp','managesieve')),
  network_mode         TEXT NOT NULL CHECK(network_mode IN ('inherit','override','disabled')),
  network_override_json TEXT,
  username             TEXT,
  credential_id        INTEGER REFERENCES credentials(id),
  revision             INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0),
  check_status         TEXT NOT NULL DEFAULT 'never' CHECK(check_status IN ('never','passed','failed','stale')),
  checked_at           TEXT,
  checked_versions_json TEXT,
  capabilities_json    TEXT NOT NULL DEFAULT '{}',
  last_error_code      TEXT,
  created_at           TEXT NOT NULL,
  updated_at           TEXT NOT NULL,
  UNIQUE(mailbox_id, protocol),
  CHECK(network_mode!='disabled' OR (network_override_json IS NULL AND username IS NULL AND credential_id IS NULL))
);
CREATE INDEX idx_mailbox_endpoints_credential ON mailbox_endpoints(credential_id);

ALTER TABLE members ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0);

ALTER TABLE groups ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0);

CREATE TABLE addresses_v2 (
  id INTEGER PRIMARY KEY,
  org_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  domain_id INTEGER NOT NULL REFERENCES domains(id),
  local_part TEXT NOT NULL,
  address TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('primary','alias','forward','group','catchall','prefix','external_rule')),
  mailbox_id INTEGER REFERENCES mailboxes(id) ON DELETE SET NULL,
  group_id INTEGER REFERENCES groups(id) ON DELETE SET NULL,
  targets_json TEXT NOT NULL DEFAULT '[]',
  pm_rule_id INTEGER,
  is_prefix INTEGER NOT NULL DEFAULT 0,
  is_catchall INTEGER NOT NULL DEFAULT 0,
  note TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
INSERT INTO addresses_v2 SELECT id,org_id,domain_id,local_part,address,kind,mailbox_id,group_id,targets_json,pm_rule_id,is_prefix,is_catchall,note,created_at,updated_at FROM addresses;
DROP TABLE addresses;
ALTER TABLE addresses_v2 RENAME TO addresses;
CREATE INDEX idx_addresses_mailbox ON addresses(mailbox_id);
CREATE INDEX idx_addresses_group ON addresses(group_id);
ALTER TABLE addresses ADD COLUMN connection_id INTEGER NOT NULL DEFAULT 0 REFERENCES mail_connections(id);
ALTER TABLE addresses ADD COLUMN domain_binding_id INTEGER REFERENCES domain_bindings(id);
ALTER TABLE addresses ADD COLUMN address_key TEXT NOT NULL DEFAULT '';
ALTER TABLE addresses ADD COLUMN management_mode TEXT NOT NULL DEFAULT 'api' CHECK(management_mode IN ('api','external'));
ALTER TABLE addresses ADD COLUMN sync_state TEXT NOT NULL DEFAULT 'unknown' CHECK(sync_state IN ('synced','pending','error','unknown'));
ALTER TABLE addresses ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0);

CREATE TABLE mailbox_forwardings (
  id                  INTEGER PRIMARY KEY,
  mailbox_id          INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
  targets_json        TEXT NOT NULL DEFAULT '[]',
  delivery_mode       TEXT NOT NULL CHECK(delivery_mode IN ('redirect','copy')),
  remote_status_json  TEXT NOT NULL DEFAULT '{}',
  sync_state          TEXT NOT NULL DEFAULT 'unknown' CHECK(sync_state IN ('synced','pending','error','unknown')),
  revision            INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0),
  created_at          TEXT NOT NULL,
  updated_at          TEXT NOT NULL,
  UNIQUE(mailbox_id)
);

ALTER TABLE identities ADD COLUMN authorization_source TEXT NOT NULL DEFAULT 'admin' CHECK(authorization_source IN ('provider','admin'));
ALTER TABLE identities ADD COLUMN authorization_status TEXT NOT NULL DEFAULT 'allowed' CHECK(authorization_status IN ('allowed','unverified','denied'));
ALTER TABLE identities ADD COLUMN authorization_checked_at TEXT;
ALTER TABLE identities ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0);

CREATE TABLE provider_resources (
  id                      INTEGER PRIMARY KEY,
  connection_id           INTEGER NOT NULL REFERENCES mail_connections(id) ON DELETE CASCADE,
  resource_type           TEXT NOT NULL,
  remote_key              TEXT NOT NULL,
  remote_locator_json     TEXT NOT NULL DEFAULT '{}',
  purpose                 TEXT NOT NULL CHECK(purpose IN ('domain','mailbox','routing','forwarding','sender_identity','login_credential')),
  owned_by_mailhearth     INTEGER NOT NULL DEFAULT 0 CHECK(owned_by_mailhearth IN (0,1)),
  last_seen_at            TEXT,
  remote_state            TEXT NOT NULL CHECK(remote_state IN ('present','missing','inaccessible','unknown','external')),
  domain_binding_id       INTEGER REFERENCES domain_bindings(id),
  mailbox_id              INTEGER REFERENCES mailboxes(id),
  address_id              INTEGER REFERENCES addresses(id),
  identity_id             INTEGER REFERENCES identities(id),
  credential_id           INTEGER REFERENCES credentials(id),
  mailbox_forwarding_id   INTEGER REFERENCES mailbox_forwardings(id),
  created_at              TEXT NOT NULL,
  updated_at              TEXT NOT NULL,
  UNIQUE(connection_id, resource_type, remote_key, purpose),
  CHECK(
    CASE WHEN domain_binding_id IS NOT NULL THEN 1 ELSE 0 END +
    CASE WHEN mailbox_id IS NOT NULL THEN 1 ELSE 0 END +
    CASE WHEN address_id IS NOT NULL THEN 1 ELSE 0 END +
    CASE WHEN identity_id IS NOT NULL THEN 1 ELSE 0 END +
    CASE WHEN credential_id IS NOT NULL THEN 1 ELSE 0 END +
    CASE WHEN mailbox_forwarding_id IS NOT NULL THEN 1 ELSE 0 END = 1
  )
);
CREATE INDEX idx_provider_resources_mailbox ON provider_resources(mailbox_id);
CREATE INDEX idx_provider_resources_address ON provider_resources(address_id);

CREATE TABLE operations (
  id                TEXT PRIMARY KEY,
  org_id            INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  actor_member_id   INTEGER REFERENCES members(id),
  kind              TEXT NOT NULL,
  request_id        TEXT NOT NULL,
  request_digest    TEXT NOT NULL,
  payload_enc       TEXT,
  status            TEXT NOT NULL CHECK(status IN ('queued','running','succeeded','failed','unknown','needs_action','cancelled')),
  result_json       TEXT NOT NULL DEFAULT '{}',
  error_code        TEXT,
  claim_secret_enc  TEXT,
  secret_claimed_at TEXT,
  created_at        TEXT NOT NULL,
  updated_at        TEXT NOT NULL,
  UNIQUE(org_id, actor_member_id, request_id)
);
CREATE INDEX idx_operations_kind ON operations(org_id, kind, status);

CREATE TABLE operation_steps (
  operation_id     TEXT NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
  step_key         TEXT NOT NULL,
  sequence         INTEGER NOT NULL CHECK(sequence > 0),
  status           TEXT NOT NULL CHECK(status IN ('pending','running','succeeded','failed','unknown','external_reported')),
  remote_ref_json  TEXT NOT NULL DEFAULT '{}',
  result_json      TEXT NOT NULL DEFAULT '{}',
  error_code       TEXT,
  started_at       TEXT,
  finished_at      TEXT,
  PRIMARY KEY(operation_id, step_key)
);

CREATE TABLE operation_locks (
  org_id        INTEGER NOT NULL,
  resource_key  TEXT NOT NULL,
  operation_id  TEXT NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
  PRIMARY KEY(org_id, resource_key)
);

CREATE TABLE submissions (
  id                    TEXT PRIMARY KEY,
  mailbox_id            INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
  member_id             INTEGER NOT NULL REFERENCES members(id),
  request_id            TEXT NOT NULL,
  request_digest        TEXT NOT NULL,
  message_id            TEXT NOT NULL,
  message_sha256        TEXT NOT NULL,
  envelope_enc          TEXT NOT NULL,
  status                TEXT NOT NULL CHECK(status IN ('preparing','queued','running','sent','sent_copy_failed','failed','unknown')),
  smtp_status           TEXT NOT NULL CHECK(smtp_status IN ('not_started','submitting','accepted','rejected','unknown')),
  sent_status           TEXT NOT NULL CHECK(sent_status IN ('not_started','saving','saved','server_managed','failed','unknown')),
  draft_locator_json    TEXT,
  sent_locator_json     TEXT,
  smtp_endpoint_revision INTEGER NOT NULL DEFAULT 1,
  error_code            TEXT,
  created_at            TEXT NOT NULL,
  updated_at            TEXT NOT NULL,
  UNIQUE(mailbox_id, member_id, request_id)
);

-- 迁移 Purelymail 单连接数据。
INSERT INTO mail_connections(org_id, provider_kind, label, enabled, revision, api_base_url, api_username, api_credential_id, domain_scope_json, protocol_defaults_json, last_api_check_at, last_api_check_status, last_api_error_code, last_sync_at, created_at, updated_at)
SELECT a.org_id, 'purelymail', CASE WHEN a.label='' THEN 'Purelymail' ELSE a.label END, 1, 1, 'https://purelymail.com/api/v0', NULL, NULL, '{"mode":"all"}', '{"imap":{"enabled":true,"host":"imap.purelymail.com","port":993,"tlsMode":"tls","caBundleId":null},"smtp":{"enabled":true,"host":"smtp.purelymail.com","port":465,"tlsMode":"tls","caBundleId":null},"managesieve":{"enabled":true,"host":"mailserver.purelymail.com","port":4190,"tlsMode":"starttls","caBundleId":null}}', a.last_sync_at, CASE WHEN a.last_error='' THEN 'never' ELSE 'failed' END, CASE WHEN a.last_error='' THEN NULL ELSE 'upstream_failed' END, a.last_sync_at, a.created_at, a.updated_at
FROM purelymail_accounts a;

INSERT INTO credentials(org_id, connection_id, mailbox_id, purpose, source, secret_enc, generation, state, hint, created_at, updated_at)
SELECT c.org_id, c.id, NULL, 'api', 'entered', a.api_token_enc, 1, 'active', a.token_hint, a.created_at, a.updated_at
FROM purelymail_accounts a JOIN mail_connections c ON c.org_id=a.org_id AND c.provider_kind='purelymail';

UPDATE mail_connections
SET api_credential_id=(SELECT credentials.id FROM credentials WHERE credentials.connection_id=mail_connections.id AND credentials.purpose='api' LIMIT 1)
WHERE provider_kind!='manual';

UPDATE mailboxes
SET connection_id=(SELECT mail_connections.id FROM mail_connections WHERE mail_connections.org_id=mailboxes.org_id AND mail_connections.provider_kind='purelymail' LIMIT 1),
    address_key=LOWER(mailboxes.address),
    management_mode='api',
    remote_state='present',
    folder_mapping_json='{"sent":null,"drafts":null,"trash":null,"junk":null,"archive":null}';

UPDATE addresses
SET connection_id=(SELECT mail_connections.id FROM mail_connections WHERE mail_connections.org_id=addresses.org_id AND mail_connections.provider_kind='purelymail' LIMIT 1),
    address_key=LOWER(addresses.address),
    management_mode='api',
    sync_state=CASE WHEN addresses.pm_rule_id IS NULL THEN 'unknown' ELSE 'synced' END;

INSERT INTO mailbox_endpoints(mailbox_id, protocol, network_mode, network_override_json, username, credential_id, revision, check_status, capabilities_json, created_at, updated_at)
SELECT b.id, 'imap', 'inherit', NULL, b.address, NULL, 1, 'never', '{}', b.created_at, b.updated_at FROM mailboxes b;
INSERT INTO mailbox_endpoints(mailbox_id, protocol, network_mode, network_override_json, username, credential_id, revision, check_status, capabilities_json, created_at, updated_at)
SELECT b.id, 'smtp', 'inherit', NULL, b.address, NULL, 1, 'never', '{}', b.created_at, b.updated_at FROM mailboxes b;
INSERT INTO mailbox_endpoints(mailbox_id, protocol, network_mode, network_override_json, username, credential_id, revision, check_status, capabilities_json, created_at, updated_at)
SELECT b.id, 'managesieve', 'inherit', NULL, b.address, NULL, 1, 'never', '{}', b.created_at, b.updated_at FROM mailboxes b;

INSERT INTO credentials(org_id, connection_id, mailbox_id, purpose, source, secret_enc, generation, state, hint, created_at, updated_at)
SELECT b.org_id, b.connection_id, b.id, 'mail', 'purelymail_app_password', b.credential_enc, 1, 'active', b.credential_label, b.created_at, b.updated_at
FROM mailboxes b WHERE b.credential_enc!='';

UPDATE mailbox_endpoints
SET credential_id=(SELECT credentials.id FROM credentials WHERE credentials.mailbox_id=mailbox_endpoints.mailbox_id AND credentials.purpose='mail' LIMIT 1)
WHERE mailbox_id IN (SELECT id FROM mailboxes WHERE credential_enc!='');

UPDATE identities
SET authorization_source='admin', authorization_status='allowed', revision=1;

INSERT INTO domain_bindings(org_id, domain_id, connection_id, management_mode, remote_state, provider_settings_json, dns_status_json, dns_checked_at, revision, created_at, updated_at)
SELECT d.org_id, d.id, c.id, CASE WHEN d.is_shared=1 THEN 'external' ELSE 'api' END, 'present',
  json_object('isShared',d.is_shared,'allowAccountReset',d.allow_account_reset,'symbolicSubaddressing',d.symbolic_subaddressing),
  json_object('mx',CASE WHEN d.dns_mx IS NULL THEN 'unknown' WHEN d.dns_mx=1 THEN 'pass' ELSE 'fail' END,
              'spf',CASE WHEN d.dns_spf IS NULL THEN 'unknown' WHEN d.dns_spf=1 THEN 'pass' ELSE 'fail' END,
              'dkim',CASE WHEN d.dns_dkim IS NULL THEN 'unknown' WHEN d.dns_dkim=1 THEN 'pass' ELSE 'fail' END,
              'dmarc',CASE WHEN d.dns_dmarc IS NULL THEN 'unknown' WHEN d.dns_dmarc=1 THEN 'pass' ELSE 'fail' END),
  d.dns_checked_at, 1, d.created_at, d.updated_at
FROM domains d JOIN mail_connections c ON c.org_id=d.org_id AND c.provider_kind='purelymail';

UPDATE mailboxes
SET domain_binding_id=(SELECT domain_bindings.id FROM domain_bindings WHERE domain_bindings.domain_id=mailboxes.domain_id LIMIT 1)
WHERE domain_id IS NOT NULL;

UPDATE addresses SET domain_binding_id=(SELECT id FROM domain_bindings WHERE domain_id=addresses.domain_id AND connection_id=addresses.connection_id);

INSERT INTO mailbox_forwardings(mailbox_id,targets_json,delivery_mode,remote_status_json,sync_state,revision,created_at,updated_at)
SELECT b.id,a.targets_json,'redirect','{}','synced',1,a.created_at,a.updated_at
FROM addresses a JOIN mailboxes b ON b.connection_id=a.connection_id AND b.address_key=a.address_key
WHERE a.kind='forward' AND a.pm_rule_id IS NOT NULL;

UPDATE addresses SET kind='primary',mailbox_id=(SELECT id FROM mailboxes WHERE connection_id=addresses.connection_id AND address_key=addresses.address_key)
WHERE EXISTS(SELECT 1 FROM mailboxes WHERE connection_id=addresses.connection_id AND address_key=addresses.address_key);

CREATE UNIQUE INDEX idx_mailboxes_connection_address ON mailboxes(connection_id,address_key);
CREATE UNIQUE INDEX idx_addresses_connection_address ON addresses(connection_id,address_key) WHERE kind!='catchall';
CREATE UNIQUE INDEX idx_addresses_catchall ON addresses(domain_binding_id) WHERE kind='catchall';

INSERT INTO provider_resources(connection_id, resource_type, remote_key, remote_locator_json, purpose, owned_by_mailhearth, last_seen_at, remote_state, domain_binding_id, created_at, updated_at)
SELECT c.id, 'domain', LOWER(d.name), json_object('domain',LOWER(d.name)), 'domain', 0, d.updated_at, 'present', db.id, d.created_at, d.updated_at
FROM domains d JOIN mail_connections c ON c.org_id=d.org_id AND c.provider_kind='purelymail'
LEFT JOIN domain_bindings db ON db.domain_id=d.id AND db.connection_id=c.id;

INSERT INTO provider_resources(connection_id, resource_type, remote_key, remote_locator_json, purpose, owned_by_mailhearth, last_seen_at, remote_state, mailbox_id, created_at, updated_at)
SELECT b.connection_id, 'mailbox', b.address_key, json_object('address',b.address), 'mailbox', 0, b.updated_at, 'present', b.id, b.created_at, b.updated_at
FROM mailboxes b WHERE b.connection_id!=0;

INSERT INTO provider_resources(connection_id, resource_type, remote_key, remote_locator_json, purpose, owned_by_mailhearth, last_seen_at, remote_state, address_id, created_at, updated_at)
SELECT a.connection_id, 'routing_rule', CAST(a.pm_rule_id AS TEXT), json_object('address',a.address), 'routing', 0, a.updated_at, 'present', a.id, a.created_at, a.updated_at
FROM addresses a WHERE a.pm_rule_id IS NOT NULL AND a.connection_id!=0 AND a.kind!='primary';

INSERT INTO provider_resources(connection_id,resource_type,remote_key,remote_locator_json,purpose,owned_by_mailhearth,last_seen_at,remote_state,mailbox_forwarding_id,created_at,updated_at)
SELECT a.connection_id,'routing_rule',CAST(a.pm_rule_id AS TEXT),json_object('address',a.address),'forwarding',0,a.updated_at,'present',f.id,a.created_at,a.updated_at
FROM addresses a JOIN mailbox_forwardings f ON f.mailbox_id=a.mailbox_id WHERE a.pm_rule_id IS NOT NULL AND a.kind='primary';

INSERT INTO provider_resources(connection_id, resource_type, remote_key, remote_locator_json, purpose, owned_by_mailhearth, last_seen_at, remote_state, mailbox_id, created_at, updated_at)
SELECT b.connection_id, 'mailbox_identity', b.address_key, json_object('address',b.address), 'sender_identity', 0, b.updated_at, 'present', b.id, b.created_at, b.updated_at
FROM mailboxes b WHERE b.connection_id!=0;

INSERT INTO provider_resources(connection_id, resource_type, remote_key, remote_locator_json, purpose, owned_by_mailhearth, last_seen_at, remote_state, credential_id, created_at, updated_at)
SELECT c.connection_id, 'app_password', CAST(c.id AS TEXT), json_object('mailboxId',c.mailbox_id), 'login_credential', 0, c.updated_at, 'present', c.id, c.created_at, c.updated_at
FROM credentials c WHERE c.purpose='mail' AND c.connection_id!=0;
