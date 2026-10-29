package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"

	"mailhearth/internal/core"
	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/mailproto/mailops"
	"mailhearth/internal/mailproto/mimeutil"
	"mailhearth/internal/mailproto/sieve"
	"mailhearth/internal/secrets"
)

// mailReq bundles what every mailbox handler needs.
type mailReq struct {
	w    http.ResponseWriter
	r    *http.Request
	p    *principal
	mc   *core.MailboxContext
	conn *imappool.Conn
	ctx  context.Context
}

func (s *Server) folderParam(r *http.Request) string {
	f := r.URL.Query().Get("folder")
	if f == "" {
		return "INBOX"
	}
	return f
}

// withMailbox resolves the mailbox, checks access and acquires a pooled
// connection. The handler must not hold the connection past return.
func (s *Server) withMailbox(needConn bool, fn func(m *mailReq)) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		id, err := pathInt(r, "id")
		if err != nil {
			s.fail(w, r, err)
			return
		}
		requestCtx, requestCancel := s.Svc.RegisterMailRequest(r.Context(), p.Member.ID, id, p.Token)
		defer requestCancel()
		r = r.WithContext(requestCtx)
		ctx, cancel := context.WithTimeout(requestCtx, 90*time.Second)
		defer cancel()
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
		if needConn {
			mc, err = s.Svc.ResolveMailbox(ctx, p.OrgID, p.Member.ID, id)
			if err != nil {
				s.fail(w, r, err)
				return
			}
		}
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		m := &mailReq{w: sw, r: r, p: p, mc: mc, ctx: ctx}
		if needConn {
			conn, err := s.Pool.Get(ctx, mc.Cred)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			m.conn = conn
			defer s.Pool.Put(conn)
			// A server-side failure (timeout, upstream error) may leave the
			// IMAP connection mid-command: never hand it back for reuse.
			defer func() {
				if sw.status >= 500 {
					conn.MarkBroken()
				}
			}()
		}
		fn(m)
	})
}

type listResponse struct {
	*mailops.Page
	Team map[string]*core.MessageState `json:"team"`
}

func (s *Server) routesMail(mux *http.ServeMux) {
	s.routesSubmissions(mux)
	s.routesFolderMapping(mux)
	mux.HandleFunc("GET /api/mail/mailboxes", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		v, err := s.Svc.AccessibleMailboxes(r.Context(), p.OrgID, p.Member.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}))

	mux.HandleFunc("GET /api/mail/contacts", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		v, err := s.Svc.SuggestContacts(r.Context(), p.OrgID, r.URL.Query().Get("q"), queryInt(r, "limit", 8))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}))

	// Folders
	mux.HandleFunc("GET /api/mail/mailboxes/{id}/folders", s.withMailbox(true, func(m *mailReq) {
		folders, err := mailops.ListFolders(m.ctx, m.conn)
		if err != nil {
			m.conn.MarkBroken()
			s.fail(m.w, m.r, err)
			return
		}
		writeJSON(m.w, http.StatusOK, folders)
	}))
	mux.HandleFunc("POST /api/mail/mailboxes/{id}/folders", s.withMailbox(true, func(m *mailReq) {
		if !m.mc.CanDelete() {
			s.fail(m.w, m.r, core.ErrForbidden)
			return
		}
		var in struct {
			Name   string `json:"name"`
			Parent string `json:"parent"`
		}
		if err := readJSON(m.r, &in); err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		name := strings.TrimSpace(in.Name)
		if name == "" || strings.ContainsAny(name, "\r\n") {
			s.fail(m.w, m.r, &core.ValidationError{Msg: "folder name is required"})
			return
		}
		if in.Parent != "" {
			folders, err := mailops.ListFolders(m.ctx, m.conn)
			if err != nil {
				m.conn.MarkBroken()
				s.fail(m.w, m.r, err)
				return
			}
			delim := "/"
			for _, f := range folders {
				if f.Name == in.Parent && f.Delim != "" {
					delim = f.Delim
				}
			}
			name = in.Parent + delim + name
		}
		if err := mailops.CreateFolder(m.ctx, m.conn, name); err != nil {
			s.folderFailure(m, "create", err)
			return
		}
		writeJSON(m.w, http.StatusOK, map[string]string{"name": name})
	}))
	mux.HandleFunc("PATCH /api/mail/mailboxes/{id}/folders", s.withMailbox(true, func(m *mailReq) {
		if !m.mc.CanDelete() {
			s.fail(m.w, m.r, core.ErrForbidden)
			return
		}
		var in struct {
			Name    string `json:"name"`
			NewName string `json:"newName"`
		}
		if err := readJSON(m.r, &in); err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		in.NewName = strings.TrimSpace(in.NewName)
		if in.Name == "" || in.NewName == "" || strings.ContainsAny(in.NewName, "\r\n") {
			s.fail(m.w, m.r, &core.ValidationError{Msg: "this folder cannot be renamed"})
			return
		}
		if protected, err := s.protectedFolder(m, in.Name); err != nil {
			s.fail(m.w, m.r, err)
			return
		} else if protected {
			s.fail(m.w, m.r, &core.ValidationError{Msg: "special folders (Inbox, Sent, Drafts, Trash, Junk, Archive) cannot be renamed"})
			return
		}
		if err := mailops.RenameFolder(m.ctx, m.conn, in.Name, in.NewName); err != nil {
			s.folderFailure(m, "rename", err)
			return
		}
		writeJSON(m.w, http.StatusOK, map[string]string{"name": in.NewName})
	}))
	mux.HandleFunc("DELETE /api/mail/mailboxes/{id}/folders", s.withMailbox(true, func(m *mailReq) {
		if !m.mc.CanDelete() {
			s.fail(m.w, m.r, core.ErrForbidden)
			return
		}
		name := m.r.URL.Query().Get("name")
		if name == "" {
			s.fail(m.w, m.r, &core.ValidationError{Msg: "this folder cannot be deleted"})
			return
		}
		if protected, err := s.protectedFolder(m, name); err != nil {
			s.fail(m.w, m.r, err)
			return
		} else if protected {
			s.fail(m.w, m.r, &core.ValidationError{Msg: "special folders (Inbox, Sent, Drafts, Trash, Junk, Archive) cannot be deleted"})
			return
		}
		if err := mailops.DeleteFolder(m.ctx, m.conn, name); err != nil {
			s.folderFailure(m, "delete", err)
			return
		}
		writeJSON(m.w, http.StatusOK, map[string]bool{"ok": true})
	}))

	// Message list
	mux.HandleFunc("GET /api/mail/mailboxes/{id}/messages", s.withMailbox(true, func(m *mailReq) {
		folder := s.folderParam(m.r)
		page, err := mailops.ListMessages(m.ctx, m.conn, folder, queryInt(m.r, "page", 0), queryInt(m.r, "size", 50), m.r.URL.Query().Get("q"))
		if err != nil {
			m.conn.MarkBroken()
			s.fail(m.w, m.r, err)
			return
		}
		keys := make([]string, 0, len(page.Messages))
		for _, msg := range page.Messages {
			keys = append(keys, core.MessageKey(msg.MessageID, folder, page.UIDValidity, msg.UID))
		}
		team, _ := s.Svc.StatesFor(m.ctx, m.mc.Mailbox.ID, keys)
		if team == nil {
			team = map[string]*core.MessageState{}
		}
		writeJSON(m.w, http.StatusOK, listResponse{Page: page, Team: team})
	}))

	// Single message (JSON)
	mux.HandleFunc("GET /api/mail/mailboxes/{id}/messages/{uid}", s.withMailbox(true, func(m *mailReq) {
		folder := s.folderParam(m.r)
		uid, err := pathInt(m.r, "uid")
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		token := s.viewToken(m.p.Member.ID, m.mc.Mailbox.ID, folder, uint32(uid), 45*time.Minute, secrets.HashToken(m.p.Token))
		base := fmt.Sprintf("/api/mail/mailboxes/%d/messages/%d", m.mc.Mailbox.ID, uid)
		msg, err := mailops.GetMessage(m.ctx, m.conn, folder, uint32(uid), mailops.RenderOptions{
			AllowRemote: m.r.URL.Query().Get("remote") == "1",
			PartURL: func(path string) string {
				return partURL(base, path, folder, token)
			},
			MaxBodyBytes: 2 << 20,
		})
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		sel, _ := m.conn.Select(m.ctx, folder, true)
		var uidv uint32
		if sel != nil {
			uidv = sel.UIDValidity
		}
		key := core.MessageKey(msg.MessageID, folder, uidv, msg.UID)
		// Reading is free of side effects: clients mark messages as read with
		// POST .../messages/flags, which carries the CSRF header.
		team, err := s.Svc.GetMessageState(m.ctx, m.mc.Mailbox.ID, key)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		writeJSON(m.w, http.StatusOK, map[string]any{
			"message":   msg,
			"viewToken": token,
			"viewUrl":   base + "/view?folder=" + url.QueryEscape(folder) + "&t=" + url.QueryEscape(token),
			"team":      team,
			"key":       key,
		})
	}))

	// Sandboxed HTML view (cookie-less, token-authenticated, own CSP)
	mux.HandleFunc("GET /api/mail/mailboxes/{id}/messages/{uid}/view", func(w http.ResponseWriter, r *http.Request) {
		s.serveWithToken(w, r, func(ctx context.Context, mc *core.MailboxContext, conn *imappool.Conn, folder string, uid uint32, token string) {
			allowRemote := r.URL.Query().Get("remote") == "1"
			base := fmt.Sprintf("/api/mail/mailboxes/%d/messages/%d", mc.Mailbox.ID, uid)
			msg, err := mailops.GetMessage(ctx, conn, folder, uid, mailops.RenderOptions{
				AllowRemote: allowRemote,
				PartURL: func(path string) string {
					return partURL(base, path, folder, token)
				},
			})
			if err != nil {
				http.Error(w, "message unavailable", http.StatusNotFound)
				return
			}
			img := "'self' data:"
			if allowRemote {
				img = "'self' data: https: http:"
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			// No scripts, no forms, no navigation; the client embeds this with a
			// sandbox attribute that keeps same-origin (for auto-sizing) but
			// never allow-scripts.
			w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src "+img+"; style-src 'unsafe-inline'; script-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'")
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("X-Frame-Options", "SAMEORIGIN")
			w.Header().Del("Content-Security-Policy-Report-Only")
			body := msg.HTML
			if body == "" {
				body = mimeutil.TextToHTML(msg.Text)
			}
			io.WriteString(w, viewDocument(body, msg.HTML == "" || strings.Contains(msg.HTML, `class="mh-text"`)))
		})
	})

	// Parts (attachments / inline images); cookie or token
	mux.HandleFunc("GET /api/mail/mailboxes/{id}/messages/{uid}/parts/{path}", func(w http.ResponseWriter, r *http.Request) {
		s.serveWithToken(w, r, func(ctx context.Context, mc *core.MailboxContext, conn *imappool.Conn, folder string, uid uint32, token string) {
			path := r.PathValue("path")
			part, err := mailops.FindPart(ctx, conn, folder, uid, path)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			name := mailops.SafeFilename(part.Filename, "part-"+path)
			ct, disposition := partHeaders(part.MIME, part.Charset, part.IsInline, r.URL.Query().Get("inline") == "1")
			w.Header().Set("Content-Type", ct)
			w.Header().Set("Content-Disposition", contentDisposition(disposition, name))
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cache-Control", "private, max-age=3600")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
			if err := mailops.StreamPart(ctx, conn, folder, uid, part, w); err != nil {
				s.Log.Debug("stream part failed", "err", err)
			}
		})
	})
	mux.HandleFunc("GET /api/mail/mailboxes/{id}/messages/{uid}/raw", func(w http.ResponseWriter, r *http.Request) {
		s.serveWithToken(w, r, func(ctx context.Context, mc *core.MailboxContext, conn *imappool.Conn, folder string, uid uint32, token string) {
			w.Header().Set("Content-Type", "message/rfc822")
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="message-%d.eml"`, uid))
			w.Header().Set("X-Content-Type-Options", "nosniff")
			mailops.StreamRaw(ctx, conn, folder, uid, w)
		})
	})

	// Actions
	mux.HandleFunc("POST /api/mail/mailboxes/{id}/messages/flags", s.withMailbox(true, func(m *mailReq) {
		if !m.mc.CanSend() {
			s.fail(m.w, m.r, core.ErrForbidden)
			return
		}
		var in struct {
			Folder string   `json:"folder"`
			UIDs   []uint32 `json:"uids"`
			Add    []string `json:"add"`
			Remove []string `json:"remove"`
		}
		if err := readJSON(m.r, &in); err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		for _, f := range append(in.Add, in.Remove...) {
			if !allowedFlag(f) {
				s.fail(m.w, m.r, &core.ValidationError{Msg: "flag not allowed: " + f})
				return
			}
		}
		if err := mailops.StoreFlags(m.ctx, m.conn, in.Folder, in.UIDs, in.Add, in.Remove); err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		writeJSON(m.w, http.StatusOK, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("POST /api/mail/mailboxes/{id}/messages/move", s.withMailbox(true, func(m *mailReq) {
		if !m.mc.CanDelete() {
			s.fail(m.w, m.r, core.ErrForbidden)
			return
		}
		var in struct {
			Folder string   `json:"folder"`
			UIDs   []uint32 `json:"uids"`
			To     string   `json:"to"`
		}
		if err := readJSON(m.r, &in); err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		if in.To == "" {
			s.fail(m.w, m.r, &core.ValidationError{Msg: "destination folder is required"})
			return
		}
		if err := mailops.Move(m.ctx, m.conn, in.Folder, in.UIDs, in.To); err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		writeJSON(m.w, http.StatusOK, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("POST /api/mail/mailboxes/{id}/messages/delete", s.withMailbox(true, func(m *mailReq) {
		if !m.mc.CanDelete() {
			s.fail(m.w, m.r, core.ErrForbidden)
			return
		}
		var in struct {
			Folder    string   `json:"folder"`
			UIDs      []uint32 `json:"uids"`
			Permanent bool     `json:"permanent"`
		}
		if err := readJSON(m.r, &in); err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		folders, err := mailops.ListFolders(m.ctx, m.conn)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		trash, err := mailops.ResolveSpecialFolder(folders, mailops.RoleTrash, m.mc.Mailbox.FolderMapping.Trash)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		if err := mailops.Delete(m.ctx, m.conn, in.Folder, in.UIDs, trash, in.Permanent); err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		writeJSON(m.w, http.StatusOK, map[string]any{"ok": true, "trash": trash})
	}))

	// Compose: send & drafts
	mux.HandleFunc("POST /api/mail/mailboxes/{id}/send", s.requireAuth(s.composeSubmission))
	mux.HandleFunc("POST /api/mail/mailboxes/{id}/drafts", s.withMailbox(true, func(m *mailReq) { s.handleCompose(m, false) }))

	// Uploads
	mux.HandleFunc("POST /api/mail/uploads", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		if !s.limiter.allow("upload:"+strconv.FormatInt(p.Member.ID, 10), 60, time.Minute) {
			writeJSON(w, http.StatusTooManyRequests, apiError{Error: "too many uploads, try again in a minute", Code: "rate_limited"})
			return
		}
		quota := s.Cfg.UploadQuotaBytes
		if quota <= 0 {
			quota = 3 * s.Cfg.MaxMessageBytes
		}
		r.Body = http.MaxBytesReader(w, r.Body, s.Cfg.MaxUploadBytes+1<<20)
		mr, err := r.MultipartReader()
		if err != nil {
			s.fail(w, r, &core.ValidationError{Msg: "multipart form expected"})
			return
		}
		var out []uploadInfo
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				s.fail(w, r, &core.ValidationError{Msg: "upload failed: " + err.Error()})
				return
			}
			if part.FormName() != "file" || part.FileName() == "" {
				continue
			}
			info, err := s.uploads.save(r.Context(), p.Member.ID, mailops.SafeFilename(part.FileName(), "attachment"), part.Header.Get("Content-Type"), part, s.Cfg.MaxUploadBytes, quota)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			out = append(out, *info)
		}
		if out == nil {
			out = []uploadInfo{}
		}
		writeJSON(w, http.StatusOK, out)
	}))
	mux.HandleFunc("DELETE /api/mail/uploads/{uploadId}", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if err := s.uploads.delete(r.Context(), principalFrom(r).Member.ID, r.PathValue("uploadId")); err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))

	// Live updates (SSE)
	mux.HandleFunc("GET /api/mail/mailboxes/{id}/events", s.withMailbox(false, func(m *mailReq) {
		flusher, ok := m.w.(http.Flusher)
		if !ok {
			s.fail(m.w, m.r, &core.ValidationError{Msg: "streaming unsupported"})
			return
		}
		folder := s.folderParam(m.r)
		mc, err := s.Svc.ResolveMailbox(m.ctx, m.p.OrgID, m.p.Member.ID, m.mc.Mailbox.ID)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		events, cancel, err := s.Pool.SubscribeChecked(mc.Cred, folder)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		defer cancel()
		h := m.w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Accel-Buffering", "no")
		m.w.WriteHeader(http.StatusOK)
		fmt.Fprintf(m.w, "event: ready\ndata: {\"folder\":%q}\n\n", folder)
		flusher.Flush()
		ping := time.NewTicker(25 * time.Second)
		defer ping.Stop()
		deadline := time.NewTimer(50 * time.Minute)
		defer deadline.Stop()
		for {
			select {
			case <-m.r.Context().Done():
				fmt.Fprint(m.w, "event: access_revoked\ndata: {}\n\n")
				flusher.Flush()
				return
			case <-deadline.C:
				fmt.Fprint(m.w, "event: reconnect\ndata: {}\n\n")
				flusher.Flush()
				return
			case <-ping.C:
				if _, err := s.Svc.CheckMailboxAccess(m.r.Context(), m.p.OrgID, m.p.Member.ID, m.mc.Mailbox.ID); err != nil {
					fmt.Fprint(m.w, "event: access_revoked\ndata: {}\n\n")
					flusher.Flush()
					return
				}
				fmt.Fprint(m.w, ": ping\n\n")
				flusher.Flush()
			case ev, ok := <-events:
				if !ok {
					fmt.Fprint(m.w, "event: reconnect\ndata: {}\n\n")
					flusher.Flush()
					return
				}
				if _, err := s.Svc.CheckMailboxAccess(m.r.Context(), m.p.OrgID, m.p.Member.ID, m.mc.Mailbox.ID); err != nil {
					fmt.Fprint(m.w, "event: access_revoked\ndata: {}\n\n")
					flusher.Flush()
					return
				}
				b, _ := jsonMarshal(ev)
				fmt.Fprintf(m.w, "event: change\ndata: %s\n\n", b)
				flusher.Flush()
			}
		}
	}))

	// Team collaboration on a message
	mux.HandleFunc("GET /api/mail/mailboxes/{id}/team", s.withMailbox(false, func(m *mailReq) {
		members, err := s.Svc.MailboxMembers(m.ctx, m.p.OrgID, m.mc.Mailbox.ID)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		writeJSON(m.w, http.StatusOK, members)
	}))
	mux.HandleFunc("POST /api/mail/mailboxes/{id}/team", s.withMailbox(false, func(m *mailReq) {
		var in struct {
			Key      string `json:"key"`
			Action   string `json:"action"` // assign | status | note
			MemberID *int64 `json:"memberId"`
			Status   string `json:"status"`
			Text     string `json:"text"`
		}
		if err := readJSON(m.r, &in); err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		if in.Key == "" {
			s.fail(m.w, m.r, &core.ValidationError{Msg: "message key is required"})
			return
		}
		// Read-only access lets people follow and annotate a conversation;
		// assigning and resolving change shared state and need send access,
		// like changing flags.
		if (in.Action == "assign" || in.Action == "status") && !m.mc.CanSend() {
			s.fail(m.w, m.r, core.ErrForbidden)
			return
		}
		var err error
		switch in.Action {
		case "assign":
			err = s.Svc.Assign(m.ctx, m.mc.Mailbox.ID, m.p.Member.ID, in.Key, in.MemberID)
		case "status":
			err = s.Svc.SetStatus(m.ctx, m.mc.Mailbox.ID, m.p.Member.ID, in.Key, in.Status)
		case "note":
			err = s.Svc.AddNote(m.ctx, m.mc.Mailbox.ID, m.p.Member.ID, in.Key, in.Text)
		default:
			err = &core.ValidationError{Msg: "unknown action"}
		}
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		st, err := s.Svc.GetMessageState(m.ctx, m.mc.Mailbox.ID, in.Key)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		writeJSON(m.w, http.StatusOK, st)
	}))

	// Rules (Sieve) and vacation
	mux.HandleFunc("GET /api/mail/mailboxes/{id}/rules", s.withMailbox(false, func(m *mailReq) {
		st := m.mc.Mailbox.Settings
		script, err := sieve.Compile(st.SieveRules, st.Vacation)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		caps, err := s.Svc.ObjectCapabilities(m.ctx, m.p.OrgID, m.p.Member.ID, m.mc.Mailbox.ConnectionID, m.mc.Mailbox.ID)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		cap := caps["rules.manage"]
		writeJSON(m.w, http.StatusOK, map[string]any{"rules": st.SieveRules, "vacation": st.Vacation, "script": script, "lastSync": st.SieveSync, "available": cap.Support == "automatic" && cap.Readiness == "ready" && cap.PermissionAllowed, "capability": cap, "vacationCapability": caps["vacation.manage"], "revision": m.mc.Mailbox.Revision})
	}))
	mux.HandleFunc("PUT /api/mail/mailboxes/{id}/rules", s.withMailbox(false, func(m *mailReq) {
		if !m.mc.CanDelete() {
			s.fail(m.w, m.r, core.ErrForbidden)
			return
		}
		var in core.RulesOperationInput
		if err := readJSON(m.r, &in); err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		in.SessionHash = secrets.HashToken(m.p.Token)
		out, _, err := s.Svc.QueueOperation(m.ctx, m.p.OrgID, m.p.Member.ID, in.RequestID, core.OperationPayload{Kind: "mailbox.rules", MailboxID: m.mc.Mailbox.ID, Rules: &in, SessionHash: in.SessionHash}, nil)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		writeJSON(m.w, http.StatusAccepted, out)
	}))
	mux.HandleFunc("GET /api/mail/mailboxes/{id}/rules/server", s.withMailbox(false, func(m *mailReq) {
		out, err := s.Svc.SieveServer(m.ctx, m.p.OrgID, m.p.Member.ID, m.mc.Mailbox.ID)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		writeJSON(m.w, http.StatusOK, out)
	}))

	// Identities (self-service for the mailbox owner / full access)
	mux.HandleFunc("PUT /api/mail/mailboxes/{id}/identities/{identityId}", s.withMailbox(false, func(m *mailReq) {
		if !m.mc.CanDelete() {
			s.fail(m.w, m.r, core.ErrForbidden)
			return
		}
		iid, err := pathInt(m.r, "identityId")
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		var in core.IdentitySettingsInput
		if err := readJSON(m.r, &in); err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		v, err := s.Svc.UpdateIdentitySettings(m.ctx, m.p.OrgID, m.p.Member.ID, m.mc.Mailbox.ID, iid, in)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		writeJSON(m.w, http.StatusOK, v)
	}))
}

// serveWithToken authenticates either by session cookie or by a signed
// view token (needed inside the sandboxed iframe where cookies are absent).
func (s *Server) serveWithToken(w http.ResponseWriter, r *http.Request, fn func(ctx context.Context, mc *core.MailboxContext, conn *imappool.Conn, folder string, uid uint32, token string)) {
	id, err := pathInt(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	uid64, err := pathInt(r, "uid")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	uid := uint32(uid64)
	folder := s.folderParam(r)
	token := r.URL.Query().Get("t")
	var memberID int64
	var sessionHash string
	if p := principalFrom(r); p != nil {
		memberID = p.Member.ID
		sessionHash = secrets.HashToken(p.Token)
		token = ""
	} else if mid, hash, ok := s.checkViewToken(token, id, folder, uid); ok {
		memberID = mid
		sessionHash = hash
	} else {
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "sign in required", Code: "unauthenticated"})
		return
	}
	requestCtx, requestCancel := s.Svc.RegisterMailRequestSessionHash(r.Context(), memberID, id, sessionHash)
	defer requestCancel()
	ctx, cancel := context.WithTimeout(requestCtx, 120*time.Second)
	defer cancel()
	active, err := s.Svc.SessionHashActive(ctx, memberID, sessionHash)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !active {
		s.fail(w, r, core.ErrForbidden)
		return
	}
	orgID, err := s.orgID(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	mc, err := s.Svc.ResolveMailbox(ctx, orgID, memberID, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if token == "" {
		token = s.viewToken(memberID, id, folder, uid, 45*time.Minute, sessionHash)
	}
	conn, err := s.Pool.Get(ctx, mc.Cred)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer s.Pool.Put(conn)
	fn(ctx, mc, conn, folder, uid, token)
}

// folderFailure answers a failed CREATE/RENAME/DELETE. A refusal by the
// server (NO/BAD) is reported with its standard response code only, not the
// server's free-form text; any other failure breaks the connection.
func (s *Server) folderFailure(m *mailReq, op string, err error) {
	var refused *imap.Error
	if errors.As(err, &refused) {
		msg := "the mail server refused to " + op + " this folder"
		if refused.Code != "" {
			msg += " (" + strings.ToLower(string(refused.Code)) + ")"
		}
		s.fail(m.w, m.r, &core.ValidationError{Msg: msg})
		return
	}
	m.conn.MarkBroken()
	s.fail(m.w, m.r, err)
}

// protectedFolder reports whether name is INBOX or a folder the mailbox
// uses for a special role (mapped explicitly or by SPECIAL-USE); sending,
// drafts and deletion depend on those folders existing.
func (s *Server) protectedFolder(m *mailReq, name string) (bool, error) {
	if strings.EqualFold(name, "INBOX") {
		return true, nil
	}
	folders, err := mailops.ListFolders(m.ctx, m.conn)
	if err != nil {
		m.conn.MarkBroken()
		return false, err
	}
	mapping := m.mc.Mailbox.FolderMapping
	for role, explicit := range map[string]*string{mailops.RoleSent: mapping.Sent, mailops.RoleDrafts: mapping.Drafts, mailops.RoleTrash: mapping.Trash, mailops.RoleJunk: mapping.Junk, mailops.RoleArchive: mapping.Archive} {
		if resolved, err := mailops.ResolveSpecialFolder(folders, role, explicit); err == nil && resolved == name {
			return true, nil
		}
		if explicit != nil && *explicit == name {
			return true, nil
		}
	}
	return false, nil
}

func allowedFlag(f string) bool {
	switch f {
	case `\Seen`, `\Flagged`, `\Answered`, `\Draft`, `$Forwarded`, `$Junk`, `$NotJunk`, `$Important`:
		return true
	}
	return false
}

func isSafeInlineType(ct string) bool {
	ct = strings.ToLower(ct)
	switch {
	case strings.HasPrefix(ct, "image/") && ct != "image/svg+xml":
		return true
	case ct == "application/pdf", strings.HasPrefix(ct, "audio/"), strings.HasPrefix(ct, "video/"), ct == "text/plain":
		return true
	}
	return false
}

// partHeaders decides how a message part is served. The declared type is
// normalised first; anything outside the inline allow-list is served as an
// opaque download whatever its spelling, so a part can never be rendered
// as an active document.
func partHeaders(declared, charset string, partInline, wantInline bool) (contentType, disposition string) {
	mediaType, _, err := mime.ParseMediaType(declared)
	if err != nil || !isSafeInlineType(mediaType) {
		return "application/octet-stream", "attachment"
	}
	mediaType = strings.ToLower(mediaType)
	disposition = "attachment"
	if wantInline || (partInline && strings.HasPrefix(mediaType, "image/")) {
		disposition = "inline"
	}
	contentType = mediaType
	if mediaType == "text/plain" && charset != "" {
		if formatted := mime.FormatMediaType(mediaType, map[string]string{"charset": charset}); formatted != "" {
			contentType = formatted
		}
	}
	return contentType, disposition
}

// contentDisposition builds a Content-Disposition value with an ASCII
// fallback name and an RFC 5987 encoded UTF-8 name.
func contentDisposition(disposition, name string) string {
	return fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, disposition, asciiName(name), rfc5987Encode(name))
}

func asciiName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r >= 32 && r < 127 && r != '"' && r != '\\' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

// rfc5987Encode percent-encodes every byte outside RFC 5987 attr-char.
func rfc5987Encode(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("!#$&+-.^_`|~", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}

func partURL(base, path, folder, token string) string {
	return base + "/parts/" + url.PathEscape(path) + "?folder=" + url.QueryEscape(folder) + "&t=" + url.QueryEscape(token)
}

func jsonMarshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := newJSONEncoder(&buf)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// viewDocument wraps a sanitized fragment in a minimal document whose
// styles make both HTML mail and plain text look right in the frame.
func viewDocument(fragment string, isText bool) string {
	textCSS := ""
	if isText {
		textCSS = "body{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:13.5px;white-space:pre-wrap;word-break:break-word}"
	}
	return `<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="color-scheme" content="light"><style>
html,body{margin:0;padding:0;background:#fff;color:#1c1c1e}
body{padding:16px 20px;font:15px/1.55 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Helvetica Neue",Arial,"Noto Sans","PingFang SC","Microsoft YaHei",sans-serif;overflow-wrap:anywhere}
img{max-width:100%;height:auto}
img.mh-blocked{background:#f2f2f7;border:1px dashed #c7c7cc;min-width:24px;min-height:24px}
table{max-width:100%}
pre{white-space:pre-wrap}
blockquote,.mh-quote{margin:4px 0 4px 0;padding-left:12px;border-left:3px solid #d1d1d6;color:#48484a}
.mh-quote-2{border-left-color:#a2a2a8}.mh-quote-3{border-left-color:#8e8e93}
a{color:#0a58ca}
` + textCSS + `</style></head><body>` + fragment + `</body></html>`
}
