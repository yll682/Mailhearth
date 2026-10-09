package multiprovider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"mailhearth/internal/core"
	"mailhearth/internal/mailproto/mailops"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

func attachRealConfiguredMailbox(t *testing.T, env *realEnvironment, name string, input *mailboxConfig) *model.Mailbox {
	t.Helper()
	connection, err := env.svc.CreateConnection(env.ctx, env.orgID, env.memberID, core.CreateConnectionInput{ProviderKind: provider.Manual, Label: name})
	if err != nil {
		t.Fatal("无法创建真实协议连接")
	}
	mailbox, err := env.svc.AttachMailbox(env.ctx, env.orgID, env.memberID, core.AttachMailboxInput{Mode: "attach", ConnectionID: connection.ID, Kind: model.MailboxPersonal, Address: input.Address, DisplayName: name, OwnerMemberID: &env.memberID, CredentialMode: "entered", Credentials: input.Credentials, Endpoints: input.Endpoints, SentCopyMode: "append"})
	if err != nil {
		t.Fatal("真实协议认证或邮箱登记没有完成")
	}
	return mailbox
}

func actualEndpointUpdate(t *testing.T, env *realEnvironment, mailboxID int64) core.UpdateEndpointsInput {
	t.Helper()
	view, err := env.svc.MailboxEndpoints(env.ctx, env.orgID, mailboxID)
	if err != nil {
		t.Fatal("无法读取协议配置")
	}
	input := core.UpdateEndpointsInput{ExpectedRevision: view.Revision}
	for _, endpoint := range view.Endpoints {
		value := &core.EndpointInput{NetworkMode: "disabled"}
		if endpoint.EffectiveNetwork.Enabled && endpoint.NetworkMode != "disabled" {
			if endpoint.Username == nil || endpoint.Credential == nil {
				t.Fatal("协议配置缺少实际凭据引用")
			}
			network := endpoint.EffectiveNetwork
			value = &core.EndpointInput{NetworkMode: "override", Network: &network, AuthMode: "password", Username: *endpoint.Username, Credential: &core.EndpointCredentialRef{CredentialID: endpoint.Credential.ID}}
		}
		switch endpoint.Protocol {
		case provider.ProtocolIMAP:
			input.Endpoints.IMAP = value
		case provider.ProtocolSMTP:
			input.Endpoints.SMTP = value
		case provider.ProtocolManageSieve:
			input.Endpoints.ManageSieve = value
		}
	}
	return input
}

func waitRealDelivery(t *testing.T, env *realEnvironment, mailboxID int64, submissionID string) *mailops.MessageLocator {
	t.Helper()
	endpoint, err := env.svc.ResolveEndpoint(env.ctx, env.orgID, mailboxID, provider.ProtocolIMAP)
	if err != nil {
		t.Fatal("无法读取真实收件协议配置")
	}
	ctx, cancel := context.WithTimeout(env.ctx, time.Duration(env.input.DeliveryTimeoutSeconds)*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		conn, err := env.svc.Pool.GetFresh(ctx, endpoint.IMAPCredential())
		if err != nil {
			t.Fatal("无法独立连接真实收件邮箱")
		}
		found, err := mailops.FindSubmissionMessage(ctx, conn, "INBOX", submissionID, 16<<20)
		env.svc.Pool.Put(conn)
		if err != nil {
			t.Fatal("真实邮件查询失败")
		}
		if found != nil {
			return found
		}
		select {
		case <-ctx.Done():
			t.Fatal("真实收件邮箱没有在规定时间内收到测试邮件")
		case <-ticker.C:
		}
	}
}

func TestRealMultiProviderProtocols(t *testing.T) {
	env := realTransactionEnvironment(t)
	checks := []string{}
	t.Run("T04独立IMAP和SMTP凭据实际投递", func(t *testing.T) {
		mailbox := attachRealConfiguredMailbox(t, env, "独立 SMTP 邮箱", env.input.Manual.IndependentSMTP)
		imap, err := env.svc.ResolveEndpoint(env.ctx, env.orgID, mailbox.ID, provider.ProtocolIMAP)
		if err != nil {
			t.Fatal("无法读取 IMAP endpoint")
		}
		smtp, err := env.svc.ResolveEndpoint(env.ctx, env.orgID, mailbox.ID, provider.ProtocolSMTP)
		if err != nil {
			t.Fatal("无法读取 SMTP endpoint")
		}
		if imap.Username == smtp.Username || imap.Secret == smtp.Secret {
			t.Fatal("T04 需要 IMAP 与 SMTP 分别使用不同的 username 和 secret")
		}
		id := uuid.NewString()
		raw, err := mailops.Build(&mailops.Draft{From: mailops.Recipient{Address: mailbox.Address}, To: []mailops.Recipient{{Address: mailbox.Address}}, Subject: "Mailhearth T04 " + id, Text: "独立 SMTP 凭据投递检查", Date: time.Now(), MessageID: mailops.NewMessageID("mailhearth.invalid"), SubmissionID: id})
		if err != nil {
			t.Fatal("无法生成测试邮件")
		}
		status, err := mailops.SubmitSMTP(env.ctx, mailops.SMTPConfig{Addr: smtp.Address(), TLSMode: smtp.Network.TLSMode, TLSConfig: smtp.TLSConfig, Dialer: smtp.Dialer, Timeout: 60 * time.Second}, smtp.Username, smtp.Secret, mailbox.Address, []string{mailbox.Address}, raw)
		if err != nil || status != "accepted" {
			t.Fatal("真实 SMTP 没有明确接受邮件")
		}
		locator := waitRealDelivery(t, env, mailbox.ID, id)
		conn, err := env.svc.Pool.GetFresh(env.ctx, imap.IMAPCredential())
		if err != nil {
			t.Fatal("无法读取实际投递邮件")
		}
		defer env.svc.Pool.Put(conn)
		message, err := mailops.GetMessage(env.ctx, conn, locator.Folder, locator.UID, mailops.RenderOptions{MaxBodyBytes: 1 << 20})
		if err != nil {
			t.Fatal("无法读取邮件正文")
		}
		if len(message.From) != 1 || message.From[0].Address != mailbox.Address {
			t.Fatal("实际投递邮件的 From 不符合配置")
		}
		checks = append(checks, "T04_independent_protocol_credentials_and_delivery")
	})
	if t.Failed() {
		return
	}
	t.Run("T06没有ManageSieve时读取和发送可用", func(t *testing.T) {
		mailbox := attachRealConfiguredMailbox(t, env, "没有 ManageSieve 的邮箱", env.input.Manual.NoSieveMailbox)
		_, err := env.svc.ResolveEndpoint(env.ctx, env.orgID, mailbox.ID, provider.ProtocolManageSieve)
		requireRealCode(t, err, "endpoint_disabled")
		caps, err := env.svc.ObjectCapabilities(env.ctx, env.orgID, env.memberID, mailbox.ConnectionID, mailbox.ID)
		if err != nil {
			t.Fatal("无法查询真实邮箱能力")
		}
		if caps["rules.manage"].Readiness == "ready" {
			t.Fatal("停用 ManageSieve 后仍声明规则管理可用")
		}
		imap, err := env.svc.ResolveEndpoint(env.ctx, env.orgID, mailbox.ID, provider.ProtocolIMAP)
		if err != nil {
			t.Fatal("无法解析可用 IMAP")
		}
		conn, err := env.svc.Pool.GetFresh(env.ctx, imap.IMAPCredential())
		if err != nil {
			t.Fatal("实际 IMAP 认证失败")
		}
		_, err = mailops.ListFolders(env.ctx, conn)
		env.svc.Pool.Put(conn)
		if err != nil {
			t.Fatal("停用 ManageSieve 后无法读取真实文件夹")
		}
		smtp, err := env.svc.ResolveEndpoint(env.ctx, env.orgID, mailbox.ID, provider.ProtocolSMTP)
		if err != nil {
			t.Fatal("停用 ManageSieve 后 SMTP 不可用")
		}
		id := uuid.NewString()
		raw, err := mailops.Build(&mailops.Draft{From: mailops.Recipient{Address: mailbox.Address}, To: []mailops.Recipient{{Address: mailbox.Address}}, Subject: "Mailhearth T06 " + id, Text: "ManageSieve disabled 投递检查", Date: time.Now(), MessageID: mailops.NewMessageID("mailhearth.invalid"), SubmissionID: id})
		if err != nil {
			t.Fatal("无法生成测试邮件")
		}
		status, err := mailops.SubmitSMTP(env.ctx, mailops.SMTPConfig{Addr: smtp.Address(), TLSMode: smtp.Network.TLSMode, TLSConfig: smtp.TLSConfig, Dialer: smtp.Dialer, Timeout: 60 * time.Second}, smtp.Username, smtp.Secret, mailbox.Address, []string{mailbox.Address}, raw)
		if err != nil || status != "accepted" {
			t.Fatal("ManageSieve disabled 时 SMTP 投递失败")
		}
		waitRealDelivery(t, env, mailbox.ID, id)
		checks = append(checks, "T06_sieve_disabled_with_real_read_and_delivery")
		t.Run("T05停用SMTP保留IMAP读取", func(t *testing.T) {
			input := actualEndpointUpdate(t, env, mailbox.ID)
			input.Endpoints.SMTP = &core.EndpointInput{NetworkMode: "disabled"}
			if _, err := env.svc.UpdateEndpoints(env.ctx, env.orgID, env.memberID, mailbox.ID, input); err != nil {
				t.Fatal("无法提交 SMTP disabled 配置")
			}
			_, err := env.svc.ResolveEndpoint(env.ctx, env.orgID, mailbox.ID, provider.ProtocolSMTP)
			requireRealCode(t, err, "endpoint_disabled")
			caps, err := env.svc.ObjectCapabilities(env.ctx, env.orgID, env.memberID, mailbox.ConnectionID, mailbox.ID)
			if err != nil {
				t.Fatal("无法查询 SMTP disabled 能力")
			}
			if caps["mail.send"].Readiness == "ready" || caps["mail.read"].Readiness != "ready" {
				t.Fatal("SMTP disabled 改变了独立读取能力")
			}
			imap, err := env.svc.ResolveEndpoint(env.ctx, env.orgID, mailbox.ID, provider.ProtocolIMAP)
			if err != nil {
				t.Fatal("SMTP disabled 后 IMAP 无法解析")
			}
			conn, err := env.svc.Pool.GetFresh(env.ctx, imap.IMAPCredential())
			if err != nil {
				t.Fatal("SMTP disabled 后 IMAP 登录失败")
			}
			_, err = mailops.ListFolders(env.ctx, conn)
			env.svc.Pool.Put(conn)
			if err != nil {
				t.Fatal("SMTP disabled 后实际读取失败")
			}
			checks = append(checks, "T05_smtp_disabled_with_real_imap_read")
		})
	})
	if t.Failed() {
		return
	}
	t.Run("T37协议版本变化关闭旧连接", func(t *testing.T) {
		mailbox := attachRealConfiguredMailbox(t, env, "协议版本检查邮箱", env.input.Manual.PrimaryMailbox)
		before, err := env.svc.ResolveEndpoint(env.ctx, env.orgID, mailbox.ID, provider.ProtocolIMAP)
		if err != nil {
			t.Fatal("无法解析原 IMAP endpoint")
		}
		held, err := env.svc.Pool.GetFresh(env.ctx, before.IMAPCredential())
		if err != nil {
			t.Fatal("无法建立原 IMAP 连接")
		}
		defer env.svc.Pool.Put(held)
		input := actualEndpointUpdate(t, env, mailbox.ID)
		if _, err := env.svc.UpdateEndpoints(env.ctx, env.orgID, env.memberID, mailbox.ID, input); err != nil {
			t.Fatal("真实候选协议验证没有完成")
		}
		if err := held.C.Noop().Wait(); err == nil {
			t.Fatal("配置版本变化后旧 IMAP 连接仍然可用")
		}
		after, err := env.svc.ResolveEndpoint(env.ctx, env.orgID, mailbox.ID, provider.ProtocolIMAP)
		if err != nil {
			t.Fatal("无法解析新 IMAP endpoint")
		}
		if after.EndpointRevision <= before.EndpointRevision {
			t.Fatal("协议版本没有增加")
		}
		current, err := env.svc.Pool.GetFresh(env.ctx, after.IMAPCredential())
		if err != nil {
			t.Fatal("新版本无法完成真实 IMAP 登录")
		}
		env.svc.Pool.Put(current)
		checks = append(checks, "T37_old_connection_closed_and_new_revision_authenticated")
	})
	if t.Failed() {
		return
	}
	body, err := json.MarshalIndent(struct {
		Checks             []string `json:"checks"`
		AcceptanceComplete bool     `json:"acceptanceComplete"`
	}{checks, false}, "", "  ")
	if err != nil {
		t.Fatal("无法生成协议检查记录")
	}
	if err := os.WriteFile(filepath.Join(env.dir, "protocols.json"), body, 0600); err != nil {
		t.Fatal("无法保存协议检查记录")
	}
}
