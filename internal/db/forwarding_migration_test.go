package db

import (
	"context"
	"testing"
)

func TestMultiProviderMigrationForwardingRecords(t *testing.T) {
	d, err := Open(testDBPath(t, "forwarding-records.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	statements := []string{
		`INSERT INTO organizations(id,name,created_at) VALUES (1,'转发迁移','2026-10-09T00:00:00Z')`,
		`INSERT INTO mail_connections(id,org_id,provider_kind,label,domain_scope_json,protocol_defaults_json,created_at,updated_at) VALUES (1,1,'manual','转发迁移连接','{"mode":"all"}','{}','2026-10-09T00:00:00Z','2026-10-09T00:00:00Z')`,
		`INSERT INTO mailboxes(id,org_id,connection_id,kind,address,address_key,created_at,updated_at) VALUES (1,1,1,'personal','source@example.org','source@example.org','2026-10-09T00:00:00Z','2026-10-09T00:00:00Z')`,
		`INSERT INTO mailbox_forwardings(id,mailbox_id,targets_json,delivery_mode,remote_status_json,sync_state,revision,created_at,updated_at) VALUES (17,1,'["target@example.net"]','redirect','{"systemVerified":false}','unknown',9,'2026-10-09T00:00:00Z','2026-10-09T00:00:00Z')`,
		`INSERT INTO provider_resources(id,connection_id,resource_type,remote_key,purpose,remote_state,mailbox_forwarding_id,observation_json,created_at,updated_at) VALUES (23,1,'routing_rule','41','forwarding','present',17,'{"summary":{"address":"source@example.org"}}','2026-10-09T00:00:00Z','2026-10-09T00:00:00Z')`,
	}
	for _, statement := range statements {
		if _, err := d.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	body, err := migrationFS.ReadFile("migrations/0013_forwarding_observations.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.migrateStructural(ctx, "forwarding_records_check", string(body)); err != nil {
		t.Fatal(err)
	}
	var id, revision, resourceID int64
	var targets, mode, status, observation string
	if err := d.QueryRow(`SELECT f.id,f.revision,p.id,f.targets_json,f.delivery_mode,f.remote_status_json,p.observation_json FROM mailbox_forwardings f JOIN provider_resources p ON p.mailbox_forwarding_id=f.id`).Scan(&id, &revision, &resourceID, &targets, &mode, &status, &observation); err != nil {
		t.Fatal(err)
	}
	if id != 17 || revision != 9 || resourceID != 23 || targets != `["target@example.net"]` || mode != "redirect" || status != `{"systemVerified":false}` || observation != `{"summary":{"address":"source@example.org"}}` {
		t.Fatal("转发迁移没有保留记录和远程关联")
	}
	if _, err := d.Exec(`UPDATE mailbox_forwardings SET delivery_mode='unverified' WHERE id=17`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE mailbox_forwardings SET delivery_mode='invalid' WHERE id=17`); err == nil {
		t.Fatal("迁移允许无效的转发方式")
	}
	if _, err := d.Exec(`DELETE FROM mailbox_forwardings WHERE id=17`); err == nil {
		t.Fatal("迁移没有保留远程引用的删除限制")
	}
	if _, err := d.Exec(`UPDATE mailbox_forwardings SET mailbox_id=99999 WHERE id=17`); err == nil {
		t.Fatal("迁移没有保留邮箱归属保护")
	}
	var enabled bool
	if err := d.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Fatal("迁移没有恢复外键检查")
	}
}
