ALTER TABLE operations ADD COLUMN required_permission TEXT NOT NULL DEFAULT 'org.manage';
ALTER TABLE operations ADD COLUMN target_connection_id INTEGER;
ALTER TABLE operations ADD COLUMN target_mailbox_id INTEGER;
CREATE INDEX idx_operations_connection_status ON operations(target_connection_id,status);
CREATE INDEX idx_operations_mailbox_status ON operations(target_mailbox_id,status);
