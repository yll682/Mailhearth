CREATE TABLE mail_connections_restrict (
 id INTEGER PRIMARY KEY,
 org_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
 provider_kind TEXT NOT NULL CHECK(provider_kind IN ('purelymail','migadu','manual')),
 label TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
 api_base_url TEXT,
 api_username TEXT,
 api_credential_id INTEGER REFERENCES credentials(id) ON DELETE RESTRICT,
 domain_scope_json TEXT NOT NULL,
 protocol_defaults_json TEXT NOT NULL,
 last_api_check_at TEXT,
 last_api_check_status TEXT NOT NULL DEFAULT 'never' CHECK(last_api_check_status IN ('never','passed','failed')),
 last_api_error_code TEXT,
 last_sync_at TEXT,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
INSERT INTO mail_connections_restrict SELECT * FROM mail_connections;
DROP TABLE mail_connections;
ALTER TABLE mail_connections_restrict RENAME TO mail_connections;

CREATE TABLE credentials_restrict (
 id INTEGER PRIMARY KEY,
 org_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
 connection_id INTEGER NOT NULL REFERENCES mail_connections(id) ON DELETE RESTRICT,
 mailbox_id INTEGER REFERENCES mailboxes(id) ON DELETE RESTRICT,
 purpose TEXT NOT NULL CHECK(purpose IN ('api','mail')),
 source TEXT NOT NULL CHECK(source IN ('entered','purelymail_app_password','migadu_identity','mailbox_password')),
 secret_enc TEXT NOT NULL,
 generation INTEGER NOT NULL DEFAULT 1 CHECK(generation>0),
 state TEXT NOT NULL CHECK(state IN ('candidate','active','retired','revocation_pending','revoked','unknown')),
 hint TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 CHECK((purpose='api' AND mailbox_id IS NULL) OR (purpose='mail' AND mailbox_id IS NOT NULL))
);
INSERT INTO credentials_restrict SELECT * FROM credentials;
DROP TABLE credentials;
ALTER TABLE credentials_restrict RENAME TO credentials;

CREATE TABLE domain_bindings_restrict (
 id INTEGER PRIMARY KEY,
 org_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
 domain_id INTEGER NOT NULL REFERENCES domains(id) ON DELETE RESTRICT,
 connection_id INTEGER NOT NULL REFERENCES mail_connections(id) ON DELETE RESTRICT,
 management_mode TEXT NOT NULL CHECK(management_mode IN ('api','external')),
 remote_state TEXT NOT NULL CHECK(remote_state IN ('present','missing','inaccessible','unknown','external')),
 provider_settings_json TEXT NOT NULL DEFAULT '{}',
 dns_status_json TEXT NOT NULL DEFAULT '{}',
 dns_checked_at TEXT,
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 UNIQUE(domain_id,connection_id)
);
INSERT INTO domain_bindings_restrict SELECT * FROM domain_bindings;
DROP TABLE domain_bindings;
ALTER TABLE domain_bindings_restrict RENAME TO domain_bindings;

CREATE TABLE mailboxes_restrict (
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
 connection_id INTEGER NOT NULL REFERENCES mail_connections(id) ON DELETE RESTRICT,
 address_key TEXT NOT NULL DEFAULT '',
 management_mode TEXT NOT NULL DEFAULT 'api' CHECK(management_mode IN ('api','external')),
 remote_state TEXT NOT NULL DEFAULT 'unknown' CHECK(remote_state IN ('present','missing','inaccessible','unknown','external')),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
 access_revision INTEGER NOT NULL DEFAULT 1 CHECK(access_revision>0),
 sent_copy_mode TEXT NOT NULL DEFAULT 'append' CHECK(sent_copy_mode IN ('append','server')),
 folder_mapping_json TEXT NOT NULL DEFAULT '{}',
 domain_binding_id INTEGER REFERENCES domain_bindings(id) ON DELETE RESTRICT,
 CHECK(kind!='shared' OR owner_member_id IS NULL)
);
INSERT INTO mailboxes_restrict SELECT * FROM mailboxes;
DROP TABLE mailboxes;
ALTER TABLE mailboxes_restrict RENAME TO mailboxes;

CREATE TABLE mailbox_endpoints_restrict (
 id INTEGER PRIMARY KEY,
 mailbox_id INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
 protocol TEXT NOT NULL CHECK(protocol IN ('imap','smtp','managesieve')),
 network_mode TEXT NOT NULL CHECK(network_mode IN ('inherit','override','disabled')),
 network_override_json TEXT,
 username TEXT,
 credential_id INTEGER REFERENCES credentials(id) ON DELETE RESTRICT,
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
 check_status TEXT NOT NULL DEFAULT 'never' CHECK(check_status IN ('never','passed','failed','stale')),
 checked_at TEXT,
 checked_versions_json TEXT,
 capabilities_json TEXT NOT NULL DEFAULT '{}',
 last_error_code TEXT,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 UNIQUE(mailbox_id,protocol),
 CHECK(network_mode!='disabled' OR (network_override_json IS NULL AND username IS NULL AND credential_id IS NULL))
);
INSERT INTO mailbox_endpoints_restrict SELECT * FROM mailbox_endpoints;
DROP TABLE mailbox_endpoints;
ALTER TABLE mailbox_endpoints_restrict RENAME TO mailbox_endpoints;

CREATE TABLE addresses_restrict (
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
 updated_at TEXT NOT NULL,
 connection_id INTEGER NOT NULL REFERENCES mail_connections(id) ON DELETE RESTRICT,
 domain_binding_id INTEGER REFERENCES domain_bindings(id) ON DELETE RESTRICT,
 address_key TEXT NOT NULL DEFAULT '',
 management_mode TEXT NOT NULL DEFAULT 'api' CHECK(management_mode IN ('api','external')),
 sync_state TEXT NOT NULL DEFAULT 'unknown' CHECK(sync_state IN ('synced','pending','error','unknown')),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
 desired_targets_json TEXT NOT NULL DEFAULT '[]',
 observed_targets_json TEXT NOT NULL DEFAULT '[]'
);
INSERT INTO addresses_restrict SELECT * FROM addresses;
DROP TABLE addresses;
ALTER TABLE addresses_restrict RENAME TO addresses;

CREATE TABLE mailbox_forwardings_restrict (
 id INTEGER PRIMARY KEY,
 mailbox_id INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE RESTRICT,
 targets_json TEXT NOT NULL DEFAULT '[]',
 delivery_mode TEXT NOT NULL CHECK(delivery_mode IN ('redirect','copy')),
 remote_status_json TEXT NOT NULL DEFAULT '{}',
 sync_state TEXT NOT NULL DEFAULT 'unknown' CHECK(sync_state IN ('synced','pending','error','unknown')),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 UNIQUE(mailbox_id)
);
INSERT INTO mailbox_forwardings_restrict SELECT * FROM mailbox_forwardings;
DROP TABLE mailbox_forwardings;
ALTER TABLE mailbox_forwardings_restrict RENAME TO mailbox_forwardings;

CREATE TABLE provider_resources_restrict (
 id INTEGER PRIMARY KEY,
 connection_id INTEGER NOT NULL REFERENCES mail_connections(id) ON DELETE RESTRICT,
 resource_type TEXT NOT NULL,
 remote_key TEXT NOT NULL,
 remote_locator_json TEXT NOT NULL DEFAULT '{}',
 purpose TEXT NOT NULL CHECK(purpose IN ('domain','mailbox','routing','forwarding','sender_identity','login_credential')),
 owned_by_mailhearth INTEGER NOT NULL DEFAULT 0 CHECK(owned_by_mailhearth IN (0,1)),
 last_seen_at TEXT,
 remote_state TEXT NOT NULL CHECK(remote_state IN ('present','missing','inaccessible','unknown','external')),
 domain_binding_id INTEGER REFERENCES domain_bindings(id) ON DELETE RESTRICT,
 mailbox_id INTEGER REFERENCES mailboxes(id) ON DELETE RESTRICT,
 address_id INTEGER REFERENCES addresses(id) ON DELETE RESTRICT,
 identity_id INTEGER REFERENCES identities(id) ON DELETE RESTRICT,
 credential_id INTEGER REFERENCES credentials(id) ON DELETE RESTRICT,
 mailbox_forwarding_id INTEGER REFERENCES mailbox_forwardings(id) ON DELETE RESTRICT,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 UNIQUE(connection_id,resource_type,remote_key,purpose),
 CHECK((domain_binding_id IS NOT NULL)+(mailbox_id IS NOT NULL)+(address_id IS NOT NULL)+(identity_id IS NOT NULL)+(credential_id IS NOT NULL)+(mailbox_forwarding_id IS NOT NULL)=1)
);
INSERT INTO provider_resources_restrict SELECT * FROM provider_resources;
DROP TABLE provider_resources;
ALTER TABLE provider_resources_restrict RENAME TO provider_resources;

CREATE TABLE operations_restrict (
 id TEXT PRIMARY KEY,
 org_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
 actor_member_id INTEGER NOT NULL REFERENCES members(id) ON DELETE RESTRICT,
 kind TEXT NOT NULL,
 request_id TEXT NOT NULL,
 request_digest TEXT NOT NULL,
 payload_enc TEXT,
 status TEXT NOT NULL CHECK(status IN ('queued','running','succeeded','failed','unknown','needs_action','cancelled')),
 result_json TEXT NOT NULL DEFAULT '{}',
 error_code TEXT,
 claim_secret_enc TEXT,
 secret_claimed_at TEXT,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 required_permission TEXT NOT NULL DEFAULT 'org.manage',
 target_connection_id INTEGER REFERENCES mail_connections(id) ON DELETE RESTRICT,
 target_mailbox_id INTEGER REFERENCES mailboxes(id) ON DELETE RESTRICT,
 claim_secret_expires_at TEXT,
 raw_request_digest TEXT,
 UNIQUE(org_id,actor_member_id,request_id)
);
INSERT INTO operations_restrict SELECT * FROM operations;
DROP TABLE operations;
ALTER TABLE operations_restrict RENAME TO operations;

CREATE TABLE submissions_restrict (
 id TEXT PRIMARY KEY,
 mailbox_id INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE RESTRICT,
 member_id INTEGER NOT NULL REFERENCES members(id) ON DELETE RESTRICT,
 request_id TEXT NOT NULL,
 request_digest TEXT NOT NULL,
 message_id TEXT NOT NULL,
 message_sha256 TEXT NOT NULL,
 envelope_enc TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('preparing','queued','running','sent','sent_copy_failed','failed','unknown')),
 smtp_status TEXT NOT NULL CHECK(smtp_status IN ('not_started','submitting','accepted','rejected','unknown')),
 sent_status TEXT NOT NULL CHECK(sent_status IN ('not_started','saving','saved','server_managed','failed','unknown')),
 draft_locator_json TEXT,
 sent_locator_json TEXT,
 smtp_endpoint_revision INTEGER NOT NULL DEFAULT 1,
 error_code TEXT,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 org_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
 session_hash TEXT NOT NULL DEFAULT '',
 execution_member_id INTEGER NOT NULL REFERENCES members(id) ON DELETE RESTRICT,
 connection_revision INTEGER NOT NULL DEFAULT 1,
 access_revision INTEGER NOT NULL DEFAULT 1,
 identity_revision INTEGER NOT NULL DEFAULT 1,
 sent_copy_mode TEXT NOT NULL DEFAULT 'append' CHECK(sent_copy_mode IN ('append','server')),
 staging_folder TEXT NOT NULL DEFAULT '',
 sent_folder TEXT,
 cleanup_error_code TEXT,
 UNIQUE(mailbox_id,member_id,request_id)
);
INSERT INTO submissions_restrict SELECT * FROM submissions;
DROP TABLE submissions;
ALTER TABLE submissions_restrict RENAME TO submissions;
