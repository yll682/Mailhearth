package mailops

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"mailhearth/internal/mailproto/mimeutil"
)

// Recipient is a name/address pair for composing.
type Recipient struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

func (r Recipient) mailAddress() *mail.Address {
	return &mail.Address{Name: r.Name, Address: r.Address}
}

// Attachment is a file to attach when composing.
type Attachment struct {
	Filename string
	MIME     string
	Size     int64
	Open     func() (io.ReadCloser, error)
}

// Draft is everything needed to build an outgoing message.
type Draft struct {
	From        Recipient
	To          []Recipient
	Cc          []Recipient
	Bcc         []Recipient
	ReplyTo     []Recipient
	Subject     string
	Text        string
	HTML        string
	InReplyTo   string
	References  []string
	Attachments []Attachment
	Date        time.Time
	MessageID   string
	Priority    string // "high" | "" | "low"
}

// NewMessageID mints a Message-ID for domain.
func NewMessageID(domain string) string {
	b := make([]byte, 12)
	rand.Read(b)
	if domain == "" {
		domain = "mailhearth.local"
	}
	return fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), hex.EncodeToString(b), domain)
}

func toMailAddresses(list []Recipient) []*mail.Address {
	out := make([]*mail.Address, 0, len(list))
	for _, r := range list {
		if strings.TrimSpace(r.Address) == "" {
			continue
		}
		out = append(out, r.mailAddress())
	}
	return out
}

// Build renders the draft as an RFC 5322 message.
func Build(d *Draft) ([]byte, error) {
	if d.From.Address == "" {
		return nil, fmt.Errorf("sender address is required")
	}
	var h mail.Header
	when := d.Date
	if when.IsZero() {
		when = time.Now()
	}
	h.SetDate(when)
	h.SetAddressList("From", []*mail.Address{d.From.mailAddress()})
	if to := toMailAddresses(d.To); len(to) > 0 {
		h.SetAddressList("To", to)
	}
	if cc := toMailAddresses(d.Cc); len(cc) > 0 {
		h.SetAddressList("Cc", cc)
	}
	if rt := toMailAddresses(d.ReplyTo); len(rt) > 0 {
		h.SetAddressList("Reply-To", rt)
	}
	h.SetSubject(d.Subject)
	id := d.MessageID
	if id == "" {
		domain := "mailhearth.local"
		if i := strings.LastIndex(d.From.Address, "@"); i >= 0 {
			domain = d.From.Address[i+1:]
		}
		id = NewMessageID(domain)
	}
	h.SetMessageID(strings.Trim(id, "<>"))
	if d.InReplyTo != "" {
		h.SetMsgIDList("In-Reply-To", []string{strings.Trim(d.InReplyTo, "<>")})
	}
	if len(d.References) > 0 {
		refs := make([]string, 0, len(d.References))
		for _, r := range d.References {
			refs = append(refs, strings.Trim(r, "<>"))
		}
		h.SetMsgIDList("References", refs)
	}
	h.Set("MIME-Version", "1.0")
	h.Set("User-Agent", "Mailhearth")
	switch d.Priority {
	case "high":
		h.Set("X-Priority", "1 (Highest)")
		h.Set("Importance", "High")
	case "low":
		h.Set("X-Priority", "5 (Lowest)")
		h.Set("Importance", "Low")
	}

	text := d.Text
	if text == "" && d.HTML != "" {
		text = mimeutil.HTMLToText(d.HTML)
	}
	var buf bytes.Buffer
	writeBody := func(w interface {
		CreateInline() (*mail.InlineWriter, error)
		CreateSingleInline(mail.InlineHeader) (io.WriteCloser, error)
	}) error {
		if d.HTML == "" {
			var th mail.InlineHeader
			th.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
			pw, err := w.CreateSingleInline(th)
			if err != nil {
				return err
			}
			io.WriteString(pw, text)
			return pw.Close()
		}
		iw, err := w.CreateInline()
		if err != nil {
			return err
		}
		var th mail.InlineHeader
		th.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
		pw, err := iw.CreatePart(th)
		if err != nil {
			return err
		}
		io.WriteString(pw, text)
		pw.Close()
		var hh mail.InlineHeader
		hh.SetContentType("text/html", map[string]string{"charset": "utf-8"})
		pw, err = iw.CreatePart(hh)
		if err != nil {
			return err
		}
		io.WriteString(pw, d.HTML)
		pw.Close()
		return iw.Close()
	}

	if len(d.Attachments) == 0 {
		if d.HTML == "" {
			h.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
			pw, err := mail.CreateSingleInlineWriter(&buf, h)
			if err != nil {
				return nil, err
			}
			io.WriteString(pw, text)
			if err := pw.Close(); err != nil {
				return nil, err
			}
			return buf.Bytes(), nil
		}
		iw, err := mail.CreateInlineWriter(&buf, h)
		if err != nil {
			return nil, err
		}
		var th mail.InlineHeader
		th.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
		pw, err := iw.CreatePart(th)
		if err != nil {
			return nil, err
		}
		io.WriteString(pw, text)
		pw.Close()
		var hh mail.InlineHeader
		hh.SetContentType("text/html", map[string]string{"charset": "utf-8"})
		pw, err = iw.CreatePart(hh)
		if err != nil {
			return nil, err
		}
		io.WriteString(pw, d.HTML)
		pw.Close()
		if err := iw.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	mw, err := mail.CreateWriter(&buf, h)
	if err != nil {
		return nil, err
	}
	if err := writeBody(mw); err != nil {
		return nil, err
	}
	for _, a := range d.Attachments {
		var ah mail.AttachmentHeader
		ah.SetFilename(a.Filename)
		mt := a.MIME
		if mt == "" {
			mt = "application/octet-stream"
		}
		ah.SetContentType(mt, nil)
		aw, err := mw.CreateAttachment(ah)
		if err != nil {
			return nil, err
		}
		rc, err := a.Open()
		if err != nil {
			return nil, err
		}
		_, cerr := io.Copy(aw, rc)
		rc.Close()
		if cerr != nil {
			return nil, cerr
		}
		aw.Close()
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// SMTPConfig describes the outgoing server.
type SMTPConfig struct {
	Addr      string
	TLSMode   string // tls | starttls | none
	TLSConfig *tls.Config
	Timeout   time.Duration
}

// Send submits raw via SMTP using the mailbox credential. envelopeFrom is
// the MAIL FROM address; rcpts the full recipient list including Bcc.
func Send(ctx context.Context, cfg SMTPConfig, user, pass, envelopeFrom string, rcpts []string, raw []byte) error {
	if len(rcpts) == 0 {
		return fmt.Errorf("no recipients")
	}
	host, _, _ := net.SplitHostPort(cfg.Addr)
	tlsCfg := cfg.TLSConfig
	if tlsCfg == nil {
		tlsCfg = &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	var (
		c   *smtp.Client
		err error
	)
	switch cfg.TLSMode {
	case "starttls":
		var conn net.Conn
		conn, err = dialer.DialContext(ctx, "tcp", cfg.Addr)
		if err == nil {
			conn.SetDeadline(time.Now().Add(timeout))
			c, err = smtp.NewClientStartTLS(conn, tlsCfg)
		}
	case "none":
		var conn net.Conn
		conn, err = dialer.DialContext(ctx, "tcp", cfg.Addr)
		if err == nil {
			conn.SetDeadline(time.Now().Add(timeout))
			c = smtp.NewClient(conn)
		}
	default:
		td := &tls.Dialer{NetDialer: dialer, Config: tlsCfg}
		var conn net.Conn
		conn, err = td.DialContext(ctx, "tcp", cfg.Addr)
		if err == nil {
			conn.SetDeadline(time.Now().Add(timeout))
			c = smtp.NewClient(conn)
		}
	}
	if err != nil {
		return fmt.Errorf("smtp connect: %w", err)
	}
	defer c.Close()
	if err := c.Hello("mailhearth"); err != nil {
		return fmt.Errorf("smtp hello: %w", err)
	}
	if err := c.Auth(sasl.NewPlainClient("", user, pass)); err != nil {
		return &SendAuthError{Err: err}
	}
	if err := c.SendMail(envelopeFrom, rcpts, bytes.NewReader(raw)); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	c.Quit()
	return nil
}

// SendAuthError marks an SMTP authentication failure.
type SendAuthError struct{ Err error }

func (e *SendAuthError) Error() string { return "smtp authentication failed: " + e.Err.Error() }
func (e *SendAuthError) Unwrap() error { return e.Err }

// ParseMessageForQuote extracts text/html bodies from a raw message (used
// when replying from a draft or forwarding inline).
func ParseMessageForQuote(raw []byte) (text, html string) {
	e, err := message.Read(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
		return "", ""
	}
	_ = e.Walk(func(path []int, entity *message.Entity, err error) error {
		if err != nil {
			return nil
		}
		t, _, _ := entity.Header.ContentType()
		switch t {
		case "text/plain":
			if text == "" {
				b, _ := io.ReadAll(io.LimitReader(entity.Body, 1<<20))
				text = string(b)
			}
		case "text/html":
			if html == "" {
				b, _ := io.ReadAll(io.LimitReader(entity.Body, 1<<20))
				html = string(b)
			}
		}
		return nil
	})
	return text, html
}
