-- Mailhearth schema. Timestamps are RFC3339 UTC strings
-- (strftime('%Y-%m-%dT%H:%M:%SZ'), the same format as db.Now()).
--
-- Referential rules:
--   * ON DELETE RESTRICT protects rows that remote resources, history or
--     audit trails still point at; the application checks first and returns
--     a specific error code, the constraint is the backstop.
--   * ON DELETE CASCADE is used only for rows owned outright by the parent.
--   * Tenant scope (a row and the rows it references belong to the same
--     organization / mail connection) is enforced by the scope_violations
--     view and its triggers at the end of this file.

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE organizations (
  id            INTEGER PRIMARY KEY,
  name          TEXT NOT NULL,
  settings_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(settings_json)),
  created_at    TEXT NOT NULL
);

-- Identity and access ------------------------------------------------------

CREATE TABLE roles (
  id               INTEGER PRIMARY KEY,
  org_id           INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  key              TEXT NOT NULL,
  name             TEXT NOT NULL,
  description      TEXT NOT NULL DEFAULT '',
  permissions_json TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(permissions_json)),
  builtin          INTEGER NOT NULL DEFAULT 0 CHECK(builtin IN (0,1)),
  UNIQUE(org_id, key)
);

CREATE TABLE members (
  id            INTEGER PRIMARY KEY,
  org_id        INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  display_name  TEXT NOT NULL,
  login_email   TEXT NOT NULL,
  password_hash TEXT NOT NULL DEFAULT '',
  role_id       INTEGER NOT NULL REFERENCES roles(id) ON DELETE RESTRICT,
  title         TEXT NOT NULL DEFAULT '',
  department    TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL DEFAULT 'invited' CHECK(status IN ('invited','active','disabled','departed')),
  settings_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(settings_json)),
  revision      INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0),
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL,
  last_login_at TEXT,
  departed_at   TEXT,
  UNIQUE(org_id, login_email)
);
CREATE INDEX idx_members_role ON members(role_id);

CREATE TABLE invites (
  id          INTEGER PRIMARY KEY,
  org_id      INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  member_id   INTEGER NOT NULL REFERENCES members(id) ON DELETE CASCADE,
  token_hash  TEXT NOT NULL UNIQUE,
  created_by  INTEGER REFERENCES members(id) ON DELETE SET NULL,
  created_at  TEXT NOT NULL,
  expires_at  TEXT NOT NULL,
  accepted_at TEXT
);
CREATE INDEX idx_invites_org ON invites(org_id);
CREATE INDEX idx_invites_member ON invites(member_id);
CREATE INDEX idx_invites_created_by ON invites(created_by);

CREATE TABLE sessions (
  id           INTEGER PRIMARY KEY,
  member_id    INTEGER NOT NULL REFERENCES members(id) ON DELETE CASCADE,
  token_hash   TEXT NOT NULL UNIQUE,
  created_at   TEXT NOT NULL,
  expires_at   TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  ip           TEXT NOT NULL DEFAULT '',
  user_agent   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_sessions_member ON sessions(member_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);

CREATE TABLE groups (
  id          INTEGER PRIMARY KEY,
  org_id      INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  revision    INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0),
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL,
  UNIQUE(org_id, name)
);

CREATE TABLE group_members (
  group_id  INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  member_id INTEGER NOT NULL REFERENCES members(id) ON DELETE CASCADE,
  PRIMARY KEY(group_id, member_id)
);
CREATE INDEX idx_group_members_member ON group_members(member_id);

CREATE TABLE audit_log (
  id              INTEGER PRIMARY KEY,
  org_id          INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  -- Deliberately not a foreign key: the trail keeps the id of members
  -- that have since been deleted.
  actor_member_id INTEGER,
  action          TEXT NOT NULL,
  target_type     TEXT NOT NULL DEFAULT '',
  target_id       TEXT NOT NULL DEFAULT '',
  detail_json     TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(detail_json)),
  created_at      TEXT NOT NULL
);
CREATE INDEX idx_audit_org_time ON audit_log(org_id, id DESC);

-- Mail connections and credentials -----------------------------------------

CREATE TABLE mail_connections (
  id                     INTEGER PRIMARY KEY,
  org_id                 INTEGER NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  provider_kind          TEXT NOT NULL CHECK(provider_kind IN ('purelymail','migadu','manual')),
  label                  TEXT NOT NULL,
  enabled                INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
  revision               INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0),
  api_base_url           TEXT,
  api_username           TEXT,
  api_credential_id      INTEGER REFERENCES credentials(id) ON DELETE RESTRICT,
  domain_scope_json      TEXT NOT NULL CHECK(json_valid(domain_scope_json)),
  protocol_defaults_json TEXT NOT NULL CHECK(json_valid(protocol_defaults_json)),
  last_api_check_at      TEXT,
  last_api_check_status  TEXT NOT NULL DEFAULT 'never' CHECK(last_api_check_status IN ('never','passed','failed')),
  last_api_error_code    TEXT,
  last_sync_at           TEXT,
  created_at             TEXT NOT NULL,
  updated_at             TEXT NOT NULL
);
CREATE INDEX idx_mail_connections_org ON mail_connections(org_id);
CREATE INDEX idx_mail_connections_credential ON mail_connections(api_credential_id);

CREATE TABLE credentials (
  id            INTEGER PRIMARY KEY,
  org_id        INTEGER NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  connection_id INTEGER NOT NULL REFERENCES mail_connections(id) ON DELETE RESTRICT,
  mailbox_id    INTEGER REFERENCES mailboxes(id) ON DELETE RESTRICT,
  purpose       TEXT NOT NULL CHECK(purpose IN ('api','mail')),
  source        TEXT NOT NULL CHECK(source IN ('entered','purelymail_app_password','migadu_identity','mailbox_password')),
  secret_enc    TEXT NOT NULL,
  generation    INTEGER NOT NULL DEFAULT 1 CHECK(generation > 0),
  state         TEXT NOT NULL CHECK(state IN ('candidate','active','retired','revocation_pending','revoked','unknown')),
  hint          TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL,
  CHECK((purpose='api' AND mailbox_id IS NULL) OR (purpose='mail' AND mailbox_id IS NOT NULL))
);
CREATE INDEX idx_credentials_org ON credentials(org_id);
CREATE INDEX idx_credentials_connection ON credentials(connection_id);
CREATE INDEX idx_credentials_mailbox ON credentials(mailbox_id);

CREATE TABLE discovery_snapshots (
  id                  INTEGER PRIMARY KEY,
  connection_id       INTEGER NOT NULL REFERENCES mail_connections(id) ON DELETE CASCADE,
  connection_revision INTEGER NOT NULL,
  scope_json          TEXT NOT NULL CHECK(json_valid(scope_json)),
  resources_json      TEXT NOT NULL CHECK(json_valid(resources_json)),
  complete            INTEGER NOT NULL CHECK(complete IN (0,1)),
  created_at          TEXT NOT NULL,
  updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  expires_at          TEXT NOT NULL
);
CREATE INDEX idx_discovery_snapshots_connection ON discovery_snapshots(connection_id, created_at);

-- Domains -------------------------------------------------------------------

CREATE TABLE domains (
  id         INTEGER PRIMARY KEY,
  org_id     INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(org_id, name)
);

CREATE TABLE domain_bindings (
  id                     INTEGER PRIMARY KEY,
  org_id                 INTEGER NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  domain_id              INTEGER NOT NULL REFERENCES domains(id) ON DELETE RESTRICT,
  connection_id          INTEGER NOT NULL REFERENCES mail_connections(id) ON DELETE RESTRICT,
  management_mode        TEXT NOT NULL CHECK(management_mode IN ('api','external')),
  remote_state           TEXT NOT NULL CHECK(remote_state IN ('present','missing','inaccessible','unknown','external')),
  provider_settings_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(provider_settings_json)),
  dns_status_json        TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(dns_status_json)),
  dns_checked_at         TEXT,
  revision               INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0),
  created_at             TEXT NOT NULL,
  updated_at             TEXT NOT NULL,
  UNIQUE(domain_id, connection_id)
);
CREATE INDEX idx_domain_bindings_org ON domain_bindings(org_id);
CREATE INDEX idx_domain_bindings_connection ON domain_bindings(connection_id);

-- Mailboxes -----------------------------------------------------------------

CREATE TABLE mailboxes (
  id                  INTEGER PRIMARY KEY,
  org_id              INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  connection_id       INTEGER NOT NULL REFERENCES mail_connections(id) ON DELETE RESTRICT,
  domain_id           INTEGER REFERENCES domains(id) ON DELETE SET NULL,
  domain_binding_id   INTEGER REFERENCES domain_bindings(id) ON DELETE RESTRICT,
  kind                TEXT NOT NULL CHECK(kind IN ('personal','shared')),
  address             TEXT NOT NULL,
  address_key         TEXT NOT NULL CHECK(address_key = lower(address_key) AND address_key != ''),
  display_name        TEXT NOT NULL DEFAULT '',
  owner_member_id     INTEGER REFERENCES members(id) ON DELETE SET NULL,
  status              TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','suspended','archived')),
  imported            INTEGER NOT NULL DEFAULT 0 CHECK(imported IN (0,1)),
  management_mode     TEXT NOT NULL DEFAULT 'api' CHECK(management_mode IN ('api','external')),
  remote_state        TEXT NOT NULL DEFAULT 'unknown' CHECK(remote_state IN ('present','missing','inaccessible','unknown','external')),
  revision            INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0),
  access_revision     INTEGER NOT NULL DEFAULT 1 CHECK(access_revision > 0),
  sent_copy_mode      TEXT NOT NULL DEFAULT 'append' CHECK(sent_copy_mode IN ('append','server')),
  folder_mapping_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(folder_mapping_json)),
  settings_json       TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(settings_json)),
  created_at          TEXT NOT NULL,
  updated_at          TEXT NOT NULL,
  CHECK(kind != 'shared' OR owner_member_id IS NULL)
);
CREATE UNIQUE INDEX idx_mailboxes_connection_address ON mailboxes(connection_id, address_key);
CREATE INDEX idx_mailboxes_org ON mailboxes(org_id);
CREATE INDEX idx_mailboxes_owner ON mailboxes(owner_member_id);
CREATE INDEX idx_mailboxes_domain ON mailboxes(domain_id);
CREATE INDEX idx_mailboxes_binding ON mailboxes(domain_binding_id);

CREATE TABLE mailbox_access (
  id         INTEGER PRIMARY KEY,
  mailbox_id INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
  member_id  INTEGER NOT NULL REFERENCES members(id) ON DELETE CASCADE,
  level      TEXT NOT NULL DEFAULT 'full' CHECK(level IN ('full','send','read')),
  granted_by INTEGER REFERENCES members(id) ON DELETE SET NULL,
  granted_at TEXT NOT NULL,
  UNIQUE(mailbox_id, member_id)
);
CREATE INDEX idx_mailbox_access_member ON mailbox_access(member_id);
CREATE INDEX idx_mailbox_access_granted_by ON mailbox_access(granted_by);

CREATE TABLE mailbox_endpoints (
  id                    INTEGER PRIMARY KEY,
  mailbox_id            INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
  protocol              TEXT NOT NULL CHECK(protocol IN ('imap','smtp','managesieve')),
  network_mode          TEXT NOT NULL CHECK(network_mode IN ('inherit','override','disabled')),
  network_override_json TEXT CHECK(network_override_json IS NULL OR json_valid(network_override_json)),
  username              TEXT,
  credential_id         INTEGER REFERENCES credentials(id) ON DELETE RESTRICT,
  revision              INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0),
  check_status          TEXT NOT NULL DEFAULT 'never' CHECK(check_status IN ('never','passed','failed','stale')),
  checked_at            TEXT,
  checked_versions_json TEXT,
  capabilities_json     TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(capabilities_json)),
  last_error_code       TEXT,
  created_at            TEXT NOT NULL,
  updated_at            TEXT NOT NULL,
  UNIQUE(mailbox_id, protocol),
  CHECK(network_mode != 'disabled' OR (network_override_json IS NULL AND username IS NULL AND credential_id IS NULL))
);
CREATE INDEX idx_mailbox_endpoints_credential ON mailbox_endpoints(credential_id);

CREATE TABLE mailbox_forwardings (
  id                 INTEGER PRIMARY KEY,
  mailbox_id         INTEGER NOT NULL UNIQUE REFERENCES mailboxes(id) ON DELETE RESTRICT,
  targets_json       TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(targets_json)),
  delivery_mode      TEXT NOT NULL CHECK(delivery_mode IN ('redirect','copy','unverified')),
  remote_status_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(remote_status_json)),
  sync_state         TEXT NOT NULL DEFAULT 'unknown' CHECK(sync_state IN ('synced','pending','error','unknown')),
  revision           INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0),
  created_at         TEXT NOT NULL,
  updated_at         TEXT NOT NULL
);

CREATE TABLE identities (
  id                       INTEGER PRIMARY KEY,
  mailbox_id               INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
  address                  TEXT NOT NULL,
  display_name             TEXT NOT NULL DEFAULT '',
  reply_to                 TEXT NOT NULL DEFAULT '',
  signature_html           TEXT NOT NULL DEFAULT '',
  is_default               INTEGER NOT NULL DEFAULT 0 CHECK(is_default IN (0,1)),
  authorization_source     TEXT NOT NULL DEFAULT 'admin' CHECK(authorization_source IN ('provider','admin')),
  authorization_status     TEXT NOT NULL DEFAULT 'allowed' CHECK(authorization_status IN ('allowed','unverified','denied')),
  authorization_checked_at TEXT,
  revision                 INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0),
  created_at               TEXT NOT NULL,
  updated_at               TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  UNIQUE(mailbox_id, address)
);
CREATE UNIQUE INDEX idx_identities_default ON identities(mailbox_id) WHERE is_default = 1;

-- Routing -------------------------------------------------------------------

CREATE TABLE addresses (
  id                    INTEGER PRIMARY KEY,
  org_id                INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  connection_id         INTEGER NOT NULL REFERENCES mail_connections(id) ON DELETE RESTRICT,
  domain_id             INTEGER NOT NULL REFERENCES domains(id) ON DELETE RESTRICT,
  domain_binding_id     INTEGER REFERENCES domain_bindings(id) ON DELETE RESTRICT,
  local_part            TEXT NOT NULL,
  address               TEXT NOT NULL,
  address_key           TEXT NOT NULL CHECK(address_key = lower(address_key)),
  kind                  TEXT NOT NULL CHECK(kind IN ('primary','alias','forward','group','catchall','prefix','external_rule')),
  mailbox_id            INTEGER REFERENCES mailboxes(id) ON DELETE SET NULL,
  group_id              INTEGER REFERENCES groups(id) ON DELETE SET NULL,
  targets_json          TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(targets_json)),
  desired_targets_json  TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(desired_targets_json)),
  observed_targets_json TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(observed_targets_json)),
  is_prefix             INTEGER NOT NULL DEFAULT 0 CHECK(is_prefix IN (0,1)),
  is_catchall           INTEGER NOT NULL DEFAULT 0 CHECK(is_catchall IN (0,1)),
  note                  TEXT NOT NULL DEFAULT '',
  management_mode       TEXT NOT NULL DEFAULT 'api' CHECK(management_mode IN ('api','external')),
  sync_state            TEXT NOT NULL DEFAULT 'unknown' CHECK(sync_state IN ('synced','pending','error','unknown')),
  revision              INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0),
  created_at            TEXT NOT NULL,
  updated_at            TEXT NOT NULL
);
CREATE UNIQUE INDEX idx_addresses_catchall ON addresses(domain_binding_id) WHERE kind = 'catchall';
CREATE UNIQUE INDEX idx_addresses_connection_address ON addresses(connection_id, address_key) WHERE kind != 'catchall';
CREATE INDEX idx_addresses_org ON addresses(org_id);
CREATE INDEX idx_addresses_domain ON addresses(domain_id);
CREATE INDEX idx_addresses_mailbox ON addresses(mailbox_id);
CREATE INDEX idx_addresses_group ON addresses(group_id);

CREATE TABLE provider_resources (
  id                    INTEGER PRIMARY KEY,
  connection_id         INTEGER NOT NULL REFERENCES mail_connections(id) ON DELETE RESTRICT,
  resource_type         TEXT NOT NULL,
  remote_key            TEXT NOT NULL,
  remote_locator_json   TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(remote_locator_json)),
  purpose               TEXT NOT NULL CHECK(purpose IN ('domain','mailbox','routing','forwarding','sender_identity','login_credential')),
  owned_by_mailhearth   INTEGER NOT NULL DEFAULT 0 CHECK(owned_by_mailhearth IN (0,1)),
  remote_state          TEXT NOT NULL CHECK(remote_state IN ('present','missing','inaccessible','unknown','external')),
  observation_json      TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(observation_json)),
  observed_at           TEXT,
  last_seen_at          TEXT,
  domain_binding_id     INTEGER REFERENCES domain_bindings(id) ON DELETE RESTRICT,
  mailbox_id            INTEGER REFERENCES mailboxes(id) ON DELETE RESTRICT,
  address_id            INTEGER REFERENCES addresses(id) ON DELETE RESTRICT,
  identity_id           INTEGER REFERENCES identities(id) ON DELETE RESTRICT,
  credential_id         INTEGER REFERENCES credentials(id) ON DELETE RESTRICT,
  mailbox_forwarding_id INTEGER REFERENCES mailbox_forwardings(id) ON DELETE RESTRICT,
  created_at            TEXT NOT NULL,
  updated_at            TEXT NOT NULL,
  UNIQUE(connection_id, resource_type, remote_key, purpose),
  CHECK((domain_binding_id IS NOT NULL)+(mailbox_id IS NOT NULL)+(address_id IS NOT NULL)+(identity_id IS NOT NULL)+(credential_id IS NOT NULL)+(mailbox_forwarding_id IS NOT NULL) = 1)
);
CREATE INDEX idx_provider_resources_binding ON provider_resources(domain_binding_id);
CREATE INDEX idx_provider_resources_mailbox ON provider_resources(mailbox_id);
CREATE INDEX idx_provider_resources_address ON provider_resources(address_id);
CREATE INDEX idx_provider_resources_identity ON provider_resources(identity_id);
CREATE INDEX idx_provider_resources_credential ON provider_resources(credential_id);
CREATE INDEX idx_provider_resources_forwarding ON provider_resources(mailbox_forwarding_id);

-- Mail history: kept for audit, so a mailbox with history cannot be deleted
-- (archive it instead).

CREATE TABLE message_state (
  mailbox_id         INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE RESTRICT,
  message_key        TEXT NOT NULL,
  assignee_member_id INTEGER REFERENCES members(id) ON DELETE SET NULL,
  status             TEXT NOT NULL DEFAULT 'open' CHECK(status IN ('open','resolved')),
  updated_at         TEXT NOT NULL,
  PRIMARY KEY(mailbox_id, message_key)
);
CREATE INDEX idx_message_state_assignee ON message_state(assignee_member_id);

CREATE TABLE mail_activity (
  id          INTEGER PRIMARY KEY,
  mailbox_id  INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE RESTRICT,
  message_key TEXT NOT NULL,
  member_id   INTEGER REFERENCES members(id) ON DELETE SET NULL,
  action      TEXT NOT NULL,
  detail      TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL
);
CREATE INDEX idx_mail_activity_msg ON mail_activity(mailbox_id, message_key, id);
CREATE INDEX idx_mail_activity_member ON mail_activity(member_id);

CREATE TABLE uploads (
  id         TEXT PRIMARY KEY,
  member_id  INTEGER NOT NULL REFERENCES members(id) ON DELETE CASCADE,
  filename   TEXT NOT NULL,
  mime       TEXT NOT NULL,
  size       INTEGER NOT NULL CHECK(size >= 0),
  created_at TEXT NOT NULL
);
CREATE INDEX idx_uploads_member ON uploads(member_id, created_at);

CREATE TABLE submissions (
  id                     TEXT PRIMARY KEY,
  org_id                 INTEGER NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  mailbox_id             INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE RESTRICT,
  member_id              INTEGER NOT NULL REFERENCES members(id) ON DELETE RESTRICT,
  execution_member_id    INTEGER NOT NULL REFERENCES members(id) ON DELETE RESTRICT,
  request_id             TEXT NOT NULL,
  request_digest         TEXT NOT NULL,
  session_hash           TEXT NOT NULL DEFAULT '',
  message_id             TEXT NOT NULL,
  message_sha256         TEXT NOT NULL,
  envelope_enc           TEXT NOT NULL,
  status                 TEXT NOT NULL CHECK(status IN ('preparing','queued','running','sent','sent_copy_failed','failed','unknown')),
  smtp_status            TEXT NOT NULL CHECK(smtp_status IN ('not_started','submitting','accepted','rejected','unknown')),
  sent_status            TEXT NOT NULL CHECK(sent_status IN ('not_started','saving','saved','server_managed','failed','unknown')),
  sent_copy_mode         TEXT NOT NULL DEFAULT 'append' CHECK(sent_copy_mode IN ('append','server')),
  draft_locator_json     TEXT,
  sent_locator_json      TEXT,
  staging_folder         TEXT NOT NULL DEFAULT '',
  sent_folder            TEXT,
  smtp_endpoint_revision INTEGER NOT NULL DEFAULT 1,
  connection_revision    INTEGER NOT NULL DEFAULT 1,
  access_revision        INTEGER NOT NULL DEFAULT 1,
  identity_revision      INTEGER NOT NULL DEFAULT 1,
  error_code             TEXT,
  cleanup_error_code     TEXT,
  created_at             TEXT NOT NULL,
  updated_at             TEXT NOT NULL,
  UNIQUE(mailbox_id, member_id, request_id)
);
CREATE INDEX idx_submissions_work ON submissions(status, mailbox_id, created_at);
CREATE INDEX idx_submissions_org ON submissions(org_id);
CREATE INDEX idx_submissions_member ON submissions(member_id);
CREATE INDEX idx_submissions_execution_member ON submissions(execution_member_id);

-- Management operations -------------------------------------------------------

CREATE TABLE operations (
  id                          TEXT PRIMARY KEY,
  org_id                      INTEGER NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  actor_member_id             INTEGER NOT NULL REFERENCES members(id) ON DELETE RESTRICT,
  kind                        TEXT NOT NULL,
  required_permission         TEXT NOT NULL DEFAULT 'org.manage',
  additional_permissions_json TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(additional_permissions_json)),
  request_id                  TEXT NOT NULL,
  request_digest              TEXT NOT NULL,
  raw_request_digest          TEXT,
  payload_enc                 TEXT,
  status                      TEXT NOT NULL CHECK(status IN ('queued','running','succeeded','failed','unknown','needs_action','cancelled')),
  result_json                 TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(result_json)),
  error_code                  TEXT,
  target_connection_id        INTEGER REFERENCES mail_connections(id) ON DELETE RESTRICT,
  target_mailbox_id           INTEGER REFERENCES mailboxes(id) ON DELETE RESTRICT,
  claim_secret_enc            TEXT,
  claim_secret_expires_at     TEXT,
  secret_claimed_at           TEXT,
  created_at                  TEXT NOT NULL,
  updated_at                  TEXT NOT NULL,
  UNIQUE(org_id, actor_member_id, request_id)
);
CREATE INDEX idx_operations_kind ON operations(org_id, kind, status);
CREATE INDEX idx_operations_actor ON operations(actor_member_id);
CREATE INDEX idx_operations_connection_status ON operations(target_connection_id, status);
CREATE INDEX idx_operations_mailbox_status ON operations(target_mailbox_id, status);

CREATE TABLE operation_steps (
  operation_id    TEXT NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
  step_key        TEXT NOT NULL,
  sequence        INTEGER NOT NULL CHECK(sequence > 0),
  status          TEXT NOT NULL CHECK(status IN ('pending','running','succeeded','failed','unknown','external_reported')),
  remote_ref_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(remote_ref_json)),
  result_json     TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(result_json)),
  error_code      TEXT,
  started_at      TEXT,
  finished_at     TEXT,
  created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  PRIMARY KEY(operation_id, step_key)
);

CREATE TABLE operation_private (
  operation_id TEXT NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
  field_key    TEXT NOT NULL,
  secret_enc   TEXT NOT NULL,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  PRIMARY KEY(operation_id, field_key)
);

CREATE TABLE operation_locks (
  org_id       INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  resource_key TEXT NOT NULL,
  operation_id TEXT NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
  PRIMARY KEY(org_id, resource_key)
);
CREATE INDEX idx_operation_locks_operation ON operation_locks(operation_id);

-- updated_at maintenance for rows that are changed by many code paths -------

CREATE TRIGGER identity_updated_at AFTER UPDATE ON identities
WHEN NEW.updated_at = OLD.updated_at BEGIN
  UPDATE identities SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id = NEW.id;
END;

CREATE TRIGGER discovery_snapshot_updated_at AFTER UPDATE OF connection_revision,scope_json,resources_json,complete,expires_at ON discovery_snapshots
WHEN NEW.updated_at = OLD.updated_at BEGIN
  UPDATE discovery_snapshots SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id = NEW.id;
END;

CREATE TRIGGER operation_step_updated_at AFTER UPDATE OF status,remote_ref_json,result_json,error_code,started_at,finished_at ON operation_steps
WHEN NEW.updated_at = OLD.updated_at BEGIN
  UPDATE operation_steps SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE operation_id = NEW.operation_id AND step_key = NEW.step_key;
END;

CREATE TRIGGER operation_private_updated_at AFTER UPDATE OF secret_enc ON operation_private
WHEN NEW.updated_at = OLD.updated_at BEGIN
  UPDATE operation_private SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE operation_id = NEW.operation_id AND field_key = NEW.field_key;
END;

-- Ownership is immutable: a row never moves to another organization,
-- connection or mailbox. ----------------------------------------------------

CREATE TRIGGER role_org_immutable BEFORE UPDATE OF org_id ON roles WHEN NEW.org_id != OLD.org_id BEGIN
  SELECT RAISE(ABORT,'role_org_immutable');
END;
CREATE TRIGGER member_org_immutable BEFORE UPDATE OF org_id ON members WHEN NEW.org_id != OLD.org_id BEGIN
  SELECT RAISE(ABORT,'member_org_immutable');
END;
CREATE TRIGGER group_org_immutable BEFORE UPDATE OF org_id ON groups WHEN NEW.org_id != OLD.org_id BEGIN
  SELECT RAISE(ABORT,'group_org_immutable');
END;
CREATE TRIGGER domain_org_immutable BEFORE UPDATE OF org_id ON domains WHEN NEW.org_id != OLD.org_id BEGIN
  SELECT RAISE(ABORT,'domain_org_immutable');
END;
CREATE TRIGGER connection_kind_immutable BEFORE UPDATE OF provider_kind,org_id ON mail_connections
WHEN NEW.provider_kind != OLD.provider_kind OR NEW.org_id != OLD.org_id BEGIN
  SELECT RAISE(ABORT,'connection_kind_immutable');
END;
CREATE TRIGGER credential_ownership_immutable BEFORE UPDATE OF org_id,connection_id,mailbox_id,purpose ON credentials
WHEN NEW.org_id != OLD.org_id OR NEW.connection_id != OLD.connection_id OR NEW.mailbox_id IS NOT OLD.mailbox_id OR NEW.purpose != OLD.purpose BEGIN
  SELECT RAISE(ABORT,'credential_ownership_immutable');
END;
CREATE TRIGGER binding_ownership_immutable BEFORE UPDATE OF org_id,connection_id,domain_id ON domain_bindings
WHEN NEW.org_id != OLD.org_id OR NEW.connection_id != OLD.connection_id OR NEW.domain_id != OLD.domain_id BEGIN
  SELECT RAISE(ABORT,'binding_ownership_immutable');
END;
CREATE TRIGGER mailbox_connection_immutable BEFORE UPDATE OF connection_id,org_id ON mailboxes
WHEN NEW.connection_id != OLD.connection_id OR NEW.org_id != OLD.org_id BEGIN
  SELECT RAISE(ABORT,'mailbox_connection_immutable');
END;
CREATE TRIGGER endpoint_ownership_immutable BEFORE UPDATE OF mailbox_id,protocol ON mailbox_endpoints
WHEN NEW.mailbox_id != OLD.mailbox_id OR NEW.protocol != OLD.protocol BEGIN
  SELECT RAISE(ABORT,'endpoint_ownership_immutable');
END;
CREATE TRIGGER forwarding_ownership_immutable BEFORE UPDATE OF mailbox_id ON mailbox_forwardings WHEN NEW.mailbox_id != OLD.mailbox_id BEGIN
  SELECT RAISE(ABORT,'forwarding_ownership_immutable');
END;
CREATE TRIGGER identity_ownership_immutable BEFORE UPDATE OF mailbox_id ON identities WHEN NEW.mailbox_id != OLD.mailbox_id BEGIN
  SELECT RAISE(ABORT,'identity_ownership_immutable');
END;
CREATE TRIGGER address_ownership_immutable BEFORE UPDATE OF org_id,connection_id ON addresses
WHEN NEW.org_id != OLD.org_id OR NEW.connection_id != OLD.connection_id BEGIN
  SELECT RAISE(ABORT,'address_ownership_immutable');
END;

-- Tenant scope ----------------------------------------------------------------
-- Every row listed here references rows that must share its organization
-- (and, where it applies, its mail connection). Single-column foreign keys
-- cannot express that, so each insert/update re-checks the row.

CREATE VIEW scope_violations AS
SELECT 'mailbox' AS object_type, b.id AS object_id, b.connection_id AS related_id FROM mailboxes b WHERE
  NOT EXISTS(SELECT 1 FROM mail_connections c WHERE c.id=b.connection_id AND c.org_id=b.org_id)
  OR (b.owner_member_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM members m WHERE m.id=b.owner_member_id AND m.org_id=b.org_id))
  OR (b.domain_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM domains d WHERE d.id=b.domain_id AND d.org_id=b.org_id))
  OR (b.domain_binding_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM domain_bindings d WHERE d.id=b.domain_binding_id AND d.org_id=b.org_id AND d.connection_id=b.connection_id AND d.domain_id=b.domain_id))
UNION ALL
SELECT 'credential', cr.id, cr.connection_id FROM credentials cr WHERE
  NOT EXISTS(SELECT 1 FROM mail_connections c WHERE c.id=cr.connection_id AND c.org_id=cr.org_id)
  OR (cr.mailbox_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM mailboxes b WHERE b.id=cr.mailbox_id AND b.connection_id=cr.connection_id AND b.org_id=cr.org_id))
UNION ALL
SELECT 'connection_credential', c.id, c.api_credential_id FROM mail_connections c WHERE
  c.api_credential_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM credentials cr WHERE cr.id=c.api_credential_id AND cr.connection_id=c.id AND cr.org_id=c.org_id AND cr.purpose='api')
UNION ALL
SELECT 'endpoint', e.id, e.credential_id FROM mailbox_endpoints e WHERE
  e.credential_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM credentials cr WHERE cr.id=e.credential_id AND cr.mailbox_id=e.mailbox_id)
UNION ALL
SELECT 'domain_binding', b.id, b.connection_id FROM domain_bindings b WHERE
  NOT EXISTS(SELECT 1 FROM mail_connections c WHERE c.id=b.connection_id AND c.org_id=b.org_id)
  OR NOT EXISTS(SELECT 1 FROM domains d WHERE d.id=b.domain_id AND d.org_id=b.org_id)
UNION ALL
SELECT 'address', a.id, a.connection_id FROM addresses a WHERE
  NOT EXISTS(SELECT 1 FROM mail_connections c WHERE c.id=a.connection_id AND c.org_id=a.org_id)
  OR NOT EXISTS(SELECT 1 FROM domains d WHERE d.id=a.domain_id AND d.org_id=a.org_id)
  OR (a.mailbox_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM mailboxes b WHERE b.id=a.mailbox_id AND b.org_id=a.org_id AND b.connection_id=a.connection_id))
  OR (a.group_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM groups g WHERE g.id=a.group_id AND g.org_id=a.org_id))
  OR (a.domain_binding_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM domain_bindings b WHERE b.id=a.domain_binding_id AND b.connection_id=a.connection_id AND b.org_id=a.org_id AND b.domain_id=a.domain_id))
UNION ALL
SELECT 'provider_resource', r.id, r.connection_id FROM provider_resources r WHERE
  (r.domain_binding_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM domain_bindings b WHERE b.id=r.domain_binding_id AND b.connection_id=r.connection_id))
  OR (r.mailbox_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM mailboxes b WHERE b.id=r.mailbox_id AND b.connection_id=r.connection_id))
  OR (r.address_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM addresses a WHERE a.id=r.address_id AND a.connection_id=r.connection_id))
  OR (r.identity_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM identities i JOIN mailboxes b ON b.id=i.mailbox_id WHERE i.id=r.identity_id AND b.connection_id=r.connection_id))
  OR (r.credential_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM credentials cr WHERE cr.id=r.credential_id AND cr.connection_id=r.connection_id))
  OR (r.mailbox_forwarding_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM mailbox_forwardings f JOIN mailboxes b ON b.id=f.mailbox_id WHERE f.id=r.mailbox_forwarding_id AND b.connection_id=r.connection_id))
UNION ALL
SELECT 'mailbox_access', a.id, a.member_id FROM mailbox_access a WHERE
  NOT EXISTS(SELECT 1 FROM members m JOIN mailboxes b ON b.id=a.mailbox_id WHERE m.id=a.member_id AND m.org_id=b.org_id)
UNION ALL
SELECT 'member_role', m.id, m.role_id FROM members m WHERE
  NOT EXISTS(SELECT 1 FROM roles r WHERE r.id=m.role_id AND r.org_id=m.org_id)
UNION ALL
SELECT 'group_member', g.group_id, g.member_id FROM group_members g WHERE
  NOT EXISTS(SELECT 1 FROM groups team JOIN members m ON m.org_id=team.org_id WHERE team.id=g.group_id AND m.id=g.member_id)
UNION ALL
SELECT 'submission', s.id, s.mailbox_id FROM submissions s WHERE
  NOT EXISTS(SELECT 1 FROM mailboxes b JOIN members m ON m.org_id=b.org_id AND m.id=s.member_id JOIN members actor ON actor.org_id=b.org_id AND actor.id=s.execution_member_id WHERE b.id=s.mailbox_id AND b.org_id=s.org_id)
UNION ALL
SELECT 'operation', o.id, o.actor_member_id FROM operations o WHERE
  NOT EXISTS(SELECT 1 FROM members m WHERE m.id=o.actor_member_id AND m.org_id=o.org_id)
  OR EXISTS(SELECT 1 FROM mail_connections c WHERE c.id=o.target_connection_id AND c.org_id!=o.org_id)
  OR EXISTS(SELECT 1 FROM mailboxes b WHERE b.id=o.target_mailbox_id AND (b.org_id!=o.org_id OR (o.target_connection_id IS NOT NULL AND b.connection_id!=o.target_connection_id)));

CREATE TRIGGER mailbox_scope_insert AFTER INSERT ON mailboxes BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='mailbox' AND object_id=NEW.id);
END;
CREATE TRIGGER mailbox_scope_update AFTER UPDATE ON mailboxes BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='mailbox' AND object_id=NEW.id);
END;
CREATE TRIGGER credential_scope_insert AFTER INSERT ON credentials BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='credential' AND object_id=NEW.id);
END;
CREATE TRIGGER credential_scope_update AFTER UPDATE ON credentials BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='credential' AND object_id=NEW.id);
END;
CREATE TRIGGER connection_credential_scope_insert AFTER INSERT ON mail_connections BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='connection_credential' AND object_id=NEW.id);
END;
CREATE TRIGGER connection_credential_scope_update AFTER UPDATE OF api_credential_id ON mail_connections BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='connection_credential' AND object_id=NEW.id);
END;
CREATE TRIGGER endpoint_scope_insert AFTER INSERT ON mailbox_endpoints BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='endpoint' AND object_id=NEW.id);
END;
CREATE TRIGGER endpoint_scope_update AFTER UPDATE ON mailbox_endpoints BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='endpoint' AND object_id=NEW.id);
END;
CREATE TRIGGER domain_binding_scope_insert AFTER INSERT ON domain_bindings BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='domain_binding' AND object_id=NEW.id);
END;
CREATE TRIGGER domain_binding_scope_update AFTER UPDATE ON domain_bindings BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='domain_binding' AND object_id=NEW.id);
END;
CREATE TRIGGER address_scope_insert AFTER INSERT ON addresses BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='address' AND object_id=NEW.id);
END;
CREATE TRIGGER address_scope_update AFTER UPDATE ON addresses BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='address' AND object_id=NEW.id);
END;
CREATE TRIGGER provider_resource_scope_insert AFTER INSERT ON provider_resources BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='provider_resource' AND object_id=NEW.id);
END;
CREATE TRIGGER provider_resource_scope_update AFTER UPDATE ON provider_resources BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='provider_resource' AND object_id=NEW.id);
END;
CREATE TRIGGER mailbox_access_scope_insert AFTER INSERT ON mailbox_access BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='mailbox_access' AND object_id=NEW.id);
END;
CREATE TRIGGER mailbox_access_scope_update AFTER UPDATE ON mailbox_access BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='mailbox_access' AND object_id=NEW.id);
END;
CREATE TRIGGER member_role_scope_insert AFTER INSERT ON members BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='member_role' AND object_id=NEW.id);
END;
CREATE TRIGGER member_role_scope_update AFTER UPDATE OF role_id ON members BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='member_role' AND object_id=NEW.id);
END;
CREATE TRIGGER group_member_scope_insert AFTER INSERT ON group_members BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='group_member' AND object_id=NEW.group_id AND related_id=NEW.member_id);
END;
CREATE TRIGGER group_member_scope_update AFTER UPDATE ON group_members BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='group_member' AND object_id=NEW.group_id AND related_id=NEW.member_id);
END;
CREATE TRIGGER submission_scope_insert AFTER INSERT ON submissions BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='submission' AND object_id=NEW.id);
END;
CREATE TRIGGER submission_scope_update AFTER UPDATE OF org_id,mailbox_id,member_id,execution_member_id ON submissions BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='submission' AND object_id=NEW.id);
END;
CREATE TRIGGER operation_scope_insert AFTER INSERT ON operations BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='operation' AND object_id=NEW.id);
END;
CREATE TRIGGER operation_scope_update AFTER UPDATE OF org_id,actor_member_id,target_connection_id,target_mailbox_id ON operations BEGIN
  SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM scope_violations WHERE object_type='operation' AND object_id=NEW.id);
END;
