package core

import (
	"context"
	"github.com/google/uuid"
	"mailhearth/internal/db"
	"mailhearth/internal/provider"
	"strings"
	"testing"
)

func TestMultiProviderStorageAssociationOwnership(t *testing.T) {
	s, m := newStorageService(t)
	ctx := context.Background()
	mailboxID, input := storageSubmission(t, s, m)
	other := storageConnection(t, s, m, "另外的邮件连接")
	var credentialID, endpointID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT id,credential_id FROM mailbox_endpoints WHERE mailbox_id=? AND protocol='smtp'`, mailboxID).Scan(&endpointID, &credentialID); err != nil {
		t.Fatal(err)
	}
	statements := []struct {
		name, sql string
		args      []any
	}{
		{"邮箱连接", `UPDATE mailboxes SET connection_id=? WHERE id=?`, []any{other.ID, mailboxID}},
		{"凭据所属连接", `UPDATE credentials SET connection_id=? WHERE id=?`, []any{other.ID, credentialID}},
		{"endpoint 协议", `UPDATE mailbox_endpoints SET protocol='imap' WHERE id=?`, []any{endpointID}},
		{"共享邮箱所有者", `UPDATE mailboxes SET kind='shared' WHERE id=?`, []any{mailboxID}},
		{"身份所属邮箱", `UPDATE identities SET mailbox_id=99999 WHERE id=?`, []any{input.Envelope.IdentityID}},
	}
	for _, statement := range statements {
		if _, err := s.DB.ExecContext(ctx, statement.sql, statement.args...); err == nil {
			t.Fatalf("数据库允许修改%s", statement.name)
		}
	}
	if _, _, err := s.ReserveSubmission(ctx, m.OrgID, m.ID, mailboxID, input); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM mailboxes WHERE id=?`, mailboxID); err == nil || !strings.Contains(err.Error(), "mailbox_has_history") {
		t.Fatalf("数据库没有保护邮箱历史：%v", err)
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM submissions WHERE mailbox_id=?`, mailboxID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("历史发送记录发生变化")
	}
	result, err := s.DB.ExecContext(ctx, `INSERT INTO organizations(name,created_at) VALUES (?,?)`, uuid.NewString(), db.Now())
	if err != nil {
		t.Fatal(err)
	}
	otherOrg, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	result, err = s.DB.ExecContext(ctx, `INSERT INTO groups(org_id,name,created_at,updated_at) VALUES (?,?,?,?)`, otherOrg, "另外组织的群组", db.Now(), db.Now())
	if err != nil {
		t.Fatal(err)
	}
	groupID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO group_members(group_id,member_id) VALUES (?,?)`, groupID, m.ID); err == nil {
		t.Fatal("数据库允许其他组织的成员加入群组")
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE members SET org_id=? WHERE id=?`, otherOrg, m.ID); err == nil {
		t.Fatal("数据库允许修改成员所属组织")
	}
	operation, _, err := s.QueueOperation(ctx, m.OrgID, m.ID, uuid.NewString(), OperationPayload{Kind: "connection.discover", ConnectionID: other.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE operations SET org_id=? WHERE id=?`, otherOrg, operation.ID); err == nil {
		t.Fatal("数据库允许将操作关联其他组织")
	}
}

func TestMultiProviderStorageConnectionUnregisterCredential(t *testing.T) {
	s, m := newStorageService(t)
	ctx := context.Background()
	templates, err := provider.JSONString(provider.DefaultTemplates(provider.Purelymail))
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DB.ExecContext(ctx, `INSERT INTO mail_connections(org_id,provider_kind,label,api_base_url,domain_scope_json,protocol_defaults_json,created_at,updated_at) VALUES (?,'purelymail','需要解除登记的连接','https://purelymail.com/api/v0','{"mode":"all"}',?,?,?)`, m.OrgID, templates, db.Now(), db.Now())
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	connection, err := s.MailConnection(ctx, m.OrgID, id)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := s.Box.Seal(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	result, err = s.DB.ExecContext(ctx, `INSERT INTO credentials(org_id,connection_id,purpose,source,secret_enc,state,created_at,updated_at) VALUES (?,?,'api','entered',?,'active',?,?)`, m.OrgID, connection.ID, sealed, db.Now(), db.Now())
	if err != nil {
		t.Fatal(err)
	}
	credentialID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE mail_connections SET api_credential_id=? WHERE id=?`, credentialID, connection.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteConnection(ctx, m.OrgID, m.ID, connection.ID, connection.Revision, connection.Label); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM mail_connections WHERE id=?)+(SELECT COUNT(*) FROM credentials WHERE id=?)`, connection.ID, credentialID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("连接解除登记后仍有认证密文")
	}
	var detail string
	if err := s.DB.QueryRowContext(ctx, `SELECT detail_json FROM audit_log WHERE action='connection.delete' AND target_id=?`, fmtID(connection.ID)).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(detail, sealed) {
		t.Fatal("连接解除登记审计包含凭据密文")
	}
}
