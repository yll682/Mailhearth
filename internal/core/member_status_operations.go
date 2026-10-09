package core

import (
	"context"
	"database/sql"
	"encoding/json"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

type MemberStatusInput struct {
	RequestID        string `json:"requestId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Enabled          *bool  `json:"enabled"`
}

type memberStatusPlan struct {
	MemberID int64         `json:"memberId"`
	Revision int64         `json:"revision"`
	Status   string        `json:"status"`
	Before   []routingPlan `json:"before"`
	Groups   []routingPlan `json:"groups"`
}

func memberStatusKeys(plan *memberStatusPlan) []string {
	keys := []string{"member:" + fmtID(plan.MemberID)}
	for i := range plan.Before {
		keys = append(keys, routingKeys(&plan.Before[i])...)
	}
	for i := range plan.Groups {
		keys = append(keys, routingKeys(&plan.Groups[i])...)
	}
	return keys
}

func (s *Service) prepareMemberStatus(ctx context.Context, orgID, actor int64, payload *OperationPayload) (*memberStatusPlan, error) {
	in := payload.MemberStatus
	if in == nil || in.Enabled == nil {
		return nil, provider.Errorf("invalid", "需要明确的 enabled 值")
	}
	member, err := s.Member(ctx, orgID, payload.MemberID)
	if err != nil {
		return nil, err
	}
	if in.ExpectedRevision < 1 || in.ExpectedRevision != member.Revision {
		return nil, provider.Errorf("revision_conflict", "需要成员当前版本")
	}
	if member.Status == model.MemberDeparted {
		return nil, provider.Errorf("invalid", "离职成员不能通过启用或停用操作修改")
	}
	if !*in.Enabled && (member.ID == actor || member.RoleKey == model.RoleOwner) {
		return nil, provider.Errorf("invalid", "不能停用当前成员或组织所有者")
	}
	plan := &memberStatusPlan{MemberID: member.ID, Revision: member.Revision, Status: model.MemberDisabled, Before: []routingPlan{}, Groups: []routingPlan{}}
	if *in.Enabled {
		plan.Status = model.MemberInvited
		if member.HasPassword {
			plan.Status = model.MemberActive
		}
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT g.id FROM groups g JOIN group_members gm ON gm.group_id=g.id WHERE g.org_id=? AND gm.member_id=? ORDER BY g.id`, orgID, member.ID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	future := *member
	future.Status = plan.Status
	future.Revision++
	for _, id := range ids {
		if err := s.requireOperationPermission(ctx, orgID, actor, 0, model.PermGroupsManage); err != nil {
			return nil, err
		}
		group, err := s.Group(ctx, orgID, id)
		if err != nil {
			return nil, err
		}
		input := GroupInput{ExpectedRevision: group.Revision, Name: group.Name, Description: group.Description, MemberIDs: group.MemberIDs}
		before, err := s.prepareRouting(ctx, orgID, "group.update", &RoutingOperationInput{GroupID: id, Group: &input})
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(before)
		if err != nil {
			return nil, err
		}
		var desired routingPlan
		if err := json.Unmarshal(data, &desired); err != nil {
			return nil, err
		}
		for j := range desired.Members {
			if desired.Members[j].ID == member.ID {
				desired.Members[j].Revision = future.Revision
			}
		}
		for j := range desired.Rules {
			targets, versions, err := computeGroupCandidateTargets(ctx, s.DB, orgID, id, desired.GroupMembers, desired.Rules[j], &model.Mailbox{}, &future)
			if err != nil {
				return nil, err
			}
			desired.Rules[j].Targets = targets
			desired.Mailboxes = versions
		}
		plan.Before = append(plan.Before, *before)
		plan.Groups = append(plan.Groups, desired)
	}
	return plan, nil
}

func validateMemberStatusProgress(ctx context.Context, q managementQuery, orgID int64, id string, plan *memberStatusPlan) error {
	var committed bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND step_key='member.status.commit' AND status='succeeded')`, id).Scan(&committed); err != nil {
		return err
	}
	revision := plan.Revision
	if committed {
		revision++
	}
	var current bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM members WHERE org_id=? AND id=? AND revision=? AND status!='departed')`, orgID, plan.MemberID, revision).Scan(&current); err != nil {
		return err
	}
	if !current {
		return provider.Errorf("revision_conflict", "成员已经更新")
	}
	groups := plan.Before
	if committed {
		groups = plan.Groups
	}
	for i := range groups {
		group := &groups[i]
		var done bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND step_key=? AND status='succeeded')`, id, "member.status.group."+fmtID(group.GroupID)+".local.commit").Scan(&done); err != nil {
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

func commitMemberStatus(ctx context.Context, tx *sql.Tx, orgID int64, id string, plan *memberStatusPlan) error {
	if err := checkOperationPermissionTx(ctx, tx, id); err != nil {
		return err
	}
	if err := validateMemberStatusProgress(ctx, tx, orgID, id, plan); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE members SET status=?,revision=revision+1,updated_at=? WHERE org_id=? AND id=? AND revision=? AND status!='departed'`, plan.Status, db.Now(), orgID, plan.MemberID, plan.Revision)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return provider.Errorf("revision_conflict", "成员已经更新")
	}
	if plan.Status == model.MemberDisabled {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE member_id=?`, plan.MemberID); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO operation_steps(operation_id,step_key,sequence,status,result_json,finished_at) SELECT ?,'member.status.commit',COALESCE(MAX(sequence),0)+1,'succeeded',?,? FROM operation_steps WHERE operation_id=?`, id, toJSON(map[string]any{"memberId": plan.MemberID, "revision": plan.Revision + 1, "status": plan.Status}), db.Now(), id)
	return err
}

func reserveMemberStatus(ctx context.Context, tx *sql.Tx, orgID int64, id string, plan *memberStatusPlan) error {
	if err := validateMemberStatusProgress(ctx, tx, orgID, id, plan); err != nil {
		return err
	}
	for i := range plan.Groups {
		for j := range plan.Groups[i].Rules {
			rule := &plan.Groups[i].Rules[j]
			res, err := tx.ExecContext(ctx, `UPDATE addresses SET desired_targets_json=?,sync_state='pending',revision=revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=?`, toJSON(rule.Targets), db.Now(), rule.ID, orgID, rule.Revision)
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
			rule.Revision++
			plan.Before[i].Rules[j].Revision = rule.Revision
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_steps(operation_id,step_key,sequence,status,result_json,finished_at) VALUES (?,'member.status.prepare',2,'succeeded',?,?)`, id, toJSON(plan), db.Now()); err != nil {
		return err
	}
	if plan.Status == model.MemberDisabled {
		return commitMemberStatus(ctx, tx, orgID, id, plan)
	}
	return nil
}

func readMemberStatusPlan(ctx context.Context, q managementQuery, id string) (*memberStatusPlan, error) {
	var body string
	if err := q.QueryRowContext(ctx, `SELECT result_json FROM operation_steps WHERE operation_id=? AND step_key='member.status.prepare'`, id).Scan(&body); err != nil {
		return nil, err
	}
	var plan memberStatusPlan
	if err := json.Unmarshal([]byte(body), &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func (s *Service) executeMemberStatus(ctx context.Context, orgID, actor int64, payload OperationPayload) (any, error) {
	id := ctx.Value(operationContextKey{}).(string)
	plan, err := readMemberStatusPlan(ctx, s.DB, id)
	if err != nil {
		return nil, err
	}
	var committed bool
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND step_key='member.status.commit' AND status='succeeded')`, id).Scan(&committed); err != nil {
		return nil, err
	}
	if !committed {
		if err := s.DB.Tx(ctx, func(tx *sql.Tx) error { return commitMemberStatus(ctx, tx, orgID, id, plan) }); err != nil {
			return nil, err
		}
		if plan.Status == model.MemberDisabled {
			s.cancelMailRequests(plan.MemberID, 0)
		}
	}
	if err := validateMemberStatusProgress(ctx, s.DB, orgID, id, plan); err != nil {
		return nil, err
	}
	for i := range plan.Groups {
		group := &plan.Groups[i]
		prefix := "member.status.group." + fmtID(group.GroupID) + "."
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
	result := map[string]any{"memberId": plan.MemberID, "status": plan.Status}
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := validateMemberStatusProgress(ctx, tx, orgID, id, plan); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(org_id,actor_member_id,action,target_type,target_id,detail_json,created_at) VALUES (?,?,'member.status','member',?,?,?)`, orgID, actor, fmtID(plan.MemberID), toJSON(result), db.Now()); err != nil {
			return err
		}
		return completeLocalOperation(ctx, tx, result)
	})
	return result, err
}
