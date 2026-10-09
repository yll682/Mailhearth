package core

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

func TestMultiProviderStorageDirectManagementPermissions(t *testing.T) {
	s, m := newStorageService(t)
	ctx := context.Background()
	c := storageConnection(t, s, m, "受保护连接")
	actor := storageOffboardMember(t, s, m, "普通成员")
	role, err := s.CreateRole(ctx, m.OrgID, m.ID, RoleInput{Name: "邮件成员", Permissions: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	actor, err = s.UpdateMember(ctx, m.OrgID, m.ID, actor.ID, MemberInput{ExpectedRevision: actor.Revision, RoleID: role.ID})
	if err != nil {
		t.Fatal(err)
	}
	label := "无权限修改"
	scope := provider.DomainScope{Mode: "all"}
	checks := []struct {
		name string
		run  func() error
	}{
		{"create", func() error {
			_, err := s.CreateConnection(ctx, m.OrgID, actor.ID, CreateConnectionInput{ProviderKind: provider.Manual, Label: label})
			return err
		}},
		{"update", func() error {
			_, err := s.UpdateConnection(ctx, m.OrgID, actor.ID, c.ID, UpdateConnectionInput{ExpectedRevision: c.Revision, Label: &label})
			return err
		}},
		{"configure", func() error {
			_, err := s.ConfigureConnection(ctx, m.OrgID, actor.ID, c.ID, UpdateConnectionInput{ExpectedRevision: c.Revision, DomainScope: &scope})
			return err
		}},
		{"test", func() error { _, err := s.TestConnection(ctx, m.OrgID, actor.ID, c.ID); return err }},
		{"delete", func() error { return s.DeleteConnection(ctx, m.OrgID, actor.ID, c.ID, c.Revision, c.Label) }},
		{"member", func() error {
			_, err := s.UpdateMember(ctx, m.OrgID, actor.ID, m.ID, MemberInput{ExpectedRevision: m.Revision, DisplayName: label})
			return err
		}},
		{"invite", func() error { _, err := s.CreateInvite(ctx, m.OrgID, actor.ID, m.ID); return err }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.run(); !errors.Is(err, ErrForbidden) {
				t.Fatalf("管理方法没有拒绝缺少权限的成员：%v", err)
			}
		})
	}
	current, err := s.MailConnection(ctx, m.OrgID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != c.Revision || current.Label != c.Label {
		t.Fatal("权限不足的请求改变了连接")
	}
	op, _, err := s.QueueOperation(ctx, m.OrgID, m.ID, uuid.NewString(), OperationPayload{Kind: "connection.discover", ConnectionID: c.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateConnection(ctx, m.OrgID, m.ID, c.ID, UpdateConnectionInput{ExpectedRevision: c.Revision, Label: &label})
	requireProviderCode(t, err, "operation_in_progress")
	_, err = s.ConfigureConnection(ctx, m.OrgID, m.ID, c.ID, UpdateConnectionInput{ExpectedRevision: c.Revision, DomainScope: &scope})
	requireProviderCode(t, err, "operation_in_progress")
	_, err = s.TestConnection(ctx, m.OrgID, m.ID, c.ID)
	requireProviderCode(t, err, "operation_in_progress")
	if _, err := s.ControlOperation(ctx, m.OrgID, m.ID, op.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateConnection(ctx, m.OrgID, m.ID, c.ID, UpdateConnectionInput{ExpectedRevision: c.Revision, Label: &label}); err != nil {
		t.Fatal(err)
	}
}

func TestMultiProviderStorageMemberDeletionDependencies(t *testing.T) {
	s, m := newStorageService(t)
	ctx := context.Background()
	create := func(name string) *model.Member {
		t.Helper()
		result, err := s.CreateMember(ctx, m.OrgID, m.ID, CreateMemberRequest{MemberInput: MemberInput{DisplayName: name, LoginEmail: uuid.NewString() + "@example.org"}, MailboxAction: "none"})
		if err != nil {
			t.Fatal(err)
		}
		return result.Member
	}
	member := create("待删除成员")
	requireProviderCode(t, s.DeleteMember(ctx, m.OrgID, m.ID, member.ID), "revision_conflict")
	group, err := s.CreateGroup(ctx, m.OrgID, m.ID, GroupInput{Name: "保留成员群组", MemberIDs: []int64{member.ID}})
	if err != nil {
		t.Fatal(err)
	}
	requireProviderCode(t, s.DeleteMember(ctx, m.OrgID, m.ID, member.ID, member.Revision), "member_in_use")
	current, err := s.Group(ctx, m.OrgID, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.MemberIDs) != 1 || current.MemberIDs[0] != member.ID {
		t.Fatal("删除请求改变了群组成员")
	}
	if err := s.DeleteGroup(ctx, m.OrgID, m.ID, group.ID, group.Revision); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMember(ctx, m.OrgID, m.ID, member.ID, member.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Member(ctx, m.OrgID, member.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("没有依赖的受邀成员未删除")
	}
	active := storageOffboardMember(t, s, m, "启用成员")
	requireProviderCode(t, s.DeleteMember(ctx, m.OrgID, m.ID, active.ID, active.Revision), "invalid")
	history := create("具有管理历史的成员")
	if _, err := s.DB.Exec(`INSERT INTO operations(id,org_id,actor_member_id,kind,request_id,request_digest,status,created_at,updated_at) VALUES (?,?,?,'connection.discover',?,'saved','cancelled',datetime('now'),datetime('now'))`, uuid.NewString(), m.OrgID, history.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	requireProviderCode(t, s.DeleteMember(ctx, m.OrgID, m.ID, history.ID, history.Revision), "member_in_use")
	if _, err := s.Member(ctx, m.OrgID, history.ID); err != nil {
		t.Fatal(err)
	}
}
