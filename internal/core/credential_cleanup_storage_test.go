package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"mailhearth/internal/db"
	"mailhearth/internal/model"
)

func TestMultiProviderStorageLostCredentialCleanupReport(t *testing.T) {
	s, m := newStorageService(t)
	ctx := context.Background()
	now := db.Now()
	res, err := s.DB.Exec(`INSERT INTO mail_connections(org_id,provider_kind,label,enabled,revision,domain_scope_json,protocol_defaults_json,created_at,updated_at) VALUES (?,'purelymail','凭据清理连接',1,1,'{"mode":"all"}','{"imap":{"enabled":false},"smtp":{"enabled":false},"managesieve":{"enabled":false}}',?,?)`, m.OrgID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	connectionID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	mailbox := storageOffboardMailbox(t, s, m, m.ID, connectionID, "cleanup@example.org")
	id := uuid.NewString()
	sealed, err := s.Box.Seal(toJSON(OperationPayload{Kind: "mailbox.connect", ConnectionID: connectionID, MailboxID: mailbox.ID}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO operations(id,org_id,actor_member_id,kind,request_id,request_digest,status,payload_enc,target_connection_id,target_mailbox_id,required_permission,created_at,updated_at) VALUES (?,?,?,'mailbox.connect',?,'cleanup-storage','unknown',?,?,?, ?,?,?)`, id, m.OrgID, m.ID, uuid.NewString(), sealed, connectionID, mailbox.ID, model.PermMailboxesManage, now, now); err != nil {
		t.Fatal(err)
	}
	ref := toJSON(map[string]any{"address": mailbox.Address, "mailboxId": mailbox.ID, "connectionId": connectionID})
	if _, err := s.DB.Exec(`INSERT INTO operation_steps(operation_id,step_key,sequence,status,remote_ref_json) VALUES (?,'execute',1,'unknown','{}'),(?,'credential.create',2,'unknown',?)`, id, id, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO operation_locks(org_id,resource_key,operation_id) VALUES (?,?,?)`, m.OrgID, "connection:"+fmtID(connectionID), id); err != nil {
		t.Fatal(err)
	}
	in := CredentialCleanupInput{StepKey: "credential.create", ConfirmAddress: mailbox.Address, Note: "管理员在服务商界面完成候选应用凭据清理"}
	wrong := in
	wrong.ConfirmAddress = "another@example.org"
	_, err = s.ReportLostCredentialCleanup(ctx, m.OrgID, m.ID, id, wrong)
	requireProviderCode(t, err, "invalid")
	if _, err := s.DB.Exec(`UPDATE operation_steps SET result_json='{"credentialId":123}' WHERE operation_id=? AND step_key='credential.create'`, id); err != nil {
		t.Fatal(err)
	}
	_, err = s.ReportLostCredentialCleanup(ctx, m.OrgID, m.ID, id, in)
	requireProviderCode(t, err, "verification_required")
	if _, err := s.DB.Exec(`UPDATE operation_steps SET result_json='{}' WHERE operation_id=? AND step_key='credential.create'`, id); err != nil {
		t.Fatal(err)
	}
	view, err := s.ReportLostCredentialCleanup(ctx, m.OrgID, m.ID, id, in)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != "cancelled" {
		t.Fatal("清理报告没有取消原操作")
	}
	var report struct {
		Verified bool   `json:"systemVerified"`
		Source   string `json:"verificationSource"`
		Retained bool   `json:"retainedResources"`
	}
	if err := json.Unmarshal(view.Result, &report); err != nil {
		t.Fatal(err)
	}
	if report.Verified || report.Source != "administrator_report" || !report.Retained {
		t.Fatal("清理报告没有保存验证来源和资源保留状态")
	}
	current, err := s.Mailbox(ctx, m.OrgID, mailbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != mailbox.Revision || current.Status != mailbox.Status {
		t.Fatal("清理报告改变了已登记邮箱")
	}
	var count int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM operation_locks WHERE operation_id=?`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("清理报告没有释放资源占用")
	}
	var retained bool
	if err := s.DB.QueryRow(`SELECT payload_enc IS NOT NULL OR claim_secret_enc IS NOT NULL FROM operations WHERE id=?`, id).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if retained {
		t.Fatal("已取消操作保留了秘密")
	}
	_, err = s.ReportLostCredentialCleanup(ctx, m.OrgID, m.ID, id, in)
	requireProviderCode(t, err, "revision_conflict")
}
