package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"

	"mailhearth/internal/core"
	"mailhearth/internal/mailproto/mailops"
	"mailhearth/internal/mailproto/mimeutil"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
	"mailhearth/internal/secrets"
)

func newJSONEncoder(w io.Writer) *json.Encoder {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc
}

// composeRequest is the payload for send and draft.
type composeRequest struct {
	RequestID     string              `json:"requestId"`
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
	var requestDigest string
	if send {
		requestID := in.RequestID
		in.RequestID = ""
		body, err := json.Marshal(in)
		in.RequestID = requestID
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		requestDigest = s.Svc.Box.RequestDigest(body)
		prior, err := s.Svc.SubmissionByRequest(m.ctx, m.p.OrgID, m.p.Member.ID, m.mc.Mailbox.ID, requestID, requestDigest)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		if prior != nil {
			writeJSON(m.w, http.StatusOK, prior)
			return
		}
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
	if send && identity.AuthorizationStatus != "allowed" {
		s.fail(m.w, m.r, core.ErrForbidden)
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
	if m.conn == nil {
		endpoint, err := s.Svc.ResolveEndpoint(m.ctx, m.p.OrgID, m.mc.Mailbox.ID, provider.ProtocolIMAP)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		conn, err := s.Pool.Get(m.ctx, endpoint.IMAPCredential())
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		defer s.Pool.Put(conn)
		m.conn = conn
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
	_, senderDomain := core.SplitAddress(identity.Address)
	draft.MessageID = mailops.NewMessageID(senderDomain)
	if send {
		draft.SubmissionID = uuid.NewString()
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
	messageID, err := extractMessageID(raw)
	if err != nil {
		s.fail(m.w, m.r, err)
		return
	}

	if !send {
		draftsFolder, err := mailops.ResolveSpecialFolder(folders, mailops.RoleDrafts, m.mc.Mailbox.FolderMapping.Drafts)
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
	var sentFolder string
	if m.mc.Mailbox.SentCopyMode == "append" {
		sentFolder, err = mailops.ResolveSpecialFolder(folders, mailops.RoleSent, m.mc.Mailbox.FolderMapping.Sent)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
	}
	stagingFolder, err := mailops.ResolveSpecialFolder(folders, mailops.RoleDrafts, m.mc.Mailbox.FolderMapping.Drafts)
	if err != nil {
		s.fail(m.w, m.r, err)
		return
	}
	smtpEndpoint, err := s.Svc.ResolveEndpoint(m.ctx, m.p.OrgID, m.mc.Mailbox.ID, provider.ProtocolSMTP)
	if err != nil {
		s.fail(m.w, m.r, err)
		return
	}
	envelope := core.SubmissionEnvelope{IdentityID: identity.ID, EnvelopeFrom: identity.Address, Recipients: rcpts}
	if in.Draft != nil && in.Draft.UID != 0 {
		selected, err := m.conn.Select(m.ctx, in.Draft.Folder, true)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		envelope.OriginalDraft = &mailops.MessageLocator{Folder: in.Draft.Folder, UID: in.Draft.UID, UIDValidity: selected.UIDValidity}
	}
	if in.InReplyTo != nil && in.InReplyTo.UID != 0 {
		reply := in.InReplyTo
		selected, err := m.conn.Select(m.ctx, reply.Folder, true)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		envelope.ReplyContext = &core.SubmissionReplyContext{MessageLocator: mailops.MessageLocator{Folder: reply.Folder, UID: reply.UID, UIDValidity: selected.UIDValidity}, MessageID: reply.MessageID, Forward: reply.Forward}
	}
	var sent *string
	if sentFolder != "" {
		sent = &sentFolder
	}
	view, repeated, err := s.Svc.ReserveSubmission(m.ctx, m.p.OrgID, m.p.Member.ID, m.mc.Mailbox.ID, core.ReserveSubmissionInput{ID: draft.SubmissionID, RequestID: in.RequestID, RequestDigest: requestDigest, MessageID: messageID, Raw: raw, Envelope: envelope, SessionHash: secrets.HashToken(m.p.Token), SMTP: smtpEndpoint, AccessRevision: m.mc.Mailbox.AccessRevision, IdentityRevision: identity.Revision, SentCopyMode: m.mc.Mailbox.SentCopyMode, StagingFolder: stagingFolder, SentFolder: sent})
	if err != nil {
		s.fail(m.w, m.r, err)
		return
	}
	if repeated {
		writeJSON(m.w, http.StatusOK, view)
		return
	}
	locator, stageErr := mailops.AppendSubmissionMessage(m.ctx, m.conn, stagingFolder, view.ID, []string{`\Draft`, `\Seen`}, raw, s.Cfg.MaxMessageBytes)
	persistCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Svc.CompleteSubmissionStaging(persistCtx, view.ID, locator, stageErr); err != nil {
		s.fail(m.w, m.r, err)
		return
	}
	view, err = s.Svc.Submission(persistCtx, m.p.OrgID, m.p.Member.ID, m.mc.Mailbox.ID, view.ID)
	if err != nil {
		s.fail(m.w, m.r, err)
		return
	}
	status := http.StatusAccepted
	if stageErr != nil {
		status = http.StatusOK
	}
	writeJSON(m.w, status, view)
}

func (s *Server) composeSubmission(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := pathInt(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx, cancel := s.Svc.RegisterMailRequest(r.Context(), p.Member.ID, id, p.Token)
	defer cancel()
	ctx, timeoutCancel := context.WithTimeout(ctx, 90*time.Second)
	defer timeoutCancel()
	active, err := s.Svc.SessionHashActive(ctx, p.Member.ID, secrets.HashToken(p.Token))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !active {
		s.fail(w, r, core.ErrForbidden)
		return
	}
	mc, err := s.Svc.CheckMailboxAccess(ctx, p.OrgID, p.Member.ID, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.handleCompose(&mailReq{w: w, r: r.WithContext(ctx), p: p, mc: mc, ctx: ctx}, true)
}

func extractMessageID(raw []byte) (string, error) {
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	return message.Header.Get("Message-ID"), nil
}
