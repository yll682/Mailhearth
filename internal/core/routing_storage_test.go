package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"mailhearth/internal/db"
	"mailhearth/internal/model"
)

func storageRoutingDomain(t *testing.T, s *Service, m *storageMember, name string) int64 {
	t.Helper()
	res, err := s.DB.Exec(`INSERT INTO domains(org_id,name,created_at,updated_at) VALUES (?,?,?,?)`, m.OrgID, name, db.Now(), db.Now())
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func queueStorageRouting(t *testing.T, s *Service, m *storageMember, kind string, input *RoutingOperationInput) *OperationView {
	t.Helper()
	op, _, err := s.QueueOperation(context.Background(), m.OrgID, m.ID, uuid.NewString(), OperationPayload{Kind: kind, Routing: input}, nil)
	if err != nil {
		t.Fatal(err)
	}
	finished := awaitStorageOperation(t, s, m.OrgID, op.ID)
	if finished.Status != "succeeded" {
		t.Fatalf("地址或群组操作失败：%s %v %s", finished.Status, finished.ErrorCode, finished.Result)
	}
	return finished
}

func TestMultiProviderStorageExternalAddressIsolation(t *testing.T) {
	s, m := newStorageService(t)
	ctx := context.Background()
	member := storageOffboardMember(t, s, m, "地址目标成员")
	one := storageConnection(t, s, m, "地址连接一")
	two := storageConnection(t, s, m, "地址连接二")
	domainID := storageRoutingDomain(t, s, m, "example.org")
	first := storageOffboardMailbox(t, s, m, member.ID, one.ID, "recipient@example.org")
	second := storageOffboardMailbox(t, s, m, member.ID, two.ID, "recipient@example.org")
	startStorageOperations(t, s)
	ids := []int64{}
	for _, item := range []struct{ connectionID, mailboxID int64 }{{one.ID, first.ID}, {two.ID, second.ID}} {
		input := AddressInput{ConnectionID: item.connectionID, DomainID: domainID, LocalPart: "alias", Kind: model.AddressAlias, MailboxID: item.mailboxID, Mode: "external"}
		finished := queueStorageRouting(t, s, m, "address.create", &RoutingOperationInput{Address: &input})
		var result struct {
			ID       int64  `json:"addressId"`
			Source   string `json:"verificationSource"`
			Verified bool   `json:"systemVerified"`
		}
		if err := json.Unmarshal(finished.Result, &result); err != nil {
			t.Fatal(err)
		}
		if result.Source != "local_registration" || result.Verified {
			t.Fatal("外部登记被标记为远程验证")
		}
		ids = append(ids, result.ID)
		address, err := s.Address(ctx, m.OrgID, result.ID)
		if err != nil {
			t.Fatal(err)
		}
		if address.ConnectionID != item.connectionID || address.MailboxID == nil || *address.MailboxID != item.mailboxID || address.SyncState != "unknown" || len(address.ObservedTargets) != 0 {
			t.Fatal("外部规则没有保存独立归属或未验证状态")
		}
	}
	if ids[0] == ids[1] {
		t.Fatal("不同连接同名地址共用了对象")
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM identities WHERE mailbox_id IN (?,?)`, first.ID, second.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("收件 alias 自动授予了 SMTP 发件身份")
	}
	bad := AddressInput{ConnectionID: one.ID, DomainID: domainID, LocalPart: "other", Kind: model.AddressAlias, MailboxID: second.ID, Mode: "external"}
	_, _, err := s.QueueOperation(ctx, m.OrgID, m.ID, uuid.NewString(), OperationPayload{Kind: "address.create", Routing: &RoutingOperationInput{Address: &bad}}, nil)
	requireProviderCode(t, err, "target_constraint_failed")
	address, err := s.Address(ctx, m.OrgID, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	queueStorageRouting(t, s, m, "address.delete", &RoutingOperationInput{AddressID: address.ID, ExpectedRevision: address.Revision})
	if _, err := s.Address(ctx, m.OrgID, ids[1]); err != nil {
		t.Fatal("移除一个连接的规则改变了另一个连接")
	}
}

func TestMultiProviderStorageGroupCompleteTargets(t *testing.T) {
	s, m := newStorageService(t)
	ctx := context.Background()
	member := storageOffboardMember(t, s, m, "多邮箱群组成员")
	c := storageConnection(t, s, m, "多邮箱群组连接")
	domainID := storageRoutingDomain(t, s, m, "example.org")
	storageOffboardMailbox(t, s, m, member.ID, c.ID, "one@example.org")
	storageOffboardMailbox(t, s, m, member.ID, c.ID, "two@example.org")
	startStorageOperations(t, s)
	in := GroupInput{Name: "完整目标群组", MemberIDs: []int64{member.ID}, ConnectionID: c.ID, AddressDomainID: domainID, AddressLocal: "team", Mode: "external"}
	finished := queueStorageRouting(t, s, m, "group.create", &RoutingOperationInput{Group: &in})
	var result struct {
		ID int64 `json:"groupId"`
	}
	if err := json.Unmarshal(finished.Result, &result); err != nil {
		t.Fatal(err)
	}
	group, err := s.Group(ctx, m.OrgID, result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if group.Revision != 2 || group.Address == nil || len(group.Address.Targets) != 2 || group.Address.Targets[0] != "one@example.org" || group.Address.Targets[1] != "two@example.org" {
		t.Fatal("群组没有包含成员的全部 personal 邮箱")
	}
	empty := GroupInput{ExpectedRevision: group.Revision, MemberIDs: []int64{}}
	queueStorageRouting(t, s, m, "group.update", &RoutingOperationInput{GroupID: group.ID, Group: &empty})
	current, err := s.Group(ctx, m.OrgID, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Address == nil || len(current.MemberIDs) != 0 || len(current.Address.Targets) != 0 {
		t.Fatal("没有目标的群组没有保留本地地址")
	}
	_, _, err = s.QueueOperation(ctx, m.OrgID, m.ID, uuid.NewString(), OperationPayload{Kind: "group.update", Routing: &RoutingOperationInput{GroupID: group.ID, Group: &empty}}, nil)
	requireProviderCode(t, err, "revision_conflict")
	queueStorageRouting(t, s, m, "group.delete", &RoutingOperationInput{GroupID: group.ID, ExpectedRevision: current.Revision})
	if _, err := s.Group(ctx, m.OrgID, group.ID); err != ErrNotFound {
		t.Fatal("群组删除没有处理全部本地依赖")
	}
}

func TestMultiProviderStorageRoutingPreflightAndCancellation(t *testing.T) {
	s, m := newStorageService(t)
	ctx := context.Background()
	c := storageConnection(t, s, m, "规则预检查连接")
	domainID := storageRoutingDomain(t, s, m, "example.org")
	invalid := GroupInput{Name: "无效成员群组", MemberIDs: []int64{m.ID + 999}, ConnectionID: c.ID, AddressDomainID: domainID, AddressLocal: "team", Mode: "external"}
	_, _, err := s.QueueOperation(ctx, m.OrgID, m.ID, uuid.NewString(), OperationPayload{Kind: "group.create", Routing: &RoutingOperationInput{Group: &invalid}}, nil)
	requireProviderCode(t, err, "invalid")
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM groups WHERE name=?`, invalid.Name).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("失败的预检查创建了群组")
	}
	in := AddressInput{ConnectionID: c.ID, DomainID: domainID, LocalPart: "forward", Kind: model.AddressForward, Targets: []string{"target@example.org"}, Mode: "external"}
	requestID := uuid.NewString()
	payload := OperationPayload{Kind: "address.create", Routing: &RoutingOperationInput{Address: &in}}
	op, _, err := s.QueueOperation(ctx, m.OrgID, m.ID, requestID, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	again, repeated, err := s.QueueOperation(ctx, m.OrgID, m.ID, requestID, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !repeated || again.ID != op.ID {
		t.Fatal("规则重复请求生成了其他 Operation")
	}
	cancelled, err := s.ControlOperation(ctx, m.OrgID, m.ID, op.ID, "cancel")
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != "cancelled" {
		t.Fatal("尚未执行的规则操作没有取消")
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM operation_locks WHERE operation_id=?`, op.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("规则取消没有释放资源占用")
	}
}
