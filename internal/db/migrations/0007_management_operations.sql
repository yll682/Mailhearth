ALTER TABLE operations ADD COLUMN claim_secret_expires_at TEXT;
ALTER TABLE operations ADD COLUMN raw_request_digest TEXT;
ALTER TABLE identities ADD COLUMN updated_at TEXT NOT NULL DEFAULT '';
UPDATE identities SET updated_at=created_at;

CREATE TABLE operation_private (
 operation_id TEXT NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
 field_key TEXT NOT NULL,
 secret_enc TEXT NOT NULL,
 created_at TEXT NOT NULL,
 PRIMARY KEY(operation_id,field_key)
);

CREATE TRIGGER identity_updated_at AFTER UPDATE ON identities
WHEN NEW.updated_at=OLD.updated_at BEGIN
 UPDATE identities SET updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id=NEW.id;
END;
