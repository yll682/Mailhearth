package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mailhearth/internal/secrets"

	_ "modernc.org/sqlite"
)

func TestMultiProviderMigrationFreshDatabase(t *testing.T) {
	path := testDBPath(t, "fresh.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	for _, table := range []string{"mail_connections", "credentials", "domain_bindings", "mailbox_endpoints", "provider_resources", "operations", "submissions"} {
		var n int
		if err := d.QueryRowContext(ctx, "SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("table %s missing", table)
		}
	}
}

func TestMultiProviderMigrationFromVersionOne(t *testing.T) {
	inputs := testMigrationInputs(t)
	apiSecret, err := inputs.CredentialsBox.Seal(" api-password \t")
	if err != nil {
		t.Fatal(err)
	}
	mailSecret, err := inputs.CredentialsBox.Seal(" mail-password \t")
	if err != nil {
		t.Fatal(err)
	}
	path := testDBPath(t, "upgrade.db")
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
	base, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	if _, err := base.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL);`); err != nil {
		t.Fatal(err)
	}
	if err := applyEmbedded(base, "0001_init.sql"); err != nil {
		t.Fatal(err)
	}
	if _, err := base.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES ('0001_init.sql','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := base.Exec(`INSERT INTO organizations(name, created_at) VALUES ('Acme','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := base.Exec(`INSERT INTO purelymail_accounts(org_id,label,api_token_enc,token_hint,created_at,updated_at) VALUES (1,'Purelymail',?,'hint','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, apiSecret); err != nil {
		t.Fatal(err)
	}
	if _, err := base.Exec(`INSERT INTO domains(org_id,name,created_at,updated_at) VALUES (1,'acme.test','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := base.Exec(`INSERT INTO mailboxes(org_id,kind,pm_user,domain_id,credential_enc,credential_label,credential_at,status,created_at,updated_at) VALUES (1,'personal','alice@acme.test',1,?,'Mailhearth','2026-01-01T00:00:00Z','active','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, mailSecret); err != nil {
		t.Fatal(err)
	}
	if _, err := base.Exec(`INSERT INTO addresses(org_id,domain_id,local_part,address,kind,mailbox_id,pm_rule_id,created_at,updated_at) VALUES (1,1,'sales','sales@acme.test','alias',1,7,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := base.Close(); err != nil {
		t.Fatal(err)
	}

	d, err := Open(path, inputs)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	var connID, mailboxID, credentialID int64
	var mailboxAddress string
	if err := d.QueryRowContext(ctx, `SELECT c.id, b.id, c.api_credential_id, b.address FROM mail_connections c JOIN mailboxes b ON b.connection_id=c.id JOIN credentials api ON api.id=c.api_credential_id WHERE c.provider_kind='purelymail' AND b.address='alice@acme.test'`).Scan(&connID, &mailboxID, &credentialID, &mailboxAddress); err != nil {
		t.Fatal(err)
	}
	if connID == 0 || mailboxID == 0 || credentialID == 0 || mailboxAddress != "alice@acme.test" {
		t.Fatalf("migration produced invalid references: connection=%d mailbox=%d credential=%d address=%s", connID, mailboxID, credentialID, mailboxAddress)
	}
	var endpoints int
	if err := d.QueryRowContext(ctx, `SELECT COUNT(1) FROM mailbox_endpoints WHERE mailbox_id=?`, mailboxID).Scan(&endpoints); err != nil {
		t.Fatal(err)
	}
	if endpoints != 3 {
		t.Fatalf("expected 3 endpoints, got %d", endpoints)
	}
	var resources int
	if err := d.QueryRowContext(ctx, `SELECT COUNT(1) FROM provider_resources WHERE connection_id=?`, connID).Scan(&resources); err != nil {
		t.Fatal(err)
	}
	if resources < 5 {
		t.Fatalf("expected migrated provider resources, got %d", resources)
	}
	var seal string
	if err := d.QueryRowContext(ctx, `SELECT secret_enc FROM credentials WHERE mailbox_id=? AND purpose='mail'`, mailboxID).Scan(&seal); err != nil {
		t.Fatal(err)
	}
	if seal != mailSecret {
		t.Fatal("邮件凭据密文发生变化")
	}
	var apiSeal string
	if err := d.QueryRowContext(ctx, `SELECT secret_enc FROM credentials WHERE id=?`, credentialID).Scan(&apiSeal); err != nil {
		t.Fatal(err)
	}
	if apiSeal != apiSecret {
		t.Fatal("API 凭据密文发生变化")
	}
	var mode string
	if err := d.QueryRow(`SELECT network_mode FROM mailbox_endpoints WHERE mailbox_id=? AND protocol='managesieve'`, mailboxID).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "disabled" {
		t.Fatal("ManageSieve 应当保持停用")
	}
	if _, err := inputs.CredentialsBox.Open(seal); err != nil {
		t.Fatal(err)
	}
}

func TestMultiProviderMigrationSecondOpen(t *testing.T) {
	path := testDBPath(t, "second-open.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	var conns, creds int
	if err := d.QueryRowContext(ctx, `SELECT (SELECT COUNT(1) FROM mail_connections),(SELECT COUNT(1) FROM credentials)`).Scan(&conns, &creds); err != nil {
		t.Fatal(err)
	}
	if conns != 0 || creds != 0 {
		t.Fatalf("second open changed data: connections=%d credentials=%d", conns, creds)
	}
}

func TestMultiProviderMigrationConstraintConflict(t *testing.T) {
	inputs := testMigrationInputs(t)
	apiSeal, err := inputs.CredentialsBox.Seal(" API credential \t")
	if err != nil {
		t.Fatal(err)
	}
	path := testDBPath(t, "constraint-conflict.db")
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	base, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := base.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL);`); err != nil {
		t.Fatal(err)
	}
	if err := applyEmbedded(base, "0001_init.sql"); err != nil {
		t.Fatal(err)
	}
	if _, err := base.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES ('0001_init.sql','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := base.Exec(`INSERT INTO organizations(name, created_at) VALUES ('Acme','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := base.Exec(`INSERT INTO purelymail_accounts(org_id,label,api_token_enc,token_hint,created_at,updated_at) VALUES (1,'Purelymail',?,'hint','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, apiSeal); err != nil {
		t.Fatal(err)
	}
	if _, err := base.Exec(`INSERT INTO mailboxes(org_id,kind,pm_user,created_at,updated_at) VALUES (1,'personal','alice@acme.test','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z'),(1,'personal','ALICE@acme.test','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	base.Close()

	d, err := Open(path, inputs)
	if err == nil {
		d.Close()
		t.Fatal("migration succeeded with a unique constraint conflict")
	}
	if !strings.Contains(err.Error(), "migration_duplicate_address") {
		t.Fatalf("迁移错误没有报告重复地址：%v", err)
	}
	check, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var count int
	if err := check.QueryRow(`SELECT COUNT(*) FROM mailboxes WHERE pm_user IN ('alice@acme.test','ALICE@acme.test')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatal("失败迁移修改了原有数据")
	}
	if err := check.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version='0002_multi_provider.sql'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("失败迁移提交了版本记录")
	}
}

func testDBPath(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join("..", "..", "data", "integration", "multi-provider", "db-"+strings.ReplaceAll(t.Name(), "/", "-")+"-"+time.Now().UTC().Format("20060102T150405.000000000"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, name)
}

func testMigrationInputs(t *testing.T) MigrationInputs {
	t.Helper()
	master := make([]byte, 32)
	if _, err := rand.Read(master); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.NewBox(master, "credentials")
	if err != nil {
		t.Fatal(err)
	}
	return MigrationInputs{PurelymailAPIURL: "https://purelymail.com/api/v0", IMAPAddr: "imap.purelymail.com:993", IMAPTLS: "tls", SMTPAddr: "smtp.purelymail.com:465", SMTPTLS: "tls", CredentialsBox: box}
}

func applyEmbedded(q *sql.DB, name string) error {
	b, err := migrationFS.ReadFile("migrations/" + name)
	if err != nil {
		return err
	}
	_, err = q.Exec(string(b))
	return err
}
