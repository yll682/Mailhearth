package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"html"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mailhearth/internal/core"
	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/mailproto/mailops"
	"mailhearth/internal/mailproto/mimeutil"
	"mailhearth/internal/mailproto/sieve"
	"mailhearth/internal/model"
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
		mc, err := s.Svc.ResolveMailbox(r.Context(), p.OrgID, p.Member.ID, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		m := &mailReq{w: w, r: r, p: p, mc: mc}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		m.ctx = ctx
		if needConn {
			conn, err := s.Pool.Get(ctx, mc.Cred)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			m.conn = conn
			defer s.Pool.Put(conn)
		}
		fn(m)
	})
}

func (s *Server) sieveClient(ctx context.Context, cred imappool.Cred) (*sieve.Client, error) {
	if s.Cfg.SieveAddr == "" {
		return nil, nil
	}
	var tlsCfg *tls.Config
	c, err := sieve.Dial(ctx, s.Cfg.SieveAddr, string(s.Cfg.SieveTLS), tlsCfg)
	if err != nil {
		return nil, &core.UpstreamError{Op: "managesieve connect", Err: err}
	}
	if err := c.Authenticate(cred.User, cred.Pass); err != nil {
		c.Close()
		return nil, &core.UpstreamError{Op: "managesieve login", Err: err}
	}
	return c, nil
}

type listResponse struct {
	*mailops.Page
	Team map[string]*core.MessageState `json:"team"`
}

func (s *Server) routesMail(mux *http.ServeMux) {
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
			s.fail(m.w, m.r, &core.ValidationError{Msg: "could not create folder: " + err.Error()})
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
		if strings.EqualFold(in.Name, "INBOX") || in.NewName == "" {
			s.fail(m.w, m.r, &core.ValidationError{Msg: "this folder cannot be renamed"})
			return
		}
		if err := mailops.RenameFolder(m.ctx, m.conn, in.Name, in.NewName); err != nil {
			s.fail(m.w, m.r, &core.ValidationError{Msg: "could not rename folder: " + err.Error()})
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
		if strings.EqualFold(name, "INBOX") || name == "" {
			s.fail(m.w, m.r, &core.ValidationError{Msg: "this folder cannot be deleted"})
			return
		}
		if err := mailops.DeleteFolder(m.ctx, m.conn, name); err != nil {
			s.fail(m.w, m.r, &core.ValidationError{Msg: "could not delete folder: " + err.Error()})
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
		token := s.viewToken(m.p.Member.ID, m.mc.Mailbox.ID, folder, uint32(uid), 45*time.Minute)
		base := fmt.Sprintf("/api/mail/mailboxes/%d/messages/%d", m.mc.Mailbox.ID, uid)
		msg, err := mailops.GetMessage(m.ctx, m.conn, folder, uint32(uid), mailops.RenderOptions{
			AllowRemote: m.r.URL.Query().Get("remote") == "1",
			PartURL: func(path string) string {
				return base + "/parts/" + path + "?folder=" + urlQuery(folder) + "&t=" + token
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
		team, _ := s.Svc.GetMessageState(m.ctx, m.mc.Mailbox.ID, key)
		if m.r.URL.Query().Get("markRead") != "0" && !msg.Seen && m.mc.CanSend() {
			if err := mailops.StoreFlags(m.ctx, m.conn, folder, []uint32{msg.UID}, []string{`\Seen`}, nil); err == nil {
				msg.Seen = true
			}
		}
		writeJSON(m.w, http.StatusOK, map[string]any{
			"message":   msg,
			"viewToken": token,
			"viewUrl":   base + "/view?folder=" + urlQuery(folder) + "&t=" + token,
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
					return base + "/parts/" + path + "?folder=" + urlQuery(folder) + "&t=" + token
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
			ct := part.MIME
			disposition := "attachment"
			if r.URL.Query().Get("inline") == "1" || (part.IsInline && strings.HasPrefix(ct, "image/")) {
				disposition = "inline"
			}
			if !isSafeInlineType(ct) {
				disposition = "attachment"
				if strings.HasPrefix(ct, "text/html") || ct == "image/svg+xml" || strings.Contains(ct, "xml") {
					ct = "application/octet-stream"
				}
			}
			w.Header().Set("Content-Type", ct)
			w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, disposition, asciiName(name), urlQuery(name)))
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
		trash, err := mailops.EnsureFolder(m.ctx, m.conn, folders, mailops.RoleTrash)
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
	mux.HandleFunc("POST /api/mail/mailboxes/{id}/send", s.withMailbox(true, func(m *mailReq) { s.handleCompose(m, true) }))
	mux.HandleFunc("POST /api/mail/mailboxes/{id}/drafts", s.withMailbox(true, func(m *mailReq) { s.handleCompose(m, false) }))

	// Uploads
	mux.HandleFunc("POST /api/mail/uploads", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
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
			info, err := s.uploads.save(r.Context(), p.Member.ID, mailops.SafeFilename(part.FileName(), "attachment"), part.Header.Get("Content-Type"), part, s.Cfg.MaxUploadBytes)
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
		s.uploads.delete(r.Context(), principalFrom(r).Member.ID, r.PathValue("uploadId"))
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
		events, cancel := s.Pool.Subscribe(m.mc.Cred, folder)
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
				return
			case <-deadline.C:
				fmt.Fprint(m.w, "event: reconnect\ndata: {}\n\n")
				flusher.Flush()
				return
			case <-ping.C:
				fmt.Fprint(m.w, ": ping\n\n")
				flusher.Flush()
			case ev, ok := <-events:
				if !ok {
					fmt.Fprint(m.w, "event: reconnect\ndata: {}\n\n")
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
		script, _ := sieve.Compile(st.SieveRules, st.Vacation)
		writeJSON(m.w, http.StatusOK, map[string]any{"rules": st.SieveRules, "vacation": st.Vacation, "script": script, "lastSync": st.SieveSync, "available": s.Cfg.SieveAddr != ""})
	}))
	mux.HandleFunc("PUT /api/mail/mailboxes/{id}/rules", s.withMailbox(false, func(m *mailReq) {
		if !m.mc.CanDelete() {
			s.fail(m.w, m.r, core.ErrForbidden)
			return
		}
		var in struct {
			Rules    []model.SieveRule `json:"rules"`
			Vacation *model.Vacation   `json:"vacation"`
		}
		if err := readJSON(m.r, &in); err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		if len(in.Rules) > 100 {
			s.fail(m.w, m.r, &core.ValidationError{Msg: "too many rules"})
			return
		}
		for i := range in.Rules {
			if in.Rules[i].ID == "" {
				in.Rules[i].ID = strconv.FormatInt(time.Now().UnixNano()+int64(i), 36)
			}
		}
		script, err := sieve.Compile(in.Rules, in.Vacation)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		settings := m.mc.Mailbox.Settings
		settings.SieveRules = in.Rules
		settings.Vacation = in.Vacation
		uploaded := false
		if c, err := s.sieveClient(m.ctx, m.mc.Cred); err != nil {
			s.fail(m.w, m.r, err)
			return
		} else if c != nil {
			defer c.Logout()
			if err := c.PutScript(sieve.ScriptName, script); err != nil {
				s.fail(m.w, m.r, &core.ValidationError{Msg: "the mail server rejected the rules: " + err.Error()})
				return
			}
			if err := c.SetActive(sieve.ScriptName); err != nil {
				s.fail(m.w, m.r, &core.UpstreamError{Op: "managesieve activate", Err: err})
				return
			}
			settings.SieveSync = time.Now().UTC().Format(time.RFC3339)
			uploaded = true
		}
		if err := s.Svc.SaveMailboxSettings(m.ctx, m.mc.Mailbox.ID, settings); err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		writeJSON(m.w, http.StatusOK, map[string]any{"rules": settings.SieveRules, "vacation": settings.Vacation, "script": script, "lastSync": settings.SieveSync, "uploaded": uploaded, "available": s.Cfg.SieveAddr != ""})
	}))
	mux.HandleFunc("GET /api/mail/mailboxes/{id}/rules/server", s.withMailbox(false, func(m *mailReq) {
		c, err := s.sieveClient(m.ctx, m.mc.Cred)
		if err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		if c == nil {
			writeJSON(m.w, http.StatusOK, map[string]any{"available": false})
			return
		}
		defer c.Logout()
		scripts, err := c.ListScripts()
		if err != nil {
			s.fail(m.w, m.r, &core.UpstreamError{Op: "managesieve list", Err: err})
			return
		}
		out := map[string]any{"available": true, "scripts": scripts, "capabilities": c.Capabilities()}
		for _, sc := range scripts {
			if sc.Active {
				body, err := c.GetScript(sc.Name)
				if err == nil {
					out["active"] = sc.Name
					out["activeScript"] = body
					out["managedByMailhearth"] = sc.Name == sieve.ScriptName
				}
			}
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
		var in core.IdentityInput
		if err := readJSON(m.r, &in); err != nil {
			s.fail(m.w, m.r, err)
			return
		}
		v, err := s.Svc.UpsertIdentity(m.ctx, m.p.OrgID, m.p.Member.ID, m.mc.Mailbox.ID, iid, in)
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
	if p := principalFrom(r); p != nil {
		memberID = p.Member.ID
	} else if mid, ok := s.checkViewToken(token, id, folder, uid); ok {
		memberID = mid
	} else {
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "sign in required", Code: "unauthenticated"})
		return
	}
	org, err := s.Svc.Org(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	mc, err := s.Svc.ResolveMailbox(r.Context(), org.ID, memberID, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if token == "" {
		token = s.viewToken(memberID, id, folder, uid, 45*time.Minute)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	conn, err := s.Pool.Get(ctx, mc.Cred)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer s.Pool.Put(conn)
	fn(ctx, mc, conn, folder, uid, token)
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

func asciiName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 128 && r != '"' && r != '\\' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

func urlQuery(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(s, "%", "%25"), "&", "%26"), "+", "%2B"), " ", "%20")
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

var _ = html.EscapeString
