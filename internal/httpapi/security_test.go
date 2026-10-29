package httpapi_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
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

// testEnv is a running API server backed by a fresh database.
type testEnv struct {
	t     *testing.T
	root  string
	db    *db.DB
	svc   *core.Service
	base  string
	owner *client
	// ownerID is the member created by setup.
	ownerID int64
}

func newTestEnv(t *testing.T, mutate ...func(*config.Config)) *testEnv {
	t.Helper()
	root := t.TempDir()
	database, err := db.Open(filepath.Join(root, "mailhearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	master := make([]byte, 32)
	if _, err := rand.Read(master); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.NewBox(master, "credentials")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{DataDir: root, MaxUploadBytes: 1 << 20, MaxMessageBytes: 4 << 20, SessionTTL: time.Hour, InviteTTL: time.Hour}
	for _, fn := range mutate {
		fn(cfg)
	}
	svc := core.New(database, cfg, box, nil, slog.Default())
	ctx, stop := context.WithCancel(context.Background())
	if err := svc.StartOperations(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stop(); svc.WaitOperations() })
	api := httpapi.New(cfg, svc, nil, web.Handler(), master, slog.Default())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	env := &testEnv{t: t, root: root, db: database, svc: svc, base: "http://" + listener.Addr().String()}
	env.owner = newClient(t, env.base)
	var me struct {
		Member model.Member `json:"member"`
	}
	env.owner.do("POST", "/api/setup/init", core.InitRequest{OrgName: "安全测试", AdminName: "所有者", AdminEmail: "owner@example.org", Password: "owner-password-1"}, 200, &me)
	env.ownerID = me.Member.ID
	return env
}

// roleID returns the id of the role with key.
func (e *testEnv) roleID(key string) int64 {
	e.t.Helper()
	var roles []model.Role
	e.owner.do("GET", "/api/admin/roles", nil, 200, &roles)
	for _, r := range roles {
		if r.Key == key {
			return r.ID
		}
	}
	e.t.Fatalf("角色 %s 不存在", key)
	return 0
}

// addMember creates a member with roleID through the owner and returns a
// signed-in client for it plus its id.
func (e *testEnv) addMember(email string, roleID int64) (*client, int64) {
	e.t.Helper()
	var operation core.OperationView
	e.owner.do("POST", "/api/admin/members", map[string]any{"requestId": uuid.NewString(), "displayName": email, "loginEmail": email, "roleId": roleID, "mailboxAction": "none", "sendInvite": true}, 202, &operation)
	operation = e.owner.await(operation.ID)
	var created struct {
		MemberID   int64  `json:"memberId"`
		InviteLink string `json:"inviteLink"`
	}
	if err := json.Unmarshal(operation.Result, &created); err != nil {
		e.t.Fatal(err)
	}
	token := created.InviteLink[strings.LastIndex(created.InviteLink, "/")+1:]
	c := newClient(e.t, e.base)
	c.do("POST", "/api/auth/invite/"+token+"/accept", map[string]any{"password": email + "-password"}, 200, nil)
	return c, created.MemberID
}

func (e *testEnv) member(c *client, id int64) model.Member {
	e.t.Helper()
	var out struct {
		Member model.Member `json:"member"`
	}
	c.do("GET", "/api/admin/members/"+strconv.FormatInt(id, 10), nil, 200, &out)
	return out.Member
}

func upload(c *client, name string, content []byte, status int) []byte {
	c.t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		c.t.Fatal(err)
	}
	part.Write(content)
	writer.Close()
	req, err := http.NewRequest("POST", c.base+"/api/mail/uploads", &buffer)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Requested-With", "Mailhearth")
	return c.request(req, status, nil)
}

// #1: the upload id is a path segment; encoded slashes must never reach
// the filesystem and other members' uploads must stay untouched.
func TestUploadDeleteCannotEscapeUploadsDirectory(t *testing.T) {
	env := newTestEnv(t)
	victim := filepath.Join(env.root, "victim.key")
	if err := os.WriteFile(victim, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	member, _ := env.addMember("member@example.org", env.roleID(model.RoleMember))
	for _, id := range []string{"..%2Fvictim.key", "..%2F..%2F" + filepath.Base(env.root) + "%2Fvictim.key", "%2E%2E%2Fvictim.key"} {
		member.do("DELETE", "/api/mail/uploads/"+id, nil, 404, nil)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("路径穿越删除了上传目录之外的文件：%v", err)
	}
	if _, err := os.Stat(filepath.Join(env.root, "mailhearth.db")); err != nil {
		t.Fatalf("数据库文件被删除：%v", err)
	}
	var uploads []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(upload(env.owner, "a.txt", []byte("owner file"), 200), &uploads); err != nil {
		t.Fatal(err)
	}
	member.do("DELETE", "/api/mail/uploads/"+uploads[0].ID, nil, 200, nil)
	if _, err := os.Stat(filepath.Join(env.root, "uploads", uploads[0].ID)); err != nil {
		t.Fatalf("其他成员删除了所有者的上传文件：%v", err)
	}
	env.owner.do("DELETE", "/api/mail/uploads/"+uploads[0].ID, nil, 200, nil)
	if _, err := os.Stat(filepath.Join(env.root, "uploads", uploads[0].ID)); !os.IsNotExist(err) {
		t.Fatalf("所有者无法删除自己的上传文件：%v", err)
	}
}

// #2: administrators must not be able to take over the owner account, and
// partial managers must not act on (or create) members above themselves.
func TestMemberManagementCannotEscalate(t *testing.T) {
	env := newTestEnv(t)
	admin, adminID := env.addMember("admin@example.org", env.roleID(model.RoleAdmin))
	owner := strconv.FormatInt(env.ownerID, 10)
	admin.do("POST", "/api/admin/members/"+owner+"/password", map[string]any{"password": "taken-over-password"}, 403, nil)
	admin.do("POST", "/api/admin/members/"+owner+"/invite", nil, 403, nil)
	ownerMember := env.member(admin, env.ownerID)
	admin.do("PATCH", "/api/admin/members/"+owner, map[string]any{"expectedRevision": ownerMember.Revision, "loginEmail": "attacker@example.org"}, 403, nil)
	newClient(t, env.base).do("POST", "/api/auth/login", map[string]any{"email": "owner@example.org", "password": "owner-password-1"}, 200, nil)

	var role model.Role
	env.owner.do("POST", "/api/admin/roles", map[string]any{"name": "People", "permissions": []string{model.PermMembersManage}}, 200, &role)
	manager, _ := env.addMember("manager@example.org", role.ID)
	manager.do("POST", "/api/admin/members/"+strconv.FormatInt(adminID, 10)+"/password", map[string]any{"password": "taken-over-password"}, 403, nil)
	plain, plainID := env.addMember("plain@example.org", env.roleID(model.RoleMember))
	_ = plain
	plainMember := env.member(manager, plainID)
	manager.do("PATCH", "/api/admin/members/"+strconv.FormatInt(plainID, 10), map[string]any{"expectedRevision": plainMember.Revision, "roleId": env.roleID(model.RoleAdmin)}, 403, nil)
	manager.do("PATCH", "/api/admin/members/"+strconv.FormatInt(plainID, 10), map[string]any{"expectedRevision": plainMember.Revision, "title": "Engineer"}, 200, nil)
	manager.do("POST", "/api/admin/members/"+strconv.FormatInt(plainID, 10)+"/password", map[string]any{"password": "reset-by-manager"}, 200, nil)
	manager.do("POST", "/api/admin/members", map[string]any{"requestId": uuid.NewString(), "displayName": "x", "loginEmail": "x@example.org", "roleId": env.roleID(model.RoleAdmin), "mailboxAction": "none"}, 403, nil)

	var orgRole model.Role
	env.owner.do("POST", "/api/admin/roles", map[string]any{"name": "Org", "permissions": []string{model.PermOrgManage}}, 200, &orgRole)
	orgManager, _ := env.addMember("org@example.org", orgRole.ID)
	orgManager.do("POST", "/api/admin/roles", map[string]any{"name": "Wider", "permissions": []string{model.PermOrgManage, model.PermMembersManage}}, 403, nil)
	orgManager.do("PATCH", "/api/admin/roles/"+strconv.FormatInt(orgRole.ID, 10), map[string]any{"name": "Org", "permissions": []string{model.PermOrgManage, model.PermAuditRead}}, 403, nil)
}

// #7: changing one's own password signs out every other session but keeps
// the session that made the change.
func TestOwnPasswordChangeRevokesOtherSessions(t *testing.T) {
	env := newTestEnv(t)
	other := newClient(t, env.base)
	other.do("POST", "/api/auth/login", map[string]any{"email": "owner@example.org", "password": "owner-password-1"}, 200, nil)
	other.do("GET", "/api/auth/me", nil, 200, nil)
	env.owner.do("POST", "/api/auth/password", map[string]any{"current": "owner-password-1", "new": "owner-password-2"}, 200, nil)
	env.owner.do("GET", "/api/auth/me", nil, 200, nil)
	other.do("GET", "/api/auth/me", nil, 401, nil)
}

// #3: forwarded headers from an untrusted peer are ignored, and one account
// cannot be guessed at faster by rotating source addresses.
func TestLoginRateLimitPerAccount(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) { c.TrustProxyHeader = true })
	c := newClient(t, env.base)
	limited := false
	for i := 0; i < 40 && !limited; i++ {
		req, err := http.NewRequest("POST", env.base+"/api/auth/login", strings.NewReader(`{"email":"owner@example.org","password":"wrong-password"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Requested-With", "Mailhearth")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "198.51.100."+strconv.Itoa(i))
		resp, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		limited = resp.StatusCode == http.StatusTooManyRequests
	}
	if !limited {
		t.Fatal("轮换 X-Forwarded-For 绕过了登录限流")
	}
}

// #6: pending uploads are capped per member.
func TestUploadQuota(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) { c.MaxUploadBytes = 1 << 20; c.UploadQuotaBytes = 2 << 20 })
	chunk := bytes.Repeat([]byte("x"), 900<<10)
	upload(env.owner, "a.bin", chunk, 200)
	upload(env.owner, "b.bin", chunk, 200)
	upload(env.owner, "c.bin", chunk, 400)
	var count int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM uploads`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(env.root, "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || len(entries) != 2 {
		t.Fatalf("超出配额的上传被保存：数据库 %d 条，磁盘 %d 个文件", count, len(entries))
	}
}

// #12: role × route matrix for the management API. Members get 403
// everywhere; administrators reach everything but owner-only actions.
func TestManagementPermissionMatrix(t *testing.T) {
	env := newTestEnv(t)
	member, _ := env.addMember("member@example.org", env.roleID(model.RoleMember))
	admin, _ := env.addMember("admin@example.org", env.roleID(model.RoleAdmin))
	routes := []struct {
		method, path string
		body         any
		ownerOnly    bool
	}{
		{"GET", "/api/admin/overview", nil, false},
		{"GET", "/api/admin/members", nil, false},
		{"GET", "/api/admin/audit", nil, false},
		{"POST", "/api/admin/roles", map[string]any{"name": "Matrix", "permissions": []string{}}, false},
		{"POST", "/api/admin/connections", map[string]any{"providerKind": "manual", "label": "matrix"}, false},
		{"PATCH", "/api/admin/org", map[string]any{"name": "Matrix org"}, false},
		{"POST", "/api/admin/org/transfer", map[string]any{"memberId": env.ownerID}, true},
		{"POST", "/api/admin/members/" + strconv.FormatInt(env.ownerID, 10) + "/password", map[string]any{"password": "matrix-password"}, true},
	}
	status := func(c *client, method, path string, body any) int {
		data, _ := json.Marshal(body)
		req, err := http.NewRequest(method, env.base+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Requested-With", "Mailhearth")
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for _, route := range routes {
		body := route.body
		if got := status(member, route.method, route.path, body); got != http.StatusForbidden {
			t.Errorf("member %s %s 返回 %d，要求 403", route.method, route.path, got)
		}
		got := status(admin, route.method, route.path, body)
		if route.ownerOnly && got != http.StatusForbidden {
			t.Errorf("admin %s %s 返回 %d，要求 403", route.method, route.path, got)
		}
		if !route.ownerOnly && (got == http.StatusForbidden || got == http.StatusUnauthorized) {
			t.Errorf("admin %s %s 返回 %d，要求获得授权", route.method, route.path, got)
		}
	}
}

// #23: assignees must be able to act on the mailbox, keys and notes are
// bounded, and read-only access cannot assign or resolve.
func TestTeamCollaborationValidation(t *testing.T) {
	env := newTestEnv(t)
	reader, readerID := env.addMember("reader@example.org", env.roleID(model.RoleMember))
	_, outsiderID := env.addMember("outsider@example.org", env.roleID(model.RoleMember))
	var connection core.ConnectionView
	env.owner.do("POST", "/api/admin/connections", map[string]any{"providerKind": "manual", "label": "team"}, 201, &connection)
	org, err := env.svc.Org(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	res, err := env.db.Exec(`INSERT INTO mailboxes(org_id,connection_id,kind,address,address_key,owner_member_id,management_mode,remote_state,created_at,updated_at) VALUES (?,?,'personal','team@example.org','team@example.org',?,'external','external',?,?)`, org.ID, connection.ID, env.ownerID, db.Now(), db.Now())
	if err != nil {
		t.Fatal(err)
	}
	mailboxID, _ := res.LastInsertId()
	if _, err := env.db.Exec(`INSERT INTO mailbox_access(mailbox_id,member_id,level,granted_at) VALUES (?,?,'read',?)`, mailboxID, readerID, db.Now()); err != nil {
		t.Fatal(err)
	}
	path := "/api/mail/mailboxes/" + strconv.FormatInt(mailboxID, 10) + "/team"
	env.owner.do("POST", path, map[string]any{"key": "k1", "action": "assign", "memberId": outsiderID}, 400, nil)
	env.owner.do("POST", path, map[string]any{"key": "k1", "action": "assign", "memberId": readerID}, 200, nil)
	env.owner.do("POST", path, map[string]any{"key": strings.Repeat("k", core.MaxMessageKeyLength+1), "action": "status", "status": "resolved"}, 400, nil)
	env.owner.do("POST", path, map[string]any{"key": "k1", "action": "note", "text": strings.Repeat("n", core.MaxNoteLength+1)}, 400, nil)
	reader.do("POST", path, map[string]any{"key": "k1", "action": "status", "status": "resolved"}, 403, nil)
	reader.do("POST", path, map[string]any{"key": "k1", "action": "assign", "memberId": readerID}, 403, nil)
	reader.do("POST", path, map[string]any{"key": "k1", "action": "note", "text": "seen"}, 200, nil)
}

// #18: activity extends a session, but never past its absolute lifetime.
func TestSessionAbsoluteLifetime(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) { c.SessionTTL = 24 * time.Hour; c.SessionMaxAge = 72 * time.Hour })
	env.owner.do("GET", "/api/auth/me", nil, 200, nil)
	old := time.Now().UTC().Add(-71 * time.Hour)
	if _, err := env.db.Exec(`UPDATE sessions SET created_at=?, last_seen_at=?`, old.Format(time.RFC3339), old.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	env.owner.do("GET", "/api/auth/me", nil, 200, nil)
	var expires string
	if err := env.db.QueryRow(`SELECT expires_at FROM sessions`).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if limit := old.Add(72 * time.Hour); db.ParseTime(expires).After(limit) {
		t.Fatalf("续期越过了绝对有效期：%s > %s", expires, limit.Format(time.RFC3339))
	}
	if _, err := env.db.Exec(`UPDATE sessions SET created_at=?`, time.Now().UTC().Add(-73*time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	env.owner.do("GET", "/api/auth/me", nil, 401, nil)
	if n, err := env.svc.PurgeExpiredSessions(context.Background()); err != nil || n != 0 {
		t.Fatalf("过期会话应已在查询时删除：%d %v", n, err)
	}
}
