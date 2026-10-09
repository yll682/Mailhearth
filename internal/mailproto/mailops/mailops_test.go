package mailops_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/google/uuid"
	"mailhearth/internal/config"
	"mailhearth/internal/core"
	"mailhearth/internal/db"
	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/mailproto/mailops"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
	"mailhearth/internal/secrets"
)

type realMailboxConfig struct {
	Address     string                   `json:"address"`
	Credentials []core.EnteredCredential `json:"credentials"`
	Endpoints   core.EndpointInputs      `json:"endpoints"`
}

type realProviderConfig struct {
	APIKey      string                     `json:"apiKey"`
	APIUsername string                     `json:"apiUsername,omitempty"`
	Domain      string                     `json:"domain"`
	IMAP        *provider.ProtocolTemplate `json:"imap"`
	SMTP        *provider.ProtocolTemplate `json:"smtp"`
	ManageSieve *provider.ProtocolTemplate `json:"managesieve"`
}

type realProtocolConfig struct {
	Purelymail *realProviderConfig `json:"purelymail"`
	Migadu     *realProviderConfig `json:"migadu"`
	Manual     *struct {
		PrimaryMailbox   *realMailboxConfig `json:"primaryMailbox"`
		SecondaryMailbox *realMailboxConfig `json:"secondaryMailbox"`
		IndependentSMTP  *realMailboxConfig `json:"independentSmtp"`
		PrivateCAMailbox *realMailboxConfig `json:"privateCaMailbox"`
		NoSieveMailbox   *realMailboxConfig `json:"noSieveMailbox"`
	} `json:"manual"`
	DeliveryTimeoutSeconds int `json:"deliveryTimeoutSeconds"`
}

type realMailEnvironment struct {
	ctx             context.Context
	svc             *core.Service
	input           realProtocolConfig
	orgID, memberID int64
}

func newRealMailEnvironment(t *testing.T) *realMailEnvironment {
	t.Helper()
	path := os.Getenv("MAILHEARTH_MULTIPROVIDER_TEST_CONFIG")
	if path == "" {
		t.Skip("未设置 MAILHEARTH_MULTIPROVIDER_TEST_CONFIG；真实协议环境测试未执行")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "data", "integration", "multi-provider"))
	if err != nil {
		t.Fatal("无法确定真实测试配置目录")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal("无法读取已提供的真实测试配置")
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		t.Fatal("无法确定真实测试配置路径")
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		t.Fatal("真实测试配置必须位于项目 data/integration/multi-provider 目录")
	}
	f, err := os.Open(resolved)
	if err != nil {
		t.Fatal("无法打开真实测试配置")
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 1<<20))
	decoder.DisallowUnknownFields()
	var input realProtocolConfig
	if err := decoder.Decode(&input); err != nil {
		t.Fatal("真实测试配置 JSON 无效或包含未知字段")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatal("真实测试配置只能包含一个 JSON 值")
	}
	if input.Manual == nil || input.Manual.PrimaryMailbox == nil {
		t.Fatal("真实测试配置缺少 manual.primaryMailbox")
	}
	if input.DeliveryTimeoutSeconds == 0 {
		input.DeliveryTimeoutSeconds = 180
	}
	if input.DeliveryTimeoutSeconds < 1 {
		t.Fatal("deliveryTimeoutSeconds 必须为正整数")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("无法读取部署配置")
	}
	if cfg.DevStack {
		t.Fatal("真实协议测试要求关闭 MAILHEARTH_DEV_STACK")
	}
	dir := filepath.Join(root, "mailops-"+uuid.NewString())
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal("无法创建本地测试目录")
	}
	master, _, err := secrets.LoadMasterKey(dir)
	if err != nil {
		t.Fatal("无法建立本地凭据加密配置")
	}
	box, err := secrets.NewBox(master, "credentials")
	if err != nil {
		t.Fatal("无法建立凭据加密器")
	}
	database, err := db.Open(filepath.Join(dir, "mailhearth.db"))
	if err != nil {
		t.Fatal("无法建立本地测试数据库")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool := imappool.New(imappool.Config{MaxConns: 8, Logger: logger})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	svc := core.New(database, cfg, box, pool, logger)
	t.Cleanup(func() {
		cancel()
		pool.Close()
		if err := database.Close(); err != nil {
			t.Error("无法关闭本地测试数据库")
		}
	})
	member, err := svc.Init(ctx, core.InitRequest{OrgName: uuid.NewString(), AdminName: "协议测试", AdminEmail: "protocol-tests@example.org", Password: uuid.NewString() + "Aa1!"})
	if err != nil {
		t.Fatal("无法初始化本地测试组织")
	}
	org, err := svc.Org(ctx)
	if err != nil {
		t.Fatal("无法读取本地测试组织")
	}
	return &realMailEnvironment{ctx: ctx, svc: svc, input: input, orgID: org.ID, memberID: member.ID}
}

func (env *realMailEnvironment) attach(t *testing.T, input *realMailboxConfig) *model.Mailbox {
	t.Helper()
	if input == nil || input.Address == "" || len(input.Credentials) == 0 || input.Endpoints.IMAP == nil || input.Endpoints.SMTP == nil || input.Endpoints.ManageSieve == nil {
		t.Fatal("真实协议测试缺少邮箱地址、凭据或全部三个 endpoint 配置")
	}
	connection, err := env.svc.CreateConnection(env.ctx, env.orgID, env.memberID, core.CreateConnectionInput{ProviderKind: provider.Manual, Label: "mailops-" + uuid.NewString()})
	if err != nil {
		t.Fatal("无法建立本地协议连接登记")
	}
	mailbox, err := env.svc.AttachMailbox(env.ctx, env.orgID, env.memberID, core.AttachMailboxInput{Mode: "attach", ConnectionID: connection.ID, Kind: model.MailboxPersonal, Address: input.Address, OwnerMemberID: &env.memberID, CredentialMode: "entered", Credentials: input.Credentials, Endpoints: input.Endpoints, SentCopyMode: "append"})
	if err != nil {
		t.Fatal("真实邮箱配置无效或协议认证失败")
	}
	return mailbox
}

func (env *realMailEnvironment) endpoint(t *testing.T, mailboxID int64, protocol string) *core.ResolvedEndpoint {
	t.Helper()
	endpoint, err := env.svc.ResolveEndpoint(env.ctx, env.orgID, mailboxID, protocol)
	if err != nil {
		t.Fatal("无法解析真实协议 endpoint")
	}
	return endpoint
}

func (env *realMailEnvironment) connect(t *testing.T, endpoint *core.ResolvedEndpoint) *imappool.Conn {
	t.Helper()
	conn, err := env.svc.Pool.GetFresh(env.ctx, endpoint.IMAPCredential())
	if err != nil {
		t.Fatal("真实 IMAP 认证失败")
	}
	return conn
}

func (env *realMailEnvironment) createFolder(t *testing.T, conn *imappool.Conn, endpoint *core.ResolvedEndpoint, purpose string) *string {
	t.Helper()
	name := "000-Mailhearth-" + purpose + "-" + uuid.NewString()
	if err := mailops.CreateFolder(env.ctx, conn, name); err != nil {
		t.Fatal("无法建立测试专用文件夹")
	}
	t.Cleanup(func() {
		if name == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		fresh, err := env.svc.Pool.GetFresh(ctx, endpoint.IMAPCredential())
		if err != nil {
			t.Error("无法连接邮箱以清理测试文件夹")
			return
		}
		defer env.svc.Pool.Put(fresh)
		if err := mailops.DeleteFolder(ctx, fresh, name); err != nil {
			t.Error("无法清理测试专用文件夹")
		}
	})
	return &name
}

func realPage(t *testing.T, ctx context.Context, conn *imappool.Conn, folder string, page, size int, query string) *mailops.Page {
	t.Helper()
	conn.Invalidate()
	result, err := mailops.ListMessages(ctx, conn, folder, page, size, query)
	if err != nil {
		t.Fatal("真实邮件查询失败")
	}
	return result
}

func realFolder(t *testing.T, ctx context.Context, conn *imappool.Conn, name string) mailops.Folder {
	t.Helper()
	folders, err := mailops.ListFolders(ctx, conn)
	if err != nil {
		t.Fatal("真实文件夹查询失败")
	}
	for _, folder := range folders {
		if folder.Name == name {
			return folder
		}
	}
	t.Fatal("真实服务器没有返回测试文件夹")
	return mailops.Folder{}
}

func TestMailOpsAgainstRealProtocols(t *testing.T) {
	env := newRealMailEnvironment(t)
	primary := env.attach(t, env.input.Manual.PrimaryMailbox)
	secondary := env.attach(t, env.input.Manual.SecondaryMailbox)
	if strings.EqualFold(primary.Address, secondary.Address) {
		t.Fatal("协议测试要求 primaryMailbox 与 secondaryMailbox 使用不同邮箱")
	}
	imapEndpoint := env.endpoint(t, primary.ID, provider.ProtocolIMAP)
	wrong := imapEndpoint.IMAPCredential()
	wrong.Pass = uuid.NewString() + "Aa1!"
	bad, err := env.svc.Pool.GetFresh(env.ctx, wrong)
	if bad != nil {
		env.svc.Pool.Put(bad)
	}
	var authError *imappool.AuthError
	if !errors.As(err, &authError) {
		t.Fatal("真实 IMAP 未明确拒绝错误密码")
	}
	conn := env.connect(t, imapEndpoint)
	defer env.svc.Pool.Put(conn)
	folders, err := mailops.ListFolders(env.ctx, conn)
	if err != nil || len(folders) == 0 || folders[0].Role != mailops.RoleInbox || mailops.SpecialFolders(folders)[mailops.RoleInbox] != "INBOX" {
		t.Fatal("真实 INBOX 角色或文件夹列表无效")
	}
	source := env.createFolder(t, conn, imapEndpoint, "source")
	trash := env.createFolder(t, conn, imapEndpoint, "trash")
	drafts := env.createFolder(t, conn, imapEndpoint, "drafts")
	copies := env.createFolder(t, conn, imapEndpoint, "copies")
	now := time.Now()
	for i := 0; i < 7; i++ {
		suffix := string(rune('A' + i))
		raw, err := mailops.Build(&mailops.Draft{From: mailops.Recipient{Address: primary.Address}, To: []mailops.Recipient{{Address: secondary.Address}}, Subject: "Hello " + suffix, Text: "plain body " + suffix + " see https://example.com", HTML: `<p>html <b>body</b> <img src="http://track.example/p.gif"></p>`, Date: now.Add(time.Duration(i) * time.Minute), MessageID: mailops.NewMessageID("mailhearth.invalid")})
		if err != nil {
			t.Fatal("无法建立测试 MIME 邮件")
		}
		if _, err := mailops.Append(env.ctx, conn, *source, nil, now.Add(time.Duration(i)*time.Minute), raw); err != nil {
			t.Fatal("真实 APPEND 失败")
		}
	}
	folder := realFolder(t, env.ctx, conn, *source)
	if folder.Total != 7 || folder.Unseen != 7 {
		t.Fatal("测试文件夹的邮件数量和未读数量无效")
	}
	page := realPage(t, env.ctx, conn, *source, 0, 5, "")
	if page.Total != 7 || len(page.Messages) != 5 || page.Messages[0].Subject != "Hello G" || page.Messages[4].Subject != "Hello C" {
		t.Fatal("真实邮件第一页内容或顺序无效")
	}
	page1 := realPage(t, env.ctx, conn, *source, 1, 5, "")
	if len(page1.Messages) != 2 || page1.Messages[1].Subject != "Hello A" {
		t.Fatal("真实邮件第二页内容或顺序无效")
	}
	if page.Messages[0].Seen || len(page.Messages[0].From) != 1 || page.Messages[0].From[0].Address != primary.Address || page.Messages[0].HasAttachment {
		t.Fatal("真实邮件摘要无效")
	}
	found := realPage(t, env.ctx, conn, *source, 0, 10, `subject:"Hello C" is:unread`)
	if found.Total != 1 || len(found.Messages) != 1 || found.Messages[0].Subject != "Hello C" {
		t.Fatal("真实邮件搜索结果无效")
	}
	uid := page.Messages[0].UID
	msg, err := mailops.GetMessage(env.ctx, conn, *source, uid, mailops.RenderOptions{PartURL: func(p string) string { return "/p/" + p }})
	if err != nil || !strings.Contains(msg.HTML, "<b>body</b>") || !strings.Contains(msg.HTML, "data-mh-src") || !msg.HasRemote || !strings.Contains(msg.Text, "plain body G") {
		t.Fatal("真实 MIME 正文渲染或远程图片保护无效")
	}
	if err := mailops.StoreFlags(env.ctx, conn, *source, []uint32{uid}, []string{`\Seen`, `\Flagged`}, nil); err != nil {
		t.Fatal("真实 flags 写入失败")
	}
	page = realPage(t, env.ctx, conn, *source, 0, 1, "")
	if len(page.Messages) != 1 || !page.Messages[0].Seen || !page.Messages[0].Flagged || realFolder(t, env.ctx, conn, *source).Unseen != 6 {
		t.Fatal("真实 flags 或未读数量无效")
	}
	if _, err := conn.Select(env.ctx, *source, false); err != nil {
		t.Fatal("无法选择复制来源文件夹")
	}
	conn.Run(env.ctx, func() { _, err = conn.C.Copy(imap.UIDSetNum(imap.UID(uid)), *copies).Wait() })
	if err != nil {
		t.Fatal("真实 COPY 失败")
	}
	copyPage := realPage(t, env.ctx, conn, *copies, 0, 10, "")
	if copyPage.Total != 1 || len(copyPage.Messages) != 1 || copyPage.Messages[0].Subject != "Hello G" || realPage(t, env.ctx, conn, *source, 0, 10, "").Total != 7 {
		t.Fatal("真实 COPY 修改了来源邮件或没有保存副本")
	}
	if err := mailops.Delete(env.ctx, conn, *source, []uint32{uid}, *trash, false); err != nil {
		t.Fatal("真实 MOVE 到测试 Trash 失败")
	}
	trashPage := realPage(t, env.ctx, conn, *trash, 0, 10, "")
	if trashPage.Total != 1 || len(trashPage.Messages) != 1 || trashPage.Messages[0].Subject != "Hello G" || realPage(t, env.ctx, conn, *source, 0, 10, "").Total != 6 {
		t.Fatal("真实 MOVE 的来源或目标内容无效")
	}
	if err := mailops.Delete(env.ctx, conn, *trash, []uint32{trashPage.Messages[0].UID}, *trash, false); err != nil {
		t.Fatal("真实永久删除失败")
	}
	if realPage(t, env.ctx, conn, *trash, 0, 10, "").Total != 0 {
		t.Fatal("真实永久删除没有清理测试邮件")
	}
	id := uuid.NewString()
	draft := &mailops.Draft{
		From:         mailops.Recipient{Name: "Alice", Address: primary.Address},
		To:           []mailops.Recipient{{Name: "Bob", Address: secondary.Address}},
		Subject:      "Report 报告 " + id,
		Text:         "Hi Bob,\nsee attached.",
		HTML:         "<p>Hi Bob,<br>see <b>attached</b>.</p>",
		MessageID:    mailops.NewMessageID("mailhearth.invalid"),
		SubmissionID: id,
		Attachments: []mailops.Attachment{{Filename: "report.csv", MIME: "text/csv", Size: 11, Open: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("a,b\n1,2\n3,4")), nil
		}}},
	}
	raw, err := mailops.Build(draft)
	if err != nil || !bytes.Contains(raw, []byte("Content-Type: multipart/mixed")) || !bytes.Contains(raw, []byte("report.csv")) {
		t.Fatal("测试 MIME 附件生成失败")
	}
	locator, err := mailops.AppendSubmissionMessage(env.ctx, conn, *drafts, id, []string{`\Draft`, `\Seen`}, raw, 16<<20)
	if err != nil || locator == nil || locator.UID == 0 {
		t.Fatal("真实草稿 APPEND 或定位失败")
	}
	dm, err := mailops.GetMessage(env.ctx, conn, *drafts, locator.UID, mailops.RenderOptions{})
	if err != nil || dm.Subject != draft.Subject || !dm.HasAttachment || len(dm.Attachments) != 1 || dm.Attachments[0].Filename != "report.csv" || !strings.Contains(dm.HTML, "<b>attached</b>") {
		t.Fatal("真实草稿 MIME 读取或附件描述无效")
	}
	part, err := mailops.FindPart(env.ctx, conn, *drafts, locator.UID, dm.Attachments[0].Path)
	if err != nil {
		t.Fatal("真实 MIME 附件定位失败")
	}
	var out bytes.Buffer
	if err := mailops.StreamPart(env.ctx, conn, *drafts, locator.UID, part, &out); err != nil || out.String() != "a,b\n1,2\n3,4" {
		t.Fatal("真实 MIME 附件内容无效")
	}
	var rawOut bytes.Buffer
	if err := mailops.StreamRaw(env.ctx, conn, *drafts, locator.UID, &rawOut); err != nil || !bytes.Contains(rawOut.Bytes(), []byte("Subject:")) {
		t.Fatal("真实 MIME 原始邮件读取失败")
	}
	projects := env.createFolder(t, conn, imapEndpoint, "projects")
	originalName := *projects
	newName := "000-Mailhearth-clients-" + uuid.NewString()
	if err := mailops.RenameFolder(env.ctx, conn, *projects, newName); err != nil {
		t.Fatal("真实文件夹重命名失败")
	}
	*projects = newName
	folders, err = mailops.ListFolders(env.ctx, conn)
	if err != nil {
		t.Fatal("重命名后无法查询真实文件夹")
	}
	names := map[string]bool{}
	for _, folder := range folders {
		names[folder.Name] = true
	}
	if !names[newName] || names[originalName] {
		t.Fatal("真实文件夹重命名结果无效")
	}
	if err := mailops.DeleteFolder(env.ctx, conn, newName); err != nil {
		t.Fatal("真实文件夹删除失败")
	}
	*projects = ""
	secondaryEndpoint := env.endpoint(t, secondary.ID, provider.ProtocolIMAP)
	bconn := env.connect(t, secondaryEndpoint)
	defer env.svc.Pool.Put(bconn)
	if (!bconn.C.Caps().Has(imap.CapUIDPlus) && !bconn.C.Caps().Has(imap.CapIMAP4rev2)) || (!conn.C.Caps().Has(imap.CapUIDPlus) && !conn.C.Caps().Has(imap.CapIMAP4rev2)) {
		t.Fatal("SMTP 测试邮件清理要求两个真实邮箱支持 UID EXPUNGE")
	}
	smtpEndpoint := env.endpoint(t, primary.ID, provider.ProtocolSMTP)
	smtpConfig := mailops.SMTPConfig{Addr: smtpEndpoint.Address(), TLSMode: smtpEndpoint.Network.TLSMode, TLSConfig: smtpEndpoint.TLSConfig, Dialer: smtpEndpoint.Dialer, Timeout: 60 * time.Second}
	if err := mailops.Send(env.ctx, smtpConfig, smtpEndpoint.Username, uuid.NewString()+"Aa1!", primary.Address, []string{secondary.Address}, raw); err == nil {
		t.Fatal("真实 SMTP 未拒绝错误密码")
	} else {
		var authError *mailops.SendAuthError
		if !errors.As(err, &authError) {
			t.Fatal("真实 SMTP 错误密码没有取得明确认证拒绝")
		}
	}
	t.Cleanup(func() {
		cleanupDeliveredMessage(t, env, secondaryEndpoint, id, draft.MessageID)
		cleanupDeliveredMessage(t, env, imapEndpoint, id, draft.MessageID)
	})
	if err := mailops.Send(env.ctx, smtpConfig, smtpEndpoint.Username, smtpEndpoint.Secret, primary.Address, []string{secondary.Address}, raw); err != nil {
		t.Fatal("真实 SMTP 投递失败")
	}
	delivered := waitRealMessage(t, env, bconn, id)
	message, err := mailops.GetMessage(env.ctx, bconn, "INBOX", delivered.UID, mailops.RenderOptions{})
	if err != nil || message.Subject != draft.Subject || len(message.Attachments) != 1 || message.Attachments[0].Filename != "report.csv" {
		t.Fatal("真实 SMTP 收件内容无效")
	}
	if !conn.C.Caps().Has(imap.CapIdle) {
		t.Fatal("通知测试要求真实 IMAP 支持 IDLE")
	}
	events, cancel, err := env.svc.Pool.SubscribeChecked(imapEndpoint.IMAPCredential(), *source)
	if err != nil {
		t.Fatal("无法启动真实 IDLE 通知")
	}
	defer cancel()
	idleContext, idleCancel := context.WithTimeout(env.ctx, 30*time.Second)
	defer idleCancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	created := uint32(6)
waitIdle:
	for {
		select {
		case ev, ok := <-events:
			if !ok || ev.Mailbox != *source || ev.Err != "" || ev.Expunged || ev.NumMessages > created {
				t.Fatal("真实 IDLE 新邮件通知无效")
			}
			if ev.NumMessages > 6 {
				break waitIdle
			}
		case <-ticker.C:
			ping, err := mailops.Build(&mailops.Draft{From: mailops.Recipient{Address: primary.Address}, To: []mailops.Recipient{{Address: primary.Address}}, Subject: "Ping", Text: "ping", MessageID: mailops.NewMessageID("mailhearth.invalid")})
			if err != nil {
				t.Fatal("无法生成 IDLE 测试邮件")
			}
			if _, err := mailops.Append(idleContext, conn, *source, nil, time.Now(), ping); err != nil {
				t.Fatal("真实 IDLE 测试 APPEND 失败")
			}
			created++
		case <-idleContext.Done():
			t.Fatal("未收到真实 IDLE 新邮件通知")
		}
	}
	open, _, watchers := env.svc.Pool.Stats()
	if watchers != 1 || open < 2 {
		t.Fatal("真实 IMAP 连接统计无效")
	}
}

func waitRealMessage(t *testing.T, env *realMailEnvironment, conn *imappool.Conn, id string) *mailops.MessageLocator {
	t.Helper()
	ctx, cancel := context.WithTimeout(env.ctx, time.Duration(env.input.DeliveryTimeoutSeconds)*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		found, err := mailops.FindSubmissionMessage(ctx, conn, "INBOX", id, 16<<20)
		if err != nil {
			t.Fatal("真实收件查询失败")
		}
		if found != nil {
			return found
		}
		select {
		case <-ctx.Done():
			t.Fatal("真实 SMTP 邮件未在规定时间内送达")
		case <-ticker.C:
		}
	}
}

func cleanupDeliveredMessage(t *testing.T, env *realMailEnvironment, endpoint *core.ResolvedEndpoint, id, messageID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := env.svc.Pool.GetFresh(ctx, endpoint.IMAPCredential())
	if err != nil {
		t.Error("无法连接真实收件邮箱以清理测试邮件")
		return
	}
	defer env.svc.Pool.Put(conn)
	folders, err := mailops.ListFolders(ctx, conn)
	if err != nil {
		t.Error("无法查询 SMTP 测试邮件的文件夹")
		return
	}
	for _, folder := range folders {
		if folder.NoSelect {
			continue
		}
		conn.Invalidate()
		selected, err := conn.Select(ctx, folder.Name, true)
		if err != nil {
			t.Error("无法选择 SMTP 测试邮件的文件夹")
			return
		}
		var found *imap.SearchData
		conn.Run(ctx, func() {
			found, err = conn.C.UIDSearch(&imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "X-Mailhearth-Submission-ID", Value: id}, {Key: "Message-ID", Value: messageID}}}, nil).Wait()
		})
		if err != nil {
			t.Error("无法定位待清理的 SMTP 测试邮件")
			return
		}
		for _, uid := range found.AllUIDs() {
			locator := mailops.MessageLocator{Folder: folder.Name, UIDValidity: selected.UIDValidity, UID: uint32(uid)}
			if _, err := mailops.ReadSubmissionMessage(ctx, conn, locator, id, 16<<20); err != nil {
				t.Error("无法核验待清理的 SMTP 测试邮件")
				return
			}
			message, err := mailops.GetMessage(ctx, conn, folder.Name, locator.UID, mailops.RenderOptions{})
			if err != nil || strings.Trim(message.MessageID, "<>") != strings.Trim(messageID, "<>") {
				t.Error("SMTP 测试邮件 Message-ID 不匹配")
				return
			}
			if err := mailops.DeleteLocatedMessage(ctx, conn, locator); err != nil {
				t.Error("无法清理 SMTP 测试邮件")
				return
			}
		}
	}
}

func TestParseQuery(t *testing.T) {
	c := mailops.ParseQuery(`from:boss subject:"quarterly report" is:unread has:attachment since:2024-01-02 larger:2M invoice`)
	if len(c.Header) != 2 || c.Header[0].Key != "From" || c.Header[1].Value != "quarterly report" {
		t.Fatalf("headers: %+v", c.Header)
	}
	if len(c.NotFlag) != 1 || len(c.Or) != 1 || c.Since.IsZero() || c.Larger != 2<<20 || len(c.Text) != 1 || c.Text[0] != "invoice" {
		t.Fatalf("criteria: %+v", c)
	}
}
