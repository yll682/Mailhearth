ALTER TABLE operations ADD COLUMN additional_permissions_json TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(additional_permissions_json));
