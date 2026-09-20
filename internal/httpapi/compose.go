package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"mailhearth/internal/core"
	"mailhearth/internal/mailproto/mailops"
	"mailhearth/internal/mailproto/mimeutil"
	"mailhearth/internal/model"
)

func newJSONEncoder(w io.Writer) *json.Encoder {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc
}

// composeRequest is the payload for send and draft.
type composeRequest struct {
	IdentityID    int64               `json:"identityId"`
	To            []mailops.Recipient `json:"to"`
	Cc            []mailops.Recipient `json:"cc"`
	Bcc           []mailops.Recipient `json:"bcc"`
	Subject       string              `json:"subject"`
	Text          string              `json:"text"`
	HTML          string              `json:"html"`
	Priority      string              `json:"priority"`
	AttachmentIDs []string            `json:"attachmentIds"`
	// Parts copied from existing messages (forwarded attachments, draft attachments).
	SourceParts []struct {
		Folder string `json:"folder"`
		UID    uint32 `json:"uid"`
		Path   string `json:"path"`
	} `json:"sourceParts"`
	// Reply/forward context.
	InReplyTo *struct {
		Folder     string   `json:"folder"`
		UID        uint32   `json:"uid"`
		MessageID  string   `json:"messageId"`
		References []string `json:"references"`
		Forward    bool     `json:"forward"`
		AsAttach   bool     `json:"asAttachment"`
	} `json:"inReplyTo"`
	// Existing draft to replace.
	Draft *struct {
		Folder string `json:"folder"`
		UID    uint32 `json:"uid"`
	} `json:"draft"`
}

func (s *Server) handleCompose(m *mailReq, send bool) {
	if !m.mc.CanSend() {
		s.fail(m.w, m.r, core.ErrForbidden)
		return
	}
	var in composeRequest
	if err := readJSON(m.r, &in); err != nil {
		s.fail(m.w, m.r, err)
		return
	}
	identities, err := s.Svc.Identities(m.ctx, m.mc.Mailbox.ID)
	if err != nil {
		s.fail(m.w, m.r, err)
		return
	}
	var identity *model.Identity
	for i := range identities {
		if identities[i].ID == in.IdentityID || (in.IdentityID == 0 && identities[i].IsDefault) {
			identity = &identities[i]
			break
		}
	}
	if identity == nil {
		s.fail(m.w, m.r, &core.ValidationError{Msg: "choose a valid sender identity"})
		return
	}
	clean := func(list []mailops.Recipient) ([]mailops.Recipient, error) {
		out := list[:0]
		for _, r := range list {
			addr, err := core.NormalizeEmail(r.Address)
			if err != nil {
				return nil, err
			}
			out = append(out, mailops.Recipient{Name: strings.TrimSpace(r.Name), Address: addr})
		}
		return out, nil
	}
	if in.To, err = clean(in.To); err != nil {
		s.fail(m.w, m.r, err)
		return
	}
	if in.Cc, err = clean(in.Cc); err != nil {
		s.fail(m.w, m.r, err)
		return
	}
	if in.Bcc, err = clean(in.Bcc); err != nil {
		s.fail(m.w, m.r, err)
		return
	}
	if send && len(in.To)+len(in.Cc)+len(in.Bcc) == 0 {
		s.fail(m.w, m.r, &core.ValidationError{Msg: "add at least one recipient"})
		return
	}
	if len(in.To)+len(in.Cc)+len(in.Bcc) > 100 {
		s.fail(m.w, m.r, &core.ValidationError{Msg: "too many recipients (max 100)"})
		return
	}
	htmlBody := ""
	if strings.TrimSpace(in.HTML) != "" {
		htmlBody = mimeutil.SanitizeSignature(in.HTML)
	}
	draft := &mailops.Draft{
		From:        mailops.Recipient{Name: identity.DisplayName, Address: identity.Address},
		To:          in.To,
		Cc:          in.Cc,
		Bcc:         in.Bcc,
		Subject:     strings.TrimSpace(in.Subject),
		Text:        in.Text,
		HTML:        htmlBody,
		Priority:    in.Priority,
		Date:        time.Now(),
		Attachments: nil,
	}
	if identity.ReplyTo != "" {
		draft.ReplyTo = []mailops.Recipient{{Address: identity.ReplyTo}}
	}
	if in.InReplyTo != nil && !in.InReplyTo.Forward && in.InReplyTo.MessageID != "" {
		draft.InReplyTo = in.InReplyTo.MessageID
		draft.References = append(append([]string{}, in.InReplyTo.References...), in.InReplyTo.MessageID)
	}
	// Uploaded attachments
	var total int64
	for _, id := range in.AttachmentIDs {
		info, err := s.uploads.get(m.ctx, m.p.Member.ID, id)
		if err != nil {
			s.fail(m.w, m.r, &core.ValidationError{Msg: "attachment no longer available; please attach it again"})
			return
		}
		total += info.Size
		uid := id
		draft.Attachments = append(draft.Attachments, mailops.Attachment{Filename: info.Filename, MIME: info.MIME, Size: info.Size, Open: func() (io.ReadCloser, error) { return s.uploads.open(uid) }})
	}
	// Parts from existing messages
	for _, sp := range in.SourceParts {
		part, err := mailops.FindPart(m.ctx, m.conn, sp.Folder, sp.UID, sp.Path)
		if err != nil {
			s.fail(m.w, m.r, &core.ValidationError{Msg: "original attachment not found"})
			return
		}
		var buf bytes.Buffer
		if err := mailops.StreamPart(m.ctx, m.conn, sp.Folder, sp.UID, part, &buf); err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		total += int64(buf.Len())
		data := buf.Bytes()
		draft.Attachments = append(draft.Attachments, mailops.Attachment{Filename: mailops.SafeFilename(part.Filename, "attachment"), MIME: part.MIME, Size: int64(len(data)), Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }})
	}
	if in.InReplyTo != nil && in.InReplyTo.Forward && in.InReplyTo.AsAttach {
		raw, err := mailops.RawMessage(m.ctx, m.conn, in.InReplyTo.Folder, in.InReplyTo.UID, s.Cfg.MaxMessageBytes)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		total += int64(len(raw))
		draft.Attachments = append(draft.Attachments, mailops.Attachment{Filename: "forwarded-message.eml", MIME: "message/rfc822", Size: int64(len(raw)), Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(raw)), nil }})
	}
	if total > s.Cfg.MaxMessageBytes {
		s.fail(m.w, m.r, &core.ValidationError{Msg: fmt.Sprintf("attachments exceed the %d MB message limit", s.Cfg.MaxMessageBytes>>20)})
		return
	}
	raw, err := mailops.Build(draft)
	if err != nil {
		s.fail(m.w, m.r, err)
		return
	}
	folders, err := mailops.ListFolders(m.ctx, m.conn)
	if err != nil {
		s.fail(m.w, m.r, err)
		return
	}
	messageID := extractMessageID(raw)

	if !send {
		draftsFolder, err := mailops.EnsureFolder(m.ctx, m.conn, folders, mailops.RoleDrafts)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		uid, err := mailops.Append(m.ctx, m.conn, draftsFolder, []string{`\Draft`, `\Seen`}, time.Now(), raw)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		if in.Draft != nil && in.Draft.UID != 0 {
			mailops.Delete(m.ctx, m.conn, in.Draft.Folder, []uint32{in.Draft.UID}, "", true)
		}
		writeJSON(m.w, http.StatusOK, map[string]any{"folder": draftsFolder, "uid": uid, "messageId": messageID})
		return
	}

	rcpts := make([]string, 0, len(in.To)+len(in.Cc)+len(in.Bcc))
	for _, list := range [][]mailops.Recipient{in.To, in.Cc, in.Bcc} {
		for _, r := range list {
			rcpts = append(rcpts, r.Address)
		}
	}
	smtpCfg := mailops.SMTPConfig{Addr: s.Cfg.SMTPAddr, TLSMode: string(s.Cfg.SMTPTLS)}
	if err := mailops.Send(m.ctx, smtpCfg, m.mc.Cred.User, m.mc.Cred.Pass, identity.Address, rcpts, raw); err != nil {
		s.fail(m.w, m.r, err)
		return
	}
	result := map[string]any{"ok": true, "messageId": messageID}
	if sent, err := mailops.EnsureFolder(m.ctx, m.conn, folders, mailops.RoleSent); err == nil {
		if uid, err := mailops.Append(m.ctx, m.conn, sent, []string{`\Seen`}, time.Now(), raw); err == nil {
			result["sentFolder"], result["sentUid"] = sent, uid
		} else {
			result["warning"] = "sent, but could not save a copy to the Sent folder"
		}
	}
	for _, id := range in.AttachmentIDs {
		s.uploads.delete(m.ctx, m.p.Member.ID, id)
	}
	if in.Draft != nil && in.Draft.UID != 0 {
		mailops.Delete(m.ctx, m.conn, in.Draft.Folder, []uint32{in.Draft.UID}, "", true)
	}
	if in.InReplyTo != nil && in.InReplyTo.UID != 0 {
		flag, action := `\Answered`, "replied"
		if in.InReplyTo.Forward {
			flag, action = `$Forwarded`, "forwarded"
		}
		mailops.StoreFlags(m.ctx, m.conn, in.InReplyTo.Folder, []uint32{in.InReplyTo.UID}, []string{flag}, nil)
		sel, _ := m.conn.Select(m.ctx, in.InReplyTo.Folder, true)
		var uidv uint32
		if sel != nil {
			uidv = sel.UIDValidity
		}
		key := core.MessageKey(in.InReplyTo.MessageID, in.InReplyTo.Folder, uidv, in.InReplyTo.UID)
		detail := draft.Subject
		if len(rcpts) > 0 {
			detail = "to " + strings.Join(rcpts, ", ")
		}
		s.Svc.RecordActivity(m.ctx, m.mc.Mailbox.ID, m.p.Member.ID, key, action, detail)
		result["key"] = key
	}
	writeJSON(m.w, http.StatusOK, result)
}

func extractMessageID(raw []byte) string {
	head := raw
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i > 0 {
		head = raw[:i]
	}
	for _, line := range bytes.Split(head, []byte("\r\n")) {
		if len(line) > 11 && strings.EqualFold(string(line[:11]), "Message-ID:") {
			return strings.TrimSpace(string(line[11:]))
		}
	}
	return ""
}
