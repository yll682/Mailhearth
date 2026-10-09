package core

import (
	"context"
	"database/sql"

	"mailhearth/internal/db"
	"mailhearth/internal/provider"
)

func (s *Service) executeRemoteAccessRevocation(ctx context.Context, orgID int64, payload OperationPayload) (any, error) {
	if err := validateMailboxManagementVersions(ctx, s.DB, orgID, payload); err != nil {
		return nil, err
	}
	id := ctx.Value(operationContextKey{}).(string)
	mb, err := s.Mailbox(ctx, orgID, payload.MailboxID)
	if err != nil {
		return nil, err
	}
	if payload.RemoteMailbox == nil || mb.Revision != payload.RemoteMailbox.ExpectedRevision {
		return nil, provider.Errorf("revision_conflict", "邮箱已经更新")
	}
	endpoints, err := s.MailboxEndpoints(ctx, orgID, mb.ID)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"mailboxId": mb.ID, "connectionId": mb.ConnectionID, "address": mb.Address, "expectedRevision": mb.Revision, "systemVerified": false, "verificationSource": "administrator_report"}
	if err := s.externalOperationStep(ctx, "remoteAccess.external", map[string]any{"mailboxId": mb.ID, "connectionId": mb.ConnectionID, "address": mb.Address, "endpoints": endpoints.Endpoints, "accessMethods": []string{"primary_password", "application_credentials", "sender_identities", "delegated_access", "existing_sessions"}, "systemVerified": false}); err != nil {
		return nil, err
	}
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := validateMailboxManagementVersions(ctx, tx, orgID, payload); err != nil {
			return err
		}
		if err := checkOperationPermissionTx(ctx, tx, id); err != nil {
			return err
		}
		var current bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mailboxes WHERE id=? AND org_id=? AND revision=?)`, mb.ID, orgID, mb.Revision).Scan(&current); err != nil {
			return err
		}
		if !current {
			return provider.Errorf("revision_conflict", "邮箱已经更新")
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO operation_steps(operation_id,step_key,sequence,status,result_json,finished_at) SELECT ?,'remoteAccess.prepare',COALESCE(MAX(sequence),0)+1,'succeeded',?,? FROM operation_steps WHERE operation_id=? ON CONFLICT(operation_id,step_key) DO NOTHING`, id, toJSON(result), db.Now(), id)
		return err
	})
	if err != nil {
		return nil, err
	}
	failure := provider.Errorf("external_action_required", "全部远程访问需要管理员逐项处理并报告完成")
	failure.Details = result
	return result, failure
}
