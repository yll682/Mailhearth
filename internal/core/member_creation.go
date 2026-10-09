package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
	"mailhearth/internal/secrets"
)

type memberCreationPlan struct {
	MemberID           int64            `json:"memberId"`
	RoleID             int64            `json:"roleId"`
	LoginEmail         string           `json:"loginEmail"`
	PasswordHash       string           `json:"-"`
	MailboxID          int64            `json:"mailboxId"`
	MailboxRevision    int64            `json:"mailboxRevision"`
	ConnectionID       int64            `json:"connectionId"`
	ConnectionRevision int64            `json:"connectionRevision"`
	BindingID          int64            `json:"bindingId"`
	BindingRevision    int64            `json:"bindingRevision"`
	Address            string           `json:"address"`
	Groups             []routingPlan    `json:"groups"`
	Shared             []routingVersion `json:"shared"`
}

func memberCreationKeys(plan *memberCreationPlan) []string {
	keys := []string{"member-login:" + plan.LoginEmail}
	if plan.MemberID > 0 {
		keys = append(keys, "member:"+fmtID(plan.MemberID))
	}
	if plan.MailboxID > 0 {
		keys = append(keys, "mailbox:"+fmtID(plan.MailboxID))
	}
	if plan.ConnectionID > 0 {
		keys = append(keys, "connection:"+fmtID(plan.ConnectionID), "mailbox-address:"+fmtID(plan.ConnectionID)+":"+plan.Address)
	}
	if plan.BindingID > 0 {
		keys = append(keys, "domain-binding:"+fmtID(plan.BindingID))
	}
	for i := range plan.Groups {
		keys = append(keys, routingKeys(&plan.Groups[i])...)
	}
	for _, item := range plan.Shared {
		keys = append(keys, "mailbox:"+fmtID(item.ID))
	}
	return keys
}

func (s *Service) prepareMemberCreation(ctx context.Context, orgID, actor int64, in *CreateMemberRequest) (*memberCreationPlan, error) {
	if in == nil || strings.TrimSpace(in.DisplayName) == "" {
		return nil, provider.Errorf("invalid", "需要成员姓名和创建请求")
	}
	if in.NewMailbox != nil || in.BindMailboxID != 0 {
		return nil, provider.Errorf("invalid", "需要使用 mailboxAction 和对应请求结构")
	}
	if in.MailboxAction != "none" && in.MailboxAction != "bind" && in.MailboxAction != "create" && in.MailboxAction != "attach" {
		return nil, provider.Errorf("invalid", "mailboxAction 无效")
	}
	if (in.MailboxAction != "bind" && (in.MailboxID != 0 || in.MailboxExpectedRevision != 0)) || (in.MailboxAction != "create" && in.Create != nil) || (in.MailboxAction != "attach" && in.Attach != nil) {
		return nil, provider.Errorf("invalid", "邮箱操作字段必须互斥")
	}
	plan := &memberCreationPlan{Groups: []routingPlan{}, Shared: []routingVersion{}}
	roleID := in.RoleID
	if roleID == 0 {
		role, err := s.roleByKey(ctx, s.DB, orgID, model.RoleMember)
		if err != nil {
			return nil, err
		}
		roleID = role.ID
	}
	role, err := s.Role(ctx, orgID, roleID)
	if err != nil {
		return nil, err
	}
	if role.Key == model.RoleOwner {
		return nil, provider.Errorf("invalid", "组织所有者通过所有权转移操作设置")
	}
	plan.RoleID = role.ID
	if in.Password != "" {
		if err := checkPasswordStrength(in.Password); err != nil {
			return nil, err
		}
		plan.PasswordHash, err = secrets.HashPassword(in.Password)
		if err != nil {
			return nil, err
		}
	}
	if in.MailboxAction != "none" {
		if err := s.requireOperationPermission(ctx, orgID, actor, 0, model.PermMailboxesManage); err != nil {
			return nil, err
		}
	}
	switch in.MailboxAction {
	case "bind":
		mb, err := s.Mailbox(ctx, orgID, in.MailboxID)
		if err != nil {
			return nil, err
		}
		if mb.Kind != model.MailboxPersonal || mb.OwnerMemberID != nil || mb.Status != model.MailboxActive {
			return nil, provider.Errorf("invalid", "bind 需要未指定 owner 的 active personal 邮箱")
		}
		if in.MailboxExpectedRevision < 1 || in.MailboxExpectedRevision != mb.Revision {
			return nil, provider.Errorf("revision_conflict", "需要邮箱当前版本")
		}
		plan.MailboxID = mb.ID
		plan.MailboxRevision = mb.Revision
		plan.ConnectionID = mb.ConnectionID
		plan.Address = mb.Address
	case "create":
		if in.Create == nil || in.Create.OwnerMemberID != nil || in.Create.Kind != model.MailboxPersonal {
			return nil, provider.Errorf("invalid", "成员邮箱创建需要 personal 类型且不指定 owner")
		}
		payload := OperationPayload{Kind: "mailbox.create", Create: in.Create}
		if _, err := s.prepareMailboxManagement(ctx, orgID, &payload); err != nil {
			return nil, err
		}
		binding, err := s.DomainBinding(ctx, orgID, in.Create.DomainBindingID)
		if err != nil {
			return nil, err
		}
		plan.ConnectionID = binding.ConnectionID
		plan.BindingID = binding.ID
		plan.BindingRevision = binding.Revision
		plan.Address = in.Create.LocalPart + "@" + binding.DomainName
	case "attach":
		if in.Attach != nil {
			if err := validateFolderMappingInput(in.Attach.FolderMapping); err != nil {
				return nil, err
			}
		}
		if in.Attach == nil || in.Attach.Mode != "attach" || in.Attach.CredentialMode != "entered" || in.Attach.OwnerMemberID != nil || in.Attach.Kind != model.MailboxPersonal {
			return nil, provider.Errorf("invalid", "成员邮箱登记需要 attach、entered 和 personal，且不指定 owner")
		}
		c, err := s.MailConnection(ctx, orgID, in.Attach.ConnectionID)
		if err != nil {
			return nil, err
		}
		if !c.Enabled {
			return nil, provider.Errorf("endpoint_disabled", "连接已经停用")
		}
		address, key, err := canonicalMailboxAddress(c.ProviderKind, in.Attach.Address)
		if err != nil {
			return nil, err
		}
		in.Attach.Address = address
		plan.Address = address
		plan.ConnectionID = c.ID
		var exists bool
		if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mailboxes WHERE connection_id=? AND address_key=?)`, c.ID, key).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			return nil, provider.Errorf("address_exists", "该连接已经登记此邮箱")
		}
		if _, err := s.candidateEndpoints(ctx, orgID, 0, c, in.Attach.Credentials, in.Attach.Endpoints); err != nil {
			return nil, err
		}
		if in.Attach.SentCopyMode != "" && in.Attach.SentCopyMode != "append" && in.Attach.SentCopyMode != "server" {
			return nil, provider.Errorf("invalid", "sentCopyMode 无效")
		}
	}
	if plan.ConnectionID > 0 {
		c, err := s.MailConnection(ctx, orgID, plan.ConnectionID)
		if err != nil {
			return nil, err
		}
		plan.ConnectionRevision = c.Revision
	}
	login := strings.TrimSpace(in.LoginEmail)
	if login == "" {
		login = plan.Address
	}
	plan.LoginEmail, err = NormalizeEmail(login)
	if err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	for _, id := range in.SharedMailboxes {
		if seen[id] {
			return nil, provider.Errorf("invalid", "共享授权不能重复")
		}
		seen[id] = true
		mb, err := s.Mailbox(ctx, orgID, id)
		if err != nil {
			return nil, err
		}
		if mb.Kind != model.MailboxShared || mb.Status != model.MailboxActive {
			return nil, provider.Errorf("invalid", "共享授权需要 active shared 邮箱")
		}
		if in.SharedExpectedRevisions[id] != mb.Revision {
			return nil, provider.Errorf("revision_conflict", "共享邮箱已经更新")
		}
		if err := s.requireOperationPermission(ctx, orgID, actor, id, model.PermSharedManage); err != nil {
			return nil, err
		}
		plan.Shared = append(plan.Shared, routingVersion{id, mb.Revision})
	}
	seen = map[int64]bool{}
	for _, id := range in.GroupIDs {
		if seen[id] {
			return nil, provider.Errorf("invalid", "群组不能重复")
		}
		seen[id] = true
		if err := s.requireOperationPermission(ctx, orgID, actor, 0, model.PermGroupsManage); err != nil {
			return nil, err
		}
		group, err := s.Group(ctx, orgID, id)
		if err != nil {
			return nil, err
		}
		if in.GroupExpectedRevisions[id] != group.Revision {
			return nil, provider.Errorf("revision_conflict", "群组已经更新")
		}
		input := GroupInput{ExpectedRevision: group.Revision, Name: group.Name, Description: group.Description, MemberIDs: append([]int64{}, group.MemberIDs...)}
		routing, err := s.prepareRouting(ctx, orgID, "group.update", &RoutingOperationInput{GroupID: id, Group: &input})
		if err != nil {
			return nil, err
		}
		for i := range routing.Rules {
			rule := &routing.Rules[i]
			if plan.Address == "" {
				continue
			}
			c, err := s.MailConnection(ctx, orgID, rule.ConnectionID)
			if err != nil {
				return nil, err
			}
			_, domain := SplitAddress(plan.Address)
			if c.ProviderKind == provider.Migadu && (plan.ConnectionID != rule.ConnectionID || domain != rule.Domain) {
				failure := provider.Errorf("target_constraint_failed", "群组候选邮箱需要属于相同连接和域名")
				failure.Details = map[string]any{"mailboxId": plan.MailboxID, "groupId": id, "reasonCode": "same_connection_and_domain_required"}
				return nil, failure
			}
			rule.Targets = append(rule.Targets, strings.ToLower(plan.Address))
			sort.Strings(rule.Targets)
			rule.Targets = uniqueStrings(rule.Targets)
		}
		plan.Groups = append(plan.Groups, *routing)
	}
	return plan, nil
}

func uniqueStrings(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}

func validateMemberCreation(ctx context.Context, q managementQuery, orgID int64, plan *memberCreationPlan) error {
	if plan.ConnectionID > 0 {
		var revision int64
		var enabled bool
		if err := q.QueryRowContext(ctx, `SELECT revision,enabled FROM mail_connections WHERE id=? AND org_id=?`, plan.ConnectionID, orgID).Scan(&revision, &enabled); err != nil {
			return err
		}
		if revision != plan.ConnectionRevision {
			return provider.Errorf("revision_conflict", "成员邮箱的连接已经更新")
		}
		if !enabled {
			return provider.Errorf("endpoint_disabled", "成员邮箱的连接已经停用")
		}
	}
	if plan.BindingID > 0 {
		var revision int64
		if err := q.QueryRowContext(ctx, `SELECT revision FROM domain_bindings WHERE id=? AND org_id=?`, plan.BindingID, orgID).Scan(&revision); err != nil {
			return err
		}
		if revision != plan.BindingRevision {
			return provider.Errorf("revision_conflict", "成员邮箱的域名关联已经更新")
		}
	}
	if plan.MailboxID > 0 {
		var revision int64
		var owner sql.NullInt64
		if err := q.QueryRowContext(ctx, `SELECT revision,owner_member_id FROM mailboxes WHERE id=? AND org_id=?`, plan.MailboxID, orgID).Scan(&revision, &owner); err != nil {
			return err
		}
		if revision != plan.MailboxRevision || owner.Valid {
			return provider.Errorf("revision_conflict", "待绑定邮箱已经更新")
		}
	}
	for _, item := range plan.Shared {
		var revision int64
		if err := q.QueryRowContext(ctx, `SELECT revision FROM mailboxes WHERE id=? AND org_id=? AND kind='shared' AND status='active'`, item.ID, orgID).Scan(&revision); err != nil {
			return err
		}
		if revision != item.Revision {
			return provider.Errorf("revision_conflict", "共享邮箱已经更新")
		}
	}
	for i := range plan.Groups {
		if err := validateRoutingPlan(ctx, q, orgID, &plan.Groups[i]); err != nil {
			return err
		}
	}
	return nil
}

func reserveMemberCreation(ctx context.Context, tx *sql.Tx, orgID int64, id string, in *CreateMemberRequest, plan *memberCreationPlan) error {
	if err := validateMemberCreation(ctx, tx, orgID, plan); err != nil {
		return err
	}
	var role string
	if err := tx.QueryRowContext(ctx, `SELECT key FROM roles WHERE id=? AND org_id=?`, plan.RoleID, orgID).Scan(&role); err != nil {
		return err
	}
	if role == model.RoleOwner {
		return provider.Errorf("invalid", "成员角色无效")
	}
	now := db.Now()
	res, err := tx.ExecContext(ctx, `INSERT INTO members(org_id,display_name,login_email,password_hash,role_id,title,department,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,'invited',?,?)`, orgID, strings.TrimSpace(in.DisplayName), plan.LoginEmail, plan.PasswordHash, plan.RoleID, strings.TrimSpace(in.Title), strings.TrimSpace(in.Department), now, now)
	if err != nil {
		return err
	}
	plan.MemberID, err = res.LastInsertId()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_locks(org_id,resource_key,operation_id) VALUES (?,?,?)`, orgID, "member:"+fmtID(plan.MemberID), id); err != nil {
		return err
	}
	for i := range plan.Groups {
		group := &plan.Groups[i]
		group.GroupMembers = append(group.GroupMembers, plan.MemberID)
		sort.Slice(group.GroupMembers, func(i, j int) bool { return group.GroupMembers[i] < group.GroupMembers[j] })
		group.Members = append(group.Members, routingVersion{plan.MemberID, 1})
		for j := range group.Rules {
			rule := &group.Rules[j]
			res, err := tx.ExecContext(ctx, `UPDATE addresses SET desired_targets_json=?,sync_state='pending',revision=revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=?`, toJSON(rule.Targets), now, rule.ID, orgID, rule.Revision)
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
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE operations SET result_json=? WHERE id=?`, toJSON(map[string]int64{"memberId": plan.MemberID}), id); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO operation_steps(operation_id,step_key,sequence,status,result_json,finished_at) VALUES (?,'member.prepare',2,'succeeded',?,?)`, id, toJSON(plan), now)
	return err
}

func (s *Service) executeMemberCreation(ctx context.Context, orgID, actor int64, in *CreateMemberRequest) (result any, runErr error) {
	id := ctx.Value(operationContextKey{}).(string)
	var body string
	if err := s.DB.QueryRowContext(ctx, `SELECT result_json FROM operation_steps WHERE operation_id=? AND step_key='member.prepare'`, id).Scan(&body); err != nil {
		return nil, err
	}
	var plan memberCreationPlan
	if err := json.Unmarshal([]byte(body), &plan); err != nil {
		return nil, err
	}
	defer func() {
		if runErr != nil {
			mailboxID, readErr := memberCreationMailboxID(ctx, s.DB, id, plan.MailboxID)
			if readErr != nil {
				runErr = errors.Join(runErr, readErr)
			}
			failure := provider.Errorf(operationErrorCode(runErr), "成员创建操作需要处理未完成步骤")
			details := map[string]any{"memberId": plan.MemberID, "mailboxId": mailboxID}
			var typed *provider.TypedError
			if errors.As(runErr, &typed) {
				if values, ok := typed.Details.(map[string]any); ok {
					for key, value := range values {
						details[key] = value
					}
				}
			}
			failure.Details = details
			var unknown *remoteOutcomeUnknown
			if errors.As(runErr, &unknown) {
				runErr = &remoteOutcomeUnknown{failure}
			} else {
				runErr = failure
			}
		}
	}()
	if in.MailboxAction != "none" {
		if err := s.requireOperationPermission(ctx, orgID, actor, 0, model.PermMailboxesManage); err != nil {
			return nil, err
		}
	}
	if err := s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := checkOperationPermissionTx(ctx, tx, id); err != nil {
			return err
		}
		return validateMemberCreationProgress(ctx, tx, orgID, id, &plan)
	}); err != nil {
		return nil, err
	}
	var mailboxID, mailboxRevision int64
	err := s.DB.QueryRowContext(ctx, `SELECT json_extract(result_json,'$.mailboxId'),json_extract(result_json,'$.mailboxRevision') FROM operation_steps WHERE operation_id=? AND step_key='member.mailbox.local.commit' AND status='succeeded'`, id).Scan(&mailboxID, &mailboxRevision)
	if err != nil && !db.IsNotFound(err) {
		return nil, err
	}
	if mailboxID == 0 && in.MailboxAction != "none" {
		child := context.WithValue(ctx, intermediateOperationKey{}, true)
		child = context.WithValue(child, operationStepPrefixKey{}, "member.mailbox.")
		switch in.MailboxAction {
		case "create":
			create := *in.Create
			create.OwnerMemberID = &plan.MemberID
			value, err := s.executeMailboxManagement(child, orgID, actor, OperationPayload{Kind: "mailbox.create", Create: &create})
			if err != nil {
				return nil, err
			}
			data, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			var saved struct {
				ID int64 `json:"mailboxId"`
			}
			if err := json.Unmarshal(data, &saved); err != nil {
				return nil, err
			}
			mailboxID = saved.ID
		case "attach":
			attach := *in.Attach
			attach.OwnerMemberID = &plan.MemberID
			mb, err := s.AttachMailbox(child, orgID, actor, attach)
			if err != nil {
				return nil, err
			}
			mailboxID = mb.ID
		case "bind":
			err := s.DB.Tx(ctx, func(tx *sql.Tx) error {
				if err := checkOperationPermissionTx(ctx, tx, id); err != nil {
					return err
				}
				res, err := tx.ExecContext(ctx, `UPDATE mailboxes SET owner_member_id=?,revision=revision+1,access_revision=access_revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=? AND owner_member_id IS NULL AND kind='personal' AND status='active'`, plan.MemberID, db.Now(), plan.MailboxID, orgID, plan.MailboxRevision)
				if err != nil {
					return err
				}
				n, err := res.RowsAffected()
				if err != nil {
					return err
				}
				if n != 1 {
					return provider.Errorf("revision_conflict", "待绑定邮箱已经更新")
				}
				return completeLocalOperation(child, tx, map[string]int64{"mailboxId": plan.MailboxID})
			})
			if err != nil {
				return nil, err
			}
			mailboxID = plan.MailboxID
		}
		plan.MailboxID = mailboxID
		s.cancelMailRequests(0, mailboxID)
		if s.Pool != nil {
			s.Pool.InvalidateMailbox(mailboxID)
		}
		if err := s.DB.QueryRowContext(ctx, `SELECT json_extract(result_json,'$.mailboxRevision') FROM operation_steps WHERE operation_id=? AND step_key='member.mailbox.local.commit'`, id).Scan(&mailboxRevision); err != nil {
			return nil, err
		}
	}
	for i := range plan.Groups {
		group := &plan.Groups[i]
		prefix := "member.group." + fmtID(group.GroupID) + "."
		key := prefix + "local.commit"
		var done bool
		if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND step_key=? AND status='succeeded')`, id, key).Scan(&done); err != nil {
			return nil, err
		}
		if done {
			continue
		}
		if mailboxID > 0 {
			mb, err := s.Mailbox(ctx, orgID, mailboxID)
			if err != nil {
				return nil, err
			}
			if mb.Revision != mailboxRevision || mb.OwnerMemberID == nil || *mb.OwnerMemberID != plan.MemberID {
				return nil, provider.Errorf("revision_conflict", "成员邮箱已经更新")
			}
			found := false
			for j := range group.Mailboxes {
				if group.Mailboxes[j].ID == mailboxID {
					group.Mailboxes[j].Revision = mailboxRevision
					found = true
				}
			}
			if !found {
				group.Mailboxes = append(group.Mailboxes, routingVersion{mailboxID, mailboxRevision})
			}
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
	resultValues := map[string]any{"memberId": plan.MemberID, "mailboxId": mailboxID}
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := checkOperationPermissionTx(ctx, tx, id); err != nil {
			return err
		}
		for _, item := range plan.Shared {
			var current bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mailboxes WHERE id=? AND org_id=? AND revision=? AND kind='shared' AND status='active')`, item.ID, orgID, item.Revision).Scan(&current); err != nil {
				return err
			}
			if !current {
				return provider.Errorf("revision_conflict", "共享授权目标已经更新")
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO mailbox_access(mailbox_id,member_id,level,granted_at) VALUES (?,?,'full',?)`, item.ID, plan.MemberID, db.Now()); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE mailboxes SET access_revision=access_revision+1,updated_at=? WHERE id=?`, db.Now(), item.ID); err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx, `UPDATE members SET status=CASE WHEN password_hash='' THEN 'invited' ELSE 'active' END,revision=revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=1 AND status='invited'`, db.Now(), plan.MemberID, orgID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return provider.Errorf("revision_conflict", "新成员已经更新")
		}
		if in.SendInvite {
			token := secrets.RandomToken(32)
			now := time.Now().UTC()
			if _, err := tx.ExecContext(ctx, `INSERT INTO invites(org_id,member_id,token_hash,created_by,created_at,expires_at) VALUES (?,?,?,?,?,?)`, orgID, plan.MemberID, secrets.HashToken(token), actor, now.Format(time.RFC3339), now.Add(s.Cfg.InviteTTL).Format(time.RFC3339)); err != nil {
				return err
			}
			resultValues["inviteLink"] = s.inviteLink(token)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(org_id,actor_member_id,action,target_type,target_id,detail_json,created_at) VALUES (?,?,'member.create','member',?,?,?)`, orgID, actor, fmtID(plan.MemberID), toJSON(map[string]any{"mailboxId": mailboxID, "groupIds": in.GroupIDs, "sharedMailboxIds": in.SharedMailboxes}), db.Now()); err != nil {
			return err
		}
		return completeLocalOperation(ctx, tx, resultValues)
	})
	return resultValues, err
}
