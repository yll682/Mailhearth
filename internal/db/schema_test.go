package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	_ "modernc.org/sqlite"
)

func testDBPath(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	if os.Getenv("MAILHEARTH_TEST_KEEP_DATA") != "" {
		dir = filepath.Join("..", "..", "data", "integration", "db", strings.ReplaceAll(t.Name(), "/", "-")+"-"+time.Now().UTC().Format("20060102T150405.000000000"))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, name)
}

func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(testDBPath(t, "mailhearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func mustExec(t *testing.T, q Querier, statements ...string) {
	t.Helper()
	for _, statement := range statements {
		if _, err := q.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

func userVersion(t *testing.T, q Querier) int {
	t.Helper()
	var v int
	if err := q.QueryRowContext(t.Context(), `PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestOpenFreshDatabase(t *testing.T) {
	d := openTestDB(t)
	names, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	if got := userVersion(t, d); got != len(names) || got == 0 {
		t.Fatalf("user_version=%d, want %d", got, len(names))
	}
	tables := map[string]bool{}
	rows, err := d.Query(`SELECT name FROM sqlite_schema WHERE type='table'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables[name] = true
	}
	rows.Close()
	for _, want := range []string{"settings", "organizations", "roles", "members", "invites", "sessions", "groups", "group_members", "audit_log", "mail_connections", "credentials", "discovery_snapshots", "domains", "domain_bindings", "mailboxes", "mailbox_access", "mailbox_endpoints", "mailbox_forwardings", "identities", "addresses", "provider_resources", "message_state", "mail_activity", "uploads", "submissions", "operations", "operation_steps", "operation_private", "operation_locks"} {
		if !tables[want] {
			t.Errorf("table %s missing", want)
		}
	}
	// Upgrade-only leftovers must not exist in a fresh schema.
	for _, gone := range []string{"purelymail_accounts", "schema_migrations"} {
		if tables[gone] {
			t.Errorf("legacy table %s still created", gone)
		}
	}
	var legacyColumns int
	if err := d.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('mailboxes') WHERE name IN ('credential_enc','credential_label','credential_at','pm_user')`).Scan(&legacyColumns); err != nil {
		t.Fatal(err)
	}
	if legacyColumns != 0 {
		t.Fatal("mailboxes still carries legacy credential columns")
	}
	var fkOn bool
	if err := d.QueryRow(`PRAGMA foreign_keys`).Scan(&fkOn); err != nil || !fkOn {
		t.Fatalf("foreign keys not enforced: %v", err)
	}
}

func TestOpenTwiceIsNoop(t *testing.T) {
	path := testDBPath(t, "twice.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, d, `INSERT INTO settings(key,value) VALUES ('probe','1')`)
	before := schemaText(t, d)
	d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if schemaText(t, d) != before {
		t.Fatal("second open changed the schema")
	}
	if v, err := GetSetting(t.Context(), d, "probe"); err != nil || v != "1" {
		t.Fatalf("second open lost data: %q %v", v, err)
	}
}

// A database that was not created by this schema (for example one left by
// a pre-release build) is refused instead of being half-initialised.
func TestOpenRejectsForeignDatabase(t *testing.T) {
	path := testDBPath(t, "foreign.db")
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, raw, `CREATE TABLE schema_migrations(version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`)
	raw.Close()
	if d, err := Open(path); err == nil {
		d.Close()
		t.Fatal("opened a database with an unknown schema")
	} else if !strings.Contains(err.Error(), "empty data directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	path := testDBPath(t, "newer.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, d, `PRAGMA user_version=999`)
	d.Close()
	if d, err := Open(path); err == nil {
		d.Close()
		t.Fatal("opened a database from a newer build")
	}
}

// Initialisation runs in one transaction: a failing statement leaves an
// empty database behind, not a partial schema.
func TestOpenFailureRollsBack(t *testing.T) {
	saved := schemaFiles
	t.Cleanup(func() { schemaFiles = saved })
	schemaFiles = fstest.MapFS{"migrations/0001_init.sql": {Data: []byte(`CREATE TABLE a(id INTEGER PRIMARY KEY); CREATE TABLE b(id INTEGER PRIMARY KEY REFERENCES a(id)); INSERT INTO missing VALUES (1);`)}}
	path := testDBPath(t, "rollback.db")
	if d, err := Open(path); err == nil {
		d.Close()
		t.Fatal("broken schema applied")
	}
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var objects int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM sqlite_schema`).Scan(&objects); err != nil {
		t.Fatal(err)
	}
	if objects != 0 || userVersion(t, raw) != 0 {
		t.Fatalf("failed init left %d objects, user_version=%d", objects, userVersion(t, raw))
	}
	// Once the schema is fixed the same file initialises normally.
	schemaFiles = saved
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
}

func foreignKeys(t *testing.T, d *DB, table string) map[string]string {
	t.Helper()
	rows, err := d.Query(`SELECT "from","table",on_delete FROM pragma_foreign_key_list(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := map[string]string{}
	for rows.Next() {
		var column, parent, onDelete string
		if err := rows.Scan(&column, &parent, &onDelete); err != nil {
			t.Fatal(err)
		}
		found[column] = parent + ":" + onDelete
	}
	return found
}

func TestForeignKeyRules(t *testing.T) {
	d := openTestDB(t)
	expected := map[string]map[string]string{
		"members":             {"org_id": "organizations:CASCADE", "role_id": "roles:RESTRICT"},
		"invites":             {"org_id": "organizations:CASCADE", "member_id": "members:CASCADE", "created_by": "members:SET NULL"},
		"audit_log":           {"org_id": "organizations:CASCADE"},
		"mail_connections":    {"org_id": "organizations:RESTRICT", "api_credential_id": "credentials:RESTRICT"},
		"credentials":         {"org_id": "organizations:RESTRICT", "connection_id": "mail_connections:RESTRICT", "mailbox_id": "mailboxes:RESTRICT"},
		"domain_bindings":     {"org_id": "organizations:RESTRICT", "domain_id": "domains:RESTRICT", "connection_id": "mail_connections:RESTRICT"},
		"mailboxes":           {"connection_id": "mail_connections:RESTRICT", "domain_binding_id": "domain_bindings:RESTRICT", "owner_member_id": "members:SET NULL"},
		"mailbox_access":      {"mailbox_id": "mailboxes:CASCADE", "member_id": "members:CASCADE", "granted_by": "members:SET NULL"},
		"addresses":           {"connection_id": "mail_connections:RESTRICT", "domain_id": "domains:RESTRICT", "domain_binding_id": "domain_bindings:RESTRICT"},
		"mailbox_endpoints":   {"mailbox_id": "mailboxes:CASCADE", "credential_id": "credentials:RESTRICT"},
		"mailbox_forwardings": {"mailbox_id": "mailboxes:RESTRICT"},
		"provider_resources":  {"connection_id": "mail_connections:RESTRICT", "domain_binding_id": "domain_bindings:RESTRICT", "mailbox_id": "mailboxes:RESTRICT", "address_id": "addresses:RESTRICT", "identity_id": "identities:RESTRICT", "credential_id": "credentials:RESTRICT", "mailbox_forwarding_id": "mailbox_forwardings:RESTRICT"},
		"message_state":       {"mailbox_id": "mailboxes:RESTRICT"},
		"mail_activity":       {"mailbox_id": "mailboxes:RESTRICT"},
		"operations":          {"org_id": "organizations:RESTRICT", "actor_member_id": "members:RESTRICT", "target_connection_id": "mail_connections:RESTRICT", "target_mailbox_id": "mailboxes:RESTRICT"},
		"submissions":         {"org_id": "organizations:RESTRICT", "mailbox_id": "mailboxes:RESTRICT", "member_id": "members:RESTRICT", "execution_member_id": "members:RESTRICT"},
		"operation_steps":     {"operation_id": "operations:CASCADE"},
		"operation_locks":     {"org_id": "organizations:CASCADE", "operation_id": "operations:CASCADE"},
	}
	for table, columns := range expected {
		found := foreignKeys(t, d, table)
		for column, rule := range columns {
			if found[column] != rule {
				t.Errorf("%s.%s: want %s, got %q", table, column, rule, found[column])
			}
		}
	}
}

// Deleting a parent row has to find its children without a table scan, so
// every foreign key column leads some index (or the primary key).
func TestForeignKeysAreIndexed(t *testing.T) {
	d := openTestDB(t)
	rows, err := d.Query(`SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	for _, table := range tables {
		leading := map[string]bool{}
		var pk string
		if err := d.QueryRow(`SELECT COALESCE((SELECT name FROM pragma_table_info(?) WHERE pk=1),'')`, table).Scan(&pk); err != nil {
			t.Fatal(err)
		}
		leading[pk] = true
		indexes, err := d.Query(`SELECT (SELECT name FROM pragma_index_info(il.name) WHERE seqno=0) FROM pragma_index_list(?) il`, table)
		if err != nil {
			t.Fatal(err)
		}
		for indexes.Next() {
			var column sql.NullString
			if err := indexes.Scan(&column); err != nil {
				t.Fatal(err)
			}
			leading[column.String] = true
		}
		indexes.Close()
		for column := range foreignKeys(t, d, table) {
			if !leading[column] {
				t.Errorf("%s.%s is a foreign key without an index", table, column)
			}
		}
	}
}

func seedMailbox(t *testing.T, d *DB) {
	t.Helper()
	mustExec(t, d,
		`INSERT INTO organizations(id,name,created_at) VALUES (1,'Acme','2026-10-30T00:00:00Z'),(2,'Other','2026-10-30T00:00:00Z')`,
		`INSERT INTO roles(id,org_id,key,name) VALUES (1,1,'owner','Owner'),(2,2,'owner','Owner')`,
		`INSERT INTO members(id,org_id,display_name,login_email,role_id,status,created_at,updated_at) VALUES (1,1,'Alice','alice@acme.test',1,'active','2026-10-30T00:00:00Z','2026-10-30T00:00:00Z'),(2,2,'Mallory','m@other.test',2,'active','2026-10-30T00:00:00Z','2026-10-30T00:00:00Z')`,
		`INSERT INTO mail_connections(id,org_id,provider_kind,label,domain_scope_json,protocol_defaults_json,created_at,updated_at) VALUES (1,1,'manual','Manual','{"mode":"all"}','{}','2026-10-30T00:00:00Z','2026-10-30T00:00:00Z')`,
		`INSERT INTO mailboxes(id,org_id,connection_id,kind,address,address_key,created_at,updated_at) VALUES (1,1,1,'personal','alice@acme.test','alice@acme.test','2026-10-30T00:00:00Z','2026-10-30T00:00:00Z')`,
	)
}

// Mail history and remote references block deletion through the foreign
// keys themselves (formerly a BEFORE DELETE trigger).
func TestReferencesRestrictDelete(t *testing.T) {
	cases := map[string]string{
		"message_state": `INSERT INTO message_state(mailbox_id,message_key,updated_at) VALUES (1,'INBOX/1','2026-10-30T00:00:00Z')`,
		"mail_activity": `INSERT INTO mail_activity(mailbox_id,message_key,action,created_at) VALUES (1,'INBOX/1','read','2026-10-30T00:00:00Z')`,
		"forwarding":    `INSERT INTO mailbox_forwardings(mailbox_id,delivery_mode,created_at,updated_at) VALUES (1,'redirect','2026-10-30T00:00:00Z','2026-10-30T00:00:00Z')`,
	}
	for name, child := range cases {
		t.Run(name, func(t *testing.T) {
			d := openTestDB(t)
			seedMailbox(t, d)
			mustExec(t, d, child)
			if _, err := d.Exec(`DELETE FROM mailboxes WHERE id=1`); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
				t.Fatalf("mailbox with %s was deletable: %v", name, err)
			}
		})
	}
	d := openTestDB(t)
	seedMailbox(t, d)
	mustExec(t, d,
		`INSERT INTO mailbox_forwardings(id,mailbox_id,delivery_mode,created_at,updated_at) VALUES (17,1,'redirect','2026-10-30T00:00:00Z','2026-10-30T00:00:00Z')`,
		`INSERT INTO provider_resources(connection_id,resource_type,remote_key,purpose,remote_state,mailbox_forwarding_id,created_at,updated_at) VALUES (1,'routing_rule','41','forwarding','present',17,'2026-10-30T00:00:00Z','2026-10-30T00:00:00Z')`,
	)
	if _, err := d.Exec(`DELETE FROM mailbox_forwardings WHERE id=17`); err == nil {
		t.Fatal("forwarding referenced by a remote resource was deletable")
	}
	if _, err := d.Exec(`UPDATE mailbox_forwardings SET delivery_mode='invalid' WHERE id=17`); err == nil {
		t.Fatal("invalid delivery mode accepted")
	}
	if _, err := d.Exec(`UPDATE mailbox_forwardings SET mailbox_id=99 WHERE id=17`); err == nil || !strings.Contains(err.Error(), "forwarding_ownership_immutable") {
		t.Fatalf("forwarding moved to another mailbox: %v", err)
	}
	// Without history the mailbox (and its owned rows) can be removed.
	d2 := openTestDB(t)
	seedMailbox(t, d2)
	mustExec(t, d2, `INSERT INTO mailbox_access(mailbox_id,member_id,granted_by,granted_at) VALUES (1,1,1,'2026-10-30T00:00:00Z')`, `DELETE FROM mailboxes WHERE id=1`)
}

func TestTenantScopeEnforced(t *testing.T) {
	d := openTestDB(t)
	seedMailbox(t, d)
	rejected := map[string]string{
		"owner from another org":  `UPDATE mailboxes SET owner_member_id=2 WHERE id=1`,
		"access for another org":  `INSERT INTO mailbox_access(mailbox_id,member_id,granted_at) VALUES (1,2,'2026-10-30T00:00:00Z')`,
		"role from another org":   `UPDATE members SET role_id=2 WHERE id=1`,
		"operation actor scope":   `INSERT INTO operations(id,org_id,actor_member_id,kind,request_id,request_digest,status,created_at,updated_at) VALUES ('op',1,2,'x','r','d','queued','2026-10-30T00:00:00Z','2026-10-30T00:00:00Z')`,
		"shared mailbox w/ owner": `INSERT INTO mailboxes(org_id,connection_id,kind,address,address_key,owner_member_id,created_at,updated_at) VALUES (1,1,'shared','s@acme.test','s@acme.test',1,'2026-10-30T00:00:00Z','2026-10-30T00:00:00Z')`,
		"uppercase address key":   `INSERT INTO mailboxes(org_id,connection_id,kind,address,address_key,created_at,updated_at) VALUES (1,1,'personal','Bob@acme.test','Bob@acme.test','2026-10-30T00:00:00Z','2026-10-30T00:00:00Z')`,
		"unknown member status":   `UPDATE members SET status='zombie' WHERE id=1`,
	}
	for name, statement := range rejected {
		if _, err := d.Exec(statement); err == nil {
			t.Errorf("%s: accepted %s", name, statement)
		}
	}
	mustExec(t, d, `INSERT INTO operations(id,org_id,actor_member_id,kind,request_id,request_digest,status,target_connection_id,target_mailbox_id,created_at,updated_at) VALUES ('ok',1,1,'x','r','d','queued',1,1,'2026-10-30T00:00:00Z','2026-10-30T00:00:00Z')`)
}

// Rows written without timestamps get them from column defaults, and the
// update triggers advance updated_at.
func TestTimestampDefaults(t *testing.T) {
	d := openTestDB(t)
	seedMailbox(t, d)
	mustExec(t, d,
		`INSERT INTO operations(id,org_id,actor_member_id,kind,request_id,request_digest,status,created_at,updated_at) VALUES ('op',1,1,'x','r','d','queued','2026-10-30T00:00:00Z','2026-10-30T00:00:00Z')`,
		`INSERT INTO operation_steps(operation_id,step_key,sequence,status) VALUES ('op','execute',1,'pending')`,
		`INSERT INTO operation_private(operation_id,field_key,secret_enc,created_at) VALUES ('op','password','sealed','2026-10-30T00:00:00Z')`,
	)
	var created, updated string
	if err := d.QueryRow(`SELECT created_at,updated_at FROM operation_steps`).Scan(&created, &updated); err != nil {
		t.Fatal(err)
	}
	if ParseTime(created).IsZero() || ParseTime(updated).IsZero() {
		t.Fatalf("operation step timestamps not set: %q %q", created, updated)
	}
	mustExec(t, d, `UPDATE operation_steps SET updated_at='2000-01-01T00:00:00Z'`, `UPDATE operation_steps SET status='running'`)
	if err := d.QueryRow(`SELECT updated_at FROM operation_steps`).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	if updated == "2000-01-01T00:00:00Z" || ParseTime(updated).IsZero() {
		t.Fatalf("status change did not advance updated_at: %q", updated)
	}
	if err := d.QueryRow(`SELECT updated_at FROM operation_private`).Scan(&updated); err != nil || ParseTime(updated).IsZero() {
		t.Fatalf("operation_private.updated_at not set: %q %v", updated, err)
	}
}

func schemaText(t *testing.T, d *DB) string {
	t.Helper()
	rows, err := d.Query(`SELECT type,name,COALESCE(sql,'') FROM sqlite_schema ORDER BY type,name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var kind, name, statement string
		if err := rows.Scan(&kind, &name, &statement); err != nil {
			t.Fatal(err)
		}
		b.WriteString(kind + "\x00" + name + "\x00" + statement + "\n")
	}
	return b.String()
}
