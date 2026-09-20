// Package devstack runs an in-process fake of the infrastructure Mailhearth
// depends on: the Purelymail API, an IMAP server and an SMTP submission
// server. It exists so the product can be developed, demonstrated and
// tested end-to-end without a Purelymail account.
package devstack

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"mailhearth/internal/purelymail/fake"
)

// Stack is a running dev stack.
type Stack struct {
	API      *fake.Server
	APIURL   string
	IMAPAddr string
	SMTPAddr string
	Token    string

	apiSrv  *httptest.Server
	imapSrv *imapserver.Server
	smtpSrv *smtp.Server
	mu      sync.Mutex
	users   map[string]*imapmemserver.User
	log     *slog.Logger
}

type literal struct {
	*bytes.Reader
}

func (l literal) Size() int64 { return int64(l.Reader.Len()) }

// Start launches the stack on loopback ports.
func Start(log *slog.Logger) (*Stack, error) {
	if log == nil {
		log = slog.Default()
	}
	s := &Stack{Token: "dev-token", users: map[string]*imapmemserver.User{}, log: log}
	s.API = fake.New(s.Token)
	s.API.Hooks = fake.Hooks{
		UserCreated:     func(user, password string) { s.ensureUser(user, password) },
		UserDeleted:     func(user string) { s.mu.Lock(); delete(s.users, user); s.mu.Unlock() },
		PasswordChanged: func(user, password string) { s.ensureUser(user, password) },
	}
	s.apiSrv = httptest.NewServer(s.API)
	s.APIURL = s.apiSrv.URL

	imapLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.IMAPAddr = imapLn.Addr().String()
	s.imapSrv = imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &session{stack: s}, nil, nil
		},
		Caps: imap.CapSet{
			imap.CapIMAP4rev1: {}, imap.CapIMAP4rev2: {}, imap.CapIdle: {}, imap.CapMove: {}, imap.CapUIDPlus: {},
			imap.CapListStatus: {}, imap.CapESearch: {}, imap.CapSpecialUse: {}, imap.CapUnselect: {},
		},
		InsecureAuth: true,
		Logger:       quietLogger{},
	})
	go s.imapSrv.Serve(imapLn)

	smtpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.SMTPAddr = smtpLn.Addr().String()
	s.smtpSrv = smtp.NewServer(&backend{stack: s})
	s.smtpSrv.Domain = "dev.mailhearth.local"
	s.smtpSrv.AllowInsecureAuth = true
	s.smtpSrv.MaxMessageBytes = 50 << 20
	s.smtpSrv.ReadTimeout = 60 * time.Second
	s.smtpSrv.WriteTimeout = 60 * time.Second
	go s.smtpSrv.Serve(smtpLn)
	return s, nil
}

// Handler exposes the fake Purelymail API as an http.Handler (for tests
// that mount it themselves).
func (s *Stack) Handler() http.Handler { return s.API }

// Close stops all servers.
func (s *Stack) Close() {
	s.apiSrv.Close()
	s.imapSrv.Close()
	s.smtpSrv.Close()
}

func (s *Stack) ensureUser(user, password string) *imapmemserver.User {
	user = strings.ToLower(user)
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.users[user]; ok {
		return u
	}
	u := imapmemserver.NewUser(user, password)
	for _, name := range []string{"INBOX", "Sent", "Drafts", "Trash", "Junk", "Archive"} {
		u.Create(name, nil)
		u.Subscribe(name)
	}
	s.users[user] = u
	return u
}

// SeedUser creates a Purelymail user plus IMAP account with mail.
func (s *Stack) SeedUser(address, password string) {
	s.API.AddUser(address, password)
	s.ensureUser(address, password)
}

// Deliver appends a raw message to a local user's INBOX (or other folder).
func (s *Stack) Deliver(address, folder string, raw []byte) error {
	s.mu.Lock()
	u := s.users[strings.ToLower(address)]
	s.mu.Unlock()
	if u == nil {
		return fmt.Errorf("no local user %s", address)
	}
	if folder == "" {
		folder = "INBOX"
	}
	_, err := u.Append(folder, literal{bytes.NewReader(raw)}, &imap.AppendOptions{Time: time.Now()})
	return err
}

// SampleMessage builds a simple RFC 5322 message for seeding.
func SampleMessage(from, to, subject, text, html string, when time.Time) []byte {
	var b strings.Builder
	boundary := fmt.Sprintf("mh-%d", when.UnixNano())
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%d@dev.mailhearth.local>\r\nMIME-Version: 1.0\r\n",
		from, to, subject, when.Format(time.RFC1123Z), when.UnixNano())
	if html == "" {
		fmt.Fprintf(&b, "Content-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", text)
		return []byte(b.String())
	}
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", boundary, text)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/html; charset=utf-8\r\n\r\n%s\r\n--%s--\r\n", boundary, html, boundary)
	return []byte(b.String())
}

// --- IMAP session with Purelymail-style credential validation ---

type session struct {
	*imapmemserver.UserSession
	stack *Stack
}

func (s *session) Login(username, password string) error {
	if !s.stack.API.CredentialValid(username, password) {
		return imapserver.ErrAuthFailed
	}
	u := s.stack.ensureUser(username, password)
	s.UserSession = imapmemserver.NewUserSession(u)
	return nil
}

func (s *session) Close() error {
	if s.UserSession != nil {
		return s.UserSession.Close()
	}
	return nil
}

// --- SMTP submission that delivers to local users ---

type backend struct{ stack *Stack }

func (b *backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	return &smtpSession{stack: b.stack}, nil
}

type smtpSession struct {
	stack *Stack
	user  string
	from  string
	rcpts []string
}

func (s *smtpSession) AuthMechanisms() []string { return []string{sasl.Plain} }

func (s *smtpSession) Auth(mech string) (sasl.Server, error) {
	check := func(username, password string) error {
		if !s.stack.API.CredentialValid(username, password) {
			return &smtp.SMTPError{Code: 535, EnhancedCode: smtp.EnhancedCode{5, 7, 8}, Message: "authentication failed"}
		}
		s.user = strings.ToLower(username)
		return nil
	}
	if mech == sasl.Plain {
		return sasl.NewPlainServer(func(identity, username, password string) error { return check(username, password) }), nil
	}
	return nil, smtp.ErrAuthUnsupported
}

func (s *smtpSession) Mail(from string, opts *smtp.MailOptions) error {
	if s.user == "" {
		return smtp.ErrAuthRequired
	}
	s.from = from
	return nil
}

func (s *smtpSession) Rcpt(to string, opts *smtp.RcptOptions) error {
	s.rcpts = append(s.rcpts, to)
	return nil
}

func (s *smtpSession) Data(r io.Reader) error {
	raw, err := io.ReadAll(io.LimitReader(r, 50<<20))
	if err != nil {
		return err
	}
	for _, rcpt := range s.rcpts {
		if s.stack.API.UserExists(rcpt) {
			s.stack.ensureUser(rcpt, "")
			if err := s.stack.Deliver(rcpt, "INBOX", raw); err != nil {
				s.stack.log.Warn("devstack: deliver failed", "rcpt", rcpt, "err", err)
			}
		} else {
			s.stack.log.Info("devstack: message to external recipient accepted (dropped)", "rcpt", rcpt, "bytes", len(raw))
		}
	}
	return nil
}

func (s *smtpSession) Reset()        { s.from, s.rcpts = "", nil }
func (s *smtpSession) Logout() error { return nil }

type quietLogger struct{}

func (quietLogger) Printf(format string, v ...interface{}) {}

var _ = context.Background
