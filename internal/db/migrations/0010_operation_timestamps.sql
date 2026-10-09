ALTER TABLE operation_steps ADD COLUMN created_at TEXT NOT NULL DEFAULT '';
ALTER TABLE operation_steps ADD COLUMN updated_at TEXT NOT NULL DEFAULT '';
UPDATE operation_steps SET created_at=COALESCE(started_at,(SELECT created_at FROM operations WHERE id=operation_id)),updated_at=COALESCE(finished_at,started_at,(SELECT updated_at FROM operations WHERE id=operation_id));
CREATE TRIGGER operation_step_timestamps_insert AFTER INSERT ON operation_steps BEGIN
 UPDATE operation_steps SET created_at=CASE WHEN NEW.created_at='' THEN strftime('%Y-%m-%dT%H:%M:%SZ','now') ELSE NEW.created_at END,updated_at=CASE WHEN NEW.updated_at='' THEN strftime('%Y-%m-%dT%H:%M:%SZ','now') ELSE NEW.updated_at END WHERE operation_id=NEW.operation_id AND step_key=NEW.step_key;
END;
CREATE TRIGGER operation_step_timestamps_update AFTER UPDATE OF status,remote_ref_json,result_json,error_code,started_at,finished_at ON operation_steps WHEN NEW.updated_at=OLD.updated_at BEGIN
 UPDATE operation_steps SET updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE operation_id=NEW.operation_id AND step_key=NEW.step_key;
END;
ALTER TABLE discovery_snapshots ADD COLUMN updated_at TEXT NOT NULL DEFAULT '';
UPDATE discovery_snapshots SET updated_at=created_at;
CREATE TRIGGER discovery_snapshot_timestamps_insert AFTER INSERT ON discovery_snapshots WHEN NEW.updated_at='' BEGIN
 UPDATE discovery_snapshots SET updated_at=NEW.created_at WHERE id=NEW.id;
END;
CREATE TRIGGER discovery_snapshot_timestamps_update AFTER UPDATE OF connection_revision,scope_json,resources_json,complete,expires_at ON discovery_snapshots WHEN NEW.updated_at=OLD.updated_at BEGIN
 UPDATE discovery_snapshots SET updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id=NEW.id;
END;
ALTER TABLE operation_private ADD COLUMN updated_at TEXT NOT NULL DEFAULT '';
UPDATE operation_private SET updated_at=created_at;
CREATE TRIGGER operation_private_timestamps_insert AFTER INSERT ON operation_private WHEN NEW.updated_at='' BEGIN
 UPDATE operation_private SET updated_at=NEW.created_at WHERE operation_id=NEW.operation_id AND field_key=NEW.field_key;
END;
CREATE TRIGGER operation_private_timestamps_update AFTER UPDATE OF secret_enc ON operation_private WHEN NEW.updated_at=OLD.updated_at BEGIN
 UPDATE operation_private SET updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE operation_id=NEW.operation_id AND field_key=NEW.field_key;
END;
