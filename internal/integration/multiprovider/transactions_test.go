package multiprovider

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"mailhearth/internal/config"
	"mailhearth/internal/core"
	"mailhearth/internal/db"
	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
	"mailhearth/internal/secrets"
)

type realEnvironment struct {
	ctx             context.Context
	svc             *core.Service
	database        *db.DB
	input           testConfig
	orgID, memberID int64
	dir             string
}

func realTransactionEnvironment(t *testing.T) *realEnvironment {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "data", "integration", "multi-provider"))
	if err != nil {
		t.Fatal(err)
	}
	input := readTestConfig(t, root)
	runID := time.Now().UTC().Format("20060102T150405") + "-" + uuid.NewString()
	dir := filepath.Join(root, runID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("无法读取真实环境的部署配置")
	}
	if cfg.DevStack {
		t.Fatal("真实检查要求关闭 MAILHEARTH_DEV_STACK")
	}
	master, _, err := secrets.LoadMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.NewBox(master, "credentials")
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(dir, "mailhearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	pool := imappool.New(imappool.Config{MaxConns: 24, Logger: slog.Default()})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	svc := core.New(database, cfg, box, pool, slog.Default())
	t.Cleanup(func() {
		cancel()
		svc.WaitOperations()
		pool.Close()
		if err := database.Close(); err != nil {
			t.Error("无法关闭验收数据库")
		}
	})
	member, err := svc.Init(ctx, core.InitRequest{OrgName: runID, AdminName: "真实事务检查", AdminEmail: "transactions@example.org", Password: uuid.NewString() + "Aa1!"})
	if err != nil {
		t.Fatal("无法初始化验收组织")
	}
	org, err := svc.Org(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.StartOperations(ctx); err != nil {
		t.Fatal("无法启动 Operation 执行器")
	}
	return &realEnvironment{ctx: ctx, svc: svc, database: database, input: input, orgID: org.ID, memberID: member.ID, dir: dir}
}

func awaitRealOperation(t *testing.T, env *realEnvironment, id string) *core.OperationView {
	t.Helper()
	ctx, cancel := context.WithTimeout(env.ctx, 3*time.Minute)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		view, err := env.svc.Operation(ctx, env.orgID, id)
		if err != nil {
			t.Fatal("无法读取真实操作状态")
		}
		if view.Status != "queued" && view.Status != "running" {
			return view
		}
		select {
		case <-ctx.Done():
			t.Fatal("真实操作等待超时")
		case <-ticker.C:
		}
	}
}

func requireRealCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *provider.TypedError
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("真实请求没有返回预期错误码 %s", code)
	}
}

func TestRealMultiProviderTransactions(t *testing.T) {
	env := realTransactionEnvironment(t)
	var connections []*core.ConnectionView
	for _, item := range []struct {
		kind  provider.ProviderKind
		value *providerConfig
	}{{provider.Purelymail, env.input.Purelymail}, {provider.Migadu, env.input.Migadu}} {
		connection, err := env.svc.CreateConnection(env.ctx, env.orgID, env.memberID, core.CreateConnectionInput{ProviderKind: item.kind, Label: string(item.kind), APIAuth: &provider.APIAuth{APIKey: item.value.APIKey, Username: item.value.APIUsername}, DomainScope: &provider.DomainScope{Mode: "selected", Domains: []string{item.value.Domain}}, ProtocolDefaults: &provider.ProtocolTemplates{IMAP: *item.value.IMAP, SMTP: *item.value.SMTP, ManageSieve: *item.value.ManageSieve}})
		if err != nil {
			t.Fatalf("%s 管理认证检查失败", item.kind)
		}
		connections = append(connections, connection)
	}
	manual, err := env.svc.CreateConnection(env.ctx, env.orgID, env.memberID, core.CreateConnectionInput{ProviderKind: provider.Manual, Label: "手动协议检查"})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("T01连接管理隔离", func(t *testing.T) {
		label := "独立手动连接"
		if _, err := env.svc.UpdateConnection(env.ctx, env.orgID, env.memberID, manual.ID, core.UpdateConnectionInput{ExpectedRevision: manual.Revision, Label: &label}); err != nil {
			t.Fatal("无法更新手动连接名称")
		}
		for _, before := range connections {
			after, err := env.svc.MailConnection(env.ctx, env.orgID, before.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("修改手动连接改变了另一个服务商的配置")
			}
		}
		manual, err = env.svc.MailConnection(env.ctx, env.orgID, manual.ID)
		if err != nil {
			t.Fatal(err)
		}
	})
	if t.Failed() {
		return
	}
	t.Run("T17相同连接版本并发更新", func(t *testing.T) {
		var workers sync.WaitGroup
		results := make(chan error, 2)
		for _, name := range []string{"并发名称一", "并发名称二"} {
			workers.Add(1)
			go func(name string) {
				defer workers.Done()
				_, err := env.svc.UpdateConnection(env.ctx, env.orgID, env.memberID, manual.ID, core.UpdateConnectionInput{ExpectedRevision: manual.Revision, Label: &name})
				results <- err
			}(name)
		}
		workers.Wait()
		close(results)
		completed := 0
		for err := range results {
			if err == nil {
				completed++
			} else {
				requireRealCode(t, err, "revision_conflict")
			}
		}
		if completed != 1 {
			t.Fatal("相同连接版本的并发请求提交数量不正确")
		}
	})
	if t.Failed() {
		return
	}
	input := env.input.Manual.PrimaryMailbox
	attach := core.AttachMailboxInput{Mode: "attach", ConnectionID: manual.ID, Kind: model.MailboxPersonal, Address: input.Address, DisplayName: "真实已登记邮箱", OwnerMemberID: &env.memberID, CredentialMode: "entered", Credentials: input.Credentials, Endpoints: input.Endpoints, SentCopyMode: "append"}
	payload := core.OperationPayload{Kind: "mailbox.attach", ConnectionID: manual.ID, Attach: &attach}
	requestID := uuid.NewString()
	op, _, err := env.svc.QueueOperation(env.ctx, env.orgID, env.memberID, requestID, payload, nil)
	if err != nil {
		t.Fatal("无法提交真实邮箱登记")
	}
	finished := awaitRealOperation(t, env, op.ID)
	if finished.Status != "succeeded" {
		t.Fatal("真实邮箱登记认证未完成")
	}
	var result struct {
		MailboxID int64 `json:"mailboxId"`
	}
	if err := json.Unmarshal(finished.Result, &result); err != nil || result.MailboxID < 1 {
		t.Fatal("真实邮箱登记没有保存 mailboxId")
	}
	registered, err := env.svc.Mailbox(env.ctx, env.orgID, result.MailboxID)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("T18重复操作请求", func(t *testing.T) {
		again, repeated, err := env.svc.QueueOperation(env.ctx, env.orgID, env.memberID, requestID, payload, nil)
		if err != nil || !repeated || again.ID != op.ID {
			t.Fatal("重复登记请求创建了其他 Operation")
		}
		var count int
		if err := env.database.QueryRow(`SELECT COUNT(*) FROM mailboxes WHERE connection_id=? AND address=?`, manual.ID, registered.Address).Scan(&count); err != nil || count != 1 {
			t.Fatal("重复请求创建了其他本地邮箱")
		}
	})
	t.Run("T19重复请求内容冲突", func(t *testing.T) {
		changed := attach
		changed.DisplayName = "不同登记内容"
		_, _, err := env.svc.QueueOperation(env.ctx, env.orgID, env.memberID, requestID, core.OperationPayload{Kind: "mailbox.attach", ConnectionID: manual.ID, Attach: &changed}, nil)
		requireRealCode(t, err, "idempotency_conflict")
	})
	t.Run("T03相同地址的独立连接登记", func(t *testing.T) {
		second, err := env.svc.CreateConnection(env.ctx, env.orgID, env.memberID, core.CreateConnectionInput{ProviderKind: provider.Manual, Label: "相同地址第二连接"})
		if err != nil {
			t.Fatal(err)
		}
		in := attach
		in.ConnectionID = second.ID
		op, _, err := env.svc.QueueOperation(env.ctx, env.orgID, env.memberID, uuid.NewString(), core.OperationPayload{Kind: "mailbox.attach", ConnectionID: second.ID, Attach: &in}, nil)
		if err != nil {
			t.Fatal("无法在第二连接登记相同地址")
		}
		completed := awaitRealOperation(t, env, op.ID)
		if completed.Status != "succeeded" {
			t.Fatal("第二连接的真实认证未完成")
		}
		var count int
		if err := env.database.QueryRow(`SELECT COUNT(DISTINCT connection_id) FROM mailboxes WHERE address=?`, registered.Address).Scan(&count); err != nil || count != 2 {
			t.Fatal("相同地址的邮箱没有保持连接隔离")
		}
	})
	t.Run("T08候选认证失败保留原凭据", func(t *testing.T) {
		before, err := env.svc.MailboxEndpoints(env.ctx, env.orgID, result.MailboxID)
		if err != nil {
			t.Fatal(err)
		}
		original, err := env.svc.ResolveEndpoint(env.ctx, env.orgID, result.MailboxID, provider.ProtocolIMAP)
		if err != nil {
			t.Fatal("原 IMAP 凭据无法读取")
		}
		in := core.UpdateEndpointsInput{ExpectedRevision: before.Revision, Credentials: []core.EnteredCredential{{ClientKey: "candidate", Secret: uuid.NewString() + "Aa1!"}}}
		for _, endpoint := range before.Endpoints {
			value := &core.EndpointInput{NetworkMode: "disabled"}
			if endpoint.NetworkMode != "disabled" {
				if endpoint.Username == nil || endpoint.Credential == nil {
					t.Fatal("真实配置缺少协议认证信息")
				}
				network := endpoint.EffectiveNetwork
				value = &core.EndpointInput{NetworkMode: "override", Network: &network, AuthMode: "password", Username: *endpoint.Username, Credential: &core.EndpointCredentialRef{CredentialID: endpoint.Credential.ID}}
				if endpoint.Protocol == provider.ProtocolIMAP {
					value.Credential = &core.EndpointCredentialRef{ClientKey: "candidate"}
				}
			}
			switch endpoint.Protocol {
			case provider.ProtocolIMAP:
				in.Endpoints.IMAP = value
			case provider.ProtocolSMTP:
				in.Endpoints.SMTP = value
			case provider.ProtocolManageSieve:
				in.Endpoints.ManageSieve = value
			}
		}
		op, _, err := env.svc.QueueOperation(env.ctx, env.orgID, env.memberID, uuid.NewString(), core.OperationPayload{Kind: "mailbox.endpoints", MailboxID: result.MailboxID, Endpoints: &in}, nil)
		if err != nil {
			t.Fatal("无法提交候选认证检查")
		}
		failed := awaitRealOperation(t, env, op.ID)
		if failed.Status != "failed" || failed.ErrorCode == nil || *failed.ErrorCode != "mailbox_auth_failed" {
			t.Fatal("候选认证没有取得服务器明确拒绝结果")
		}
		after, err := env.svc.MailboxEndpoints(env.ctx, env.orgID, result.MailboxID)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("失败的候选认证改变了原协议配置")
		}
		active, err := env.svc.ResolveEndpoint(env.ctx, env.orgID, result.MailboxID, provider.ProtocolIMAP)
		if err != nil || active.Secret != original.Secret || active.CredentialID != original.CredentialID {
			t.Fatal("失败的候选认证替换了原凭据")
		}
		conn, err := env.svc.Pool.GetFresh(env.ctx, active.IMAPCredential())
		if err != nil {
			t.Fatal("候选失败后原凭据无法完成真实认证")
		}
		env.svc.Pool.Put(conn)
	})
	t.Run("T40公开查询隐藏秘密", func(t *testing.T) {
		connections, err := env.svc.Connections(env.ctx, env.orgID)
		if err != nil {
			t.Fatal(err)
		}
		endpoints, err := env.svc.MailboxEndpoints(env.ctx, env.orgID, result.MailboxID)
		if err != nil {
			t.Fatal(err)
		}
		operation, err := env.svc.Operation(env.ctx, env.orgID, op.ID)
		if err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal([]any{connections, endpoints, operation})
		if err != nil {
			t.Fatal(err)
		}
		private := []string{env.input.Purelymail.APIKey, env.input.Migadu.APIKey}
		for _, item := range input.Credentials {
			private = append(private, item.Secret)
		}
		rows, err := env.database.Query(`SELECT secret_enc FROM credentials WHERE secret_enc!=''`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			private = append(private, value)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range private {
			if value != "" && strings.Contains(string(body), value) {
				t.Fatal("公开查询包含认证秘密或秘密密文")
			}
		}
	})
	if t.Failed() {
		return
	}
	report, err := json.MarshalIndent(struct {
		Checks             []string `json:"checks"`
		AcceptanceComplete bool     `json:"acceptanceComplete"`
	}{[]string{"connection_management_isolation", "same_address_registration", "candidate_authentication_failure", "revision_conflict", "operation_idempotency", "request_content_conflict", "safe_query_fields"}, false}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.dir, "transactions.json"), report, 0600); err != nil {
		t.Fatal(err)
	}
}
