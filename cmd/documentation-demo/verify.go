package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"mailhearth/internal/core"
	"mailhearth/internal/mailproto/mailops"
	"mailhearth/internal/provider"
	"mailhearth/internal/secrets"
)

func verifyDemo(ctx context.Context, svc *core.Service, handler http.Handler) {
	org, err := svc.Org(ctx)
	require(err)
	var owner, mailbox, shared, connection int64
	require(svc.DB.QueryRowContext(ctx, "SELECT m.id FROM members m JOIN roles r ON r.id=m.role_id WHERE m.org_id=? AND r.key='owner'", org.ID).Scan(&owner))
	require(svc.DB.QueryRowContext(ctx, "SELECT id FROM mailboxes WHERE org_id=? AND address='alice@acme.test'", org.ID).Scan(&mailbox))
	require(svc.DB.QueryRowContext(ctx, "SELECT id FROM mailboxes WHERE org_id=? AND address='support@acme.test'", org.ID).Scan(&shared))
	require(svc.DB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='documentation.demo.connection'").Scan(&connection))
	_, err = svc.TestConnection(ctx, org.ID, owner, connection)
	require(err)
	discovered, err := svc.DiscoverConnection(ctx, org.ID, connection)
	require(err)
	var discovery struct {
		SnapshotID int64                                `json:"snapshotId"`
		Resources  []provider.DiscoverySnapshotResource `json:"resources"`
	}
	require(json.Unmarshal([]byte(jsonText(discovered)), &discovery))
	foundForwarding := false
	var forwardingSelection core.SelectedResource
	for _, resource := range discovery.Resources {
		if resource.Resource.Purpose == provider.PurposeForwarding && resource.Summary["mailboxAddress"] == "support@acme.test" {
			foundForwarding = resource.Forwarding != nil
			forwardingSelection = core.SelectedResource{ResourceType: resource.Resource.ResourceType, RemoteKey: resource.Resource.RemoteKey}
		}
	}
	if !foundForwarding {
		panic("演示资源发现缺少邮箱转发条目")
	}
	for i := 0; i < 2; i++ {
		_, err = svc.ImportConnection(ctx, org.ID, connection, core.ImportSelection{SnapshotID: discovery.SnapshotID, SelectedResources: []core.SelectedResource{forwardingSelection}})
		require(err)
	}
	var forwardingCount int
	var deliveryMode string
	require(svc.DB.QueryRowContext(ctx, "SELECT COUNT(*),delivery_mode FROM mailbox_forwardings WHERE mailbox_id=?", shared).Scan(&forwardingCount, &deliveryMode))
	if forwardingCount != 1 || deliveryMode != "unverified" {
		panic("演示转发导入结果不正确")
	}
	status, err := svc.Status(ctx)
	require(err)
	if status.NeedsSetup || status.Step != "done" {
		panic("演示初始化尚未完成")
	}
	token, err := svc.CreateSession(ctx, owner, "127.0.0.1", "documentation-demo-check")
	require(err)
	defer func() {
		_, err := svc.DB.ExecContext(context.Background(), "DELETE FROM sessions WHERE token_hash=?", secrets.HashToken(token))
		require(err)
	}()
	server := httptest.NewServer(handler)
	defer server.Close()
	client := &http.Client{Timeout: 30 * time.Second}
	get := func(path string) []byte {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
		require(err)
		request.AddCookie(&http.Cookie{Name: "mh_session", Value: token})
		response, err := client.Do(request)
		require(err)
		raw, err := io.ReadAll(response.Body)
		require(err)
		require(response.Body.Close())
		if response.StatusCode != http.StatusOK {
			panic(fmt.Sprintf("演示页面检查失败：%s HTTP %d %s", path, response.StatusCode, raw))
		}
		return raw
	}
	for _, path := range []string{
		"/", "/api/setup/status", "/api/auth/me", "/api/admin/overview",
		"/api/admin/members", "/api/admin/roles", "/api/admin/domains", "/api/admin/domain-bindings",
		"/api/admin/mailboxes", "/api/admin/addresses", "/api/admin/groups",
		"/api/admin/connections", "/api/admin/audit", "/api/mail/mailboxes",
		"/api/admin/operations", "/api/admin/operations/documentation-demo-01",
		fmt.Sprintf("/api/admin/connections/%d/billing", connection),
		fmt.Sprintf("/api/admin/mailboxes/%d/endpoints", mailbox),
		fmt.Sprintf("/api/mail/mailboxes/%d/folders", mailbox),
		fmt.Sprintf("/api/mail/mailboxes/%d/folders", shared),
		fmt.Sprintf("/api/mail/mailboxes/%d/messages?folder=INBOX", shared),
	} {
		get(path)
	}
	var bindings []map[string]json.RawMessage
	require(json.Unmarshal(get("/api/admin/domain-bindings"), &bindings))
	for _, binding := range bindings {
		var settings map[string]any
		require(json.Unmarshal(binding["providerSettings"], &settings))
		if settings == nil {
			panic("域名关联缺少 providerSettings 对象")
		}
	}
	var sharedPage struct {
		mailops.Page
		Team map[string]struct {
			Status string `json:"status"`
		} `json:"team"`
	}
	require(json.Unmarshal(get(fmt.Sprintf("/api/mail/mailboxes/%d/messages?folder=INBOX", shared)), &sharedPage))
	resolved := 0
	for _, state := range sharedPage.Team {
		if state.Status == "resolved" {
			resolved++
		}
	}
	if resolved < 2 || len(sharedPage.Team) < 8 {
		panic("演示共享邮箱协作记录不完整")
	}
	var sharedDetail struct {
		Team core.MessageState `json:"team"`
	}
	require(json.Unmarshal(get(fmt.Sprintf("/api/mail/mailboxes/%d/messages/%d?folder=INBOX&markRead=0", shared, sharedPage.Messages[0].UID)), &sharedDetail))
	if sharedDetail.Team.AssigneeID == nil || len(sharedDetail.Team.Activity) < 2 {
		panic("演示邮件缺少负责人或内部备注")
	}
	var page struct {
		mailops.Page
	}
	require(json.Unmarshal(get(fmt.Sprintf("/api/mail/mailboxes/%d/messages?folder=INBOX", mailbox)), &page))
	if page.Total != 24 || len(page.Messages) == 0 {
		panic("演示收件箱邮件数量不正确")
	}
	var detail struct {
		Message struct {
			Attachments []struct {
				Path     string `json:"path"`
				Filename string `json:"filename"`
			} `json:"attachments"`
		} `json:"message"`
		ViewURL   string `json:"viewUrl"`
		ViewToken string `json:"viewToken"`
	}
	require(json.Unmarshal(get(fmt.Sprintf("/api/mail/mailboxes/%d/messages/%d?folder=INBOX&markRead=0", mailbox, page.Messages[0].UID)), &detail))
	get(detail.ViewURL)
	if len(detail.Message.Attachments) != 2 {
		panic("演示邮件附件数量不正确")
	}
	for _, attachment := range detail.Message.Attachments {
		if attachment.Path == "" || attachment.Filename == "" || detail.ViewToken == "" {
			panic("演示附件缺少下载信息")
		}
		path := fmt.Sprintf("/api/mail/mailboxes/%d/messages/%d/parts/%s?folder=INBOX&t=%s", mailbox, page.Messages[0].UID, attachment.Path, url.QueryEscape(detail.ViewToken))
		body := string(get(path))
		if attachment.Filename == "aurora-budget.csv" && !strings.Contains(body, "Engineering,Bob Wilson,6400") {
			panic("演示预算附件内容不正确")
		}
		if attachment.Filename == "project-notes.txt" && !strings.Contains(body, "本文件用于文档演示") {
			panic("演示说明附件内容不正确")
		}
	}
	smtp, err := svc.ResolveEndpoint(ctx, org.ID, mailbox, provider.ProtocolSMTP)
	require(err)
	raw, err := mailops.Build(&mailops.Draft{
		From:    mailops.Recipient{Name: "演示管理员", Address: "alice@acme.test"},
		To:      []mailops.Recipient{{Name: "Bob Wilson", Address: "bob@acme.test"}},
		Subject: "本地 SMTP 发送验证 · 演示", Text: "本邮件确认本地演示环境的发送与接收功能。",
		MessageID: "<demo-smtp-check@acme.test>",
	})
	require(err)
	require(mailops.Send(ctx, mailops.SMTPConfig{Addr: smtp.Address(), TLSMode: smtp.Network.TLSMode, TLSConfig: smtp.TLSConfig, Dialer: smtp.Dialer, Timeout: 15 * time.Second}, smtp.Username, smtp.Secret, "alice@acme.test", []string{"bob@acme.test"}, raw))
	var bob int64
	require(svc.DB.QueryRowContext(ctx, "SELECT id FROM mailboxes WHERE org_id=? AND address='bob@acme.test'", org.ID).Scan(&bob))
	endpoint, err := svc.ResolveEndpoint(ctx, org.ID, bob, provider.ProtocolIMAP)
	require(err)
	conn, err := svc.Pool.GetFresh(ctx, endpoint.IMAPCredential())
	require(err)
	defer svc.Pool.Put(conn)
	delivered, err := mailops.ListMessages(ctx, conn, "INBOX", 0, 50, "")
	require(err)
	if delivered.Total != 25 {
		panic("本地演示 SMTP 邮件未正确送达")
	}
	var integrity string
	require(svc.DB.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity))
	if integrity != "ok" {
		panic("演示数据库完整性检查失败")
	}
	rows, err := svc.DB.QueryContext(ctx, "PRAGMA foreign_key_check")
	require(err)
	defer rows.Close()
	if rows.Next() {
		panic("演示数据库存在无效关联")
	}
	require(rows.Err())
	fmt.Println("本地演示检查通过：管理页面、邮件列表、HTML 阅读、附件下载、共享协作、资源发现、转发重复导入、IMAP 与 SMTP。")
}
