CREATE TABLE mailbox_forwardings_observed (
 id INTEGER PRIMARY KEY,
 mailbox_id INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE RESTRICT,
 targets_json TEXT NOT NULL DEFAULT '[]',
 delivery_mode TEXT NOT NULL CHECK(delivery_mode IN ('redirect','copy','unverified')),
 remote_status_json TEXT NOT NULL DEFAULT '{}',
 sync_state TEXT NOT NULL DEFAULT 'unknown' CHECK(sync_state IN ('synced','pending','error','unknown')),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 UNIQUE(mailbox_id)
);
INSERT INTO mailbox_forwardings_observed SELECT * FROM mailbox_forwardings;
DROP TABLE mailbox_forwardings;
ALTER TABLE mailbox_forwardings_observed RENAME TO mailbox_forwardings;
