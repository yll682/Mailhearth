package mailops

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/mailproto/mimeutil"
)

type fetchBuffer = imapclient.FetchMessageBuffer

func collectFetch(ctx context.Context, conn *imappool.Conn, set imap.NumSet, opts *imap.FetchOptions) ([]*fetchBuffer, error) {
	var (
		msgs []*fetchBuffer
		err  error
	)
	conn.Run(ctx, func() { msgs, err = conn.C.Fetch(set, opts).Collect() })
	return msgs, err
}

// Part describes one MIME part of a message.
type Part struct {
	Path        string `json:"path"`
	MIME        string `json:"mime"`
	Filename    string `json:"filename"`
	Size        uint32 `json:"size"`
	ContentID   string `json:"contentId,omitempty"`
	Disposition string `json:"disposition"`
	Encoding    string `json:"-"`
	Charset     string `json:"-"`
	IsInline    bool   `json:"isInline"`
}

// Message is a fully rendered message.
type Message struct {
	Summary
	Cc          []Addr            `json:"cc"`
	Bcc         []Addr            `json:"bcc"`
	ReplyTo     []Addr            `json:"replyTo"`
	HTML        string            `json:"html"`
	Text        string            `json:"text"`
	HasRemote   bool              `json:"hasRemote"`
	Attachments []Part            `json:"attachments"`
	Inline      []Part            `json:"inline"`
	Headers     map[string]string `json:"headers"`
	Truncated   bool              `json:"truncated"`
	References  []string          `json:"references"`
}

// RenderOptions controls message rendering.
type RenderOptions struct {
	AllowRemote bool
	// PartURL returns the URL serving a part path (used for cid: images).
	PartURL func(partPath string) string
	// MaxBodyBytes caps how much of a text part is fetched for display.
	MaxBodyBytes int64
}

func pathString(p []int) string {
	if len(p) == 0 {
		return "TEXT"
	}
	parts := make([]string, len(p))
	for i, n := range p {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ".")
}

// ParsePath parses "1.2" into an IMAP part path.
func ParsePath(s string) ([]int, error) {
	if s == "" {
		return nil, fmt.Errorf("empty part path")
	}
	segs := strings.Split(s, ".")
	out := make([]int, 0, len(segs))
	for _, seg := range segs {
		n, err := strconv.Atoi(seg)
		if err != nil || n <= 0 || n > 999 {
			return nil, fmt.Errorf("invalid part path %q", s)
		}
		out = append(out, n)
	}
	return out, nil
}

func partFrom(p []int, sp *imap.BodyStructureSinglePart) Part {
	part := Part{Path: pathString(p), MIME: sp.MediaType(), Size: sp.Size, Encoding: sp.Encoding}
	if sp.Params != nil {
		part.Charset = sp.Params["charset"]
	}
	part.Filename = sp.Filename()
	if part.Filename == "" && sp.Params != nil && sp.Params["name"] != "" {
		part.Filename = sp.Params["name"]
	}
	if sp.ID != "" {
		part.ContentID = strings.Trim(sp.ID, "<>")
	}
	if d := sp.Disposition(); d != nil {
		part.Disposition = strings.ToLower(d.Value)
	}
	part.IsInline = part.Disposition == "inline" || (part.ContentID != "" && strings.HasPrefix(part.MIME, "image/"))
	if part.MIME == "message/rfc822" && part.Filename == "" {
		subject := ""
		if sp.MessageRFC822 != nil && sp.MessageRFC822.Envelope != nil {
			subject = sp.MessageRFC822.Envelope.Subject
		}
		if subject == "" {
			subject = "message"
		}
		part.Filename = subject + ".eml"
	}
	return part
}

func isAttachmentPart(sp *imap.BodyStructureSinglePart, part Part) bool {
	if part.Disposition == "attachment" {
		return true
	}
	if part.MIME == "message/rfc822" {
		return true
	}
	if strings.HasPrefix(part.MIME, "text/") && part.Filename == "" {
		return false
	}
	if part.IsInline && strings.HasPrefix(part.MIME, "image/") {
		return false
	}
	if strings.HasPrefix(part.MIME, "multipart/") {
		return false
	}
	return part.Filename != "" || !strings.HasPrefix(part.MIME, "text/")
}

func structureHasAttachment(bs imap.BodyStructure) bool {
	has := false
	bs.Walk(func(p []int, part imap.BodyStructure) bool {
		if has {
			return false
		}
		if sp, ok := part.(*imap.BodyStructureSinglePart); ok {
			if isAttachmentPart(sp, partFrom(p, sp)) {
				has = true
			}
		}
		return !has
	})
	return has
}

// bodyChoice picks the parts that render the message body. It returns the
// preferred HTML part and a text/plain part (either may be nil).
type bodyChoice struct {
	html *Part
	text *Part
}

func chooseBody(bs imap.BodyStructure, p []int) bodyChoice {
	switch b := bs.(type) {
	case *imap.BodyStructureSinglePart:
		if len(p) == 0 {
			// A non-multipart message: its only body part is section 1.
			p = []int{1}
		}
		part := partFrom(p, b)
		if part.Disposition == "attachment" || part.Filename != "" && !strings.HasPrefix(part.MIME, "text/") {
			return bodyChoice{}
		}
		switch part.MIME {
		case "text/html":
			return bodyChoice{html: &part}
		case "text/plain", "text/enriched", "text/markdown":
			return bodyChoice{text: &part}
		}
		return bodyChoice{}
	case *imap.BodyStructureMultiPart:
		sub := strings.ToLower(b.Subtype)
		children := func(i int) []int {
			cp := make([]int, len(p), len(p)+1)
			copy(cp, p)
			return append(cp, i+1)
		}
		switch sub {
		case "alternative":
			var choice bodyChoice
			for i, c := range b.Children {
				cc := chooseBody(c, children(i))
				if cc.html != nil {
					choice.html = cc.html
				}
				if cc.text != nil && choice.text == nil {
					choice.text = cc.text
				}
			}
			return choice
		default:
			// mixed, related, signed, report...: the first child that yields a
			// body wins; remaining text parts are treated as attachments.
			var choice bodyChoice
			for i, c := range b.Children {
				cc := chooseBody(c, children(i))
				if cc.html != nil && choice.html == nil {
					choice.html = cc.html
				}
				if cc.text != nil && choice.text == nil {
					choice.text = cc.text
				}
				if choice.html != nil || choice.text != nil {
					if sub != "related" {
						break
					}
				}
			}
			return choice
		}
	}
	return bodyChoice{}
}

// singlePartPath returns the part path usable in BODY[...] for a
// non-multipart message (the body is section "1").
func singlePartPath(bs imap.BodyStructure, p []int) []int {
	if _, ok := bs.(*imap.BodyStructureSinglePart); ok && len(p) == 0 {
		return []int{1}
	}
	return p
}

var headerFields = []string{"References", "List-Unsubscribe", "List-Id", "X-Priority", "Importance", "Auto-Submitted", "Return-Path", "Reply-To", "X-Mailer", "Content-Language"}

// GetMessage fetches and renders a message.
func GetMessage(ctx context.Context, conn *imappool.Conn, folder string, uid uint32, opts RenderOptions) (*Message, error) {
	if _, err := conn.Select(ctx, folder, true); err != nil {
		return nil, err
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = 2 << 20
	}
	hdrSection := &imap.FetchItemBodySection{Specifier: imap.PartSpecifierHeader, HeaderFields: headerFields, Peek: true}
	fo := &imap.FetchOptions{
		UID: true, Flags: true, Envelope: true, RFC822Size: true, InternalDate: true,
		BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
		BodySection:   []*imap.FetchItemBodySection{hdrSection},
	}
	msgs, err := collectFetch(ctx, conn, imap.UIDSetNum(imap.UID(uid)), fo)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, ErrNotFound
	}
	m := msgs[0]
	msg := &Message{Summary: summaryFromBuffer(m), Headers: map[string]string{}, Attachments: []Part{}, Inline: []Part{}, References: []string{}}
	if m.Envelope != nil {
		msg.Cc = toAddrs(m.Envelope.Cc)
		msg.Bcc = toAddrs(m.Envelope.Bcc)
		msg.ReplyTo = toAddrs(m.Envelope.ReplyTo)
	}
	if raw := m.FindBodySection(hdrSection); raw != nil {
		msg.Headers = parseHeaderBlock(raw)
		if refs := msg.Headers["References"]; refs != "" {
			for _, r := range strings.Fields(refs) {
				msg.References = append(msg.References, strings.Trim(r, "<>"))
			}
		}
	}
	if m.BodyStructure == nil {
		return msg, nil
	}
	// Parts inventory.
	m.BodyStructure.Walk(func(p []int, part imap.BodyStructure) bool {
		sp, ok := part.(*imap.BodyStructureSinglePart)
		if !ok {
			return true
		}
		pp := partFrom(p, sp)
		if isAttachmentPart(sp, pp) {
			msg.Attachments = append(msg.Attachments, pp)
		} else if pp.ContentID != "" || pp.IsInline && !strings.HasPrefix(pp.MIME, "text/") {
			msg.Inline = append(msg.Inline, pp)
		}
		return true
	})
	choice := chooseBody(m.BodyStructure, nil)
	fetchPart := func(part *Part) (string, bool, error) {
		p, err := ParsePath(part.Path)
		if err != nil {
			return "", false, err
		}
		p = singlePartPath(m.BodyStructure, p)
		sec := &imap.FetchItemBodySection{Part: p, Peek: true}
		truncated := false
		if int64(part.Size) > opts.MaxBodyBytes {
			sec.Partial = &imap.SectionPartial{Offset: 0, Size: opts.MaxBodyBytes}
			truncated = true
		}
		res, err := collectFetch(ctx, conn, imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{sec}})
		if err != nil {
			return "", false, err
		}
		if len(res) == 0 {
			return "", false, ErrNotFound
		}
		raw := res[0].FindBodySection(sec)
		decoded, _ := io.ReadAll(mimeutil.DecodeBody(bytes.NewReader(raw), part.Encoding, part.Charset))
		return string(decoded), truncated, nil
	}
	cidMap := map[string]string{}
	for _, ip := range msg.Inline {
		if ip.ContentID != "" && opts.PartURL != nil {
			cidMap[strings.ToLower(ip.ContentID)] = opts.PartURL(ip.Path)
		}
	}
	if choice.html != nil {
		body, truncated, err := fetchPart(choice.html)
		if err != nil {
			return nil, err
		}
		msg.Truncated = truncated
		res := mimeutil.SanitizeHTML(body, mimeutil.SanitizeOptions{
			AllowRemote: opts.AllowRemote,
			ResolveCID: func(cid string) string {
				return cidMap[strings.ToLower(strings.Trim(cid, "<>"))]
			},
		})
		msg.HTML = res.HTML
		msg.HasRemote = res.HasRemoteContent
		if choice.text != nil && choice.text.Size < 512*1024 {
			if t, _, err := fetchPart(choice.text); err == nil {
				msg.Text = t
			}
		}
		if msg.Text == "" {
			msg.Text = mimeutil.HTMLToText(body)
		}
	} else if choice.text != nil {
		body, truncated, err := fetchPart(choice.text)
		if err != nil {
			return nil, err
		}
		msg.Truncated = truncated
		msg.Text = body
		msg.HTML = mimeutil.TextToHTML(body)
	}
	return msg, nil
}

func parseHeaderBlock(raw []byte) map[string]string {
	out := map[string]string{}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	var key, val string
	flush := func() {
		if key != "" {
			dec := new(mime.WordDecoder)
			v := strings.TrimSpace(val)
			if d, err := dec.DecodeHeader(v); err == nil {
				v = d
			}
			out[key] = v
		}
	}
	for _, l := range lines {
		if l == "" {
			continue
		}
		if l[0] == ' ' || l[0] == '\t' {
			val += " " + strings.TrimSpace(l)
			continue
		}
		flush()
		key, val = "", ""
		if i := strings.IndexByte(l, ':'); i > 0 {
			key = canonicalHeader(l[:i])
			val = l[i+1:]
		}
	}
	flush()
	return out
}

func canonicalHeader(k string) string {
	for _, h := range headerFields {
		if strings.EqualFold(h, k) {
			return h
		}
	}
	return k
}

// FindPart returns the part at partPath.
func FindPart(ctx context.Context, conn *imappool.Conn, folder string, uid uint32, partPath string) (*Part, error) {
	if _, err := conn.Select(ctx, folder, true); err != nil {
		return nil, err
	}
	msgs, err := collectFetch(ctx, conn, imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{UID: true, BodyStructure: &imap.FetchItemBodyStructure{Extended: true}})
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 || msgs[0].BodyStructure == nil {
		return nil, ErrNotFound
	}
	var found *Part
	msgs[0].BodyStructure.Walk(func(p []int, part imap.BodyStructure) bool {
		if found != nil {
			return false
		}
		if sp, ok := part.(*imap.BodyStructureSinglePart); ok {
			pp := partFrom(p, sp)
			if pp.Path == partPath {
				found = &pp
			}
		}
		return found == nil
	})
	if found == nil {
		return nil, ErrNotFound
	}
	return found, nil
}

// StreamPart writes the decoded content of a part to w.
func StreamPart(ctx context.Context, conn *imappool.Conn, folder string, uid uint32, part *Part, w io.Writer) error {
	p, err := ParsePath(part.Path)
	if err != nil {
		return err
	}
	if _, err := conn.Select(ctx, folder, true); err != nil {
		return err
	}
	sec := &imap.FetchItemBodySection{Part: p, Peek: true}
	return streamSection(ctx, conn, uid, sec, part.Encoding, w)
}

// StreamRaw writes the full RFC 5322 message to w.
func StreamRaw(ctx context.Context, conn *imappool.Conn, folder string, uid uint32, w io.Writer) error {
	if _, err := conn.Select(ctx, folder, true); err != nil {
		return err
	}
	sec := &imap.FetchItemBodySection{Peek: true}
	return streamSection(ctx, conn, uid, sec, "", w)
}

// RawMessage returns the full message bytes (used for forwarding as an
// attachment and for quoting).
func RawMessage(ctx context.Context, conn *imappool.Conn, folder string, uid uint32, limit int64) ([]byte, error) {
	var buf bytes.Buffer
	lw := &limitedWriter{w: &buf, n: limit}
	if err := StreamRaw(ctx, conn, folder, uid, lw); err != nil && !errors_isLimit(err) {
		return nil, err
	}
	return buf.Bytes(), nil
}

type limitedWriter struct {
	w io.Writer
	n int64
}

var errLimit = fmt.Errorf("size limit reached")

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, errLimit
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.w.Write(p)
	l.n -= int64(n)
	return n, err
}

func errors_isLimit(err error) bool {
	return err == errLimit || strings.Contains(err.Error(), errLimit.Error())
}

func streamSection(ctx context.Context, conn *imappool.Conn, uid uint32, sec *imap.FetchItemBodySection, encoding string, w io.Writer) error {
	var (
		found bool
		err   error
	)
	conn.Run(ctx, func() {
		cmd := conn.C.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{sec}})
		defer func() {
			if cerr := cmd.Close(); cerr != nil && err == nil && !found {
				err = cerr
			}
		}()
		for {
			msg := cmd.Next()
			if msg == nil {
				return
			}
			for {
				item := msg.Next()
				if item == nil {
					break
				}
				bs, ok := item.(imapclient.FetchItemDataBodySection)
				if !ok {
					continue
				}
				found = true
				_, err = io.Copy(w, mimeutil.DecodeBody(bs.Literal, encoding, ""))
				// drain any remainder so the connection stays in sync
				io.Copy(io.Discard, bs.Literal)
				if err != nil {
					conn.MarkBroken()
				}
			}
		}
	})
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	return nil
}

// SafeFilename returns a filename safe for Content-Disposition.
func SafeFilename(name, fallback string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = fallback
	}
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == '"' || r == '\'' || r == ';' {
			return '_'
		}
		return r
	}, name)
	if name == "." || name == ".." || name == "" {
		name = fallback
	}
	return name
}

// ParseQuery converts a search string into IMAP search criteria. Supported
// tokens: from:, to:, subject:, is:unread|read|flagged|unanswered,
// has:attachment, since:YYYY-MM-DD, before:YYYY-MM-DD, larger:N(K|M).
// Remaining words are searched in the full text.
func ParseQuery(q string) *imap.SearchCriteria {
	c := &imap.SearchCriteria{}
	for _, tok := range tokenize(q) {
		key, val := tok, ""
		if i := strings.IndexByte(tok, ':'); i > 0 && i < len(tok)-1 {
			key, val = strings.ToLower(tok[:i]), tok[i+1:]
		}
		switch key {
		case "from":
			c.Header = append(c.Header, imap.SearchCriteriaHeaderField{Key: "From", Value: val})
		case "to":
			c.Or = append(c.Or, [2]imap.SearchCriteria{
				{Header: []imap.SearchCriteriaHeaderField{{Key: "To", Value: val}}},
				{Header: []imap.SearchCriteriaHeaderField{{Key: "Cc", Value: val}}},
			})
		case "subject":
			c.Header = append(c.Header, imap.SearchCriteriaHeaderField{Key: "Subject", Value: val})
		case "is":
			switch strings.ToLower(val) {
			case "unread", "unseen":
				c.NotFlag = append(c.NotFlag, imap.FlagSeen)
			case "read", "seen":
				c.Flag = append(c.Flag, imap.FlagSeen)
			case "flagged", "starred":
				c.Flag = append(c.Flag, imap.FlagFlagged)
			case "unanswered":
				c.NotFlag = append(c.NotFlag, imap.FlagAnswered)
			case "answered", "replied":
				c.Flag = append(c.Flag, imap.FlagAnswered)
			}
		case "has":
			if strings.EqualFold(val, "attachment") {
				c.Or = append(c.Or, [2]imap.SearchCriteria{
					{Header: []imap.SearchCriteriaHeaderField{{Key: "Content-Type", Value: "multipart/mixed"}}},
					{Header: []imap.SearchCriteriaHeaderField{{Key: "Content-Disposition", Value: "attachment"}}},
				})
			}
		case "since", "after":
			if t, err := time.Parse("2006-01-02", val); err == nil {
				c.Since = t
			}
		case "before":
			if t, err := time.Parse("2006-01-02", val); err == nil {
				c.Before = t
			}
		case "larger":
			if n := parseSize(val); n > 0 {
				c.Larger = n
			}
		case "smaller":
			if n := parseSize(val); n > 0 {
				c.Smaller = n
			}
		default:
			c.Text = append(c.Text, tok)
		}
	}
	return c
}

func parseSize(s string) int64 {
	s = strings.ToUpper(strings.TrimSpace(s))
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "K"):
		mult, s = 1024, strings.TrimSuffix(s, "K")
	case strings.HasSuffix(s, "M"):
		mult, s = 1024*1024, strings.TrimSuffix(s, "M")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n * mult
}

func tokenize(q string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range q {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}
