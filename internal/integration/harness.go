// Package integration exercises Mailhearth against a real Purelymail
// account: the management API, IMAP, SMTP submission and ManageSieve. The
// in-memory fake used by the unit tests can only prove that Mailhearth is
// self-consistent; these tests prove that it matches the live service.
//
// Every test skips unless MAILHEARTH_IT_TOKEN and MAILHEARTH_IT_DOMAIN are
// set. See docs/integration-testing.md.
//
// Safety: the suite only ever creates, modifies or deletes objects whose
// local part starts with the configured prefix (default "mh-it"). Anything
// else is refused by guardOwned, so pointing the suite at a domain that also
// holds real mailboxes cannot damage them.
package integration

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mailhearth/internal/config"
	"mailhearth/internal/core"
	"mailhearth/internal/db"
	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/model"
	"mailhearth/internal/purelymail"
	"mailhearth/internal/secrets"
)

// Env is the resolved configuration of a suite run.
type Env struct {
	Token     string
	Domain    string
	Prefix    string
	APIURL    string
	IMAPAddr  string
	IMAPTLS   config.TLSMode
	SMTPAddr  string
	SMTPTLS   config.TLSMode
	SieveAddr string
	SieveTLS  config.TLSMode
	External  string        // optional address outside the account, for forward tests
	Deliver   time.Duration // how long to wait for a message to arrive
	Keep      bool          // leave created objects behind for inspection
}

func envStr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envTLS(key string, def config.TLSMode) config.TLSMode {
	switch v := config.TLSMode(strings.ToLower(envStr(key, string(def)))); v {
	case config.TLSImplicit, config.TLSStart, config.TLSNone:
		return v
	default:
		return def
	}
}

// LoadEnv reads the suite configuration, or skips the test when the account
// credentials are absent.
func LoadEnv(t *testing.T) Env {
	t.Helper()
	token := strings.TrimSpace(os.Getenv("MAILHEARTH_IT_TOKEN"))
	domain := strings.ToLower(strings.TrimSpace(os.Getenv("MAILHEARTH_IT_DOMAIN")))
	if token == "" || domain == "" {
		t.Skip("set MAILHEARTH_IT_TOKEN and MAILHEARTH_IT_DOMAIN to run the Purelymail integration suite")
	}
	e := Env{
		Token:     token,
		Domain:    domain,
		Prefix:    strings.ToLower(envStr("MAILHEARTH_IT_PREFIX", "mh-it")),
		APIURL:    strings.TrimRight(envStr("MAILHEARTH_IT_API_URL", "https://purelymail.com/api/v0"), "/"),
		IMAPAddr:  envStr("MAILHEARTH_IT_IMAP_ADDR", "imap.purelymail.com:993"),
		SMTPAddr:  envStr("MAILHEARTH_IT_SMTP_ADDR", "smtp.purelymail.com:465"),
		SieveAddr: envStr("MAILHEARTH_IT_SIEVE_ADDR", "mailserver.purelymail.com:4190"),
		External:  strings.ToLower(strings.TrimSpace(os.Getenv("MAILHEARTH_IT_EXTERNAL"))),
		Keep:      strings.TrimSpace(os.Getenv("MAILHEARTH_IT_KEEP")) != "",
	}
	e.IMAPTLS = envTLS("MAILHEARTH_IT_IMAP_TLS", config.TLSImplicit)
	e.SMTPTLS = envTLS("MAILHEARTH_IT_SMTP_TLS", config.TLSImplicit)
	e.SieveTLS = envTLS("MAILHEARTH_IT_SIEVE_TLS", config.TLSStart)
	secs := 180
	if v := strings.TrimSpace(os.Getenv("MAILHEARTH_IT_DELIVER_SECONDS")); v != "" {
		if n, err := time.ParseDuration(v + "s"); err == nil {
			secs = int(n.Seconds())
		}
	}
	e.Deliver = time.Duration(secs) * time.Second
	if !strings.HasPrefix(e.Prefix, "mh") {
		t.Fatalf("MAILHEARTH_IT_PREFIX %q must start with \"mh\" so test objects stay recognisable", e.Prefix)
	}
	return e
}

// Harness is one isolated Mailhearth installation wired to the live account.
type Harness struct {
	T     *testing.T
	Env   Env
	Svc   *core.Service
	API   purelymail.API
	Pool  *imappool.Pool
	Cfg   *config.Config
	Log   *slog.Logger
	OrgID int64
	Owner int64

	runID   string
	counter atomic.Int64
}

// New builds a fresh installation: empty database, own master key, a live
// Purelymail client, and the organisation already connected to the account.
func New(t *testing.T) *Harness {
	t.Helper()
	env := LoadEnv(t)
	ctx := context.Background()

	logDest := io.Discard
	if testing.Verbose() {
		logDest = os.Stderr
	}
	log := slog.New(slog.NewTextHandler(logDest, &slog.HandlerOptions{Level: slog.LevelDebug}))

	dir := t.TempDir()
	cfg := &config.Config{
		DataDir:          dir,
		PurelymailAPIURL: env.APIURL,
		IMAPAddr:         env.IMAPAddr,
		IMAPTLS:          env.IMAPTLS,
		SMTPAddr:         env.SMTPAddr,
		SMTPTLS:          env.SMTPTLS,
		SieveAddr:        env.SieveAddr,
		SieveTLS:         env.SieveTLS,
		IMAPMaxConns:     8,
		IMAPIdleTimeout:  90 * time.Second,
		MaxUploadBytes:   25 << 20,
		MaxMessageBytes:  40 << 20,
		SessionTTL:       24 * time.Hour,
		InviteTTL:        24 * time.Hour,
	}

	database, err := db.Open(filepath.Join(dir, "mailhearth.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	master := make([]byte, 32)
	for i := range master {
		master[i] = byte(i * 7)
	}
	box, err := secrets.NewBox(master, "credentials")
	if err != nil {
		t.Fatalf("secrets box: %v", err)
	}

	pool := imappool.New(imappool.Config{
		Addr: cfg.IMAPAddr, TLSMode: string(cfg.IMAPTLS), MaxConns: cfg.IMAPMaxConns,
		PerCredIdle: 2, IdleTimeout: cfg.IMAPIdleTimeout, Logger: log,
	})
	t.Cleanup(pool.Close)

	svc := core.New(database, cfg, box, pool, log)
	h := &Harness{
		T: t, Env: env, Svc: svc, Cfg: cfg, Log: log, Pool: pool,
		API:   purelymail.New(env.APIURL, env.Token),
		runID: fmt.Sprintf("%d", time.Now().UnixNano()%1e9),
	}

	owner, err := svc.Init(ctx, core.InitRequest{
		OrgName:    "Mailhearth integration",
		AdminName:  "Integration Owner",
		AdminEmail: h.Name("owner") + "@" + env.Domain,
		Password:   "correct-horse-battery-staple",
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	org, err := svc.Org(ctx)
	if err != nil {
		t.Fatalf("org: %v", err)
	}
	h.OrgID, h.Owner = org.ID, owner.ID

	if _, err := svc.Connect(ctx, h.OrgID, h.Owner, env.Token); err != nil {
		t.Fatalf("connect to Purelymail: %v", err)
	}
	return h
}

// Name mints a local part unique to this run, always carrying the prefix.
func (h *Harness) Name(role string) string {
	n := h.counter.Add(1)
	return fmt.Sprintf("%s-%s-%s%d", h.Env.Prefix, h.runID, role, n)
}

// Addr is Name plus the test domain.
func (h *Harness) Addr(role string) string { return h.Name(role) + "@" + h.Env.Domain }

// Owned reports whether addr belongs to this suite and may be written to.
func (h *Harness) Owned(addr string) bool {
	local, domain := core.SplitAddress(strings.ToLower(strings.TrimSpace(addr)))
	return domain == h.Env.Domain && strings.HasPrefix(local, h.Env.Prefix+"-")
}

// guardOwned aborts the run rather than touch an address the suite did not
// create. Every destructive helper calls it.
func (h *Harness) guardOwned(op, addr string) {
	h.T.Helper()
	if !h.Owned(addr) {
		h.T.Fatalf("refusing to %s %q: not created by this suite (prefix %q on domain %q)", op, addr, h.Env.Prefix, h.Env.Domain)
	}
}

// Ctx returns a context with a per-operation deadline.
func (h *Harness) Ctx(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// --- resource tracking -------------------------------------------------

// TrackUser schedules deletion of a Purelymail user created by a test.
func (h *Harness) TrackUser(addr string) {
	h.guardOwned("track", addr)
	h.T.Cleanup(func() {
		if h.Env.Keep {
			h.T.Logf("MAILHEARTH_IT_KEEP set: leaving user %s behind", addr)
			return
		}
		ctx, cancel := h.Ctx(60 * time.Second)
		defer cancel()
		if err := h.API.DeleteUser(ctx, addr); err != nil {
			h.T.Logf("cleanup: delete user %s: %v", addr, err)
		}
	})
}

// TrackRule schedules deletion of a routing rule created by a test.
func (h *Harness) TrackRule(id int64, label string) {
	h.T.Cleanup(func() {
		if h.Env.Keep {
			h.T.Logf("MAILHEARTH_IT_KEEP set: leaving routing rule %d (%s) behind", id, label)
			return
		}
		ctx, cancel := h.Ctx(60 * time.Second)
		defer cancel()
		if err := h.API.DeleteRoutingRule(ctx, id); err != nil {
			h.T.Logf("cleanup: delete routing rule %d (%s): %v", id, label, err)
		}
	})
}

// --- account helpers ---------------------------------------------------

// DomainID returns the imported domain row for the test domain.
func (h *Harness) DomainID(ctx context.Context) int64 {
	h.T.Helper()
	doms, err := h.Svc.Domains(ctx, h.OrgID)
	if err != nil {
		h.T.Fatalf("domains: %v", err)
	}
	for _, d := range doms {
		if strings.EqualFold(d.Name, h.Env.Domain) {
			return d.ID
		}
	}
	h.T.Fatalf("domain %s is not in the account; add it in Purelymail first", h.Env.Domain)
	return 0
}

// Sync imports the account into the installation.
func (h *Harness) Sync(ctx context.Context) *core.ImportResult {
	h.T.Helper()
	res, err := h.Svc.Sync(ctx, h.OrgID, h.Owner)
	if err != nil {
		h.T.Fatalf("sync: %v", err)
	}
	return res
}

// CompleteSetup runs the first sync and marks setup done.
func (h *Harness) CompleteSetup(ctx context.Context) *core.ImportResult {
	h.T.Helper()
	res, err := h.Svc.CompleteSetup(ctx, h.OrgID, h.Owner, "")
	if err != nil {
		h.T.Fatalf("complete setup: %v", err)
	}
	return res
}

// Member creates an active member with a password.
func (h *Harness) Member(ctx context.Context, name, role string) *model.Member {
	h.T.Helper()
	roles, err := h.Svc.Roles(ctx, h.OrgID)
	if err != nil {
		h.T.Fatalf("roles: %v", err)
	}
	var roleID int64
	for _, r := range roles {
		if r.Key == role {
			roleID = r.ID
		}
	}
	if roleID == 0 {
		h.T.Fatalf("role %q not found", role)
	}
	res, err := h.Svc.CreateMember(ctx, h.OrgID, h.Owner, core.CreateMemberRequest{
		MemberInput: core.MemberInput{
			DisplayName: name,
			LoginEmail:  h.Addr("login"),
			RoleID:      roleID,
		},
		Password: "correct-horse-battery-staple",
	})
	if err != nil {
		h.T.Fatalf("create member %s: %v", name, err)
	}
	return res.Member
}

// Mailbox provisions a real Purelymail user through the service and tracks
// it for cleanup. ownerID 0 leaves it unowned (shared mailboxes).
func (h *Harness) Mailbox(ctx context.Context, kind, role string, ownerID int64) *model.Mailbox {
	h.T.Helper()
	local := h.Name(role)
	mb, err := h.Svc.CreateMailbox(ctx, h.OrgID, h.Owner, core.CreateMailboxRequest{
		Kind:          kind,
		DomainID:      h.DomainID(ctx),
		LocalPart:     local,
		DisplayName:   role,
		OwnerMemberID: ownerID,
	})
	if err != nil {
		h.T.Fatalf("create mailbox %s: %v", local, err)
	}
	h.TrackUser(mb.Address)
	if !mb.HasCredential {
		h.T.Fatalf("mailbox %s was created without an app password", mb.Address)
	}
	return mb
}

// Cred resolves the IMAP/SMTP credential a member uses for a mailbox.
func (h *Harness) Cred(ctx context.Context, memberID, mailboxID int64) imappool.Cred {
	h.T.Helper()
	mc, err := h.Svc.ResolveMailbox(ctx, h.OrgID, memberID, mailboxID)
	if err != nil {
		h.T.Fatalf("resolve mailbox: %v", err)
	}
	return mc.Cred
}

// UserExists reports whether the Purelymail account still has the user.
func (h *Harness) UserExists(ctx context.Context, addr string) bool {
	h.T.Helper()
	users, err := h.API.ListUsers(ctx)
	if err != nil {
		h.T.Fatalf("list users: %v", err)
	}
	for _, u := range users {
		if strings.EqualFold(u, addr) {
			return true
		}
	}
	return false
}

// RuleFor returns the live routing rule for an address, or nil.
func (h *Harness) RuleFor(ctx context.Context, local string) *purelymail.RoutingRule {
	h.T.Helper()
	rules, err := h.API.ListRoutingRules(ctx)
	if err != nil {
		h.T.Fatalf("list routing rules: %v", err)
	}
	for i := range rules {
		r := rules[i]
		if strings.EqualFold(r.DomainName, h.Env.Domain) && strings.EqualFold(r.MatchUser, local) {
			return &r
		}
	}
	return nil
}

// Nonce returns a short token unique within the run, for subject lines.
func (h *Harness) Nonce() string {
	return fmt.Sprintf("%s-%d", h.runID, h.counter.Add(1))
}

// --- seeding the account behind Mailhearth's back -----------------------
//
// Import and migration tests need objects that Mailhearth did not create.
// These helpers go straight to the Purelymail API, exactly as a person
// clicking around the provider's own control panel would.

// CreateUserDirect creates a Purelymail user through the raw API and returns
// the password it was given.
func (h *Harness) CreateUserDirect(ctx context.Context, addr string) string {
	h.T.Helper()
	h.guardOwned("create", addr)
	local, domain := core.SplitAddress(addr)
	pw := secrets.RandomPassword()
	err := h.API.CreateUser(ctx, purelymail.CreateUserRequest{
		UserName: local, DomainName: domain, Password: pw,
		EnableSearchIndexing: true, SendWelcomeEmail: false,
	})
	if err != nil {
		h.T.Fatalf("seed user %s: %v", addr, err)
	}
	h.TrackUser(addr)
	return pw
}

// CreateRuleDirect creates a routing rule through the raw API and returns its
// live id.
func (h *Harness) CreateRuleDirect(ctx context.Context, local string, targets []string) int64 {
	h.T.Helper()
	h.guardOwned("create rule for", local+"@"+h.Env.Domain)
	err := h.API.CreateRoutingRule(ctx, purelymail.CreateRoutingRuleRequest{
		DomainName: h.Env.Domain, MatchUser: local, TargetAddresses: targets,
	})
	if err != nil {
		h.T.Fatalf("seed routing rule %s: %v", local, err)
	}
	r := h.RuleFor(ctx, local)
	if r == nil {
		h.T.Fatalf("seeded routing rule %s does not appear in listRoutingRules", local)
	}
	h.TrackRule(r.ID, local)
	return r.ID
}

// --- model lookups ------------------------------------------------------

// MailboxByAddress finds an imported or created mailbox by its address.
func (h *Harness) MailboxByAddress(ctx context.Context, addr string) *model.Mailbox {
	h.T.Helper()
	boxes, err := h.Svc.Mailboxes(ctx, h.OrgID)
	if err != nil {
		h.T.Fatalf("mailboxes: %v", err)
	}
	for i := range boxes {
		if strings.EqualFold(boxes[i].Address, addr) {
			return &boxes[i]
		}
	}
	h.T.Fatalf("no mailbox %s in the organisation", addr)
	return nil
}

// AddressByFull finds an address row by its full address.
func (h *Harness) AddressByFull(ctx context.Context, full string) *model.Address {
	h.T.Helper()
	addrs, err := h.Svc.Addresses(ctx, h.OrgID)
	if err != nil {
		h.T.Fatalf("addresses: %v", err)
	}
	for i := range addrs {
		if strings.EqualFold(addrs[i].Address, full) {
			return &addrs[i]
		}
	}
	h.T.Fatalf("no address %s in the organisation", full)
	return nil
}
