package core

import (
	"context"
	"github.com/google/uuid"
	"mailhearth/internal/model"
	"testing"
)

func TestMultiProviderStorageIdentityAuthorization(t *testing.T) {
	s, m := newStorageService(t)
	ctx := context.Background()
	mailboxID, _ := storageSubmission(t, s, m)
	identity, err := s.UpsertIdentity(ctx, m.OrgID, m.ID, mailboxID, 0, IdentityInput{Address: "reply@example.org", DisplayName: "回复身份"})
	if err != nil {
		t.Fatal(err)
	}
	if identity.AuthorizationStatus != "unverified" || identity.AuthorizationSource != "admin" {
		t.Fatal("新增身份没有要求明确授权")
	}
	req, finish := s.RegisterMailRequest(ctx, m.ID, mailboxID, "")
	defer finish()
	identity, err = s.AuthorizeIdentity(ctx, m.OrgID, m.ID, mailboxID, identity.ID, identity.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if req.Err() == nil || identity.AuthorizationStatus != "allowed" || identity.AuthorizationCheckedAt == nil {
		t.Fatal("发送授权状态或请求终止行为不正确")
	}
	_, err = s.AuthorizeIdentity(ctx, m.OrgID, m.ID, mailboxID, identity.ID, identity.Revision-1, false)
	requireProviderCode(t, err, "revision_conflict")
	identity, err = s.UpdateIdentitySettings(ctx, m.OrgID, m.ID, mailboxID, identity.ID, IdentitySettingsInput{ExpectedRevision: identity.Revision, DisplayName: "显示名称", ReplyTo: "reply@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	if identity.AuthorizationStatus != "allowed" || identity.AuthorizationSource != "admin" {
		t.Fatal("个人显示设置改变了发送授权")
	}
	_, err = s.UpdateIdentitySettings(ctx, m.OrgID, m.ID, mailboxID, identity.ID, IdentitySettingsInput{ExpectedRevision: identity.Revision - 1})
	requireProviderCode(t, err, "revision_conflict")
	req, finish = s.RegisterMailRequest(ctx, m.ID, mailboxID, "")
	defer finish()
	identity, err = s.UpsertIdentity(ctx, m.OrgID, m.ID, mailboxID, identity.ID, IdentityInput{ExpectedRevision: identity.Revision, Address: "Changed@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	if identity.Address != "Changed@example.org" {
		t.Fatal("手动连接身份地址没有保留 local part 大小写")
	}
	if identity.AuthorizationStatus != "unverified" || identity.AuthorizationCheckedAt != nil || req.Err() == nil {
		t.Fatal("地址更新没有要求重新授权或终止现有请求")
	}
	_, err = s.UpsertIdentity(ctx, m.OrgID, m.ID, mailboxID, 0, IdentityInput{Address: identity.Address})
	requireProviderCode(t, err, "identity_exists")
	identity, err = s.AuthorizeIdentity(ctx, m.OrgID, m.ID, mailboxID, identity.ID, identity.Revision, false)
	if err != nil {
		t.Fatal(err)
	}
	if identity.AuthorizationStatus != "denied" {
		t.Fatal("撤销授权未保存拒绝状态")
	}
	if err := s.DeleteIdentity(ctx, m.OrgID, m.ID, mailboxID, identity.ID, identity.Revision-1); err == nil {
		t.Fatal("过期身份版本允许删除")
	} else {
		requireProviderCode(t, err, "revision_conflict")
	}
	if err := s.DeleteIdentity(ctx, m.OrgID, m.ID, mailboxID, identity.ID, identity.Revision); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE action='identity.authorize' AND actor_member_id=?`, m.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatal("发送授权审计数量不正确")
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM identities WHERE mailbox_id=? AND is_default=1`, mailboxID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("默认身份数量不正确")
	}
}

func TestMultiProviderStorageSendingAccessIsolation(t *testing.T) {
	s, m := newStorageService(t)
	ctx := context.Background()
	mailboxID, input := storageSubmission(t, s, m)
	first, _, err := s.ReserveSubmission(ctx, m.OrgID, m.ID, mailboxID, input)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateMember(ctx, m.OrgID, m.ID, CreateMemberRequest{MemberInput: MemberInput{DisplayName: "邮件读取成员", LoginEmail: "reader@example.org"}, Password: uuid.NewString() + "Aa1!"})
	if err != nil {
		t.Fatal(err)
	}
	reader := created.Member
	if _, err := s.Submissions(ctx, m.OrgID, reader.ID, mailboxID); err != ErrForbidden {
		t.Fatalf("无授权成员可以查询发送记录：%v", err)
	}
	if _, err := s.GrantAccess(ctx, m.OrgID, m.ID, mailboxID, reader.ID, model.AccessRead); err != nil {
		t.Fatal(err)
	}
	list, err := s.Submissions(ctx, m.OrgID, reader.ID, mailboxID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatal("读取权限成员看到了其他成员的发送记录")
	}
	if _, err := s.Submission(ctx, m.OrgID, reader.ID, mailboxID, first.ID); err != ErrForbidden {
		t.Fatalf("读取权限成员可以查询其他成员的提交：%v", err)
	}
	if _, err := s.FolderMapping(ctx, m.OrgID, reader.ID, mailboxID); err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateFolderMapping(ctx, m.OrgID, reader.ID, mailboxID, UpdateFolderMappingInput{ExpectedRevision: 2, Mapping: &model.FolderMapping{}, SentCopyMode: "append"})
	if err != ErrForbidden {
		t.Fatalf("读取权限成员可以更改文件夹映射：%v", err)
	}
	_, err = s.AuthorizeIdentity(ctx, m.OrgID, reader.ID, mailboxID, input.Envelope.IdentityID, 1, true)
	if err != ErrForbidden {
		t.Fatalf("普通成员可以授权发件身份：%v", err)
	}
	_, err = s.UpdateIdentitySettings(ctx, m.OrgID, reader.ID, mailboxID, input.Envelope.IdentityID, IdentitySettingsInput{ExpectedRevision: 1})
	if err != ErrForbidden {
		t.Fatalf("读取权限成员可以编辑发件身份：%v", err)
	}
	if _, err := s.GrantAccess(ctx, m.OrgID, m.ID, mailboxID, reader.ID, model.AccessFull); err != nil {
		t.Fatal(err)
	}
	list, err = s.Submissions(ctx, m.OrgID, reader.ID, mailboxID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != first.ID {
		t.Fatal("显式 full 权限未提供提交核查访问")
	}
	if _, err := s.Submission(ctx, m.OrgID, reader.ID, mailboxID, first.ID); err != nil {
		t.Fatal(err)
	}
	token, err := s.CreateSession(ctx, m.ID, "127.0.0.1", "密码更新验证")
	if err != nil {
		t.Fatal(err)
	}
	req, finish := s.RegisterMailRequest(ctx, m.ID, mailboxID, token)
	defer finish()
	password := uuid.NewString() + "Aa1!"
	if err := s.SetPassword(ctx, m.OrgID, m.ID, m.ID, password, true); err != nil {
		t.Fatal(err)
	}
	if req.Err() == nil {
		t.Fatal("密码更新未终止现有请求")
	}
	loggedIn, err := s.LookupSession(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if loggedIn != nil {
		t.Fatal("密码更新后原会话仍然可用")
	}
	if _, err := s.VerifyLogin(ctx, m.LoginEmail, password); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MailboxEndpoints(ctx, m.OrgID, mailboxID); err != nil {
		t.Fatal(err)
	}
}

func TestMultiProviderStorageFolderMappingPermissions(t *testing.T) {
	s, m := newStorageService(t)
	ctx := context.Background()
	mailboxID, _ := storageSubmission(t, s, m)
	view, err := s.FolderMapping(ctx, m.OrgID, m.ID, mailboxID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Revision != 1 || view.SentCopyMode != "append" || view.Mapping.Sent != nil {
		t.Fatal("初始文件夹配置不正确")
	}
	_, err = s.UpdateFolderMapping(ctx, m.OrgID, m.ID, mailboxID, UpdateFolderMappingInput{ExpectedRevision: 2, Mapping: &model.FolderMapping{}, SentCopyMode: "append"})
	requireProviderCode(t, err, "revision_conflict")
	_, err = s.UpdateFolderMapping(ctx, m.OrgID, m.ID, mailboxID, UpdateFolderMappingInput{ExpectedRevision: 1, Mapping: &model.FolderMapping{}, SentCopyMode: "invalid"})
	requireProviderCode(t, err, "invalid")
	badName := "Sent\r\n"
	_, err = s.UpdateFolderMapping(ctx, m.OrgID, m.ID, mailboxID, UpdateFolderMappingInput{ExpectedRevision: 1, Mapping: &model.FolderMapping{Sent: &badName}, SentCopyMode: "append"})
	requireProviderCode(t, err, "invalid")
	_, err = s.UpdateFolderMapping(ctx, m.OrgID, m.ID, mailboxID, UpdateFolderMappingInput{ExpectedRevision: 1, Mapping: &model.FolderMapping{}, SentCopyMode: "append"})
	requireProviderCode(t, err, "endpoint_unconfigured")
	if _, err := s.FolderMapping(ctx, m.OrgID+1, m.ID, mailboxID); err == nil {
		t.Fatal("其他组织可查询文件夹配置")
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT revision FROM mailboxes WHERE id=?`, mailboxID).Scan(&view.Revision); err != nil {
		t.Fatal(err)
	}
	if view.Revision != 1 {
		t.Fatal("无效文件夹配置修改了邮箱版本")
	}
}
