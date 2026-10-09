ALTER TABLE provider_resources ADD COLUMN observation_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(observation_json));
ALTER TABLE provider_resources ADD COLUMN observed_at TEXT;
