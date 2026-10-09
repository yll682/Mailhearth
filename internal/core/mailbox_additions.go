package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

type mailboxAdditionPlan struct {
	ConnectionID       int64         `json:"connectionId"`
	ConnectionRevision int64         `json:"connectionRevision"`
	Address            string        `json:"address"`
	OwnerID            int64         `json:"ownerId"`
	OwnerRevision      int64         `json:"ownerRevision"`
	Before             []routingPlan `json:"before"`
	Groups             []routingPlan `json:"groups"`
}

func mailboxAdditionOperation(kind string) bool {
	return kind == "mailbox.create" || kind == "mailbox.attach"
}

func mailboxAdditionKeys(plan *mailboxAdditionPlan) []string {
	keys := []string{"connection:" + fmtID(plan.ConnectionID), "mailbox-address:" + fmtID(plan.ConnectionID) + ":" + plan.Address}
	if plan.OwnerID > 0 {
		keys = append(keys, "member:"+fmtID(plan.OwnerID))
	}
	for i := range plan.Before {
		keys = append(keys, routingKeys(&plan.Before[i])...)
	}
	return keys
}

func (s *Service) prepareMailboxAddition(ctx context.Context, orgID, actor int64, payload *OperationPayload) (*mailboxAdditionPlan, error) {
	var owner *int64
	var kind, address string
	if payload.Kind == "mailbox.create" {
		if payload.Create == nil {
			return nil, provider.Errorf("invalid", "需要创建邮箱请求")
		}
		binding, err := s.DomainBinding(ctx, orgID, payload.Create.DomainBindingID)
		if err != nil {
			return nil, err
		}
		payload.ConnectionID = payload.Create.ConnectionID
		address = payload.Create.LocalPart + "@" + binding.DomainName
		owner = payload.Create.OwnerMemberID
		kind = payload.Create.Kind
	} else {
		if payload.Attach == nil {
			return nil, provider.Errorf("invalid", "需要登记邮箱请求")
		}
		in := payload.Attach
		if in.Mode != "attach" || in.CredentialMode != "entered" {
			return nil, provider.Errorf("invalid", "登记邮箱需要 attach 和 entered")
		}
		payload.ConnectionID = in.ConnectionID
		address = in.Address
		owner = in.OwnerMemberID
		kind = in.Kind
	}
	if kind != model.MailboxPersonal && kind != model.MailboxShared {
		return nil, provider.Errorf("invalid", "邮箱类型无效")
	}
	if kind == model.MailboxShared && owner != nil {
		return nil, provider.Errorf("invalid", "共享邮箱不能设置 owner")
	}
	c, err := s.MailConnection(ctx, orgID, payload.ConnectionID)
	if err != nil {
		return nil, err
	}
	if !c.Enabled {
		return nil, provider.Errorf("endpoint_disabled", "连接已经停用")
	}
	canonical, key, err := canonicalMailboxAddress(c.ProviderKind, address)
	if err != nil {
		return nil, err
	}
	if payload.Attach != nil {
		payload.Attach.Address = canonical
		if err := validateFolderMappingInput(payload.Attach.FolderMapping); err != nil {
			return nil, err
		}
		if _, err := s.candidateEndpoints(ctx, orgID, 0, c, payload.Attach.Credentials, payload.Attach.Endpoints); err != nil {
			return nil, err
		}
	}
	var exists bool
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mailboxes WHERE connection_id=? AND address_key=?)`, c.ID, key).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, provider.Errorf("address_exists", "该连接已经登记此邮箱")
	}
	plan := &mailboxAdditionPlan{ConnectionID: c.ID, ConnectionRevision: c.Revision, Address: canonical, Before: []routingPlan{}, Groups: []routingPlan{}}
	if owner == nil {
		return plan, nil
	}
	member, err := s.Member(ctx, orgID, *owner)
	if err != nil {
		return nil, err
	}
	if member.Status != model.MemberActive && member.Status != model.MemberInvited {
		return nil, provider.Errorf("invalid", "owner 状态无效")
	}
	plan.OwnerID = member.ID
	plan.OwnerRevision = member.Revision
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
		body, err := json.Marshal(before)
		if err != nil {
			return nil, err
		}
		var desired routingPlan
		if err := json.Unmarshal(body, &desired); err != nil {
			return nil, err
		}
		for i := range desired.Rules {
			rule := &desired.Rules[i]
			connection, err := s.MailConnection(ctx, orgID, rule.ConnectionID)
			if err != nil {
				return nil, err
			}
			_, domain := SplitAddress(canonical)
			if connection.ProviderKind == provider.Migadu && (c.ID != rule.ConnectionID || domain != rule.Domain) {
				failure := provider.Errorf("target_constraint_failed", "群组候选邮箱需要属于相同连接和域名")
				failure.Details = map[string]any{"memberId": member.ID, "groupId": id, "reasonCode": "same_connection_and_domain_required"}
				return nil, failure
			}
			rule.Targets = append(rule.Targets, strings.ToLower(canonical))
			sort.Strings(rule.Targets)
			rule.Targets = uniqueStrings(rule.Targets)
		}
		plan.Before = append(plan.Before, *before)
		plan.Groups = append(plan.Groups, desired)
	}
	return plan, nil
}

func validateMailboxAdditionOwner(ctx context.Context, q managementQuery, orgID int64, plan *mailboxAdditionPlan) error {
	var revision int64
	var enabled bool
	if err := q.QueryRowContext(ctx, `SELECT revision,enabled FROM mail_connections WHERE id=? AND org_id=?`, plan.ConnectionID, orgID).Scan(&revision, &enabled); err != nil {
		return err
	}
	if revision != plan.ConnectionRevision {
		return provider.Errorf("revision_conflict", "邮箱所属连接已经更新")
	}
	if !enabled {
		return provider.Errorf("endpoint_disabled", "邮箱所属连接已经停用")
	}
	if plan.OwnerID > 0 {
		var status string
		if err := q.QueryRowContext(ctx, `SELECT revision,status FROM members WHERE id=? AND org_id=?`, plan.OwnerID, orgID).Scan(&revision, &status); err != nil {
			return err
		}
		if revision != plan.OwnerRevision || (status != model.MemberActive && status != model.MemberInvited) {
			return provider.Errorf("revision_conflict", "邮箱所有者已经更新")
		}
	}
	return nil
}

func reserveMailboxAddition(ctx context.Context, tx *sql.Tx, orgID int64, id string, plan *mailboxAdditionPlan) error {
	if err := validateMailboxAdditionOwner(ctx, tx, orgID, plan); err != nil {
		return err
	}
	for i := range plan.Before {
		if err := validateRoutingPlan(ctx, tx, orgID, &plan.Before[i]); err != nil {
			return err
		}
		if err := validateGroupCandidates(ctx, tx, orgID, &plan.Before[i]); err != nil {
			return err
		}
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
	_, err := tx.ExecContext(ctx, `INSERT INTO operation_steps(operation_id,step_key,sequence,status,result_json,finished_at) VALUES (?,'mailbox.addition.prepare',2,'succeeded',?,?)`, id, toJSON(plan), db.Now())
	return err
}

func readMailboxAdditionPlan(ctx context.Context, q managementQuery, id string) (*mailboxAdditionPlan, error) {
	var body string
	if err := q.QueryRowContext(ctx, `SELECT result_json FROM operation_steps WHERE operation_id=? AND step_key='mailbox.addition.prepare'`, id).Scan(&body); err != nil {
		return nil, err
	}
	var plan mailboxAdditionPlan
	if err := json.Unmarshal([]byte(body), &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func mailboxAdditionCommitted(ctx context.Context, q managementQuery, id string) (int64, int64, error) {
	var mailboxID, revision int64
	err := q.QueryRowContext(ctx, `SELECT json_extract(result_json,'$.mailboxId'),json_extract(result_json,'$.mailboxRevision') FROM operation_steps WHERE operation_id=? AND step_key='local.commit' AND status='succeeded'`, id).Scan(&mailboxID, &revision)
	if db.IsNotFound(err) {
		return 0, 0, nil
	}
	return mailboxID, revision, err
}

func validateMailboxAdditionProgress(ctx context.Context, q managementQuery, orgID int64, id string, plan *mailboxAdditionPlan) error {
	if err := validateMailboxAdditionOwner(ctx, q, orgID, plan); err != nil {
		return err
	}
	mailboxID, revision, err := mailboxAdditionCommitted(ctx, q, id)
	if err != nil {
		return err
	}
	if mailboxID > 0 {
		var valid bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mailboxes WHERE id=? AND org_id=? AND connection_id=? AND address=? AND revision=? AND kind IN ('personal','shared') AND status='active' AND COALESCE(owner_member_id,0)=?)`, mailboxID, orgID, plan.ConnectionID, plan.Address, revision, plan.OwnerID).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return provider.Errorf("revision_conflict", "新增邮箱已经更新")
		}
	}
	for i := range plan.Groups {
		group := plan.Groups[i]
		var done bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND step_key=? AND status='succeeded')`, id, "mailbox.addition.group."+fmtID(group.GroupID)+".local.commit").Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		if mailboxID == 0 {
			if err := validateRoutingPlan(ctx, q, orgID, &plan.Before[i]); err != nil {
				return err
			}
			if err := validateGroupCandidates(ctx, q, orgID, &plan.Before[i]); err != nil {
				return err
			}
		} else {
			group.Mailboxes = append(group.Mailboxes, routingVersion{mailboxID, revision})
			if err := validateRoutingPlan(ctx, q, orgID, &group); err != nil {
				return err
			}
			if err := validateGroupCandidates(ctx, q, orgID, &group); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) executeMailboxAddition(ctx context.Context, orgID, actor int64, payload OperationPayload) (any, error) {
	id := ctx.Value(operationContextKey{}).(string)
	plan, err := readMailboxAdditionPlan(ctx, s.DB, id)
	if err != nil {
		return nil, err
	}
	if err := s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := checkOperationPermissionTx(ctx, tx, id); err != nil {
			return err
		}
		return validateMailboxAdditionProgress(ctx, tx, orgID, id, plan)
	}); err != nil {
		return nil, err
	}
	mailboxID, revision, err := mailboxAdditionCommitted(ctx, s.DB, id)
	if err != nil {
		return nil, err
	}
	if mailboxID == 0 {
		child := context.WithValue(ctx, intermediateOperationKey{}, true)
		if payload.Kind == "mailbox.create" {
			if _, err := s.executeMailboxManagement(child, orgID, actor, payload); err != nil {
				return nil, err
			}
		} else {
			if payload.Attach == nil {
				return nil, provider.Errorf("invalid", "需要登记邮箱请求")
			}
			if _, err := s.AttachMailbox(child, orgID, actor, *payload.Attach); err != nil {
				return nil, err
			}
		}
		mailboxID, revision, err = mailboxAdditionCommitted(ctx, s.DB, id)
		if err != nil {
			return nil, err
		}
		if mailboxID < 1 || revision < 1 {
			return nil, provider.Errorf("internal", "邮箱提交结果缺少 ID 或版本")
		}
	}
	if err := validateMailboxAdditionProgress(ctx, s.DB, orgID, id, plan); err != nil {
		return nil, err
	}
	groupIDs := []int64{}
	for i := range plan.Groups {
		group := plan.Groups[i]
		groupIDs = append(groupIDs, group.GroupID)
		prefix := "mailbox.addition.group." + fmtID(group.GroupID) + "."
		var done bool
		if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND step_key=? AND status='succeeded')`, id, prefix+"local.commit").Scan(&done); err != nil {
			return nil, err
		}
		if done {
			continue
		}
		group.Mailboxes = append(group.Mailboxes, routingVersion{mailboxID, revision})
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO operation_steps(operation_id,step_key,sequence,status,result_json,finished_at) SELECT ?,?,COALESCE(MAX(sequence),0)+1,'succeeded',?,? FROM operation_steps WHERE operation_id=? ON CONFLICT(operation_id,step_key) DO NOTHING`, id, prefix+"routing.prepare", toJSON(group), db.Now(), id); err != nil {
			return nil, err
		}
		child := context.WithValue(ctx, intermediateOperationKey{}, true)
		child = context.WithValue(child, operationStepPrefixKey{}, prefix)
		if _, err := s.executeRouting(child, orgID, actor, OperationPayload{Kind: "group.update"}); err != nil {
			return nil, err
		}
	}
	result := map[string]any{"mailboxId": mailboxID, "groupIds": groupIDs}
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := validateMailboxAdditionProgress(ctx, tx, orgID, id, plan); err != nil {
			return err
		}
		return completeLocalOperation(ctx, tx, result)
	})
	return result, err
}
