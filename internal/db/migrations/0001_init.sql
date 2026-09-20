-- Mailhearth core schema. Timestamps are RFC3339 UTC strings.

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE organizations (
  id            INTEGER PRIMARY KEY,
  name          TEXT NOT NULL,
  settings_json TEXT NOT NULL DEFAULT '{}',
  created_at    TEXT NOT NULL
);

CREATE TABLE purelymail_accounts (
  id            INTEGER PRIMARY KEY,
  org_id        INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  label         TEXT NOT NULL DEFAULT '',
  api_token_enc TEXT NOT NULL,
  token_hint    TEXT NOT NULL DEFAULT '',
  credit        TEXT NOT NULL DEFAULT '',
  last_sync_at  TEXT,
  last_error    TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);

CREATE TABLE roles (
  id               INTEGER PRIMARY KEY,
  org_id           INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  key              TEXT NOT NULL,
  name             TEXT NOT NULL,
  description      TEXT NOT NULL DEFAULT '',
  permissions_json TEXT NOT NULL DEFAULT '[]',
  builtin          INTEGER NOT NULL DEFAULT 0,
  UNIQUE(org_id, key)
);

CREATE TABLE members (
  id            INTEGER PRIMARY KEY,
  org_id        INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  display_name  TEXT NOT NULL,
  login_email   TEXT NOT NULL,
  password_hash TEXT NOT NULL DEFAULT '',
  role_id       INTEGER NOT NULL REFERENCES roles(id),
  title         TEXT NOT NULL DEFAULT '',
  department    TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL DEFAULT 'invited',
  settings_json TEXT NOT NULL DEFAULT '{}',
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL,
  last_login_at TEXT,
  departed_at   TEXT,
  UNIQUE(org_id, login_email)
);

CREATE TABLE invites (
  id          INTEGER PRIMARY KEY,
  org_id      INTEGER NOT NULL,
  member_id   INTEGER NOT NULL REFERENCES members(id) ON DELETE CASCADE,
  token_hash  TEXT NOT NULL UNIQUE,
  created_by  INTEGER,
  created_at  TEXT NOT NULL,
  expires_at  TEXT NOT NULL,
  accepted_at TEXT
);

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

CREATE TABLE domains (
  id                     INTEGER PRIMARY KEY,
  org_id                 INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name                   TEXT NOT NULL,
  is_shared              INTEGER NOT NULL DEFAULT 0,
  allow_account_reset    INTEGER NOT NULL DEFAULT 0,
  symbolic_subaddressing INTEGER NOT NULL DEFAULT 0,
  dns_mx                 INTEGER,
  dns_spf                INTEGER,
  dns_dkim               INTEGER,
  dns_dmarc              INTEGER,
  dns_checked_at         TEXT,
  status                 TEXT NOT NULL DEFAULT 'active',
  created_at             TEXT NOT NULL,
  updated_at             TEXT NOT NULL,
  UNIQUE(org_id, name)
);

CREATE TABLE mailboxes (
  id               INTEGER PRIMARY KEY,
  org_id           INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  kind             TEXT NOT NULL,
  pm_user          TEXT NOT NULL,
  domain_id        INTEGER REFERENCES domains(id) ON DELETE SET NULL,
  display_name     TEXT NOT NULL DEFAULT '',
  owner_member_id  INTEGER REFERENCES members(id) ON DELETE SET NULL,
  credential_enc   TEXT NOT NULL DEFAULT '',
  credential_label TEXT NOT NULL DEFAULT '',
  credential_at    TEXT,
  status           TEXT NOT NULL DEFAULT 'active',
  imported         INTEGER NOT NULL DEFAULT 0,
  settings_json    TEXT NOT NULL DEFAULT '{}',
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL,
  UNIQUE(org_id, pm_user)
);
CREATE INDEX idx_mailboxes_owner ON mailboxes(owner_member_id);

CREATE TABLE mailbox_access (
  id         INTEGER PRIMARY KEY,
  mailbox_id INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
  member_id  INTEGER NOT NULL REFERENCES members(id) ON DELETE CASCADE,
  level      TEXT NOT NULL DEFAULT 'full',
  granted_by INTEGER,
  granted_at TEXT NOT NULL,
  UNIQUE(mailbox_id, member_id)
);
CREATE INDEX idx_mailbox_access_member ON mailbox_access(member_id);

CREATE TABLE groups (
  id          INTEGER PRIMARY KEY,
  org_id      INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL,
  UNIQUE(org_id, name)
);

CREATE TABLE group_members (
  group_id  INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  member_id INTEGER NOT NULL REFERENCES members(id) ON DELETE CASCADE,
  PRIMARY KEY(group_id, member_id)
);

CREATE TABLE addresses (
  id           INTEGER PRIMARY KEY,
  org_id       INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  domain_id    INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
  local_part   TEXT NOT NULL,
  address      TEXT NOT NULL,
  kind         TEXT NOT NULL,
  mailbox_id   INTEGER REFERENCES mailboxes(id) ON DELETE SET NULL,
  group_id     INTEGER REFERENCES groups(id) ON DELETE SET NULL,
  targets_json TEXT NOT NULL DEFAULT '[]',
  pm_rule_id   INTEGER,
  is_prefix    INTEGER NOT NULL DEFAULT 0,
  is_catchall  INTEGER NOT NULL DEFAULT 0,
  note         TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  UNIQUE(org_id, address)
);
CREATE INDEX idx_addresses_mailbox ON addresses(mailbox_id);
CREATE INDEX idx_addresses_group ON addresses(group_id);

CREATE TABLE identities (
  id             INTEGER PRIMARY KEY,
  mailbox_id     INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
  address        TEXT NOT NULL,
  display_name   TEXT NOT NULL DEFAULT '',
  reply_to       TEXT NOT NULL DEFAULT '',
  signature_html TEXT NOT NULL DEFAULT '',
  is_default     INTEGER NOT NULL DEFAULT 0,
  created_at     TEXT NOT NULL,
  UNIQUE(mailbox_id, address)
);

CREATE TABLE message_state (
  mailbox_id         INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
  message_key        TEXT NOT NULL,
  assignee_member_id INTEGER REFERENCES members(id) ON DELETE SET NULL,
  status             TEXT NOT NULL DEFAULT 'open',
  updated_at         TEXT NOT NULL,
  PRIMARY KEY(mailbox_id, message_key)
);

CREATE TABLE mail_activity (
  id          INTEGER PRIMARY KEY,
  mailbox_id  INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
  message_key TEXT NOT NULL,
  member_id   INTEGER REFERENCES members(id) ON DELETE SET NULL,
  action      TEXT NOT NULL,
  detail      TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL
);
CREATE INDEX idx_mail_activity_msg ON mail_activity(mailbox_id, message_key, id);

CREATE TABLE audit_log (
  id              INTEGER PRIMARY KEY,
  org_id          INTEGER NOT NULL,
  actor_member_id INTEGER,
  action          TEXT NOT NULL,
  target_type     TEXT NOT NULL DEFAULT '',
  target_id       TEXT NOT NULL DEFAULT '',
  detail_json     TEXT NOT NULL DEFAULT '{}',
  created_at      TEXT NOT NULL
);
CREATE INDEX idx_audit_org_time ON audit_log(org_id, id DESC);

CREATE TABLE uploads (
  id         TEXT PRIMARY KEY,
  member_id  INTEGER NOT NULL REFERENCES members(id) ON DELETE CASCADE,
  filename   TEXT NOT NULL,
  mime       TEXT NOT NULL,
  size       INTEGER NOT NULL,
  created_at TEXT NOT NULL
);
