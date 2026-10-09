package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

func (s *Service) OperationForMember(ctx context.Context, orgID, actor int64, id string) (*OperationView, error) {
	m, err := s.Member(ctx, orgID, actor)
	if err != nil {
		return nil, err
	}
	if m.Status != model.MemberActive {
		return nil, ErrForbidden
	}
	v, err := s.Operation(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if v.RequiredPermission == permissionMailFull {
		var mailboxID int64
		if err := s.DB.QueryRowContext(ctx, `SELECT target_mailbox_id FROM operations WHERE id=?`, id).Scan(&mailboxID); err != nil {
			return nil, err
		}
		if actor != v.ActorID {
			return nil, ErrForbidden
		}
		if err := s.requireOperationPermission(ctx, orgID, actor, mailboxID, permissionMailFull); err != nil {
			return nil, err
		}
		return v, nil
	}
	permissions, _, err := s.Permissions(ctx, actor)
	if err != nil {
		return nil, err
	}
	if !HasPermission(permissions, model.PermOrgManage) && (actor != v.ActorID || !HasPermission(permissions, v.RequiredPermission)) {
		return nil, ErrForbidden
	}
	return v, nil
}

func (s *Service) OperationsForMember(ctx context.Context, orgID, actor int64) ([]OperationView, error) {
	m, err := s.Member(ctx, orgID, actor)
	if err != nil {
		return nil, err
	}
	if m.Status != model.MemberActive {
		return nil, ErrForbidden
	}
	permissions, _, err := s.Permissions(ctx, actor)
	if err != nil {
		return nil, err
	}
	manage := HasPermission(permissions, model.PermOrgManage)
	rows, err := s.DB.QueryContext(ctx, `SELECT id,required_permission FROM operations WHERE org_id=? AND (actor_member_id=? OR ?) ORDER BY created_at DESC,id DESC LIMIT 100`, orgID, actor, manage)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id, required string
		if err := rows.Scan(&id, &required); err != nil {
			rows.Close()
			return nil, err
		}
		if manage || required == permissionMailFull || HasPermission(permissions, required) {
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := []OperationView{}
	for _, id := range ids {
		v, err := s.OperationForMember(ctx, orgID, actor, id)
		if err == ErrForbidden {
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, *v)
	}
	return result, nil
}

func localTransactionalOperation(kind string) bool {
	switch kind {
	case "connection.discover", "connection.import", "connection.configure", "connection.sync", "mailbox.endpoints":
		return true
	}
	return false
}

func (s *Service) ControlOperation(ctx context.Context, orgID, actor int64, id, action string) (*OperationView, error) {
	v, err := s.OperationForMember(ctx, orgID, actor, id)
	if err != nil {
		return nil, err
	}
	if !localTransactionalOperation(v.Kind) {
		return s.controlRemoteOperation(ctx, orgID, actor, v, action)
	}
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := checkOperationControllerPermissionTx(ctx, tx, id, actor); err != nil {
			return err
		}
		if action == "retry" {
			if err := checkOperationPermissionTx(ctx, tx, id); err != nil {
				return err
			}
		}
		var permissionsJSON string
		err := tx.QueryRowContext(ctx, `SELECT r.permissions_json FROM members m JOIN roles r ON r.id=m.role_id AND r.org_id=m.org_id WHERE m.id=? AND m.org_id=? AND m.status='active'`, actor, orgID).Scan(&permissionsJSON)
		if db.IsNotFound(err) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		var permissions []string
		if err := json.Unmarshal([]byte(permissionsJSON), &permissions); err != nil {
			return err
		}
		if !HasPermission(permissions, v.RequiredPermission) {
			return ErrForbidden
		}
		var status, sealed string
		var connectionID, mailboxID sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT status,COALESCE(payload_enc,''),target_connection_id,target_mailbox_id FROM operations WHERE org_id=? AND id=?`, orgID, id).Scan(&status, &sealed, &connectionID, &mailboxID); err != nil {
			return err
		}
		switch action {
		case "cancel":
			if status != "queued" && status != "failed" {
				return provider.Errorf("operation_not_cancellable", "需要确认操作尚未产生远程影响")
			}
			if _, err := tx.ExecContext(ctx, `UPDATE operations SET status='cancelled',payload_enc=NULL,updated_at=? WHERE id=?`, db.Now(), id); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `DELETE FROM operation_locks WHERE operation_id=?`, id)
			return err
		case "reconcile":
			if status != "unknown" {
				return provider.Errorf("operation_not_reconcilable", "仅允许核查结果未知的操作")
			}
			// 本地事务同时提交业务数据和成功状态，unknown 表示该事务没有提交。
			result := `{"verificationSource":"local_transaction","effectsCommitted":false}`
			if _, err := tx.ExecContext(ctx, `UPDATE operation_steps SET status='failed',result_json=?,error_code='process_interrupted_confirmed',finished_at=? WHERE operation_id=? AND status='unknown'`, result, db.Now(), id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE operations SET status='failed',result_json=?,error_code='process_interrupted_confirmed',updated_at=? WHERE id=?`, result, db.Now(), id); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `DELETE FROM operation_locks WHERE operation_id=?`, id)
			return err
		case "retry":
			if status != "failed" || sealed == "" {
				return provider.Errorf("operation_not_retryable", "操作需要明确失败并保留原始请求")
			}
			plain, err := s.Box.Open(sealed)
			if err != nil {
				return provider.Errorf("credential_decryption_failed", "操作请求解密失败")
			}
			var payload OperationPayload
			if err := json.Unmarshal([]byte(plain), &payload); err != nil {
				return err
			}
			if payload.Kind != v.Kind {
				return provider.Errorf("invalid", "操作请求类型不一致")
			}
			var revision int64
			if payload.Connection != nil {
				if err := tx.QueryRowContext(ctx, `SELECT revision FROM mail_connections WHERE id=? AND org_id=?`, connectionID, orgID).Scan(&revision); err != nil {
					return err
				}
				if revision != payload.Connection.ExpectedRevision {
					return provider.Errorf("revision_conflict", "连接配置已更新")
				}
			}
			if payload.Endpoints != nil || payload.Kind == "mailbox.reactivate" {
				if err := tx.QueryRowContext(ctx, `SELECT revision FROM mailboxes WHERE id=? AND org_id=?`, mailboxID, orgID).Scan(&revision); err != nil {
					return err
				}
				expected := payload.ExpectedRevision
				if payload.Endpoints != nil {
					expected = payload.Endpoints.ExpectedRevision
				}
				if revision != expected {
					return provider.Errorf("revision_conflict", "邮箱配置已更新")
				}
			}
			var keys []string
			if connectionID.Valid && connectionID.Int64 > 0 {
				keys = append(keys, "connection:"+fmtID(connectionID.Int64))
			}
			if mailboxID.Valid && mailboxID.Int64 > 0 {
				keys = append(keys, "mailbox:"+fmtID(mailboxID.Int64))
			}
			sort.Strings(keys)
			for _, key := range keys {
				var locked bool
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operation_locks WHERE org_id=? AND resource_key=? AND operation_id!=?)`, orgID, key, id).Scan(&locked); err != nil {
					return err
				}
				if locked {
					return provider.Errorf("operation_in_progress", "资源已有进行中的操作")
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO operation_locks(org_id,resource_key,operation_id) VALUES (?,?,?) ON CONFLICT(org_id,resource_key) DO NOTHING`, orgID, key, id); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE operation_steps SET status='pending',error_code=NULL,started_at=NULL,finished_at=NULL WHERE operation_id=? AND status IN ('failed','pending')`, id); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `UPDATE operations SET status='queued',error_code=NULL,updated_at=? WHERE id=?`, db.Now(), id)
			return err
		default:
			return provider.Errorf("invalid", "操作动作无效")
		}
	})
	if err != nil {
		return nil, err
	}
	s.audit(ctx, orgID, actor, "operation."+action, "operation", id, nil)
	select {
	case s.operationWake <- struct{}{}:
	default:
	}
	return s.OperationForMember(ctx, orgID, actor, id)
}
