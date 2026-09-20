package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mailhearth/internal/config"
	"mailhearth/internal/core"
	"mailhearth/internal/db"
	"mailhearth/internal/devstack"
	"mailhearth/internal/httpapi"
	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/purelymail"
	"mailhearth/internal/purelymail/fake"
	"mailhearth/internal/secrets"
)

type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func (c *client) do(method, path string, body any, out any) (int, []byte) {
	c.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rdr)
	req.Header.Set("X-Requested-With", "Mailhearth")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("decode %s %s: %v: %s", method, path, err, raw)
		}
	}
	return resp.StatusCode, raw
}

func (c *client) must(method, path string, body any, out any) []byte {
	c.t.Helper()
	st, raw := c.do(method, path, body, out)
	if st >= 300 {
		c.t.Fatalf("%s %s -> %d: %s", method, path, st, raw)
	}
	return raw
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: base, http: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
}

func TestEndToEnd(t *testing.T) {
	stack, err := devstack.Start(slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	stack.API.AddDomain(fake.Domain{Name: "acme.test", MX: true, SPF: true, DKIM: true, DMARC: true})
	stack.SeedUser("alice@acme.test", "alice-pass")
	stack.SeedUser("support@acme.test", "support-pass")
	for i := 0; i < 3; i++ {
		stack.Deliver("alice@acme.test", "INBOX", devstack.SampleMessage("Dana <dana@ext.example>", "alice@acme.test", fmt.Sprintf("Hello %d", i), "body "+fmt.Sprint(i), "<p>html <b>"+fmt.Sprint(i)+"</b></p>", time.Now().Add(time.Duration(i)*time.Minute)))
	}
	stack.Deliver("support@acme.test", "INBOX", devstack.SampleMessage("Customer <c@ext.example>", "support@acme.test", "Need help", "please help", "", time.Now()))

	dir := t.TempDir()
	cfg := &config.Config{
		DataDir: dir, PurelymailAPIURL: stack.APIURL, IMAPAddr: stack.IMAPAddr, IMAPTLS: config.TLSNone,
		SMTPAddr: stack.SMTPAddr, SMTPTLS: config.TLSNone, SieveAddr: "", IMAPMaxConns: 8, IMAPIdleTimeout: time.Minute,
		MaxUploadBytes: 5 << 20, MaxMessageBytes: 10 << 20, SessionTTL: time.Hour, InviteTTL: time.Hour,
	}
	database, err := db.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	master := make([]byte, 32)
	box, _ := secrets.NewBox(master, "credentials")
	pool := imappool.New(imappool.Config{Addr: cfg.IMAPAddr, TLSMode: imappool.TLSNone, MaxConns: 8})
	defer pool.Close()
	svc := core.New(database, cfg, box, pool, slog.Default())
	svc.NewPM = func(token string) purelymail.API { return purelymail.New(stack.APIURL, token) }
	api := httpapi.New(cfg, svc, pool, nil, []byte("sign-key"), slog.Default())
	srv := httptest.NewServer(api.Handler())
	defer srv.Close()

	c := newClient(t, srv.URL)

	// CSRF: state changes without the header are rejected.
	req, _ := http.NewRequest("POST", srv.URL+"/api/setup/init", strings.NewReader("{}"))
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("csrf: %d", resp.StatusCode)
	}

	var st core.SetupStatus
	c.must("GET", "/api/setup/status", nil, &st)
	if !st.NeedsSetup || st.Step != "org" {
		t.Fatalf("status: %+v", st)
	}
	var me struct {
		Member      map[string]any `json:"member"`
		Permissions []string       `json:"permissions"`
	}
	c.must("POST", "/api/setup/init", map[string]any{"orgName": "Acme", "adminName": "Alice", "adminEmail": "alice@acme.test", "password": "correct-horse-battery"}, &me)
	if len(me.Permissions) == 0 {
		t.Fatalf("owner should have permissions: %+v", me)
	}
	if code, raw := c.do("POST", "/api/setup/connect", map[string]any{"apiToken": "bad"}, nil); code != 400 {
		t.Fatalf("bad token: %d %s", code, raw)
	}
	var disc core.Discovery
	c.must("POST", "/api/setup/connect", map[string]any{"apiToken": stack.Token}, &disc)
	if len(disc.Users) != 2 {
		t.Fatalf("discovery: %+v", disc)
	}
	var imp core.ImportResult
	c.must("POST", "/api/setup/complete", map[string]any{"bindMailbox": "alice@acme.test"}, &imp)
	if imp.MailboxesNew != 2 {
		t.Fatalf("import: %+v", imp)
	}

	// Mail: mailboxes, folders, list, read, flags, reply, drafts, search.
	var mbs []core.AccessibleMailbox
	c.must("GET", "/api/mail/mailboxes", nil, &mbs)
	if len(mbs) != 1 || mbs[0].Address != "alice@acme.test" || len(mbs[0].Identities) != 1 {
		t.Fatalf("mailboxes: %+v", mbs)
	}
	mbID := mbs[0].ID
	base := fmt.Sprintf("/api/mail/mailboxes/%d", mbID)
	var folders []map[string]any
	c.must("GET", base+"/folders", nil, &folders)
	if len(folders) < 5 || folders[0]["role"] != "inbox" || folders[0]["unseen"].(float64) != 3 {
		t.Fatalf("folders: %+v", folders)
	}
	var list struct {
		Total    float64          `json:"total"`
		Messages []map[string]any `json:"messages"`
		Team     map[string]any   `json:"team"`
	}
	c.must("GET", base+"/messages?folder=INBOX&size=10", nil, &list)
	if list.Total != 3 || len(list.Messages) != 3 || list.Messages[0]["subject"] != "Hello 2" {
		t.Fatalf("list: %+v", list)
	}
	uid := int(list.Messages[0]["uid"].(float64))
	var full struct {
		Message struct {
			HTML      string `json:"html"`
			Text      string `json:"text"`
			Seen      bool   `json:"seen"`
			MessageID string `json:"messageId"`
		} `json:"message"`
		ViewURL string `json:"viewUrl"`
		Key     string `json:"key"`
	}
	c.must("GET", fmt.Sprintf("%s/messages/%d?folder=INBOX", base, uid), nil, &full)
	if !strings.Contains(full.Message.HTML, "<b>2</b>") || !full.Message.Seen || full.Key == "" {
		t.Fatalf("message: %+v", full)
	}
	// Sandboxed view works without cookies via the signed token.
	noCookie := &http.Client{}
	vresp, err := noCookie.Get(srv.URL + full.ViewURL)
	if err != nil {
		t.Fatal(err)
	}
	vbody, _ := io.ReadAll(vresp.Body)
	if vresp.StatusCode != 200 || !strings.Contains(string(vbody), "<b>2</b>") || !strings.Contains(vresp.Header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("view: %d %s csp=%q", vresp.StatusCode, vbody, vresp.Header.Get("Content-Security-Policy"))
	}
	if r2, _ := noCookie.Get(srv.URL + strings.Replace(full.ViewURL, "&t=", "&t=x", 1)); r2.StatusCode != 401 {
		t.Fatalf("tampered token should be rejected: %d", r2.StatusCode)
	}
	c.must("GET", base+"/folders", nil, &folders)
	if folders[0]["unseen"].(float64) != 2 {
		t.Fatalf("unseen after read: %v", folders[0]["unseen"])
	}
	c.must("POST", base+"/messages/flags", map[string]any{"folder": "INBOX", "uids": []int{uid}, "add": []string{`\Flagged`}}, nil)
	if code, _ := c.do("POST", base+"/messages/flags", map[string]any{"folder": "INBOX", "uids": []int{uid}, "add": []string{`\Recent`}}, nil); code != 400 {
		t.Fatalf("disallowed flag accepted: %d", code)
	}
	c.must("GET", base+"/messages?folder=INBOX&q=is:flagged", nil, &list)
	if list.Total != 1 {
		t.Fatalf("search flagged: %+v", list)
	}

	// Upload + draft + send with reply context.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "notes.txt")
	fw.Write([]byte("hello notes"))
	mw.Close()
	upReq, _ := http.NewRequest("POST", srv.URL+"/api/mail/uploads", &buf)
	upReq.Header.Set("Content-Type", mw.FormDataContentType())
	upReq.Header.Set("X-Requested-With", "Mailhearth")
	upResp, err := c.http.Do(upReq)
	if err != nil {
		t.Fatal(err)
	}
	var ups []map[string]any
	json.NewDecoder(upResp.Body).Decode(&ups)
	if upResp.StatusCode != 200 || len(ups) != 1 {
		t.Fatalf("upload: %d %+v", upResp.StatusCode, ups)
	}
	var draft struct {
		Folder string `json:"folder"`
		UID    int    `json:"uid"`
	}
	c.must("POST", base+"/drafts", map[string]any{"to": []map[string]string{{"address": "support@acme.test"}}, "subject": "Draft one", "text": "wip"}, &draft)
	if draft.Folder != "Drafts" || draft.UID == 0 {
		t.Fatalf("draft: %+v", draft)
	}
	var sent map[string]any
	c.must("POST", base+"/send", map[string]any{
		"to": []map[string]string{{"name": "Support", "address": "support@acme.test"}}, "subject": "Re: Hello 2", "text": "reply body", "html": "<p>reply <i>body</i></p>",
		"attachmentIds": []string{ups[0]["id"].(string)},
		"inReplyTo":     map[string]any{"folder": "INBOX", "uid": uid, "messageId": full.Message.MessageID},
		"draft":         map[string]any{"folder": draft.Folder, "uid": draft.UID},
	}, &sent)
	if sent["ok"] != true || sent["sentFolder"] != "Sent" || sent["key"] != full.Key {
		t.Fatalf("send: %+v", sent)
	}
	c.must("GET", base+"/messages?folder=Drafts", nil, &list)
	if list.Total != 0 {
		t.Fatalf("draft should be removed after send: %+v", list)
	}
	c.must("GET", base+"/messages?folder=INBOX&size=10", nil, &list)
	if list.Messages[0]["answered"] != true {
		t.Fatalf("original should be marked answered: %+v", list.Messages[0])
	}
	teamState := list.Team[full.Key].(map[string]any)
	if acts := teamState["activity"].([]any); len(acts) != 1 || acts[0].(map[string]any)["action"] != "replied" || acts[0].(map[string]any)["memberName"] != "Alice" {
		t.Fatalf("activity: %+v", teamState)
	}
	c.must("GET", base+"/messages?folder=Sent", nil, &list)
	if list.Total != 1 || list.Messages[0]["hasAttachment"] != true {
		t.Fatalf("sent copy: %+v", list)
	}

	// Admin: create shared mailbox access for a new member, then act as them.
	var admMbs []map[string]any
	c.must("GET", "/api/admin/mailboxes", nil, &admMbs)
	var supportID int
	for _, m := range admMbs {
		if m["address"] == "support@acme.test" {
			supportID = int(m["id"].(float64))
		}
	}
	c.must("PATCH", fmt.Sprintf("/api/admin/mailboxes/%d", supportID), map[string]any{"kind": "shared", "displayName": "Support"}, nil)
	c.must("POST", fmt.Sprintf("/api/admin/mailboxes/%d/connect", supportID), nil, nil)
	var created struct {
		Member     map[string]any `json:"member"`
		InviteLink string         `json:"inviteLink"`
	}
	c.must("POST", "/api/admin/members", map[string]any{"displayName": "Bob", "loginEmail": "bob@acme.test", "sharedMailboxes": []int{supportID}}, &created)
	if created.InviteLink == "" {
		t.Fatalf("invite link missing: %+v", created)
	}
	token := created.InviteLink[strings.LastIndex(created.InviteLink, "/")+1:]
	bob := newClient(t, srv.URL)
	bob.must("GET", "/api/auth/invite/"+token, nil, nil)
	bob.must("POST", "/api/auth/invite/"+token+"/accept", map[string]any{"password": "bobs-strong-password"}, nil)
	var bobMe struct {
		Mailboxes []core.AccessibleMailbox `json:"mailboxes"`
	}
	bob.must("GET", "/api/auth/me", nil, &bobMe)
	if len(bobMe.Mailboxes) != 1 || bobMe.Mailboxes[0].Address != "support@acme.test" || bobMe.Mailboxes[0].Level != "full" {
		t.Fatalf("bob mailboxes: %+v", bobMe.Mailboxes)
	}
	if code, _ := bob.do("GET", "/api/admin/members", nil, nil); code != 403 {
		t.Fatalf("member must not access admin: %d", code)
	}
	if code, _ := bob.do("GET", base+"/folders", nil, nil); code != 403 {
		t.Fatalf("bob must not read alice's mailbox: %d", code)
	}
	sbase := fmt.Sprintf("/api/mail/mailboxes/%d", supportID)
	list = struct {
		Total    float64          `json:"total"`
		Messages []map[string]any `json:"messages"`
		Team     map[string]any   `json:"team"`
	}{}
	bob.must("GET", sbase+"/messages", nil, &list)
	// "Need help" plus the reply Alice sent (the dev SMTP delivers locally).
	if list.Total != 2 {
		t.Fatalf("support inbox: %+v", list)
	}
	if _, stale := list.Team[full.Key]; stale {
		t.Fatalf("activity from another mailbox leaked into support listing: %+v", list.Team)
	}
	var suid int
	for _, m := range list.Messages {
		if m["subject"] == "Need help" {
			suid = int(m["uid"].(float64))
		}
	}
	if suid == 0 {
		t.Fatalf("customer message missing: %+v", list.Messages)
	}
	bob.must("GET", fmt.Sprintf("%s/messages/%d", sbase, suid), nil, &full)
	var teamOut map[string]any
	bob.must("POST", sbase+"/team", map[string]any{"key": full.Key, "action": "assign", "memberId": created.Member["id"]}, &teamOut)
	bob.must("POST", sbase+"/team", map[string]any{"key": full.Key, "action": "note", "text": "Customer called back"}, &teamOut)
	if teamOut["assigneeName"] != "Bob" || len(teamOut["activity"].([]any)) != 2 {
		t.Fatalf("team: %+v", teamOut)
	}

	// Logout ends the session.
	c.must("POST", "/api/auth/logout", nil, nil)
	if code, _ := c.do("GET", "/api/auth/me", nil, nil); code != 401 {
		t.Fatalf("after logout: %d", code)
	}
	if code, _ := c.do("POST", "/api/auth/login", map[string]any{"email": "alice@acme.test", "password": "wrong"}, nil); code != 401 {
		t.Fatalf("bad login: %d", code)
	}
	c.must("POST", "/api/auth/login", map[string]any{"email": "alice@acme.test", "password": "correct-horse-battery"}, nil)
	var ov core.Overview
	c.must("GET", "/api/admin/overview", nil, &ov)
	if ov.Org.Name != "Acme" || ov.Members["active"] != 2 {
		t.Fatalf("overview: %+v", ov)
	}
	_ = url.QueryEscape
}
