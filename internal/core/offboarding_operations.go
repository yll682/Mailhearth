package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

type managementQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type OffboardMailboxSnapshot struct {
	ID                 int64        `json:"mailboxId"`
	ConnectionID       int64        `json:"connectionId"`
	ConnectionRevision int64        `json:"connectionRevision"`
	Revision           int64        `json:"revision"`
	Address            string       `json:"address"`
	RotateManaged      bool         `json:"rotateManaged"`
	Plan               *MailboxPlan `json:"plan,omitempty"`
}

type OffboardGroupSnapshot struct {
	GroupID       int64    `json:"groupId"`
	GroupRevision int64    `json:"groupRevision"`
	AddressID     int64    `json:"addressId"`
	ConnectionID  int64    `json:"connectionId"`
	Revision      int64    `json:"revision"`
	Domain        string   `json:"domain"`
	LocalPart     string   `json:"localPart"`
	Automatic     bool     `json:"automatic"`
	Targets       []string `json:"targets"`
}

type OffboardOperationInput struct {
	MemberID  int64                     `json:"memberId"`
	Request   OffboardRequest           `json:"request"`
	Mailboxes []OffboardMailboxSnapshot `json:"mailboxes,omitempty"`
	Groups    []OffboardGroupSnapshot   `json:"groups,omitempty"`
}

func offboardOperationKeys(in *OffboardOperationInput) []string {
	keys := []string{"member:" + fmtID(in.MemberID)}
	for _, mb := range in.Mailboxes {
		keys = append(keys, "mailbox:"+fmtID(mb.ID), "connection:"+fmtID(mb.ConnectionID))
		if mb.Plan != nil {
			if mb.Plan.NewOwnerID > 0 {
				keys = append(keys, "member:"+fmtID(mb.Plan.NewOwnerID))
			}
			for _, memberID := range mb.Plan.GrantMemberIDs {
				keys = append(keys, "member:"+fmtID(memberID))
			}
		}
	}
	for _, group := range in.Groups {
		keys = append(keys, "group:"+fmtID(group.GroupID), "address:"+fmtID(group.AddressID), "connection:"+fmtID(group.ConnectionID))
	}
	sort.Strings(keys)
	return keys
}

func (s *Service) prepareOffboard(ctx context.Context, orgID, actor int64, in *OffboardOperationInput) (*OffboardOperationInput, error) {
	if in == nil {
		return nil, provider.Errorf("invalid", "需要离职处理请求")
	}
	var prepared *OffboardOperationInput
	err := s.DB.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		prepared, err = prepareOffboardQuery(ctx, tx, orgID, actor, in)
		return err
	})
	return prepared, err
}

func prepareOffboardQuery(ctx context.Context, q managementQuery, orgID, actor int64, in *OffboardOperationInput) (*OffboardOperationInput, error) {
	if in.MemberID == actor {
		return nil, provider.Errorf("invalid", "不能对当前成员执行离职")
	}
	if in.Request.ExpectedRevision < 1 {
		return nil, provider.Errorf("invalid", "需要成员 expectedRevision")
	}
	var status, role string
	var revision int64
	err := q.QueryRowContext(ctx, `SELECT m.status,r.key,m.revision FROM members m JOIN roles r ON r.id=m.role_id WHERE m.org_id=? AND m.id=?`, orgID, in.MemberID).Scan(&status, &role, &revision)
	if db.IsNotFound(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if role == model.RoleOwner {
		return nil, provider.Errorf("invalid", "组织所有者需要完成所有权转移")
	}
	if revision != in.Request.ExpectedRevision {
		return nil, provider.Errorf("revision_conflict", "成员已经更新")
	}
	if status == model.MemberDeparted {
		return nil, provider.Errorf("invalid", "成员已经离职")
	}
	out := &OffboardOperationInput{MemberID: in.MemberID, Request: in.Request, Mailboxes: []OffboardMailboxSnapshot{}, Groups: []OffboardGroupSnapshot{}}
	out.Request.Plans = append([]MailboxPlan{}, in.Request.Plans...)
	plans := map[int64]*MailboxPlan{}
	for i := range out.Request.Plans {
		p := &out.Request.Plans[i]
		if p.MailboxID < 1 || p.ExpectedRevision < 1 || plans[p.MailboxID] != nil {
			return nil, provider.Errorf("invalid", "邮箱计划需要唯一 mailboxId 和 expectedRevision")
		}
		if p.Action != "handover" && p.Action != "shared" && p.Action != "suspend" && p.Action != "keep" {
			return nil, provider.Errorf("invalid", "邮箱计划 action 无效")
		}
		if p.Action == "handover" && p.NewOwnerID < 1 {
			return nil, provider.Errorf("invalid", "需要指定接收邮箱的成员")
		}
		if p.Action != "handover" && p.NewOwnerID != 0 {
			return nil, provider.Errorf("invalid", "此邮箱计划不能指定 newOwnerId")
		}
		if p.Action != "shared" && len(p.GrantMemberIDs) > 0 {
			return nil, provider.Errorf("invalid", "此邮箱计划不能指定共享授权成员")
		}
		targets := map[int64]bool{}
		if p.NewOwnerID > 0 {
			targets[p.NewOwnerID] = true
		}
		for _, id := range p.GrantMemberIDs {
			if id < 1 || targets[id] {
				return nil, provider.Errorf("invalid", "授权成员不能重复")
			}
			targets[id] = true
		}
		for id := range targets {
			var active bool
			if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM members WHERE id=? AND org_id=? AND status='active')`, id, orgID).Scan(&active); err != nil {
				return nil, err
			}
			if !active || id == in.MemberID {
				return nil, provider.Errorf("invalid", "目标成员必须属于当前组织且处于 active 状态")
			}
		}
		clean := map[string]bool{}
		for _, target := range p.ForwardTo {
			address, err := NormalizeEmail(target)
			if err != nil {
				return nil, err
			}
			clean[address] = true
		}
		p.ForwardTo = make([]string, 0, len(clean))
		for address := range clean {
			p.ForwardTo = append(p.ForwardTo, address)
		}
		sort.Strings(p.ForwardTo)
		plans[p.MailboxID] = p
	}
	rows, err := q.QueryContext(ctx, `SELECT b.id,b.connection_id,c.revision,b.revision,b.address,b.owner_member_id,c.provider_kind,c.enabled,b.management_mode,b.remote_state,
	 EXISTS(SELECT 1 FROM mailbox_endpoints e JOIN credentials cr ON cr.id=e.credential_id WHERE e.mailbox_id=b.id AND e.network_mode!='disabled' AND cr.source='purelymail_app_password' AND cr.state='active')
	 FROM mailboxes b JOIN mail_connections c ON c.id=b.connection_id WHERE b.org_id=? AND (b.owner_member_id=? OR EXISTS(SELECT 1 FROM mailbox_access a WHERE a.mailbox_id=b.id AND a.member_id=?)) ORDER BY b.id`, orgID, in.MemberID, in.MemberID)
	if err != nil {
		return nil, err
	}
	ownedCount := 0
	for rows.Next() {
		var mb OffboardMailboxSnapshot
		var owner sql.NullInt64
		var kind, mode, state string
		var enabled, managed bool
		if err := rows.Scan(&mb.ID, &mb.ConnectionID, &mb.ConnectionRevision, &mb.Revision, &mb.Address, &owner, &kind, &enabled, &mode, &state, &managed); err != nil {
			rows.Close()
			return nil, err
		}
		mb.RotateManaged = kind == string(provider.Purelymail) && enabled && mode == "api" && state == "present" && managed
		if owner.Valid && owner.Int64 == in.MemberID {
			ownedCount++
			p := plans[mb.ID]
			if p == nil {
				rows.Close()
				return nil, provider.Errorf("invalid", "需要为全部所属邮箱提供处理计划")
			}
			if p.ExpectedRevision != mb.Revision {
				rows.Close()
				return nil, provider.Errorf("revision_conflict", "邮箱计划版本已经更新")
			}
			for _, target := range p.ForwardTo {
				if target == strings.ToLower(mb.Address) {
					rows.Close()
					return nil, provider.Errorf("invalid", "邮箱不能转发到自身")
				}
			}
			mb.Plan = p
		}
		out.Mailboxes = append(out.Mailboxes, mb)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(plans) != ownedCount {
		return nil, provider.Errorf("invalid", "计划包含其他成员的邮箱")
	}
	groups, err := prepareOffboardGroups(ctx, q, orgID, out, plans)
	if err != nil {
		return nil, err
	}
	out.Groups = groups
	return out, nil
}

func prepareOffboardGroups(ctx context.Context, q managementQuery, orgID int64, in *OffboardOperationInput, plans map[int64]*MailboxPlan) ([]OffboardGroupSnapshot, error) {
	rows, err := q.QueryContext(ctx, `SELECT a.group_id,g.revision,a.id,a.connection_id,a.revision,d.name,a.local_part,c.provider_kind,c.enabled,a.management_mode
	 FROM addresses a JOIN groups g ON g.id=a.group_id JOIN domains d ON d.id=a.domain_id JOIN mail_connections c ON c.id=a.connection_id WHERE a.org_id=? AND a.kind='group' ORDER BY a.group_id`, orgID)
	if err != nil {
		return nil, err
	}
	type groupCandidate struct {
		snapshot OffboardGroupSnapshot
		kind     provider.ProviderKind
	}
	var candidates []groupCandidate
	for rows.Next() {
		var item groupCandidate
		var enabled bool
		var mode string
		if err := rows.Scan(&item.snapshot.GroupID, &item.snapshot.GroupRevision, &item.snapshot.AddressID, &item.snapshot.ConnectionID, &item.snapshot.Revision, &item.snapshot.Domain, &item.snapshot.LocalPart, &item.kind, &enabled, &mode); err != nil {
			rows.Close()
			return nil, err
		}
		item.snapshot.Automatic = item.kind == provider.Purelymail && enabled && mode == "api"
		candidates = append(candidates, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := []OffboardGroupSnapshot{}
	for _, item := range candidates {
		group := item.snapshot
		members := map[int64]bool{}
		rows, err := q.QueryContext(ctx, `SELECT gm.member_id FROM group_members gm JOIN members m ON m.id=gm.member_id WHERE gm.group_id=? AND m.org_id=? AND m.status IN ('active','invited')`, group.GroupID, orgID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			members[id] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		affected := members[in.MemberID]
		delete(members, in.MemberID)
		for _, p := range plans {
			if p.Action == "handover" && members[p.NewOwnerID] {
				affected = true
			}
		}
		if !affected {
			continue
		}
		rows, err = q.QueryContext(ctx, `SELECT id,connection_id,address,owner_member_id FROM mailboxes WHERE org_id=? AND kind='personal' AND status='active' AND owner_member_id IS NOT NULL ORDER BY id`, orgID)
		if err != nil {
			return nil, err
		}
		targets := map[string]bool{}
		for rows.Next() {
			var id, connectionID, ownerID int64
			var address string
			if err := rows.Scan(&id, &connectionID, &address, &ownerID); err != nil {
				rows.Close()
				return nil, err
			}
			if p := plans[id]; p != nil {
				if p.Action != "handover" {
					continue
				}
				ownerID = p.NewOwnerID
			}
			if !members[ownerID] {
				continue
			}
			_, domain := SplitAddress(address)
			if item.kind == provider.Migadu && (connectionID != group.ConnectionID || domain != group.Domain) {
				rows.Close()
				failure := provider.Errorf("target_constraint_failed", "群组目标需要属于相同连接和域名")
				failure.Details = map[string]any{"memberId": ownerID, "mailboxId": id, "groupId": group.GroupID, "reasonCode": "same_connection_and_domain_required"}
				return nil, failure
			}
			targets[strings.ToLower(address)] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		group.Targets = make([]string, 0, len(targets))
		for address := range targets {
			group.Targets = append(group.Targets, address)
		}
		sort.Strings(group.Targets)
		result = append(result, group)
	}
	return result, nil
}

func (s *Service) commitOffboardDeparture(ctx context.Context, tx *sql.Tx, orgID, actor int64, id string, in *OffboardOperationInput) error {
	if err := checkOperationPermissionTx(ctx, tx, id); err != nil {
		return err
	}
	current, err := prepareOffboardQuery(ctx, tx, orgID, actor, in)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, in) {
		return provider.Errorf("revision_conflict", "离职计划关联的数据已经更新")
	}
	now := db.Now()
	res, err := tx.ExecContext(ctx, `UPDATE members SET status='departed',revision=revision+1,departed_at=?,updated_at=? WHERE id=? AND org_id=? AND revision=?`, now, now, in.MemberID, orgID, in.Request.ExpectedRevision)
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE member_id=?`, in.MemberID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM mailbox_access WHERE member_id=?`, in.MemberID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM invites WHERE member_id=? AND accepted_at IS NULL`, in.MemberID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mailboxes SET access_revision=access_revision+1 WHERE id IN (SELECT value FROM json_each(?))`, toJSON(offboardMailboxIDs(in))); err != nil {
		return err
	}
	detail := map[string]any{"memberId": in.MemberID, "localAccessRevoked": true, "mailboxes": in.Mailboxes, "groups": in.Groups}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(org_id,actor_member_id,action,target_type,target_id,detail_json,created_at) VALUES (?,?,'member.offboard','member',?,?,?)`, orgID, actor, fmtID(in.MemberID), toJSON(detail), now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO operation_steps(operation_id,step_key,sequence,status,result_json,finished_at) VALUES (?,'member.departure',2,'succeeded',?,?)`, id, toJSON(map[string]any{"memberId": in.MemberID, "localAccessRevoked": true, "verificationSource": "local_transaction"}), now); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO operation_steps(operation_id,step_key,sequence,status) VALUES (?,'member.processing',3,'pending')`, id)
	return err
}

func offboardMailboxIDs(in *OffboardOperationInput) []int64 {
	ids := make([]int64, 0, len(in.Mailboxes))
	for _, mb := range in.Mailboxes {
		ids = append(ids, mb.ID)
	}
	return ids
}

func (s *Service) offboardLocalChanges(ctx context.Context, orgID int64, in *OffboardOperationInput) error {
	id := ctx.Value(operationContextKey{}).(string)
	return s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := checkOperationPermissionTx(ctx, tx, id); err != nil {
			return err
		}
		var done bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND step_key='member.handover' AND status='succeeded')`, id).Scan(&done); err != nil {
			return err
		}
		if done {
			return nil
		}
		for _, mb := range in.Mailboxes {
			if mb.Plan == nil {
				continue
			}
			p := mb.Plan
			kind, status := model.MailboxPersonal, model.MailboxActive
			var owner any
			if p.Action == "handover" {
				owner = p.NewOwnerID
			}
			if p.Action == "shared" {
				kind = model.MailboxShared
			}
			if p.Action == "suspend" {
				status = model.MailboxSuspended
			}
			if owner != nil {
				var active bool
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM members WHERE id=? AND org_id=? AND status='active')`, owner, orgID).Scan(&active); err != nil {
					return err
				}
				if !active {
					return provider.Errorf("revision_conflict", "目标成员已经更新")
				}
			}
			res, err := tx.ExecContext(ctx, `UPDATE mailboxes SET owner_member_id=?,kind=?,status=CASE WHEN status='archived' THEN status ELSE ? END,revision=revision+1,access_revision=access_revision+1,updated_at=? WHERE id=? AND org_id=? AND owner_member_id=? AND revision=?`, owner, kind, status, db.Now(), mb.ID, orgID, in.MemberID, mb.Revision)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n != 1 {
				return provider.Errorf("revision_conflict", "邮箱处理计划已经更新")
			}
			for _, memberID := range p.GrantMemberIDs {
				var active bool
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM members WHERE id=? AND org_id=? AND status='active')`, memberID, orgID).Scan(&active); err != nil {
					return err
				}
				if !active {
					return provider.Errorf("revision_conflict", "授权目标成员已经更新")
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO mailbox_access(mailbox_id,member_id,level,granted_at) VALUES (?,?,'full',?) ON CONFLICT(mailbox_id,member_id) DO UPDATE SET level='full',granted_at=excluded.granted_at`, mb.ID, memberID, db.Now()); err != nil {
					return err
				}
			}
		}
		if in.Request.RemoveFromGroups {
			ids := []int64{}
			for _, group := range in.Groups {
				ids = append(ids, group.GroupID)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE groups SET revision=revision+1,updated_at=? WHERE org_id=? AND id IN (SELECT group_id FROM group_members WHERE member_id=?) AND id NOT IN (SELECT value FROM json_each(?))`, db.Now(), orgID, in.MemberID, toJSON(ids)); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM group_members WHERE member_id=?`, in.MemberID); err != nil {
				return err
			}
		}
		for _, group := range in.Groups {
			res, err := tx.ExecContext(ctx, `UPDATE groups SET revision=revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=?`, db.Now(), group.GroupID, orgID, group.GroupRevision)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n != 1 {
				return provider.Errorf("revision_conflict", "群组已经更新")
			}
			res, err = tx.ExecContext(ctx, `UPDATE addresses SET desired_targets_json=?,sync_state='pending',revision=revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=?`, toJSON(group.Targets), db.Now(), group.AddressID, orgID, group.Revision)
			if err != nil {
				return err
			}
			n, err = res.RowsAffected()
			if err != nil {
				return err
			}
			if n != 1 {
				return provider.Errorf("revision_conflict", "群组分发地址已经更新")
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO operation_steps(operation_id,step_key,sequence,status,result_json,finished_at) SELECT ?,'member.handover',COALESCE(MAX(sequence),0)+1,'succeeded',?,? FROM operation_steps WHERE operation_id=?`, id, toJSON(map[string]any{"mailboxes": in.Mailboxes, "groups": in.Groups, "verificationSource": "local_transaction"}), db.Now(), id)
		return err
	})
}

func (s *Service) externalOperationStep(ctx context.Context, key string, detail any) error {
	id := ctx.Value(operationContextKey{}).(string)
	key = operationStepKey(ctx, key)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO operation_steps(operation_id,step_key,sequence,status,result_json,error_code) SELECT ?,?,COALESCE(MAX(sequence),0)+1,'pending',?,'external_action_required' FROM operation_steps WHERE operation_id=? ON CONFLICT(operation_id,step_key) DO NOTHING`, id, key, toJSON(detail), id)
	return err
}

func (s *Service) executeOffboard(ctx context.Context, orgID, actor int64, in *OffboardOperationInput) (any, error) {
	if in == nil {
		return nil, provider.Errorf("invalid", "需要离职请求")
	}
	if err := s.offboardLocalChanges(ctx, orgID, in); err != nil {
		return nil, err
	}
	for _, mb := range in.Mailboxes {
		s.cancelMailRequests(0, mb.ID)
		if s.Pool != nil {
			s.Pool.InvalidateMailbox(mb.ID)
		}
	}
	for _, group := range in.Groups {
		if !group.Automatic {
			if err := s.externalOperationStep(ctx, "group."+fmtID(group.GroupID)+".external", map[string]any{"action": "group_distribution", "addressId": group.AddressID, "desiredTargets": group.Targets, "verificationSource": "administrator_report", "systemVerified": false}); err != nil {
				return nil, err
			}
			continue
		}
		if err := s.applyOffboardGroup(ctx, orgID, group); err != nil {
			return nil, err
		}
	}
	for _, snapshot := range in.Mailboxes {
		child := context.WithValue(ctx, operationStepPrefixKey{}, "mailbox."+fmtID(snapshot.ID)+".")
		if snapshot.RotateManaged {
			connection, err := s.MailConnection(ctx, orgID, snapshot.ConnectionID)
			if err != nil {
				return nil, err
			}
			if !connection.Enabled || connection.Revision != snapshot.ConnectionRevision {
				return nil, provider.Errorf("revision_conflict", "离职处理所用连接已经更新")
			}
			mb, err := s.Mailbox(ctx, orgID, snapshot.ID)
			if err != nil {
				return nil, err
			}
			if err := s.managedCredential(child, orgID, mb, true); err != nil {
				return nil, err
			}
		}
		if err := s.externalOperationStep(child, "revokeAll.external", map[string]any{"action": "remote_access_revoke_all", "mailboxId": snapshot.ID, "connectionId": snapshot.ConnectionID, "address": snapshot.Address, "accessMethods": []string{"primary_password", "application_credentials", "sender_identities", "delegated_access", "existing_sessions"}, "verificationSource": "administrator_report", "systemVerified": false}); err != nil {
			return nil, err
		}
		if snapshot.Plan != nil && len(snapshot.Plan.ForwardTo) > 0 {
			if err := s.externalOperationStep(child, "forwarding.external", map[string]any{"action": "forwarding", "mailboxId": snapshot.ID, "targets": snapshot.Plan.ForwardTo, "deliveryMode": "redirect", "verificationSource": "administrator_report", "systemVerified": false}); err != nil {
				return nil, err
			}
		}
	}
	id := ctx.Value(operationContextKey{}).(string)
	if _, err := s.DB.ExecContext(ctx, `UPDATE operation_steps SET status='succeeded',result_json='{"verificationSource":"local_transaction"}',finished_at=? WHERE operation_id=? AND step_key='member.processing'`, db.Now(), id); err != nil {
		return nil, err
	}
	var pending bool
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND step_key!='execute' AND status NOT IN ('succeeded','external_reported'))`, id).Scan(&pending); err != nil {
		return nil, err
	}
	result := map[string]any{"memberId": in.MemberID, "localAccessRevoked": true, "externalAccessSystemVerified": false}
	if pending {
		failure := provider.Errorf("external_action_required", "离职成员的本地访问已撤销；远程访问处理需要管理员确认")
		failure.Details = result
		return result, failure
	}
	err := s.DB.Tx(ctx, func(tx *sql.Tx) error { return completeLocalOperation(ctx, tx, result) })
	if err == nil {
		s.audit(ctx, orgID, actor, "member.offboard.complete", "member", fmtID(in.MemberID), result)
	}
	return result, err
}

func (s *Service) applyOffboardGroup(ctx context.Context, orgID int64, group OffboardGroupSnapshot) error {
	api, c, err := s.connectionAdapter(ctx, orgID, group.ConnectionID)
	if err != nil {
		return err
	}
	if !c.Enabled {
		return provider.Errorf("endpoint_disabled", "群组所属连接已经停用")
	}
	request := provider.AddressRuleRequest{Domain: group.Domain, LocalPart: group.LocalPart, Targets: group.Targets}
	child := context.WithValue(ctx, operationStepPrefixKey{}, "group."+fmtID(group.GroupID)+".")
	if err := s.replaceRoutingRule(child, api, request); err != nil {
		return err
	}
	var observed provider.AddressRuleInfo
	if len(request.Targets) > 0 {
		observed, err = api.GetAddressRule(ctx, request)
		if err != nil {
			return &remoteOutcomeUnknown{err}
		}
		if observed.RemoteKey == "" || !sameRoutingTargets(observed.Targets, request.Targets) {
			return &remoteOutcomeUnknown{provider.Errorf("verification_required", "群组规则需要独立核验")}
		}
	}
	return s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := checkOperationPermissionTx(ctx, tx, ctx.Value(operationContextKey{}).(string)); err != nil {
			return err
		}
		if err := saveRoutingObservation(ctx, tx, group.ConnectionID, group.AddressID, observed); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE addresses SET targets_json=?,observed_targets_json=?,sync_state='synced',updated_at=? WHERE id=? AND org_id=? AND revision=? AND desired_targets_json=?`, toJSON(group.Targets), toJSON(group.Targets), db.Now(), group.AddressID, orgID, group.Revision+1, toJSON(group.Targets))
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return provider.Errorf("revision_conflict", "群组候选目标已经更新")
		}
		return nil
	})
}

func (s *Service) replaceRoutingRule(ctx context.Context, api provider.Provider, request provider.AddressRuleRequest) error {
	if err := s.remoteOperationStep(ctx, "routing.delete", request, func() error {
		_, err := api.GetAddressRule(ctx, request)
		if operationErrorCode(err) == "not_found" {
			return nil
		}
		if err != nil {
			return err
		}
		return api.DeleteAddressRule(ctx, request)
	}); err != nil {
		return err
	}
	if len(request.Targets) > 0 {
		if err := s.remoteOperationStep(ctx, "routing.create", request, func() error { _, err := api.CreateAddressRule(ctx, request); return err }); err != nil {
			return err
		}
	}
	observed, err := api.GetAddressRule(ctx, request)
	if len(request.Targets) == 0 {
		if operationErrorCode(err) == "not_found" {
			return nil
		}
		return &remoteOutcomeUnknown{provider.Errorf("verification_required", "需要核查远程分发规则已经删除")}
	}
	if err != nil {
		return err
	}
	wanted := append([]string{}, request.Targets...)
	actual := append([]string{}, observed.Targets...)
	sort.Strings(wanted)
	sort.Strings(actual)
	if !reflect.DeepEqual(wanted, actual) {
		failure := provider.Errorf("verification_required", "远程目标与候选目标不相同")
		failure.Details = map[string]any{"desiredTargets": wanted, "observedTargets": actual}
		return &remoteOutcomeUnknown{failure}
	}
	return nil
}

type ConfirmExternalInput struct {
	ItemID   string   `json:"itemId"`
	StepKeys []string `json:"-"`
	Note     string   `json:"note"`
}

func (s *Service) ConfirmExternalOperation(ctx context.Context, orgID, actor int64, id string, in ConfirmExternalInput) (*OperationView, error) {
	if in.ItemID != "" {
		if len(in.StepKeys) > 0 {
			return nil, provider.Errorf("invalid", "需要唯一的外部事项标识")
		}
		in.StepKeys = []string{in.ItemID}
	}
	view, err := s.OperationForMember(ctx, orgID, actor, id)
	if err != nil {
		return nil, err
	}
	if (view.Kind != "member.offboard" && view.Kind != "mailbox.revokeRemoteAccess" && !strings.HasPrefix(view.Kind, "mailbox.forwarding.")) || view.Status != "needs_action" {
		return nil, provider.Errorf("invalid", "此操作没有可确认的外部事项")
	}
	if len(in.StepKeys) == 0 || strings.TrimSpace(in.Note) == "" || len(in.Note) > 2000 {
		return nil, provider.Errorf("invalid", "需要指定外部步骤并记录完成说明")
	}
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := checkOperationControllerPermissionTx(ctx, tx, id, actor); err != nil {
			return err
		}
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM operations WHERE id=? AND org_id=?`, id, orgID).Scan(&status); err != nil {
			return err
		}
		if status != "needs_action" {
			return provider.Errorf("revision_conflict", "操作状态已经更新")
		}
		var unknown bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND status IN ('unknown','running'))`, id).Scan(&unknown); err != nil {
			return err
		}
		if unknown {
			return provider.Errorf("verification_required", "远程写入需要独立核验")
		}
		seen := map[string]bool{}
		for _, key := range in.StepKeys {
			if seen[key] || !strings.HasSuffix(key, ".external") {
				return provider.Errorf("invalid", "外部步骤标识无效")
			}
			seen[key] = true
			var body string
			var stepStatus, code sql.NullString
			if err := tx.QueryRowContext(ctx, `SELECT result_json,status,error_code FROM operation_steps WHERE operation_id=? AND step_key=?`, id, key).Scan(&body, &stepStatus, &code); err != nil {
				return err
			}
			if !stepStatus.Valid || stepStatus.String != "pending" || !code.Valid || code.String != "external_action_required" {
				return provider.Errorf("invalid", "步骤没有等待外部确认")
			}
			var detail map[string]any
			if err := json.Unmarshal([]byte(body), &detail); err != nil {
				return err
			}
			detail["reportedBy"] = actor
			detail["reportedAt"] = db.Now()
			detail["note"] = strings.TrimSpace(in.Note)
			detail["verificationSource"] = "administrator_report"
			detail["systemVerified"] = false
			if _, err := tx.ExecContext(ctx, `UPDATE operation_steps SET status='external_reported',result_json=?,error_code=NULL,finished_at=? WHERE operation_id=? AND step_key=?`, toJSON(detail), db.Now(), id, key); err != nil {
				return err
			}
		}
		var pending bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND step_key!='execute' AND status NOT IN ('succeeded','external_reported'))`, id).Scan(&pending); err != nil {
			return err
		}
		if !pending {
			resultValues := map[string]any{"systemVerified": false, "verificationSource": "administrator_report"}
			if view.Kind == "mailbox.revokeRemoteAccess" {
				var body string
				if err := tx.QueryRowContext(ctx, `SELECT result_json FROM operation_steps WHERE operation_id=? AND step_key='remoteAccess.prepare'`, id).Scan(&body); err != nil {
					return err
				}
				if err := json.Unmarshal([]byte(body), &resultValues); err != nil {
					return err
				}
				res, err := tx.ExecContext(ctx, `UPDATE mailboxes SET revision=revision+1,access_revision=access_revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=?`, db.Now(), resultValues["mailboxId"], orgID, resultValues["expectedRevision"])
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
				if _, err := tx.ExecContext(ctx, `UPDATE credentials SET state='retired',updated_at=? WHERE mailbox_id=? AND purpose='mail' AND state IN ('active','candidate','revocation_pending')`, db.Now(), resultValues["mailboxId"]); err != nil {
					return err
				}
				resultValues["platformCredentialsRetired"] = true
			} else if view.Kind == "member.offboard" {
				var memberID int64
				if err := tx.QueryRowContext(ctx, `SELECT json_extract(result_json,'$.memberId') FROM operation_steps WHERE operation_id=? AND step_key='member.departure'`, id).Scan(&memberID); err != nil {
					return err
				}
				resultValues["memberId"] = memberID
				resultValues["localAccessRevoked"] = true
				resultValues["externalAccessSystemVerified"] = false
			} else {
				var body string
				if err := tx.QueryRowContext(ctx, `SELECT result_json FROM operation_steps WHERE operation_id=? AND step_key='forwarding.commit'`, id).Scan(&body); err != nil {
					return err
				}
				if err := json.Unmarshal([]byte(body), &resultValues); err != nil {
					return err
				}
				resultValues["verificationSource"] = "administrator_report"
				resultValues["systemVerified"] = false
				if view.Kind == "mailbox.forwarding.delete" {
					if _, err := tx.ExecContext(ctx, `DELETE FROM provider_resources WHERE mailbox_forwarding_id=?`, resultValues["forwardingId"]); err != nil {
						return err
					}
					if _, err := tx.ExecContext(ctx, `DELETE FROM mailbox_forwardings WHERE id=?`, resultValues["forwardingId"]); err != nil {
						return err
					}
				}
			}
			result := toJSON(resultValues)
			if _, err := tx.ExecContext(ctx, `UPDATE operations SET status='succeeded',result_json=?,error_code=NULL,payload_enc=NULL,updated_at=? WHERE id=?`, result, db.Now(), id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE operation_steps SET status='succeeded',result_json=?,error_code=NULL,finished_at=? WHERE operation_id=? AND step_key='execute'`, result, db.Now(), id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM operation_locks WHERE operation_id=?`, id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM operation_private WHERE operation_id=?`, id); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log(org_id,actor_member_id,action,target_type,target_id,detail_json,created_at) VALUES (?,?,'operation.confirm_external','operation',?,?,?)`, orgID, actor, id, toJSON(map[string]any{"itemIds": in.StepKeys, "note": in.Note, "verificationSource": "administrator_report", "systemVerified": false}), db.Now())
		return err
	})
	if err != nil {
		return nil, err
	}
	out, err := s.OperationForMember(ctx, orgID, actor, id)
	if err != nil {
		return nil, err
	}
	if out.Kind == "mailbox.revokeRemoteAccess" && out.Status == "succeeded" {
		var result struct {
			MailboxID int64 `json:"mailboxId"`
		}
		if err := json.Unmarshal(out.Result, &result); err != nil {
			return nil, err
		}
		s.cancelMailRequests(0, result.MailboxID)
		if s.Pool != nil {
			s.Pool.InvalidateMailbox(result.MailboxID)
		}
	}
	return out, nil
}
