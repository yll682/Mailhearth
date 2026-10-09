package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"mailhearth/internal/model"
	"mailhearth/internal/provider"
	"mailhearth/internal/secrets"
)

func TestSetupAndOrganisationLifecycle(t *testing.T) {
	s, owner := newStorageService(t)
	ctx := context.Background()
	s.Cfg.SessionTTL = time.Hour
	s.Cfg.InviteTTL = time.Hour
	status, err := s.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.NeedsSetup || status.Step != "connection" {
		t.Fatalf("初始化状态无效：%+v", status)
	}
	if _, err := s.Init(ctx, InitRequest{OrgName: "另一组织", AdminName: "管理员", AdminEmail: "other@example.org", Password: uuid.NewString()}); !errors.Is(err, ErrConflict) {
		t.Fatalf("重复初始化没有被拒绝：%v", err)
	}
	connection := storageConnection(t, s, owner, "组织邮件连接")
	status, err = s.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.NeedsSetup || status.Step != "mailboxes" {
		t.Fatalf("连接登记后的初始化状态无效：%+v", status)
	}
	if err := s.CompleteConnectionSetup(ctx, owner.OrgID, owner.ID, connection.ID, nil); err != nil {
		t.Fatal(err)
	}
	status, err = s.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.NeedsSetup || status.Step != "done" {
		t.Fatalf("初始化没有完成：%+v", status)
	}
	mailboxes, err := s.Mailboxes(ctx, owner.OrgID)
	if err != nil {
		t.Fatal(err)
	}
	if len(mailboxes) != 0 {
		t.Fatal("没有选择邮箱仍登记了邮箱")
	}
	permissions, key, err := s.Permissions(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if key != model.RoleOwner || !HasPermission(permissions, model.PermDomainsManage) {
		t.Fatal("组织所有者权限无效")
	}
	overview, err := s.Overview(ctx, owner.OrgID)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Org.Name != t.Name() || len(overview.Connections) != 1 || overview.Connections[0].ProviderKind != provider.Manual {
		t.Fatal("Overview 没有返回登记的组织与连接")
	}
}

func TestMemberInviteLoginAndRoles(t *testing.T) {
	s, owner := newStorageService(t)
	ctx := context.Background()
	s.Cfg.SessionTTL = time.Hour
	s.Cfg.InviteTTL = time.Hour
	created, err := s.CreateMember(ctx, owner.OrgID, owner.ID, CreateMemberRequest{MemberInput: MemberInput{DisplayName: "受邀成员", LoginEmail: "invite@example.org"}, MailboxAction: "none", SendInvite: true})
	if err != nil {
		t.Fatal(err)
	}
	if created.Operation.Status != "succeeded" || created.Member.Status != model.MemberInvited || created.InviteLink == "" || created.Mailbox != nil {
		t.Fatal("成员创建与邀请状态无效")
	}
	token := created.InviteLink[strings.LastIndex(created.InviteLink, "/")+1:]
	info, err := s.Invite(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if info.MemberName != created.Member.DisplayName {
		t.Fatal("邀请没有对应实际成员")
	}
	if _, err := s.AcceptInvite(ctx, token, "short", ""); err == nil {
		t.Fatal("邀请接受了不符合长度要求的密码")
	}
	password := uuid.NewString() + "Aa1!"
	member, err := s.AcceptInvite(ctx, token, password, "接受邀请的成员")
	if err != nil {
		t.Fatal(err)
	}
	if member.Status != model.MemberActive || member.DisplayName != "接受邀请的成员" {
		t.Fatal("接受邀请没有更新成员资料")
	}
	if _, err := s.AcceptInvite(ctx, token, password, ""); err == nil {
		t.Fatal("同一邀请被重复使用")
	}
	if _, err := s.VerifyLogin(ctx, member.LoginEmail, password); err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyLogin(ctx, member.LoginEmail, uuid.NewString()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("错误密码没有被拒绝：%v", err)
	}
	session, err := s.CreateSession(ctx, member.ID, "127.0.0.1", "organisation-test")
	if err != nil {
		t.Fatal(err)
	}
	active, err := s.SessionHashActive(ctx, member.ID, secrets.HashToken(session))
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("创建的会话没有生效")
	}
	role, err := s.CreateRole(ctx, owner.OrgID, owner.ID, RoleInput{Name: "成员管理", Permissions: []string{model.PermMembersManage, model.PermAuditRead}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRole(ctx, owner.OrgID, owner.ID, RoleInput{Name: "所有者权限", Permissions: []string{model.PermOrgOwner}}); err == nil {
		t.Fatal("自定义角色接受了组织所有者权限")
	}
	member, err = s.UpdateMember(ctx, owner.OrgID, owner.ID, member.ID, MemberInput{RoleID: role.ID, ExpectedRevision: member.Revision})
	if err != nil {
		t.Fatal(err)
	}
	permissions, _, err := s.Permissions(ctx, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !HasPermission(permissions, model.PermMembersManage) || HasPermission(permissions, model.PermDomainsManage) {
		t.Fatal("自定义角色权限无效")
	}
	if err := s.DeleteRole(ctx, owner.OrgID, owner.ID, role.ID); err == nil {
		t.Fatal("仍有成员使用的角色被删除")
	}
	if err := s.SetPassword(ctx, owner.OrgID, owner.ID, member.ID, uuid.NewString()+"Aa1!", true); err != nil {
		t.Fatal(err)
	}
	active, err = s.SessionHashActive(ctx, member.ID, secrets.HashToken(session))
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("密码重置后保留了旧会话")
	}
	audit, err := s.Audit(ctx, owner.OrgID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) < 4 {
		t.Fatal("组织操作没有保留审计记录")
	}
}

func TestSharedMailboxCollaboration(t *testing.T) {
	s, owner := newStorageService(t)
	ctx := context.Background()
	connection := storageConnection(t, s, owner, "协作连接")
	one := storageOffboardMember(t, s, owner, "协作成员")
	two := storageOffboardMember(t, s, owner, "另一成员")
	mailbox := storageOffboardMailbox(t, s, owner, owner.ID, connection.ID, "shared@example.org")
	if _, err := s.DB.Exec(`UPDATE mailboxes SET kind='shared',owner_member_id=NULL WHERE id=?`, mailbox.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GrantAccess(ctx, owner.OrgID, owner.ID, mailbox.ID, one.ID, model.AccessSend); err != nil {
		t.Fatal(err)
	}
	access, err := s.CheckMailboxAccess(ctx, owner.OrgID, one.ID, mailbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if access.Level != model.AccessSend || !access.CanSend() || access.CanDelete() {
		t.Fatal("共享邮箱访问级别无效")
	}
	for _, id := range []int64{owner.ID, two.ID} {
		if _, err := s.CheckMailboxAccess(ctx, owner.OrgID, id, mailbox.ID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("未授权成员获得了邮箱访问：%v", err)
		}
	}
	key := MessageKey("<collaboration@example.org>", "INBOX", 1, 1)
	if err := s.Assign(ctx, mailbox.ID, one.ID, key, &one.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordActivity(ctx, mailbox.ID, one.ID, key, "replied", "回复内容"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(ctx, mailbox.ID, one.ID, key, "resolved"); err != nil {
		t.Fatal(err)
	}
	state, err := s.GetMessageState(ctx, mailbox.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	if state.AssigneeName != one.DisplayName || state.Status != "resolved" || len(state.Activity) != 3 {
		t.Fatal("协作记录没有保存指派、回复与处理状态")
	}
	states, err := s.StatesFor(ctx, mailbox.ID, []string{key, "mid:missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[key] == nil {
		t.Fatal("协作记录查询包含未登记的邮件")
	}
	if err := s.RevokeAccess(ctx, owner.OrgID, owner.ID, mailbox.ID, one.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CheckMailboxAccess(ctx, owner.OrgID, one.ID, mailbox.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("访问撤销后仍可以读取邮箱：%v", err)
	}
}
