package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

func storageMutationGroup(t *testing.T, s *Service, m *storageMember, connectionID, memberID int64, name string) int64 {
	t.Helper()
	id := storageMemberGroup(t, s, m, connectionID, true)
	if _, err := s.DB.Exec(`UPDATE groups SET name=? WHERE id=?`, name, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO group_members(group_id,member_id) VALUES (?,?)`, id, memberID); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMultiProviderStorageRemoteDeletionGroupPreflight(t *testing.T) {
	s, m := newStorageService(t)
	ctx := context.Background()
	c := storageConnection(t, s, m, "删除依赖连接")
	owner := storageOffboardMember(t, s, m, "删除依赖成员")
	one := storageOffboardMailbox(t, s, m, owner.ID, c.ID, "delete@example.org")
	two := storageOffboardMailbox(t, s, m, owner.ID, c.ID, "retain@example.org")
	groupID := storageMutationGroup(t, s, m, c.ID, owner.ID, "删除依赖群组")
	payload := OperationPayload{Kind: "mailbox.deleteRemote", MailboxID: one.ID, RemoteMailbox: &RemoteMailboxInput{ExpectedRevision: one.Revision, ConfirmAddress: one.Address}}
	plan, _, err := s.prepareMailboxMutation(ctx, m.OrgID, m.ID, &payload)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != model.MailboxArchived || len(plan.Groups) != 1 || plan.Groups[0].GroupID != groupID || len(plan.Groups[0].Rules) != 1 {
		t.Fatal("远程删除没有保存归档和群组候选")
	}
	targets := plan.Groups[0].Rules[0].Targets
	if len(targets) != 1 || targets[0] != two.Address {
		t.Fatal("远程删除群组候选没有准确移除指定邮箱")
	}
	current, err := s.Mailbox(ctx, m.OrgID, one.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != one.Status || current.Revision != one.Revision {
		t.Fatal("远程删除预检查改变了邮箱状态")
	}
	var domainID int64
	if err := s.DB.QueryRow(`SELECT domain_id FROM addresses WHERE group_id=?`, groupID).Scan(&domainID); err != nil {
		t.Fatal(err)
	}
	now := db.Now()
	if _, err := s.DB.Exec(`INSERT INTO addresses(org_id,connection_id,domain_id,address_key,local_part,address,kind,mailbox_id,management_mode,sync_state,created_at,updated_at) VALUES (?,?,?,'dependent@example.org','dependent','dependent@example.org','alias',?,'external','unknown',?,?)`, m.OrgID, c.ID, domainID, one.ID, now, now); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.prepareMailboxMutation(ctx, m.OrgID, m.ID, &payload)
	requireProviderCode(t, err, "mailbox_in_use")
}

func TestMultiProviderStorageMailboxOwnerGroups(t *testing.T) {
	s, m := newStorageService(t)
	ctx := context.Background()
	one := storageConnection(t, s, m, "所有者连接")
	two := storageConnection(t, s, m, "接收群组连接")
	old := storageOffboardMember(t, s, m, "原所有者")
	next := storageOffboardMember(t, s, m, "新所有者")
	mb := storageOffboardMailbox(t, s, m, old.ID, one.ID, "transfer@example.org")
	other := storageOffboardMailbox(t, s, m, next.ID, one.ID, "other@example.org")
	first := storageMutationGroup(t, s, m, one.ID, old.ID, "原成员群组")
	second := storageMutationGroup(t, s, m, two.ID, next.ID, "接收成员群组")
	in := UpdateMailboxInput{ExpectedRevision: mb.Revision, OwnerMemberID: &next.ID}
	payload := OperationPayload{Kind: "mailbox.update", MailboxID: mb.ID, MailboxEdit: &in}
	requestID := uuid.NewString()
	op, _, err := s.QueueOperation(ctx, m.OrgID, m.ID, requestID, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.Mailbox(ctx, m.OrgID, mb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.OwnerMemberID == nil || *current.OwnerMemberID != old.ID {
		t.Fatal("排队时提前修改了邮箱所有者")
	}
	startStorageOperations(t, s)
	finished := awaitStorageOperation(t, s, m.OrgID, op.ID)
	if finished.Status != "succeeded" {
		t.Fatalf("所有者和关联群组没有完成：%s %s", finished.Status, finished.Result)
	}
	current, err = s.Mailbox(ctx, m.OrgID, mb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.OwnerMemberID == nil || *current.OwnerMemberID != next.ID || current.Revision != 2 || current.AccessRevision != 2 {
		t.Fatal("邮箱所有者或版本没有提交")
	}
	g, err := s.Group(ctx, m.OrgID, first)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Address.Targets) != 0 {
		t.Fatal("原成员群组仍然包含已移交邮箱")
	}
	g, err = s.Group(ctx, m.OrgID, second)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Address.Targets) != 2 || g.Address.Targets[0] != other.Address || g.Address.Targets[1] != mb.Address {
		t.Fatal("新成员群组没有包含全部邮箱")
	}
	again, repeated, err := s.QueueOperation(ctx, m.OrgID, m.ID, requestID, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !repeated || again.ID != op.ID {
		t.Fatal("所有者变更重复请求生成了新操作")
	}
	var result struct {
		IDs []int64 `json:"groupIds"`
	}
	if err := json.Unmarshal(finished.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.IDs) != 2 {
		t.Fatal("操作结果没有列出全部关联群组")
	}
}

func TestMultiProviderStorageMailboxSuspendImmediate(t *testing.T) {
	s, m := newStorageService(t)
	ctx := context.Background()
	c := storageConnection(t, s, m, "暂停连接")
	owner := storageOffboardMember(t, s, m, "暂停邮箱成员")
	mb := storageOffboardMailbox(t, s, m, owner.ID, c.ID, "suspend@example.org")
	groupID := storageMutationGroup(t, s, m, c.ID, owner.ID, "暂停群组")
	if _, err := s.DB.Exec(`UPDATE addresses SET targets_json=?,desired_targets_json=?,observed_targets_json=? WHERE group_id=?`, toJSON([]string{mb.Address}), toJSON([]string{mb.Address}), toJSON([]string{mb.Address}), groupID); err != nil {
		t.Fatal(err)
	}
	request, finish := s.RegisterMailRequest(ctx, owner.ID, mb.ID, "")
	defer finish()
	payload := OperationPayload{Kind: "mailbox.suspend", MailboxID: mb.ID, ExpectedRevision: mb.Revision}
	requestID := uuid.NewString()
	op, _, err := s.QueueOperation(ctx, m.OrgID, m.ID, requestID, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.Mailbox(ctx, m.OrgID, mb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != model.MailboxSuspended || current.Revision != 2 || current.AccessRevision != 2 {
		t.Fatal("暂停请求没有立即停止本地访问")
	}
	if request.Err() == nil {
		t.Fatal("暂停邮箱没有取消已有请求")
	}
	group, err := s.Group(ctx, m.OrgID, groupID)
	if err != nil {
		t.Fatal(err)
	}
	if len(group.Address.Targets) != 1 || len(group.Address.DesiredTargets) != 0 || group.Address.SyncState != "pending" {
		t.Fatal("暂停邮箱没有分别保存当前目标和待更新目标")
	}
	startStorageOperations(t, s)
	finished := awaitStorageOperation(t, s, m.OrgID, op.ID)
	if finished.Status != "succeeded" {
		t.Fatalf("暂停群组处理没有完成：%s %s", finished.Status, finished.Result)
	}
	group, err = s.Group(ctx, m.OrgID, groupID)
	if err != nil {
		t.Fatal(err)
	}
	if group.Address == nil || len(group.Address.Targets) != 0 || group.Revision != 2 {
		t.Fatal("暂停邮箱没有清除其群组投递目标")
	}
	_, repeated, err := s.QueueOperation(ctx, m.OrgID, m.ID, requestID, payload, nil)
	if err != nil || !repeated {
		t.Fatal("重复暂停没有返回原操作")
	}
}

func TestMultiProviderStorageMailboxMutationMigaduPreflight(t *testing.T) {
	s, m := newStorageService(t)
	ctx := context.Background()
	one := storageConnection(t, s, m, "邮箱连接")
	templates, err := provider.JSONString(provider.DefaultTemplates(provider.Migadu))
	if err != nil {
		t.Fatal(err)
	}
	now := db.Now()
	created, err := s.DB.Exec(`INSERT INTO mail_connections(org_id,provider_kind,label,api_username,domain_scope_json,protocol_defaults_json,created_at,updated_at) VALUES (?,'migadu','Migadu 群组连接','admin','{"mode":"explicit","domains":["example.org"]}',?,?,?)`, m.OrgID, templates, now, now)
	if err != nil {
		t.Fatal(err)
	}
	connectionID, err := created.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	old := storageOffboardMember(t, s, m, "原邮箱成员")
	next := storageOffboardMember(t, s, m, "Migadu 成员")
	mb := storageOffboardMailbox(t, s, m, old.ID, one.ID, "same@example.org")
	storageOffboardMailbox(t, s, m, next.ID, connectionID, "same@example.org")
	storageMutationGroup(t, s, m, connectionID, next.ID, "Migadu 约束群组")
	in := UpdateMailboxInput{ExpectedRevision: mb.Revision, OwnerMemberID: &next.ID}
	_, _, err = s.QueueOperation(ctx, m.OrgID, m.ID, uuid.NewString(), OperationPayload{Kind: "mailbox.update", MailboxID: mb.ID, MailboxEdit: &in}, nil)
	requireProviderCode(t, err, "target_constraint_failed")
	current, err := s.Mailbox(ctx, m.OrgID, mb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.OwnerMemberID == nil || *current.OwnerMemberID != old.ID || current.Revision != 1 {
		t.Fatal("目标限制失败仍然改变了所有者")
	}
	var count int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM operations WHERE kind='mailbox.update'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("目标限制失败仍然创建了操作")
	}
}

func TestMultiProviderStorageMailboxMutationStaleGroup(t *testing.T) {
	s, m := newStorageService(t)
	ctx := context.Background()
	c := storageConnection(t, s, m, "版本连接")
	owner := storageOffboardMember(t, s, m, "版本成员")
	mb := storageOffboardMailbox(t, s, m, owner.ID, c.ID, "revision@example.org")
	groupID := storageMutationGroup(t, s, m, c.ID, owner.ID, "版本群组")
	kind := model.MailboxShared
	in := UpdateMailboxInput{ExpectedRevision: mb.Revision, Kind: &kind}
	op, _, err := s.QueueOperation(ctx, m.OrgID, m.ID, uuid.NewString(), OperationPayload{Kind: "mailbox.update", MailboxID: mb.ID, MailboxEdit: &in}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE groups SET revision=revision+1,updated_at=? WHERE id=?`, db.Now(), groupID); err != nil {
		t.Fatal(err)
	}
	startStorageOperations(t, s)
	failed := awaitStorageOperation(t, s, m.OrgID, op.ID)
	if failed.Status != "needs_action" {
		t.Fatalf("群组版本冲突没有停止修改：%s", failed.Status)
	}
	current, err := s.Mailbox(ctx, m.OrgID, mb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Kind != model.MailboxPersonal || current.Revision != 1 || current.OwnerMemberID == nil {
		t.Fatal("预检查失败仍然修改了邮箱")
	}
	_, err = s.ControlOperation(ctx, m.OrgID, m.ID, op.ID, "retry")
	requireProviderCode(t, err, "revision_conflict")
	if _, err := s.ControlOperation(ctx, m.OrgID, m.ID, op.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	g, err := s.Group(ctx, m.OrgID, groupID)
	if err != nil {
		t.Fatal(err)
	}
	if g.Address.SyncState != "unknown" {
		t.Fatal("取消邮箱修改没有保留群组的未验证状态")
	}
}
