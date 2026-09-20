package httpapi

import (
	"net/http"
	"strings"

	"mailhearth/internal/core"
	"mailhearth/internal/model"
)

func (s *Server) routesAdmin(mux *http.ServeMux) {
	perm := s.requirePerm
	type idFn func(w http.ResponseWriter, r *http.Request, p *principal, id int64)
	withID := func(name string, fn idFn) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			id, err := pathInt(r, name)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			fn(w, r, principalFrom(r), id)
		}
	}
	ok := func(w http.ResponseWriter) { writeJSON(w, http.StatusOK, map[string]bool{"ok": true}) }
	respond := func(w http.ResponseWriter, r *http.Request, v any, err error) {
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}

	// Overview & organisation
	mux.HandleFunc("GET /api/admin/overview", perm(model.PermMembersManage, func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		v, err := s.Svc.Overview(r.Context(), p.OrgID)
		if v != nil && !p.can(model.PermBillingRead) && v.Connection != nil {
			v.Connection.Credit = ""
		}
		respond(w, r, v, err)
	}))
	mux.HandleFunc("PATCH /api/admin/org", perm(model.PermOrgManage, func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		var in struct {
			Name string `json:"name"`
		}
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		if err := s.Svc.UpdateOrg(r.Context(), p.Member.ID, in.Name); err != nil {
			s.fail(w, r, err)
			return
		}
		ok(w)
	}))
	mux.HandleFunc("POST /api/admin/org/transfer", perm(model.PermOrgOwner, func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		var in struct {
			MemberID int64 `json:"memberId"`
		}
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		if err := s.Svc.TransferOwnership(r.Context(), p.OrgID, p.Member.ID, in.MemberID); err != nil {
			s.fail(w, r, err)
			return
		}
		ok(w)
	}))
	mux.HandleFunc("GET /api/admin/audit", perm(model.PermAuditRead, func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		v, err := s.Svc.Audit(r.Context(), p.OrgID, int64(queryInt(r, "before", 0)), queryInt(r, "limit", 50))
		respond(w, r, v, err)
	}))

	// Purelymail connection
	mux.HandleFunc("GET /api/admin/connection", perm(model.PermOrgManage, func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		v, err := s.Svc.Connection(r.Context(), p.OrgID)
		respond(w, r, v, err)
	}))
	mux.HandleFunc("PUT /api/admin/connection", perm(model.PermOrgManage, func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		var in struct {
			APIToken string `json:"apiToken"`
		}
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		v, err := s.Svc.Connect(r.Context(), p.OrgID, p.Member.ID, in.APIToken)
		respond(w, r, v, err)
	}))
	mux.HandleFunc("POST /api/admin/connection/sync", perm(model.PermOrgManage, func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		v, err := s.Svc.Sync(r.Context(), p.OrgID, p.Member.ID)
		respond(w, r, v, err)
	}))
	mux.HandleFunc("GET /api/admin/connection/discover", perm(model.PermOrgManage, func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		v, err := s.Svc.Discover(r.Context(), p.OrgID)
		respond(w, r, v, err)
	}))

	// Roles
	mux.HandleFunc("GET /api/admin/roles", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		v, err := s.Svc.Roles(r.Context(), principalFrom(r).OrgID)
		respond(w, r, v, err)
	}))
	mux.HandleFunc("POST /api/admin/roles", perm(model.PermOrgManage, func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		var in core.RoleInput
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		v, err := s.Svc.CreateRole(r.Context(), p.OrgID, p.Member.ID, in)
		respond(w, r, v, err)
	}))
	mux.HandleFunc("PATCH /api/admin/roles/{id}", perm(model.PermOrgManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		var in core.RoleInput
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		v, err := s.Svc.UpdateRole(r.Context(), p.OrgID, p.Member.ID, id, in)
		respond(w, r, v, err)
	})))
	mux.HandleFunc("DELETE /api/admin/roles/{id}", perm(model.PermOrgManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		if err := s.Svc.DeleteRole(r.Context(), p.OrgID, p.Member.ID, id); err != nil {
			s.fail(w, r, err)
			return
		}
		ok(w)
	})))

	// Members
	mux.HandleFunc("GET /api/admin/members", perm(model.PermMembersManage, func(w http.ResponseWriter, r *http.Request) {
		v, err := s.Svc.Members(r.Context(), principalFrom(r).OrgID)
		respond(w, r, v, err)
	}))
	mux.HandleFunc("GET /api/admin/directory", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		// Lightweight directory for pickers (any signed-in member).
		v, err := s.Svc.Members(r.Context(), principalFrom(r).OrgID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out := make([]map[string]any, 0, len(v))
		for _, m := range v {
			if m.Status == model.MemberActive || m.Status == model.MemberInvited {
				out = append(out, map[string]any{"id": m.ID, "displayName": m.DisplayName, "title": m.Title, "department": m.Department, "status": m.Status})
			}
		}
		writeJSON(w, http.StatusOK, out)
	}))
	mux.HandleFunc("POST /api/admin/members", perm(model.PermMembersManage, func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		var in core.CreateMemberRequest
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		if (in.NewMailbox != nil || in.BindMailboxID != 0) && !p.can(model.PermMailboxesManage) {
			writeJSON(w, http.StatusForbidden, apiError{Error: "you cannot create or bind mailboxes", Code: "forbidden"})
			return
		}
		v, err := s.Svc.CreateMember(r.Context(), p.OrgID, p.Member.ID, in)
		respond(w, r, v, err)
	}))
	mux.HandleFunc("GET /api/admin/members/{id}", perm(model.PermMembersManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		m, err := s.Svc.Member(r.Context(), p.OrgID, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		mbs, _ := s.Svc.AccessibleMailboxes(r.Context(), p.OrgID, id)
		groups, _ := s.Svc.Groups(r.Context(), p.OrgID)
		var gids []int64
		for _, g := range groups {
			for _, mid := range g.MemberIDs {
				if mid == id {
					gids = append(gids, g.ID)
				}
			}
		}
		if gids == nil {
			gids = []int64{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"member": m, "mailboxes": mbs, "groupIds": gids})
	})))
	mux.HandleFunc("PATCH /api/admin/members/{id}", perm(model.PermMembersManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		var in core.MemberInput
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		v, err := s.Svc.UpdateMember(r.Context(), p.OrgID, p.Member.ID, id, in)
		respond(w, r, v, err)
	})))
	mux.HandleFunc("POST /api/admin/members/{id}/invite", perm(model.PermMembersManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		link, err := s.Svc.CreateInvite(r.Context(), p.OrgID, p.Member.ID, id)
		respond(w, r, map[string]string{"inviteLink": link}, err)
	})))
	mux.HandleFunc("POST /api/admin/members/{id}/status", perm(model.PermMembersManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		v, err := s.Svc.SetMemberStatus(r.Context(), p.OrgID, p.Member.ID, id, in.Enabled)
		respond(w, r, v, err)
	})))
	mux.HandleFunc("POST /api/admin/members/{id}/password", perm(model.PermMembersManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		var in struct {
			Password string `json:"password"`
		}
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		if err := s.Svc.SetPassword(r.Context(), p.OrgID, p.Member.ID, id, in.Password, true); err != nil {
			s.fail(w, r, err)
			return
		}
		ok(w)
	})))
	mux.HandleFunc("POST /api/admin/members/{id}/offboard", perm(model.PermMembersManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		var in core.OffboardRequest
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		v, err := s.Svc.Offboard(r.Context(), p.OrgID, p.Member.ID, id, in)
		respond(w, r, v, err)
	})))
	mux.HandleFunc("DELETE /api/admin/members/{id}", perm(model.PermMembersManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		if err := s.Svc.DeleteMember(r.Context(), p.OrgID, p.Member.ID, id); err != nil {
			s.fail(w, r, err)
			return
		}
		ok(w)
	})))

	// Domains
	mux.HandleFunc("GET /api/admin/domains", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		v, err := s.Svc.Domains(r.Context(), principalFrom(r).OrgID)
		respond(w, r, v, err)
	}))
	mux.HandleFunc("GET /api/admin/domains/dns-guide", perm(model.PermDomainsManage, func(w http.ResponseWriter, r *http.Request) {
		v, err := s.Svc.DNSGuideFor(r.Context(), principalFrom(r).OrgID, r.URL.Query().Get("domain"))
		respond(w, r, v, err)
	}))
	mux.HandleFunc("POST /api/admin/domains", perm(model.PermDomainsManage, func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		var in struct {
			Name string `json:"name"`
		}
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		v, err := s.Svc.AddDomain(r.Context(), p.OrgID, p.Member.ID, in.Name)
		respond(w, r, v, err)
	}))
	mux.HandleFunc("POST /api/admin/domains/{id}/recheck", perm(model.PermDomainsManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		v, err := s.Svc.RecheckDomain(r.Context(), p.OrgID, p.Member.ID, id)
		respond(w, r, v, err)
	})))
	mux.HandleFunc("PATCH /api/admin/domains/{id}", perm(model.PermDomainsManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		var in core.DomainSettingsInput
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		v, err := s.Svc.UpdateDomainSettings(r.Context(), p.OrgID, p.Member.ID, id, in)
		respond(w, r, v, err)
	})))
	mux.HandleFunc("DELETE /api/admin/domains/{id}", perm(model.PermDomainsManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		var in struct {
			Confirm string `json:"confirm"`
		}
		readJSON(r, &in)
		if err := s.Svc.DeleteDomain(r.Context(), p.OrgID, p.Member.ID, id, in.Confirm); err != nil {
			s.fail(w, r, err)
			return
		}
		ok(w)
	})))

	// Mailboxes
	mailboxPerm := func(kind string) string {
		if kind == model.MailboxShared {
			return model.PermSharedManage
		}
		return model.PermMailboxesManage
	}
	mux.HandleFunc("GET /api/admin/mailboxes", perm(model.PermMembersManage, func(w http.ResponseWriter, r *http.Request) {
		v, err := s.Svc.Mailboxes(r.Context(), principalFrom(r).OrgID)
		respond(w, r, v, err)
	}))
	mux.HandleFunc("POST /api/admin/mailboxes", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		var in core.CreateMailboxRequest
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		if !p.can(mailboxPerm(in.Kind)) {
			writeJSON(w, http.StatusForbidden, apiError{Error: "you do not have permission to create this mailbox", Code: "forbidden"})
			return
		}
		v, err := s.Svc.CreateMailbox(r.Context(), p.OrgID, p.Member.ID, in)
		respond(w, r, v, err)
	}))
	mux.HandleFunc("GET /api/admin/mailboxes/{id}", perm(model.PermMembersManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		mb, err := s.Svc.Mailbox(r.Context(), p.OrgID, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		access, _ := s.Svc.AccessList(r.Context(), p.OrgID, id)
		ids, _ := s.Svc.Identities(r.Context(), id)
		addrs, _ := s.Svc.Addresses(r.Context(), p.OrgID)
		var related []model.Address
		for _, a := range addrs {
			if (a.MailboxID != nil && *a.MailboxID == id) || strings.EqualFold(a.Address, mb.Address) {
				related = append(related, a)
				continue
			}
			for _, t := range a.Targets {
				if strings.EqualFold(t, mb.Address) {
					related = append(related, a)
					break
				}
			}
		}
		if related == nil {
			related = []model.Address{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"mailbox": mb, "access": access, "identities": ids, "addresses": related})
	})))
	mailboxAction := func(action string, fn func(r *http.Request, p *principal, mb *model.Mailbox) (any, error)) http.HandlerFunc {
		return withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
			mb, err := s.Svc.Mailbox(r.Context(), p.OrgID, id)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			if !p.can(mailboxPerm(mb.Kind)) {
				writeJSON(w, http.StatusForbidden, apiError{Error: "you do not have permission to manage this mailbox", Code: "forbidden"})
				return
			}
			v, err := fn(r, p, mb)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			if v == nil {
				v = map[string]bool{"ok": true}
			}
			writeJSON(w, http.StatusOK, v)
		})
	}
	mux.HandleFunc("PATCH /api/admin/mailboxes/{id}", s.requireAuth(mailboxAction("update", func(r *http.Request, p *principal, mb *model.Mailbox) (any, error) {
		var in core.UpdateMailboxInput
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		if in.Kind != nil && *in.Kind != mb.Kind && !p.can(mailboxPerm(*in.Kind)) {
			return nil, core.ErrForbidden
		}
		return s.Svc.UpdateMailbox(r.Context(), p.OrgID, p.Member.ID, mb.ID, in)
	})))
	mux.HandleFunc("POST /api/admin/mailboxes/{id}/connect", s.requireAuth(mailboxAction("connect", func(r *http.Request, p *principal, mb *model.Mailbox) (any, error) {
		if err := s.Svc.EnsureCredential(r.Context(), p.OrgID, p.Member.ID, mb.ID); err != nil {
			return nil, err
		}
		return s.Svc.Mailbox(r.Context(), p.OrgID, mb.ID)
	})))
	mux.HandleFunc("POST /api/admin/mailboxes/{id}/rotate", s.requireAuth(mailboxAction("rotate", func(r *http.Request, p *principal, mb *model.Mailbox) (any, error) {
		if err := s.Svc.RotateCredential(r.Context(), p.OrgID, p.Member.ID, mb.ID); err != nil {
			return nil, err
		}
		return s.Svc.Mailbox(r.Context(), p.OrgID, mb.ID)
	})))
	mux.HandleFunc("POST /api/admin/mailboxes/{id}/reset-password", s.requireAuth(mailboxAction("reset", func(r *http.Request, p *principal, mb *model.Mailbox) (any, error) {
		pw, err := s.Svc.ResetMailboxPassword(r.Context(), p.OrgID, p.Member.ID, mb.ID)
		if err != nil {
			return nil, err
		}
		return map[string]string{"password": pw, "imapHost": s.Cfg.IMAPAddr, "smtpHost": s.Cfg.SMTPAddr, "username": mb.Address}, nil
	})))
	mux.HandleFunc("POST /api/admin/mailboxes/{id}/suspend", s.requireAuth(mailboxAction("suspend", func(r *http.Request, p *principal, mb *model.Mailbox) (any, error) {
		if err := s.Svc.SuspendMailbox(r.Context(), p.OrgID, p.Member.ID, mb.ID); err != nil {
			return nil, err
		}
		return s.Svc.Mailbox(r.Context(), p.OrgID, mb.ID)
	})))
	mux.HandleFunc("POST /api/admin/mailboxes/{id}/reactivate", s.requireAuth(mailboxAction("reactivate", func(r *http.Request, p *principal, mb *model.Mailbox) (any, error) {
		if err := s.Svc.ReactivateMailbox(r.Context(), p.OrgID, p.Member.ID, mb.ID); err != nil {
			return nil, err
		}
		return s.Svc.Mailbox(r.Context(), p.OrgID, mb.ID)
	})))
	mux.HandleFunc("POST /api/admin/mailboxes/{id}/forwarding", s.requireAuth(mailboxAction("forward", func(r *http.Request, p *principal, mb *model.Mailbox) (any, error) {
		var in struct {
			Targets []string `json:"targets"`
		}
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		return s.Svc.SetMailboxForwarding(r.Context(), p.OrgID, p.Member.ID, mb.ID, in.Targets)
	})))
	mux.HandleFunc("DELETE /api/admin/mailboxes/{id}", s.requireAuth(mailboxAction("delete", func(r *http.Request, p *principal, mb *model.Mailbox) (any, error) {
		var in struct {
			Confirm string `json:"confirm"`
		}
		readJSON(r, &in)
		return nil, s.Svc.DeleteMailbox(r.Context(), p.OrgID, p.Member.ID, mb.ID, in.Confirm)
	})))
	mux.HandleFunc("GET /api/admin/mailboxes/{id}/access", perm(model.PermMembersManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		v, err := s.Svc.AccessList(r.Context(), p.OrgID, id)
		respond(w, r, v, err)
	})))
	mux.HandleFunc("POST /api/admin/mailboxes/{id}/access", s.requireAuth(mailboxAction("grant", func(r *http.Request, p *principal, mb *model.Mailbox) (any, error) {
		var in struct {
			MemberID int64  `json:"memberId"`
			Level    string `json:"level"`
		}
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		return s.Svc.GrantAccess(r.Context(), p.OrgID, p.Member.ID, mb.ID, in.MemberID, in.Level)
	})))
	mux.HandleFunc("DELETE /api/admin/mailboxes/{id}/access/{memberId}", s.requireAuth(mailboxAction("revoke", func(r *http.Request, p *principal, mb *model.Mailbox) (any, error) {
		mid, err := pathInt(r, "memberId")
		if err != nil {
			return nil, err
		}
		return nil, s.Svc.RevokeAccess(r.Context(), p.OrgID, p.Member.ID, mb.ID, mid)
	})))
	mux.HandleFunc("POST /api/admin/mailboxes/{id}/identities", s.requireAuth(mailboxAction("identity", func(r *http.Request, p *principal, mb *model.Mailbox) (any, error) {
		var in core.IdentityInput
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		return s.Svc.UpsertIdentity(r.Context(), p.OrgID, p.Member.ID, mb.ID, 0, in)
	})))
	mux.HandleFunc("PATCH /api/admin/mailboxes/{id}/identities/{identityId}", s.requireAuth(mailboxAction("identity", func(r *http.Request, p *principal, mb *model.Mailbox) (any, error) {
		iid, err := pathInt(r, "identityId")
		if err != nil {
			return nil, err
		}
		var in core.IdentityInput
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		return s.Svc.UpsertIdentity(r.Context(), p.OrgID, p.Member.ID, mb.ID, iid, in)
	})))
	mux.HandleFunc("DELETE /api/admin/mailboxes/{id}/identities/{identityId}", s.requireAuth(mailboxAction("identity", func(r *http.Request, p *principal, mb *model.Mailbox) (any, error) {
		iid, err := pathInt(r, "identityId")
		if err != nil {
			return nil, err
		}
		return nil, s.Svc.DeleteIdentity(r.Context(), p.OrgID, p.Member.ID, mb.ID, iid)
	})))

	// Addresses
	mux.HandleFunc("GET /api/admin/addresses", perm(model.PermMembersManage, func(w http.ResponseWriter, r *http.Request) {
		v, err := s.Svc.Addresses(r.Context(), principalFrom(r).OrgID)
		respond(w, r, v, err)
	}))
	mux.HandleFunc("POST /api/admin/addresses", perm(model.PermAddressesManage, func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		var in core.AddressInput
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		v, err := s.Svc.CreateAddress(r.Context(), p.OrgID, p.Member.ID, in)
		respond(w, r, v, err)
	}))
	mux.HandleFunc("PATCH /api/admin/addresses/{id}", perm(model.PermAddressesManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		var in core.AddressInput
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		v, err := s.Svc.UpdateAddress(r.Context(), p.OrgID, p.Member.ID, id, in)
		respond(w, r, v, err)
	})))
	mux.HandleFunc("DELETE /api/admin/addresses/{id}", perm(model.PermAddressesManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		if err := s.Svc.DeleteAddress(r.Context(), p.OrgID, p.Member.ID, id); err != nil {
			s.fail(w, r, err)
			return
		}
		ok(w)
	})))

	// Groups
	mux.HandleFunc("GET /api/admin/groups", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		v, err := s.Svc.Groups(r.Context(), principalFrom(r).OrgID)
		respond(w, r, v, err)
	}))
	mux.HandleFunc("POST /api/admin/groups", perm(model.PermGroupsManage, func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		var in core.GroupInput
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		v, err := s.Svc.CreateGroup(r.Context(), p.OrgID, p.Member.ID, in)
		respond(w, r, v, err)
	}))
	mux.HandleFunc("PATCH /api/admin/groups/{id}", perm(model.PermGroupsManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		var in core.GroupInput
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		v, err := s.Svc.UpdateGroup(r.Context(), p.OrgID, p.Member.ID, id, in)
		respond(w, r, v, err)
	})))
	mux.HandleFunc("DELETE /api/admin/groups/{id}", perm(model.PermGroupsManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		if err := s.Svc.DeleteGroup(r.Context(), p.OrgID, p.Member.ID, id); err != nil {
			s.fail(w, r, err)
			return
		}
		ok(w)
	})))
}
