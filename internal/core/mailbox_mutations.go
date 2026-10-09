package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

type mailboxMutationPlan struct {
	MailboxID          int64            `json:"mailboxId"`
	ExpectedRevision   int64            `json:"expectedRevision"`
	ConnectionID       int64            `json:"connectionId"`
	ConnectionRevision int64            `json:"connectionRevision"`
	DisplayName        string           `json:"displayName"`
	Kind               string           `json:"kind"`
	Status             string           `json:"status"`
	OwnerMemberID      *int64           `json:"ownerMemberId"`
	Members            []routingVersion `json:"members"`
	Before             []routingPlan    `json:"before"`
	Groups             []routingPlan    `json:"groups"`
}

func mailboxMutationOperation(kind string) bool {
	switch kind {
	case "mailbox.update", "mailbox.suspend", "mailbox.archive", "mailbox.reactivate", "mailbox.deleteRemote":
		return true
	}
	return false
}

func mailboxMutationKeys(plan *mailboxMutationPlan) []string {
	keys := []string{"mailbox:" + fmtID(plan.MailboxID), "connection:" + fmtID(plan.ConnectionID)}
	for _, member := range plan.Members {
		keys = append(keys, "member:"+fmtID(member.ID))
	}
	for i := range plan.Before {
		keys = append(keys, routingKeys(&plan.Before[i])...)
	}
	for i := range plan.Groups {
		keys = append(keys, routingKeys(&plan.Groups[i])...)
	}
	return keys
}

func (s *Service) prepareMailboxMutation(ctx context.Context, orgID, actor int64, payload *OperationPayload) (*mailboxMutationPlan, string, error) {
	mb, err := s.Mailbox(ctx, orgID, payload.MailboxID)
	if err != nil {
		return nil, "", err
	}
	expected := payload.ExpectedRevision
	if payload.MailboxEdit != nil {
		expected = payload.MailboxEdit.ExpectedRevision
	}
	if payload.Kind == "mailbox.deleteRemote" && payload.RemoteMailbox != nil {
		expected = payload.RemoteMailbox.ExpectedRevision
	}
	if expected < 1 || expected != mb.Revision {
		return nil, "", provider.Errorf("revision_conflict", "需要邮箱当前版本")
	}
	connection, err := s.MailConnection(ctx, orgID, mb.ConnectionID)
	if err != nil {
		return nil, "", err
	}
	plan := &mailboxMutationPlan{MailboxID: mb.ID, ExpectedRevision: expected, ConnectionID: mb.ConnectionID, ConnectionRevision: connection.Revision, DisplayName: mb.DisplayName, Kind: mb.Kind, Status: mb.Status, OwnerMemberID: mb.OwnerMemberID, Members: []routingVersion{}, Before: []routingPlan{}, Groups: []routingPlan{}}
	switch payload.Kind {
	case "mailbox.update":
		if payload.MailboxEdit == nil {
			return nil, "", provider.Errorf("invalid", "需要邮箱编辑请求")
		}
		in := payload.MailboxEdit
		if in.DisplayName != nil {
			plan.DisplayName = strings.TrimSpace(*in.DisplayName)
			if plan.DisplayName == "" {
				return nil, "", provider.Errorf("invalid", "显示名称不能为空")
			}
		}
		if in.Kind != nil {
			if *in.Kind != model.MailboxPersonal && *in.Kind != model.MailboxShared {
				return nil, "", provider.Errorf("invalid", "邮箱类型无效")
			}
			plan.Kind = *in.Kind
		}
		if in.OwnerMemberID != nil {
			if *in.OwnerMemberID < 0 {
				return nil, "", provider.Errorf("invalid", "ownerMemberId 无效")
			}
			plan.OwnerMemberID = nil
			if *in.OwnerMemberID > 0 {
				value := *in.OwnerMemberID
				plan.OwnerMemberID = &value
			}
		}
		if plan.Kind == model.MailboxShared {
			plan.OwnerMemberID = nil
		}
	case "mailbox.suspend":
		if mb.Status == "archived" {
			return nil, "", provider.Errorf("invalid", "归档邮箱不能通过暂停操作修改")
		}
		plan.Status = "suspended"
	case "mailbox.archive":
		if mb.Status == "archived" {
			return nil, "", provider.Errorf("invalid", "邮箱已经归档")
		}
		plan.Status = "archived"
	case "mailbox.deleteRemote":
		plan.Status = "archived"
		var dependents int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM addresses a WHERE a.org_id=? AND a.connection_id=? AND a.kind NOT IN ('primary','group') AND (a.mailbox_id=? OR EXISTS(SELECT 1 FROM json_each(a.targets_json) WHERE lower(value)=lower(?)))`, orgID, mb.ConnectionID, mb.ID, mb.Address).Scan(&dependents); err != nil {
			return nil, "", err
		}
		if dependents > 0 {
			failure := provider.Errorf("mailbox_in_use", "其他收件地址仍然使用此邮箱")
			failure.Details = map[string]int{"addresses": dependents}
			return nil, "", failure
		}
	case "mailbox.reactivate":
		if mb.Status != "suspended" {
			return nil, "", provider.Errorf("invalid", "仅允许恢复已暂停的邮箱")
		}
		if !connection.Enabled {
			return nil, "", provider.Errorf("endpoint_disabled", "连接已经停用")
		}
		plan.Status = "active"
	default:
		return nil, "", provider.Errorf("unsupported_operation", "邮箱变更类型无效")
	}
	seen := map[int64]bool{}
	for _, owner := range []*int64{mb.OwnerMemberID, plan.OwnerMemberID} {
		if owner == nil || seen[*owner] {
			continue
		}
		seen[*owner] = true
		member, err := s.Member(ctx, orgID, *owner)
		if err != nil {
			return nil, "", err
		}
		if plan.OwnerMemberID != nil && *owner == *plan.OwnerMemberID && (mb.OwnerMemberID == nil || *mb.OwnerMemberID != *plan.OwnerMemberID) && member.Status != "active" && member.Status != "invited" {
			return nil, "", provider.Errorf("invalid", "新的 owner 状态无效")
		}
		plan.Members = append(plan.Members, routingVersion{member.ID, member.Revision})
	}
	owners := []int64{}
	for _, member := range plan.Members {
		owners = append(owners, member.ID)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT g.id FROM groups g JOIN group_members gm ON gm.group_id=g.id WHERE g.org_id=? AND gm.member_id IN (SELECT value FROM json_each(?)) ORDER BY g.id`, orgID, toJSON(owners))
	if err != nil {
		return nil, "", err
	}
	var groupIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, "", err
		}
		groupIDs = append(groupIDs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, "", err
	}
	future := *mb
	future.Kind = plan.Kind
	future.Status = plan.Status
	future.OwnerMemberID = plan.OwnerMemberID
	future.Revision++
	for _, id := range groupIDs {
		if err := s.requireOperationPermission(ctx, orgID, actor, 0, model.PermGroupsManage); err != nil {
			return nil, "", err
		}
		group, err := s.Group(ctx, orgID, id)
		if err != nil {
			return nil, "", err
		}
		input := GroupInput{ExpectedRevision: group.Revision, Name: group.Name, Description: group.Description, MemberIDs: group.MemberIDs}
		before, err := s.prepareRouting(ctx, orgID, "group.update", &RoutingOperationInput{GroupID: id, Group: &input})
		if err != nil {
			return nil, "", err
		}
		data, err := json.Marshal(before)
		if err != nil {
			return nil, "", err
		}
		var desired routingPlan
		if err := json.Unmarshal(data, &desired); err != nil {
			return nil, "", err
		}
		for i := range desired.Rules {
			targets, versions, err := groupCandidateTargets(ctx, s.DB, orgID, id, desired.GroupMembers, desired.Rules[i], &future)
			if err != nil {
				return nil, "", err
			}
			desired.Rules[i].Targets = targets
			desired.Mailboxes = versions
		}
		plan.Before = append(plan.Before, *before)
		plan.Groups = append(plan.Groups, desired)
	}
	permission := model.PermMailboxesManage
	if mb.Kind == model.MailboxShared {
		permission = model.PermSharedManage
	}
	if plan.Kind != mb.Kind {
		targetPermission := model.PermMailboxesManage
		if plan.Kind == model.MailboxShared {
			targetPermission = model.PermSharedManage
		}
		if err := s.requireOperationPermission(ctx, orgID, actor, mb.ID, targetPermission); err != nil {
			return nil, "", err
		}
	}
	payload.ConnectionID = mb.ConnectionID
	return plan, permission, nil
}

func validateMailboxMutation(ctx context.Context, q managementQuery, orgID int64, plan *mailboxMutationPlan, committed bool) error {
	var revision int64
	if err := q.QueryRowContext(ctx, `SELECT revision FROM mail_connections WHERE id=? AND org_id=?`, plan.ConnectionID, orgID).Scan(&revision); err != nil {
		return err
	}
	if revision != plan.ConnectionRevision {
		return provider.Errorf("revision_conflict", "邮箱所属连接已经更新")
	}
	expected := plan.ExpectedRevision
	if committed {
		expected++
	}
	var current bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mailboxes WHERE id=? AND org_id=? AND revision=?)`, plan.MailboxID, orgID, expected).Scan(&current); err != nil {
		return err
	}
	if !current {
		return provider.Errorf("revision_conflict", "邮箱已经更新")
	}
	for _, member := range plan.Members {
		if err := q.QueryRowContext(ctx, `SELECT revision FROM members WHERE id=? AND org_id=?`, member.ID, orgID).Scan(&revision); err != nil {
			return err
		}
		if revision != member.Revision {
			return provider.Errorf("revision_conflict", "邮箱关联成员已经更新")
		}
	}
	return nil
}

func commitMailboxMutation(ctx context.Context, tx *sql.Tx, orgID int64, plan *mailboxMutationPlan) error {
	if err := checkOperationPermissionTx(ctx, tx, ctx.Value(operationContextKey{}).(string)); err != nil {
		return err
	}
	if err := validateMailboxMutation(ctx, tx, orgID, plan, false); err != nil {
		return err
	}
	for i := range plan.Before {
		if err := validateRoutingPlan(ctx, tx, orgID, &plan.Before[i]); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE mailboxes SET display_name=?,kind=?,status=?,owner_member_id=?,revision=revision+1,access_revision=access_revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=?`, plan.DisplayName, plan.Kind, plan.Status, sqlNullInt(plan.OwnerMemberID), db.Now(), plan.MailboxID, orgID, plan.ExpectedRevision)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return provider.Errorf("revision_conflict", "邮箱已经更新")
	}
	child := context.WithValue(ctx, intermediateOperationKey{}, true)
	child = context.WithValue(child, operationStepPrefixKey{}, "mailbox.mutation.")
	return completeLocalOperation(child, tx, map[string]int64{"mailboxId": plan.MailboxID})
}

func reserveMailboxMutation(ctx context.Context, tx *sql.Tx, orgID int64, id string, kind string, plan *mailboxMutationPlan) error {
	if err := validateMailboxMutation(ctx, tx, orgID, plan, false); err != nil {
		return err
	}
	for i := range plan.Before {
		if err := validateRoutingPlan(ctx, tx, orgID, &plan.Before[i]); err != nil {
			return err
		}
		for j := range plan.Groups[i].Rules {
			desired := &plan.Groups[i].Rules[j]
			res, err := tx.ExecContext(ctx, `UPDATE addresses SET desired_targets_json=?,sync_state='pending',revision=revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=?`, toJSON(desired.Targets), db.Now(), desired.ID, orgID, desired.Revision)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n != 1 {
				return provider.Errorf("revision_conflict", "群组地址已经更新")
			}
			desired.Revision++
			plan.Before[i].Rules[j].Revision = desired.Revision
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_steps(operation_id,step_key,sequence,status,result_json,finished_at) VALUES (?,'mailbox.mutation.prepare',2,'succeeded',?,?)`, id, toJSON(plan), db.Now()); err != nil {
		return err
	}
	if kind == "mailbox.suspend" || kind == "mailbox.archive" || kind == "mailbox.deleteRemote" {
		ctx = context.WithValue(ctx, operationContextKey{}, id)
		return commitMailboxMutation(ctx, tx, orgID, plan)
	}
	return nil
}

func readMailboxMutationPlan(ctx context.Context, q managementQuery, id string) (*mailboxMutationPlan, error) {
	var body string
	if err := q.QueryRowContext(ctx, `SELECT result_json FROM operation_steps WHERE operation_id=? AND step_key='mailbox.mutation.prepare'`, id).Scan(&body); err != nil {
		return nil, err
	}
	var plan mailboxMutationPlan
	if err := json.Unmarshal([]byte(body), &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func validateMailboxMutationProgress(ctx context.Context, q managementQuery, orgID int64, id string, plan *mailboxMutationPlan) error {
	var committed bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND step_key='mailbox.mutation.local.commit' AND status='succeeded')`, id).Scan(&committed); err != nil {
		return err
	}
	if err := validateMailboxMutation(ctx, q, orgID, plan, committed); err != nil {
		return err
	}
	groups := plan.Before
	if committed {
		groups = plan.Groups
	}
	for i := range groups {
		group := &groups[i]
		var done bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND step_key=? AND status='succeeded')`, id, "mailbox.group."+fmtID(group.GroupID)+".local.commit").Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		if err := validateRoutingPlan(ctx, q, orgID, group); err != nil {
			return err
		}
		if committed {
			if err := validateGroupCandidates(ctx, q, orgID, group); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) executeMailboxMutation(ctx context.Context, orgID, actor int64, payload OperationPayload) (any, error) {
	id := ctx.Value(operationContextKey{}).(string)
	plan, err := readMailboxMutationPlan(ctx, s.DB, id)
	if err != nil {
		return nil, err
	}
	var committed bool
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND step_key='mailbox.mutation.local.commit' AND status='succeeded')`, id).Scan(&committed); err != nil {
		return nil, err
	}
	if !committed {
		if payload.Kind == "mailbox.reactivate" {
			child := context.WithValue(ctx, intermediateOperationKey{}, true)
			child = context.WithValue(child, operationStepPrefixKey{}, "mailbox.mutation.")
			if err := s.DB.Tx(ctx, func(tx *sql.Tx) error {
				if err := checkOperationPermissionTx(ctx, tx, id); err != nil {
					return err
				}
				if err := validateMailboxMutation(ctx, tx, orgID, plan, false); err != nil {
					return err
				}
				for i := range plan.Before {
					if err := validateRoutingPlan(ctx, tx, orgID, &plan.Before[i]); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				return nil, err
			}
			if err := s.reactivateMailboxProtocols(child, orgID, actor, plan.MailboxID, plan.ExpectedRevision); err != nil {
				return nil, err
			}
		} else if err := s.DB.Tx(ctx, func(tx *sql.Tx) error { return commitMailboxMutation(ctx, tx, orgID, plan) }); err != nil {
			return nil, err
		}
		s.cancelMailRequests(0, plan.MailboxID)
		if s.Pool != nil {
			s.Pool.InvalidateMailbox(plan.MailboxID)
		}
	}
	if err := validateMailboxMutation(ctx, s.DB, orgID, plan, true); err != nil {
		return nil, err
	}
	for i := range plan.Groups {
		group := &plan.Groups[i]
		prefix := "mailbox.group." + fmtID(group.GroupID) + "."
		var done bool
		if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND step_key=? AND status='succeeded')`, id, prefix+"local.commit").Scan(&done); err != nil {
			return nil, err
		}
		if done {
			continue
		}
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO operation_steps(operation_id,step_key,sequence,status,result_json,finished_at) SELECT ?,?,COALESCE(MAX(sequence),0)+1,'succeeded',?,? FROM operation_steps WHERE operation_id=? ON CONFLICT(operation_id,step_key) DO NOTHING`, id, prefix+"routing.prepare", toJSON(group), db.Now(), id); err != nil {
			return nil, err
		}
		child := context.WithValue(ctx, intermediateOperationKey{}, true)
		child = context.WithValue(child, operationStepPrefixKey{}, prefix)
		if _, err := s.executeRouting(child, orgID, actor, OperationPayload{Kind: "group.update"}); err != nil {
			return nil, err
		}
	}
	result := map[string]any{"mailboxId": plan.MailboxID, "groupIds": []int64{}}
	ids := []int64{}
	for _, group := range plan.Groups {
		ids = append(ids, group.GroupID)
	}
	result["groupIds"] = ids
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := validateMailboxMutation(ctx, tx, orgID, plan, true); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(org_id,actor_member_id,action,target_type,target_id,detail_json,created_at) VALUES (?,?,?,'mailbox',?,?,?)`, orgID, actor, payload.Kind, fmtID(plan.MailboxID), toJSON(result), db.Now()); err != nil {
			return err
		}
		return completeLocalOperation(ctx, tx, result)
	})
	return result, err
}

func (s *Service) runLocalManagementOperation(ctx context.Context, orgID, actor int64, requestID string, payload OperationPayload) (*OperationView, error) {
	if requestID == "" {
		requestID = uuid.NewString()
	}
	op, _, err := s.QueueOperation(ctx, orgID, actor, requestID, payload, nil)
	if err != nil {
		return nil, err
	}
	if !s.operationsStarted.Load() && op.Status == "queued" {
		var sealed string
		if err := s.DB.QueryRowContext(ctx, `SELECT payload_enc FROM operations WHERE id=?`, op.ID).Scan(&sealed); err != nil {
			return nil, err
		}
		if err := s.executeOperation(ctx, orgID, actor, op.ID, sealed); err != nil {
			return nil, err
		}
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		op, err = s.Operation(ctx, orgID, op.ID)
		if err != nil {
			return nil, err
		}
		if op.Status == "succeeded" {
			return op, nil
		}
		if op.Status != "queued" && op.Status != "running" {
			code := "operation_needs_action"
			if op.ErrorCode != nil {
				code = *op.ErrorCode
			}
			failure := provider.Errorf(code, "管理操作需要处理未完成步骤")
			failure.OperationID = &op.ID
			failure.Details = op.Result
			return op, failure
		}
		select {
		case <-ctx.Done():
			return op, ctx.Err()
		case <-ticker.C:
		}
	}
}
