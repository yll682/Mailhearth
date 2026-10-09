ALTER TABLE addresses ADD COLUMN desired_targets_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE addresses ADD COLUMN observed_targets_json TEXT NOT NULL DEFAULT '[]';
UPDATE addresses SET desired_targets_json=targets_json,observed_targets_json=targets_json;
