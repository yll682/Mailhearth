package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"mailhearth/internal/db"
	"mailhearth/internal/provider"
)

type CredentialCleanupInput struct {
	StepKey        string `json:"stepKey"`
	ConfirmAddress string `json:"confirmAddress"`
	Note           string `json:"note"`
}

func (s *Service) ReportLostCredentialCleanup(ctx context.Context, orgID, actor int64, id string, in CredentialCleanupInput) (*OperationView, error) {
	if !strings.HasSuffix(in.StepKey, "credential.create") || strings.TrimSpace(in.Note) == "" || len(in.Note) > 2000 {
		return nil, provider.Errorf("invalid", "需要凭据创建步骤和清理说明")
	}
	view, err := s.OperationForMember(ctx, orgID, actor, id)
	if err != nil {
		return nil, err
	}
	switch view.Kind {
	case "mailbox.create", "mailbox.connect", "mailbox.rotate", "member.create", "member.offboard":
	default:
		return nil, provider.Errorf("invalid", "此操作没有 managed 凭据创建流程")
	}
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := checkOperationControllerPermissionTx(ctx, tx, id, actor); err != nil {
			return err
		}
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM operations WHERE org_id=? AND id=?`, orgID, id).Scan(&status); err != nil {
			return err
		}
		if status != "unknown" {
			return provider.Errorf("revision_conflict", "操作没有待清理的未知结果")
		}
		var reference, result, stepStatus string
		if err := tx.QueryRowContext(ctx, `SELECT remote_ref_json,result_json,status FROM operation_steps WHERE operation_id=? AND step_key=?`, id, in.StepKey).Scan(&reference, &result, &stepStatus); err != nil {
			if db.IsNotFound(err) {
				return ErrNotFound
			}
			return err
		}
		if stepStatus != "unknown" {
			return provider.Errorf("verification_required", "凭据步骤未处于未知状态")
		}
		var saved struct {
			CredentialID *int64 `json:"credentialId"`
		}
		if err := json.Unmarshal([]byte(result), &saved); err != nil {
			return err
		}
		if saved.CredentialID != nil {
			return provider.Errorf("verification_required", "已保存的候选凭据需要执行独立认证与撤销核查")
		}
		var target struct {
			MailboxID    int64  `json:"mailboxId"`
			ConnectionID int64  `json:"connectionId"`
			Address      string `json:"address"`
		}
		if err := json.Unmarshal([]byte(reference), &target); err != nil {
			return err
		}
		if target.MailboxID < 1 || target.ConnectionID < 1 || target.Address == "" || in.ConfirmAddress != target.Address {
			return provider.Errorf("invalid", "需要输入远程步骤记录的完整邮箱地址")
		}
		var matches bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mailboxes b JOIN mail_connections c ON c.id=b.connection_id WHERE b.id=? AND b.org_id=? AND b.connection_id=? AND b.address=? AND c.provider_kind='purelymail')`, target.MailboxID, orgID, target.ConnectionID, target.Address).Scan(&matches); err != nil {
			return err
		}
		if !matches {
			return provider.Errorf("verification_required", "凭据步骤与当前邮箱的关联不完整")
		}
		var unresolved bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND step_key NOT IN ('execute',?) AND status IN ('unknown','running'))`, id, in.StepKey).Scan(&unresolved); err != nil {
			return err
		}
		if unresolved {
			return provider.Errorf("verification_required", "其他远程步骤仍需要独立核查")
		}
		report := map[string]any{"mailboxId": target.MailboxID, "connectionId": target.ConnectionID, "address": target.Address, "stepKey": in.StepKey, "reportedBy": actor, "reportedAt": db.Now(), "note": strings.TrimSpace(in.Note), "verificationSource": "administrator_report", "systemVerified": false, "retainedResources": true}
		if _, err := tx.ExecContext(ctx, `UPDATE operation_steps SET status='external_reported',result_json=?,error_code=NULL,finished_at=? WHERE operation_id=? AND step_key=?`, toJSON(report), db.Now(), id, in.StepKey); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE operation_steps SET status='failed',error_code='external_action_reported',result_json=?,finished_at=? WHERE operation_id=? AND step_key='execute'`, toJSON(report), db.Now(), id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE operations SET status='cancelled',error_code='external_action_reported',result_json=?,payload_enc=NULL,claim_secret_enc=NULL,updated_at=? WHERE id=? AND org_id=?`, toJSON(report), db.Now(), id, orgID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM operation_private WHERE operation_id=?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE addresses SET sync_state='unknown',updated_at=? WHERE org_id=? AND sync_state='pending' AND EXISTS(SELECT 1 FROM operation_locks l WHERE l.org_id=addresses.org_id AND l.operation_id=? AND l.resource_key='address:'||addresses.id)`, db.Now(), orgID, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM operation_locks WHERE operation_id=?`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log(org_id,actor_member_id,action,target_type,target_id,detail_json,created_at) VALUES (?,?,'operation.credentialCleanup','operation',?,?,?)`, orgID, actor, id, toJSON(report), db.Now())
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.OperationForMember(ctx, orgID, actor, id)
}
