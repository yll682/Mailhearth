package integration

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"time"

	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/mailproto/mailops"
	"mailhearth/internal/mailproto/sieve"
	"mailhearth/internal/model"
)

// pollInterval is how often the suite re-checks a folder while waiting for
// delivery. Purelymail delivers in a second or two; the generous overall
// deadline exists for greylisting and spam scanning.
const pollInterval = 3 * time.Second

// withConn borrows a pooled IMAP connection, runs fn and returns the
// connection immediately. Holding connections across a polling loop would
// exhaust the pool, so every helper in this file goes through here.
func (h *Harness) withConn(ctx context.Context, cred imappool.Cred, fn func(*imappool.Conn)) {
	h.T.Helper()
	c, err := h.Pool.Get(ctx, cred)
	if err != nil {
		h.T.Fatalf("imap connect as %s: %v", cred.User, err)
	}
	defer h.Pool.Put(c)
	fn(c)
}

// dialFresh opens a brand-new IMAP connection outside the shared pool, so
// that a cached session under an older password cannot mask a credential
// that the provider has since revoked.
func (h *Harness) dialFresh(ctx context.Context, cred imappool.Cred) error {
	pool := imappool.New(imappool.Config{
		Addr: h.Cfg.IMAPAddr, TLSMode: string(h.Cfg.IMAPTLS),
		MaxConns: 1, PerCredIdle: 0, IdleTimeout: 30 * time.Second, Logger: h.Log,
	})
	defer pool.Close()
	c, err := pool.Get(ctx, cred)
	if err != nil {
		return err
	}
	pool.Put(c)
	return nil
}

// RequireLogin asserts that the credential can open the mailbox over IMAP.
func (h *Harness) RequireLogin(ctx context.Context, cred imappool.Cred) {
	h.T.Helper()
	if err := h.dialFresh(ctx, cred); err != nil {
		h.T.Fatalf("IMAP rejected %s: %v", cred.User, err)
	}
}

// LoginSucceeds asserts that a user/password pair authenticates. Used for
// the human-facing password handed out for external mail clients.
func (h *Harness) LoginSucceeds(ctx context.Context, user, pass string) {
	h.T.Helper()
	h.RequireLogin(ctx, imappool.Cred{User: user, Pass: pass})
}

// LoginFails asserts that a credential is rejected by IMAP. Departed staff
// and rotated app passwords must fail here.
func (h *Harness) LoginFails(ctx context.Context, cred imappool.Cred, why string) {
	h.T.Helper()
	err := h.dialFresh(ctx, cred)
	if err == nil {
		h.T.Fatalf("IMAP still accepts %s (%s)", cred.User, why)
	}
	var authErr *imappool.AuthError
	if !errors.As(err, &authErr) {
		h.T.Fatalf("IMAP rejected %s (%s) but not as an authentication failure: %v", cred.User, why, err)
	}
}

// Folders lists the folders of a mailbox.
func (h *Harness) Folders(ctx context.Context, cred imappool.Cred) []mailops.Folder {
	h.T.Helper()
	var (
		folders []mailops.Folder
		err     error
	)
	h.withConn(ctx, cred, func(c *imappool.Conn) {
		folders, err = mailops.ListFolders(ctx, c)
	})
	if err != nil {
		h.T.Fatalf("list folders as %s: %v", cred.User, err)
	}
	return folders
}

// SpecialFolder resolves a role (inbox, sent, trash, junk, archive, drafts)
// to the server's folder name.
func (h *Harness) SpecialFolder(ctx context.Context, cred imappool.Cred, role string) string {
	h.T.Helper()
	special := mailops.SpecialFolders(h.Folders(ctx, cred))
	name := special[role]
	if name == "" {
		h.T.Fatalf("mailbox %s has no %s folder", cred.User, role)
	}
	return name
}

// CreateFolder creates a folder and removes it again after the test.
func (h *Harness) CreateFolder(ctx context.Context, cred imappool.Cred, name string) {
	h.T.Helper()
	var err error
	h.withConn(ctx, cred, func(c *imappool.Conn) {
		err = mailops.CreateFolder(ctx, c, name)
	})
	if err != nil {
		h.T.Fatalf("create folder %s in %s: %v", name, cred.User, err)
	}
	h.T.Cleanup(func() {
		if h.Env.Keep {
			return
		}
		ctx, cancel := h.Ctx(60 * time.Second)
		defer cancel()
		h.withConn(ctx, cred, func(c *imappool.Conn) {
			if err := mailops.DeleteFolder(ctx, c, name); err != nil {
				h.T.Logf("cleanup: delete folder %s: %v", name, err)
			}
		})
	})
}

// draft builds a message from the mailbox to the given recipients.
func (h *Harness) draft(cred imappool.Cred, to []string, subject, body, messageID string) *mailops.Draft {
	_, domain := splitAddr(cred.User)
	if messageID == "" {
		messageID = mailops.NewMessageID(domain)
	}
	d := &mailops.Draft{
		From:      mailops.Recipient{Name: "Integration", Address: cred.User},
		Subject:   subject,
		Text:      body,
		Date:      time.Now(),
		MessageID: messageID,
	}
	for _, addr := range to {
		d.To = append(d.To, mailops.Recipient{Address: addr})
	}
	return d
}

// Send submits a message through Mailhearth's SMTP path using the mailbox
// credential, and returns the Message-ID it minted.
func (h *Harness) Send(ctx context.Context, cred imappool.Cred, to []string, subject, body string) string {
	h.T.Helper()
	d := h.draft(cred, to, subject, body, "")
	raw, err := mailops.Build(d)
	if err != nil {
		h.T.Fatalf("build message: %v", err)
	}
	h.SendRaw(ctx, cred, to, raw)
	return d.MessageID
}

// SendRaw submits an already-built message, for tests that need exact
// headers.
func (h *Harness) SendRaw(ctx context.Context, cred imappool.Cred, to []string, raw []byte) {
	h.T.Helper()
	cfg := mailops.SMTPConfig{
		Addr:    h.Cfg.SMTPAddr,
		TLSMode: string(h.Cfg.SMTPTLS),
		Timeout: 60 * time.Second,
	}
	if err := mailops.Send(ctx, cfg, cred.User, cred.Pass, cred.User, to, raw); err != nil {
		h.T.Fatalf("smtp send from %s to %v: %v", cred.User, to, err)
	}
}

// AppendToSent stores a copy of a sent message the way the compose path
// does, and returns the UID the server assigned (0 when it reports none).
func (h *Harness) AppendToSent(ctx context.Context, cred imappool.Cred, sent, messageID, subject string) uint32 {
	h.T.Helper()
	raw, err := mailops.Build(h.draft(cred, []string{cred.User}, subject, "Sent copy.", messageID))
	if err != nil {
		h.T.Fatalf("build sent copy: %v", err)
	}
	var uid uint32
	h.withConn(ctx, cred, func(c *imappool.Conn) {
		uid, err = mailops.Append(ctx, c, sent, []string{"\\Seen"}, time.Now(), raw)
	})
	if err != nil {
		h.T.Fatalf("append to %s: %v", sent, err)
	}
	return uid
}

// messages lists one page of a folder.
func (h *Harness) messages(ctx context.Context, cred imappool.Cred, folder string) []mailops.Summary {
	h.T.Helper()
	var (
		page *mailops.Page
		err  error
	)
	h.withConn(ctx, cred, func(c *imappool.Conn) {
		page, err = mailops.ListMessages(ctx, c, folder, 0, 50, "")
	})
	if err != nil {
		h.T.Fatalf("list %s of %s: %v", folder, cred.User, err)
	}
	return page.Messages
}

// WaitFor polls a folder until a message matching want appears, and returns
// it. It fails the test when the deadline passes.
func (h *Harness) WaitFor(ctx context.Context, cred imappool.Cred, folder string, want func(mailops.Summary) bool, what string) mailops.Summary {
	h.T.Helper()
	deadline := time.Now().Add(h.Env.Deliver)
	attempts, seen := 0, 0
	for {
		attempts++
		msgs := h.messages(ctx, cred, folder)
		seen = len(msgs)
		for _, m := range msgs {
			if want(m) {
				return m
			}
		}
		if time.Now().After(deadline) {
			h.T.Fatalf("waited %s over %d polls for %s in %s of %s, saw %d messages",
				h.Env.Deliver, attempts, what, folder, cred.User, seen)
		}
		select {
		case <-ctx.Done():
			h.T.Fatalf("context cancelled waiting for %s: %v", what, ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}

// WaitForMessageID waits for a specific Message-ID to arrive in a folder.
func (h *Harness) WaitForMessageID(ctx context.Context, cred imappool.Cred, folder, messageID string) mailops.Summary {
	h.T.Helper()
	return h.WaitFor(ctx, cred, folder, func(m mailops.Summary) bool {
		return sameMessageID(m.MessageID, messageID)
	}, "message "+messageID)
}

// WaitForSubject waits for a message whose subject contains the substring.
func (h *Harness) WaitForSubject(ctx context.Context, cred imappool.Cred, folder, subject string) mailops.Summary {
	h.T.Helper()
	return h.WaitFor(ctx, cred, folder, func(m mailops.Summary) bool {
		return strings.Contains(m.Subject, subject)
	}, "subject containing "+subject)
}

// ExpectNoDelivery waits out a window and fails if the Message-ID shows up.
// Used to prove that a Sieve rule filed mail elsewhere, or that a withdrawn
// forward really stopped delivery.
func (h *Harness) ExpectNoDelivery(ctx context.Context, cred imappool.Cred, folder, messageID string, window time.Duration) {
	h.T.Helper()
	deadline := time.Now().Add(window)
	for {
		for _, m := range h.messages(ctx, cred, folder) {
			if sameMessageID(m.MessageID, messageID) {
				h.T.Fatalf("message %s reached %s of %s but should not have", messageID, folder, cred.User)
			}
		}
		if time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(pollInterval):
		}
	}
}

// Message fetches a message with Mailhearth's own rendering path.
func (h *Harness) Message(ctx context.Context, cred imappool.Cred, folder string, uid uint32) *mailops.Message {
	h.T.Helper()
	var (
		msg *mailops.Message
		err error
	)
	h.withConn(ctx, cred, func(c *imappool.Conn) {
		msg, err = mailops.GetMessage(ctx, c, folder, uid, mailops.RenderOptions{
			MaxBodyBytes: 1 << 20,
			PartURL:      func(path string) string { return "/parts/" + path },
		})
	})
	if err != nil {
		h.T.Fatalf("get message uid %d in %s: %v", uid, folder, err)
	}
	return msg
}

// --- ManageSieve ---------------------------------------------------------

// sieveClient opens a ManageSieve session for the credential.
func (h *Harness) sieveClient(ctx context.Context, cred imappool.Cred) *sieve.Client {
	h.T.Helper()
	host, _, _ := strings.Cut(h.Cfg.SieveAddr, ":")
	c, err := sieve.Dial(ctx, h.Cfg.SieveAddr, string(h.Cfg.SieveTLS), &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err != nil {
		h.T.Fatalf("managesieve connect: %v", err)
	}
	if err := c.Authenticate(cred.User, cred.Pass); err != nil {
		c.Close()
		h.T.Fatalf("managesieve login as %s: %v", cred.User, err)
	}
	return c
}

// InstallSieve compiles the rules the way the mail API does, uploads the
// script and makes it active. It returns the compiled script.
func (h *Harness) InstallSieve(ctx context.Context, cred imappool.Cred, rules []model.SieveRule, vacation *model.Vacation) string {
	h.T.Helper()
	script, err := sieve.Compile(rules, vacation)
	if err != nil {
		h.T.Fatalf("compile sieve: %v", err)
	}
	c := h.sieveClient(ctx, cred)
	defer c.Close()
	if err := c.CheckScript(script); err != nil {
		h.T.Fatalf("provider rejected the compiled script: %v\n%s", err, script)
	}
	if err := c.PutScript(sieve.ScriptName, script); err != nil {
		h.T.Fatalf("put sieve script: %v", err)
	}
	if err := c.SetActive(sieve.ScriptName); err != nil {
		h.T.Fatalf("activate sieve script: %v", err)
	}
	h.T.Cleanup(func() {
		if h.Env.Keep {
			return
		}
		ctx, cancel := h.Ctx(60 * time.Second)
		defer cancel()
		cl := h.sieveClient(ctx, cred)
		defer cl.Close()
		cl.SetActive("")
		if err := cl.DeleteScript(sieve.ScriptName); err != nil {
			h.T.Logf("cleanup: delete sieve script: %v", err)
		}
	})
	return script
}

// SieveScripts returns the script names the provider holds, mapped to
// whether they are the active one.
func (h *Harness) SieveScripts(ctx context.Context, cred imappool.Cred) map[string]bool {
	h.T.Helper()
	c := h.sieveClient(ctx, cred)
	defer c.Close()
	list, err := c.ListScripts()
	if err != nil {
		h.T.Fatalf("list sieve scripts: %v", err)
	}
	out := map[string]bool{}
	for _, s := range list {
		out[s.Name] = s.Active
	}
	return out
}

// --- assertions ----------------------------------------------------------

// AuditActions returns the audit log actions recorded so far.
func (h *Harness) AuditActions(ctx context.Context) []string {
	h.T.Helper()
	entries, err := h.Svc.Audit(ctx, h.OrgID, 0, 200)
	if err != nil {
		h.T.Fatalf("audit: %v", err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Action)
	}
	return out
}

// RequireAudit fails unless every action was recorded.
func (h *Harness) RequireAudit(ctx context.Context, actions ...string) {
	h.T.Helper()
	got := map[string]bool{}
	for _, a := range h.AuditActions(ctx) {
		got[a] = true
	}
	var missing []string
	for _, a := range actions {
		if !got[a] {
			missing = append(missing, a)
		}
	}
	if len(missing) > 0 {
		h.T.Fatalf("audit log is missing %s", strings.Join(missing, ", "))
	}
}

// Logf prefixes suite logs so a live run is readable.
func (h *Harness) Logf(format string, args ...any) {
	h.T.Logf("[%s] %s", h.runID, fmt.Sprintf(format, args...))
}

func splitAddr(addr string) (string, string) {
	i := strings.LastIndex(addr, "@")
	if i < 0 {
		return addr, ""
	}
	return addr[:i], addr[i+1:]
}

// sameMessageID compares Message-IDs ignoring the optional angle brackets,
// which servers and clients are inconsistent about.
func sameMessageID(a, b string) bool {
	trim := func(s string) string { return strings.Trim(strings.TrimSpace(s), "<>") }
	ta, tb := trim(a), trim(b)
	return ta != "" && ta == tb
}
