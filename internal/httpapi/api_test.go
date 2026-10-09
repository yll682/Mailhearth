package httpapi_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"mailhearth/internal/config"
	"mailhearth/internal/core"
	"mailhearth/internal/db"
	"mailhearth/internal/httpapi"
	"mailhearth/internal/model"
	"mailhearth/internal/secrets"
	"mailhearth/internal/web"
)

type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func newClient(t *testing.T, base string) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &client{t: t, base: base, http: &http.Client{Jar: jar, Timeout: 10 * time.Second}}
}

func (c *client) request(req *http.Request, status int, out any) []byte {
	c.t.Helper()
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	if resp.StatusCode != status {
		c.t.Fatalf("%s %s 返回 %d，要求 %d：%s", req.Method, req.URL.Path, resp.StatusCode, status, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatal(err)
		}
	}
	return raw
}

func (c *client) do(method, path string, body any, status int, out any) []byte {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("X-Requested-With", "Mailhearth")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.request(req, status, out)
}

func (c *client) await(id string) core.OperationView {
	c.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var operation core.OperationView
		c.do("GET", "/api/admin/operations/"+id, nil, 200, &operation)
		if operation.Status == "succeeded" {
			return operation
		}
		if operation.Status != "queued" && operation.Status != "running" {
			c.t.Fatalf("Operation 未完成：%s %s", operation.Status, operation.Result)
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.t.Fatal("Operation 超出完成期限")
	return core.OperationView{}
}

func TestEndToEnd(t *testing.T) {
	root := filepath.Join("..", "..", "data", "integration", "multi-provider", "api-"+uuid.NewString())
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(root, "mailhearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	master := make([]byte, 32)
	if _, err := rand.Read(master); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.NewBox(master, "credentials")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{DataDir: root, MaxUploadBytes: 1 << 20, SessionTTL: time.Hour, InviteTTL: time.Hour}
	svc := core.New(database, cfg, box, nil, slog.Default())
	ctx, stop := context.WithCancel(context.Background())
	if err := svc.StartOperations(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { stop(); svc.WaitOperations() }()
	api := httpapi.New(cfg, svc, nil, web.Handler(), master, slog.Default())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		if err := <-done; err != http.ErrServerClosed {
			t.Error(err)
		}
	}()
	c := newClient(t, "http://"+listener.Addr().String())
	req, err := http.NewRequest("POST", c.base+"/api/setup/init", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	c.request(req, 403, nil)
	var status core.SetupStatus
	c.do("GET", "/api/setup/status", nil, 200, &status)
	if !status.NeedsSetup || status.Step != "org" {
		t.Fatalf("初始化状态无效：%+v", status)
	}
	c.do("GET", "/api/auth/me", nil, 401, nil)
	password := uuid.NewString() + "Aa1!"
	var me struct {
		Member      model.Member `json:"member"`
		Permissions []string     `json:"permissions"`
	}
	c.do("POST", "/api/setup/init", core.InitRequest{OrgName: "HTTP 组织", AdminName: "管理员", AdminEmail: "admin@example.org", Password: password}, 200, &me)
	if me.Member.ID < 1 || !core.HasPermission(me.Permissions, model.PermOrgOwner) {
		t.Fatal("初始化没有创建组织所有者")
	}
	ownerID := me.Member.ID
	var connection core.ConnectionView
	c.do("POST", "/api/admin/connections", map[string]any{"providerKind": "manual", "label": "组织连接", "apiAuth": nil}, 201, &connection)
	c.do("GET", "/api/setup/status", nil, 200, &status)
	if status.Step != "mailboxes" {
		t.Fatal("连接登记没有更新初始化状态")
	}
	c.do("POST", "/api/setup/complete", map[string]any{"connectionId": connection.ID, "bindMailboxId": nil}, 200, nil)
	c.do("GET", "/api/setup/status", nil, 200, &status)
	if status.NeedsSetup || status.Step != "done" {
		t.Fatal("初始化没有完成")
	}
	var mailboxes []core.AccessibleMailbox
	c.do("GET", "/api/mail/mailboxes", nil, 200, &mailboxes)
	if len(mailboxes) != 0 {
		t.Fatal("初始化登记了未选择的邮箱")
	}
	var operation core.OperationView
	c.do("POST", "/api/admin/domain-bindings", core.DomainBindingInput{RequestID: uuid.NewString(), ConnectionID: connection.ID, DomainName: "example.org", Mode: "register"}, 202, &operation)
	operation = c.await(operation.ID)
	if operation.Status != "succeeded" {
		t.Fatal("域名关联登记失败")
	}
	var bindings []map[string]json.RawMessage
	c.do("GET", "/api/admin/domain-bindings", nil, 200, &bindings)
	if len(bindings) != 1 {
		t.Fatal("域名关联列表数量不正确")
	}
	var settings map[string]any
	if err := json.Unmarshal(bindings[0]["providerSettings"], &settings); err != nil {
		t.Fatal(err)
	}
	if settings == nil {
		t.Fatal("域名关联必须返回 providerSettings 对象")
	}
	c.do("POST", "/api/admin/members", map[string]any{"requestId": uuid.NewString(), "displayName": "受邀成员", "loginEmail": "member@example.org", "mailboxAction": "none", "sendInvite": true}, 202, &operation)
	operation = c.await(operation.ID)
	var created struct {
		MemberID   int64  `json:"memberId"`
		InviteLink string `json:"inviteLink"`
	}
	if err := json.Unmarshal(operation.Result, &created); err != nil {
		t.Fatal(err)
	}
	if created.MemberID < 1 || created.InviteLink == "" {
		t.Fatal("成员创建没有返回成员与邀请")
	}
	token := created.InviteLink[strings.LastIndex(created.InviteLink, "/")+1:]
	member := newClient(t, c.base)
	member.do("GET", "/api/auth/invite/"+token, nil, 200, nil)
	memberPassword := uuid.NewString() + "Aa1!"
	member.do("POST", "/api/auth/invite/"+token+"/accept", map[string]any{"password": memberPassword, "displayName": "成员"}, 200, &me)
	if me.Member.ID != created.MemberID || me.Member.Status != model.MemberActive {
		t.Fatal("接受邀请没有建立对应成员会话")
	}
	member.do("GET", "/api/admin/overview", nil, 403, nil)
	member.do("POST", "/api/admin/connections", map[string]any{"providerKind": "manual", "label": "未授权连接"}, 403, nil)
	c.do("POST", "/api/auth/password", map[string]any{"current": uuid.NewString(), "new": uuid.NewString()}, 400, nil)
	newPassword := uuid.NewString() + "Aa1!"
	c.do("POST", "/api/auth/password", map[string]any{"current": password, "new": newPassword}, 200, nil)
	c.do("POST", "/api/auth/logout", nil, 200, nil)
	c.do("GET", "/api/auth/me", nil, 401, nil)
	c.do("POST", "/api/auth/login", map[string]any{"email": "admin@example.org", "password": password}, 401, nil)
	c.do("POST", "/api/auth/login", map[string]any{"email": "admin@example.org", "password": newPassword}, 200, &me)
	if me.Member.ID != ownerID {
		t.Fatal("密码修改后的登录返回了其他成员")
	}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreateFormFile("file", "内容.txt")
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("真实附件内容")
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req, err = http.NewRequest("POST", c.base+"/api/mail/uploads", &buffer)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Requested-With", "Mailhearth")
	var uploads []struct {
		ID   string `json:"id"`
		Size int64  `json:"size"`
	}
	c.request(req, 200, &uploads)
	if len(uploads) != 1 || uploads[0].ID == "" || uploads[0].Size != int64(len(content)) {
		t.Fatal("附件上传没有返回实际文件信息")
	}
	saved, err := os.ReadFile(filepath.Join(root, "uploads", uploads[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(saved, content) {
		t.Fatal("上传文件内容不一致")
	}
	var uploadOwner int64
	if err := database.QueryRow(`SELECT member_id FROM uploads WHERE id=?`, uploads[0].ID).Scan(&uploadOwner); err != nil {
		t.Fatal(err)
	}
	if uploadOwner != ownerID {
		t.Fatal("上传文件没有关联当前成员")
	}
	c.do("DELETE", "/api/mail/uploads/"+uploads[0].ID, nil, 200, nil)
	if _, err := os.Stat(filepath.Join(root, "uploads", uploads[0].ID)); !os.IsNotExist(err) {
		t.Fatalf("附件删除后文件仍然存在：%v", err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM uploads WHERE id=?`, uploads[0].ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("附件删除后数据库仍然保留记录")
	}
	org, err := svc.Org(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := database.Exec(`INSERT INTO mailboxes(org_id,connection_id,kind,address,address_key,owner_member_id,management_mode,remote_state,created_at,updated_at) VALUES (?,?,'personal','private@example.org','private@example.org',?,'external','external',?,?)`, org.ID, connection.ID, ownerID, db.Now(), db.Now())
	if err != nil {
		t.Fatal(err)
	}
	mailboxID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	member.do("GET", "/api/mail/mailboxes/"+strconv.FormatInt(mailboxID, 10)+"/folders", nil, 403, nil)
	member.do("POST", "/api/auth/logout", nil, 200, nil)
	member.do("GET", "/api/auth/me", nil, 401, nil)
	member.do("POST", "/api/auth/login", map[string]any{"email": "member@example.org", "password": memberPassword}, 200, &me)
	if me.Member.ID != created.MemberID {
		t.Fatal("成员重新登录没有恢复对应会话")
	}
}
