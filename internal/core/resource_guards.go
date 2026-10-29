package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

func requireManagementPermission(ctx context.Context, q managementQuery, orgID, actor int64, required string) error {
	var body string
	err := q.QueryRowContext(ctx, `SELECT r.permissions_json FROM members m JOIN roles r ON r.id=m.role_id AND r.org_id=m.org_id WHERE m.org_id=? AND m.id=? AND m.status=?`, orgID, actor, model.MemberActive).Scan(&body)
	if db.IsNotFound(err) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	var permissions []string
	if err := json.Unmarshal([]byte(body), &permissions); err != nil {
		return err
	}
	if !HasPermission(permissions, required) {
		return ErrForbidden
	}
	return nil
}

// activeMemberPermissions returns the permission list and role key of an
// active member of orgID.
func activeMemberPermissions(ctx context.Context, q managementQuery, orgID, memberID int64) ([]string, string, error) {
	var body, key string
	err := q.QueryRowContext(ctx, `SELECT r.permissions_json,r.key FROM members m JOIN roles r ON r.id=m.role_id AND r.org_id=m.org_id WHERE m.org_id=? AND m.id=? AND m.status=?`, orgID, memberID, model.MemberActive).Scan(&body, &key)
	if db.IsNotFound(err) {
		return nil, "", ErrForbidden
	}
	if err != nil {
		return nil, "", err
	}
	var permissions []string
	if err := json.Unmarshal([]byte(body), &permissions); err != nil {
		return nil, "", err
	}
	return permissions, key, nil
}

// permissionsCover reports whether holder includes every permission in want.
func permissionsCover(holder, want []string) bool {
	for _, p := range want {
		if !HasPermission(holder, p) {
			return false
		}
	}
	return true
}

// requireMemberManageable stops a manager from acting on a member whose
// role is the owner or carries permissions the manager does not hold
// (password reset, invite, profile/role edits, status, offboarding,
// deletion). Acting on oneself is always allowed here; callers apply their
// own self-service rules.
func requireMemberManageable(ctx context.Context, q managementQuery, orgID, actor, target int64) error {
	if actor == target {
		return nil
	}
	actorPermissions, _, err := activeMemberPermissions(ctx, q, orgID, actor)
	if err != nil {
		return err
	}
	var body, key string
	err = q.QueryRowContext(ctx, `SELECT r.permissions_json,r.key FROM members m JOIN roles r ON r.id=m.role_id AND r.org_id=m.org_id WHERE m.org_id=? AND m.id=?`, orgID, target).Scan(&body, &key)
	if db.IsNotFound(err) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if key == model.RoleOwner {
		return fmt.Errorf("%w: only the owner can manage the owner account", ErrForbidden)
	}
	var targetPermissions []string
	if err := json.Unmarshal([]byte(body), &targetPermissions); err != nil {
		return err
	}
	if !permissionsCover(actorPermissions, targetPermissions) {
		return fmt.Errorf("%w: this member has permissions you do not hold", ErrForbidden)
	}
	return nil
}

// requirePermissionsGrantable stops a manager from granting (via role
// assignment or role edits) permissions they do not hold themselves.
func requirePermissionsGrantable(ctx context.Context, q managementQuery, orgID, actor int64, permissions []string) error {
	actorPermissions, _, err := activeMemberPermissions(ctx, q, orgID, actor)
	if err != nil {
		return err
	}
	if !permissionsCover(actorPermissions, permissions) {
		return fmt.Errorf("%w: you cannot grant permissions you do not hold", ErrForbidden)
	}
	return nil
}

// requireRoleGrantable applies requirePermissionsGrantable to a role id.
func requireRoleGrantable(ctx context.Context, q managementQuery, orgID, actor, roleID int64) error {
	var body string
	err := q.QueryRowContext(ctx, `SELECT permissions_json FROM roles WHERE id=? AND org_id=?`, roleID, orgID).Scan(&body)
	if db.IsNotFound(err) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var permissions []string
	if err := json.Unmarshal([]byte(body), &permissions); err != nil {
		return err
	}
	return requirePermissionsGrantable(ctx, q, orgID, actor, permissions)
}

func requireResourceAvailable(ctx context.Context, q managementQuery, orgID int64, keys ...string) error {
	current, _ := ctx.Value(operationContextKey{}).(string)
	for _, key := range keys {
		var id string
		err := q.QueryRowContext(ctx, `SELECT operation_id FROM operation_locks WHERE org_id=? AND resource_key=? AND operation_id!=?`, orgID, key, current).Scan(&id)
		if db.IsNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}
		failure := provider.Errorf("operation_in_progress", "资源已有进行中的操作")
		failure.OperationID = &id
		return failure
	}
	return nil
}

func readMemberCreationPlan(ctx context.Context, q managementQuery, id string) (*memberCreationPlan, error) {
	var body string
	if err := q.QueryRowContext(ctx, `SELECT result_json FROM operation_steps WHERE operation_id=? AND step_key='member.prepare' AND status='succeeded'`, id).Scan(&body); err != nil {
		return nil, err
	}
	var plan memberCreationPlan
	if err := json.Unmarshal([]byte(body), &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func validateMemberCreationProgress(ctx context.Context, q managementQuery, orgID int64, id string, plan *memberCreationPlan) error {
	var current bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM members WHERE id=? AND org_id=? AND revision=1 AND status='invited' AND role_id=?)`, plan.MemberID, orgID, plan.RoleID).Scan(&current); err != nil {
		return err
	}
	if !current {
		return provider.Errorf("revision_conflict", "待创建成员已经更新")
	}
	base := *plan
	base.Groups = nil
	var mailboxID, revision int64
	err := q.QueryRowContext(ctx, `SELECT json_extract(result_json,'$.mailboxId'),json_extract(result_json,'$.mailboxRevision') FROM operation_steps WHERE operation_id=? AND step_key='member.mailbox.local.commit' AND status='succeeded'`, id).Scan(&mailboxID, &revision)
	if err != nil && !db.IsNotFound(err) {
		return err
	}
	if err == nil {
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mailboxes WHERE id=? AND org_id=? AND revision=? AND owner_member_id=? AND kind='personal' AND status='active')`, mailboxID, orgID, revision, plan.MemberID).Scan(&current); err != nil {
			return err
		}
		if !current {
			return provider.Errorf("revision_conflict", "已登记的成员邮箱已经更新")
		}
		base.MailboxID = 0
	}
	if err := validateMemberCreation(ctx, q, orgID, &base); err != nil {
		return err
	}
	for i := range plan.Groups {
		group := plan.Groups[i]
		prefix := "member.group." + fmtID(group.GroupID) + "."
		var done bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND step_key=? AND status='succeeded')`, id, prefix+"local.commit").Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		var body string
		err := q.QueryRowContext(ctx, `SELECT result_json FROM operation_steps WHERE operation_id=? AND step_key=? AND status='succeeded'`, id, prefix+"routing.prepare").Scan(&body)
		if err != nil && !db.IsNotFound(err) {
			return err
		}
		if err == nil {
			if err := json.Unmarshal([]byte(body), &group); err != nil {
				return err
			}
		} else if mailboxID > 0 {
			found := false
			for j := range group.Mailboxes {
				if group.Mailboxes[j].ID == mailboxID {
					group.Mailboxes[j].Revision = revision
					found = true
				}
			}
			if !found {
				group.Mailboxes = append(group.Mailboxes, routingVersion{mailboxID, revision})
			}
		}
		if err := validateRoutingPlan(ctx, q, orgID, &group); err != nil {
			return err
		}
	}
	return nil
}

func memberCreationMailboxID(ctx context.Context, q managementQuery, id string, known int64) (int64, error) {
	var target sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT target_mailbox_id FROM operations WHERE id=?`, id).Scan(&target); err != nil {
		return 0, err
	}
	if target.Valid {
		return target.Int64, nil
	}
	return known, nil
}
