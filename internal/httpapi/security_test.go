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
