package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestMultiProviderMigrationRestrictReferences(t *testing.T) {
	d, err := Open(testDBPath(t, "restrict.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	expected := map[string]map[string]string{
		"mail_connections":    {"org_id": "RESTRICT", "api_credential_id": "RESTRICT"},
		"credentials":         {"org_id": "RESTRICT", "connection_id": "RESTRICT", "mailbox_id": "RESTRICT"},
		"domain_bindings":     {"org_id": "RESTRICT", "domain_id": "RESTRICT", "connection_id": "RESTRICT"},
		"mailboxes":           {"connection_id": "RESTRICT", "domain_binding_id": "RESTRICT"},
		"addresses":           {"connection_id": "RESTRICT", "domain_binding_id": "RESTRICT"},
		"mailbox_endpoints":   {"mailbox_id": "CASCADE", "credential_id": "RESTRICT"},
		"mailbox_forwardings": {"mailbox_id": "RESTRICT"},
		"provider_resources":  {"connection_id": "RESTRICT", "domain_binding_id": "RESTRICT", "mailbox_id": "RESTRICT", "address_id": "RESTRICT", "identity_id": "RESTRICT", "credential_id": "RESTRICT", "mailbox_forwarding_id": "RESTRICT"},
		"operations":          {"org_id": "RESTRICT", "actor_member_id": "RESTRICT", "target_connection_id": "RESTRICT", "target_mailbox_id": "RESTRICT"},
		"submissions":         {"mailbox_id": "RESTRICT", "member_id": "RESTRICT", "org_id": "RESTRICT", "execution_member_id": "RESTRICT"},
		"operation_steps":     {"operation_id": "CASCADE"},
	}
	for table, columns := range expected {
		rows, err := d.Query(`PRAGMA foreign_key_list("` + table + `")`)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]string{}
		for rows.Next() {
			var id, sequence int
			var parent, column, target, onUpdate, onDelete, match string
			if err := rows.Scan(&id, &sequence, &parent, &column, &target, &onUpdate, &onDelete, &match); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			found[column] = onDelete
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		for column, rule := range columns {
			if found[column] != rule {
				t.Fatalf("%s.%s 删除规则应为 %s，取得 %s", table, column, rule, found[column])
			}
		}
	}
	for _, table := range []string{"operation_steps", "discovery_snapshots", "operation_private"} {
		var count int
		if err := d.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name IN ('created_at','updated_at')`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 2 {
			t.Fatalf("%s 缺少时间字段", table)
		}
	}
}

func TestMultiProviderMigrationStructuralFailureAtomic(t *testing.T) {
	path := testDBPath(t, "atomic-structure.db")
	base, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	d := &DB{base}
	ctx := context.Background()
	if _, err := d.Exec(`CREATE TABLE schema_migrations(version TEXT PRIMARY KEY,applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0001_init.sql", "0002_multi_provider.sql", "0003_operation_permissions.sql", "0004_submission_context.sql", "0005_association_scopes.sql", "0006_resource_ownership.sql", "0007_management_operations.sql", "0008_routing_state.sql"} {
		if err := applyEmbedded(base, name); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES (?,?)`, name, Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Exec(`INSERT INTO organizations(name,created_at) VALUES ('迁移检查','2026-10-09T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO mail_connections(org_id,provider_kind,label,domain_scope_json,protocol_defaults_json,created_at,updated_at) VALUES (1,'manual','保持已有连接','{"mode":"all"}','{}','2026-10-09T00:00:00Z','2026-10-09T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	before, err := schemaStatements(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	body, err := migrationFS.ReadFile("migrations/0009_restrict_management_references.sql")
	if err != nil {
		t.Fatal(err)
	}
	invalidReference := `INSERT INTO mailbox_forwardings(mailbox_id,delivery_mode,created_at,updated_at) VALUES (999999,'redirect','2026-10-09T00:00:00Z','2026-10-09T00:00:00Z');`
	err = d.migrateStructural(ctx, "test_structural_failure", string(body)+invalidReference)
	if err == nil || !strings.Contains(err.Error(), "foreign key check failed") {
		t.Fatalf("结构迁移没有拒绝无效外键：%v", err)
	}
	after, err := schemaStatements(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("失败迁移改变了 table、index、view 或 trigger")
	}
	var label string
	if err := d.QueryRow(`SELECT label FROM mail_connections WHERE id=1`).Scan(&label); err != nil {
		t.Fatal(err)
	}
	if label != "保持已有连接" {
		t.Fatal("失败迁移改变了已有连接")
	}
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version='test_structural_failure'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("失败迁移保存了完成版本")
	}
	if _, err := d.Exec(`INSERT INTO mailbox_forwardings(mailbox_id,delivery_mode,created_at,updated_at) VALUES (999999,'redirect','2026-10-09T00:00:00Z','2026-10-09T00:00:00Z')`); err == nil {
		t.Fatal("失败迁移后外键检查没有重新启用")
	}
}

func schemaStatements(ctx context.Context, d *DB) (string, error) {
	rows, err := d.QueryContext(ctx, `SELECT type,name,COALESCE(sql,'') FROM sqlite_schema ORDER BY type,name`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var result strings.Builder
	for rows.Next() {
		var kind, name, statement string
		if err := rows.Scan(&kind, &name, &statement); err != nil {
			return "", err
		}
		result.WriteString(kind + "\x00" + name + "\x00" + statement + "\n")
	}
	return result.String(), rows.Err()
}
