package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"mailhearth/internal/core"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
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
		if v!=nil && !p.can(model.PermOrgManage){for i:=range v.Connections{v.Connections[i].LastAPICheckStatus=nil;v.Connections[i].LastAPIErrorCode=nil}}
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

	// 管理连接需要明确的 connectionId。
	mux.HandleFunc("GET /api/admin/connection", perm(model.PermOrgManage, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w,http.StatusGone,apiError{Error:"请使用 /api/admin/connections",Code:"api_replaced"})
	}))
	mux.HandleFunc("PUT /api/admin/connection", perm(model.PermOrgManage, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w,http.StatusGone,apiError{Error:"请使用 /api/admin/connections",Code:"api_replaced"})
	}))
	mux.HandleFunc("POST /api/admin/connection/sync", perm(model.PermOrgManage, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w,http.StatusGone,apiError{Error:"请通过 connectionId 执行同步",Code:"api_replaced"})
	}))
	mux.HandleFunc("GET /api/admin/connection/discover", perm(model.PermOrgManage, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w,http.StatusGone,apiError{Error:"请通过 connectionId 发现资源",Code:"api_replaced"})
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
		var in core.CreateMemberRequest
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		s.acceptOperation(w,r,in.RequestID,core.OperationPayload{Kind:"member.create",MemberCreate:&in},nil)
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
		var in core.MemberStatusInput
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		s.acceptOperation(w,r,in.RequestID,core.OperationPayload{Kind:"member.status",MemberID:id,MemberStatus:&in},nil)
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
		if err!=nil{s.fail(w,r,err);return};writeJSON(w,http.StatusAccepted,v.Operation)
	})))
	mux.HandleFunc("DELETE /api/admin/members/{id}", perm(model.PermMembersManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		var in struct{ExpectedRevision int64 `json:"expectedRevision"`};if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return}
		if err := s.Svc.DeleteMember(r.Context(), p.OrgID, p.Member.ID, id,in.ExpectedRevision); err != nil {
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
	for _,route:=range []string{"GET /api/admin/domains/dns-guide","POST /api/admin/domains","POST /api/admin/domains/{id}/recheck","PATCH /api/admin/domains/{id}","DELETE /api/admin/domains/{id}"}{
		mux.HandleFunc(route,perm(model.PermDomainsManage,func(w http.ResponseWriter,r *http.Request){s.fail(w,r,provider.Errorf("api_replaced","请通过 domain-bindings 指定邮件连接与域名关联"))}))
	}

	// Mailboxes
	mailboxPerm := func(kind string) string {
		if kind == model.MailboxShared {
			return model.PermSharedManage
		}
		return model.PermMailboxesManage
	}
	mux.HandleFunc("GET /api/admin/mailboxes", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		p:=principalFrom(r);if !p.can(model.PermMembersManage) && !p.can(model.PermAddressesManage) && !p.can(model.PermGroupsManage) && !p.can(model.PermMailboxesManage) && !p.can(model.PermSharedManage){s.fail(w,r,core.ErrForbidden);return}
		v, err := s.Svc.Mailboxes(r.Context(), principalFrom(r).OrgID)
		respond(w, r, v, err)
	}))
	mux.HandleFunc("POST /api/admin/mailboxes", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		var in struct{core.AttachMailboxInput;DomainBindingID int64 `json:"domainBindingId"`;LocalPart string `json:"localPart"`}
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		if !p.can(mailboxPerm(in.Kind)) {
			writeJSON(w, http.StatusForbidden, apiError{Error: "you do not have permission to create this mailbox", Code: "forbidden"})
			return
		}
		requestID:=in.RequestID;in.RequestID=""
		if in.Mode=="create"{if in.Address!=""{s.fail(w,r,&core.ValidationError{Msg:"创建邮箱的地址由域名关联和 localPart 生成"});return};create:=core.ManagedMailboxCreateInput{Mode:in.Mode,ConnectionID:in.ConnectionID,DomainBindingID:in.DomainBindingID,LocalPart:in.LocalPart,Kind:in.Kind,DisplayName:in.DisplayName,OwnerMemberID:in.OwnerMemberID,CredentialMode:in.CredentialMode,SentCopyMode:in.SentCopyMode,Credentials:in.Credentials,Endpoints:in.Endpoints,FolderMapping:in.FolderMapping};s.acceptOperation(w,r,requestID,core.OperationPayload{Kind:"mailbox.create",Create:&create},nil);return}
		if in.DomainBindingID!=0 || in.LocalPart!=""{s.fail(w,r,&core.ValidationError{Msg:"attach 请求不能包含创建字段"});return}
		s.acceptOperation(w,r,requestID,core.OperationPayload{Kind:"mailbox.attach",ConnectionID:in.ConnectionID,Attach:&in.AttachMailboxInput},[]string{"connection:"+fmt.Sprint(in.ConnectionID),"mailbox-address:"+fmt.Sprint(in.ConnectionID)+":"+in.Address})
	}))
	mux.HandleFunc("GET /api/admin/mailboxes/{id}", s.requireAuth(withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		mb, err := s.Svc.Mailbox(r.Context(), p.OrgID, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !p.can(model.PermMembersManage) && !p.can(mailboxPerm(mb.Kind)){s.fail(w,r,core.ErrForbidden);return}
		access, err := s.Svc.AccessList(r.Context(), p.OrgID, id);if err!=nil{s.fail(w,r,err);return}
		ids, err := s.Svc.Identities(r.Context(), id);if err!=nil{s.fail(w,r,err);return}
		addrs, err := s.Svc.Addresses(r.Context(), p.OrgID);if err!=nil{s.fail(w,r,err);return}
		var related []model.Address
		for _, a := range addrs {
			if a.ConnectionID!=mb.ConnectionID{continue}
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
		forwarding,err:=s.Svc.MailboxForwarding(r.Context(),p.OrgID,id);if err!=nil{s.fail(w,r,err);return}
		writeJSON(w, http.StatusOK, map[string]any{"mailbox": mb, "access": access, "identities": ids, "addresses": related,"forwarding":forwarding})
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
				if r.Method==http.MethodDelete && action=="delete"{w.WriteHeader(http.StatusNoContent);return}
				v = map[string]bool{"ok": true}
			}
			writeJSON(w, http.StatusOK, v)
		})
	}
	mux.HandleFunc("PATCH /api/admin/mailboxes/{id}", s.requireAuth(func(w http.ResponseWriter,r *http.Request){
		id,err:=pathInt(r,"id");if err!=nil{s.fail(w,r,err);return}
		var in core.UpdateMailboxInput
		if err := readJSON(r, &in); err != nil {
			s.fail(w,r,err);return
		}
		s.acceptOperation(w,r,in.RequestID,core.OperationPayload{Kind:"mailbox.update",MailboxID:id,MailboxEdit:&in},nil)
	}))
	for _,action:=range []struct{path,kind string}{{"connect","mailbox.connect"},{"rotate","mailbox.rotate"},{"reset-password","mailbox.resetPassword"},{"delete-remote","mailbox.deleteRemote"},{"revoke-remote-access","mailbox.revokeRemoteAccess"}}{
		mux.HandleFunc("POST /api/admin/mailboxes/{id}/"+action.path,s.requireAuth(func(w http.ResponseWriter,r *http.Request){id,err:=pathInt(r,"id");if err!=nil{s.fail(w,r,err);return};var in core.RemoteMailboxInput;if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return}
			if action.kind=="mailbox.connect" && in.CredentialMode=="entered"{endpoints:=core.UpdateEndpointsInput{ExpectedRevision:in.ExpectedRevision,Credentials:in.Credentials,Endpoints:in.Endpoints};s.acceptOperation(w,r,in.RequestID,core.OperationPayload{Kind:"mailbox.endpoints",MailboxID:id,Endpoints:&endpoints},nil);return}
			if len(in.Credentials)>0 || in.Endpoints.IMAP!=nil || in.Endpoints.SMTP!=nil || in.Endpoints.ManageSieve!=nil{s.fail(w,r,&core.ValidationError{Msg:"此操作不接受输入协议凭据"});return}
			s.acceptOperation(w,r,in.RequestID,core.OperationPayload{Kind:action.kind,MailboxID:id,RemoteMailbox:&in},nil)}))
	}
	mux.HandleFunc("POST /api/admin/mailboxes/{id}/suspend", s.requireAuth(func(w http.ResponseWriter,r *http.Request){
		id,err:=pathInt(r,"id");if err!=nil{s.fail(w,r,err);return};var in struct{RequestID string `json:"requestId"`;ExpectedRevision int64 `json:"expectedRevision"`};if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return};s.acceptOperation(w,r,in.RequestID,core.OperationPayload{Kind:"mailbox.suspend",MailboxID:id,ExpectedRevision:in.ExpectedRevision},nil)
	}))
	mux.HandleFunc("POST /api/admin/mailboxes/{id}/reactivate",s.requireAuth(func(w http.ResponseWriter,r *http.Request){
		p:=principalFrom(r);id,err:=pathInt(r,"id");if err!=nil{s.fail(w,r,err);return};mb,err:=s.Svc.Mailbox(r.Context(),p.OrgID,id);if err!=nil{s.fail(w,r,err);return};if !p.can(mailboxPerm(mb.Kind)){s.fail(w,r,core.ErrForbidden);return}
		var in struct{RequestID string `json:"requestId"`;ExpectedRevision int64 `json:"expectedRevision"`};if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return}
		s.acceptOperation(w,r,in.RequestID,core.OperationPayload{Kind:"mailbox.reactivate",MailboxID:id,ExpectedRevision:in.ExpectedRevision},[]string{"mailbox:"+fmt.Sprint(id)})
	}))
	mux.HandleFunc("POST /api/admin/mailboxes/{id}/forwarding",s.requireAuth(func(w http.ResponseWriter,r *http.Request){writeJSON(w,http.StatusGone,apiError{Code:"api_replaced",Error:"使用 PUT 或 DELETE 邮箱转发接口"})}))
	for _,method:=range []string{"PUT","DELETE"}{kind:="mailbox.forwarding.set";if method=="DELETE"{kind="mailbox.forwarding.delete"};mux.HandleFunc(method+" /api/admin/mailboxes/{id}/forwarding",s.requireAuth(withID("id",func(w http.ResponseWriter,r *http.Request,p *principal,id int64){var in core.ForwardingInput;if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return};s.acceptOperation(w,r,in.RequestID,core.OperationPayload{Kind:kind,MailboxID:id,Forwarding:&in},nil)})))}
	mux.HandleFunc("DELETE /api/admin/mailboxes/{id}", s.requireAuth(mailboxAction("delete", func(r *http.Request, p *principal, mb *model.Mailbox) (any, error) {
		var in struct {
			ConfirmAddress string `json:"confirmAddress"`
			ExpectedRevision int64 `json:"expectedRevision"`
		}
		if err:=readJSON(r,&in);err!=nil{return nil,err}
		return nil, s.Svc.DeleteMailbox(r.Context(), p.OrgID, p.Member.ID, mb.ID, in.ConfirmAddress,in.ExpectedRevision)
	})))
	mux.HandleFunc("GET /api/admin/mailboxes/{id}/retirement-credentials",s.requireAuth(mailboxAction("retirement",func(r *http.Request,p *principal,mb *model.Mailbox)(any,error){return s.Svc.MailboxRetirementCredentials(r.Context(),p.OrgID,mb.ID)})))
	mux.HandleFunc("POST /api/admin/mailboxes/{id}/archive",s.requireAuth(func(w http.ResponseWriter,r *http.Request){
		id,err:=pathInt(r,"id");if err!=nil{s.fail(w,r,err);return};var in struct{RequestID string `json:"requestId"`;ExpectedRevision int64 `json:"expectedRevision"`};if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return};s.acceptOperation(w,r,in.RequestID,core.OperationPayload{Kind:"mailbox.archive",MailboxID:id,ExpectedRevision:in.ExpectedRevision},nil)
	}))
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
		var in struct{ExpectedRevision int64 `json:"expectedRevision"`};if err:=readJSON(r,&in);err!=nil{return nil,err}
		return nil, s.Svc.DeleteIdentity(r.Context(), p.OrgID, p.Member.ID, mb.ID, iid,in.ExpectedRevision)
	})))
	mux.HandleFunc("POST /api/admin/mailboxes/{id}/identities/{identityId}/authorization",s.requireAuth(mailboxAction("identity",func(r *http.Request,p *principal,mb *model.Mailbox)(any,error){
		iid,err:=pathInt(r,"identityId");if err!=nil{return nil,err};var in struct{ExpectedRevision int64 `json:"expectedRevision"`;Allowed *bool `json:"allowed"`};if err:=readJSON(r,&in);err!=nil{return nil,err};if in.Allowed==nil{return nil,&core.ValidationError{Msg:"需要 allowed"}}
		return s.Svc.AuthorizeIdentity(r.Context(),p.OrgID,p.Member.ID,mb.ID,iid,in.ExpectedRevision,*in.Allowed)
	})))

	// Addresses
	mux.HandleFunc("GET /api/admin/addresses", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		p:=principalFrom(r);if !p.can(model.PermMembersManage) && !p.can(model.PermAddressesManage) && !p.can(model.PermGroupsManage) && !p.can(model.PermMailboxesManage) && !p.can(model.PermSharedManage){s.fail(w,r,core.ErrForbidden);return}
		v, err := s.Svc.Addresses(r.Context(), principalFrom(r).OrgID)
		respond(w, r, v, err)
	}))
	mux.HandleFunc("POST /api/admin/addresses", perm(model.PermAddressesManage, func(w http.ResponseWriter, r *http.Request) {
		var in core.AddressInput
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		s.acceptOperation(w,r,in.RequestID,core.OperationPayload{Kind:"address.create",Routing:&core.RoutingOperationInput{Address:&in}},nil)
	}))
	mux.HandleFunc("PATCH /api/admin/addresses/{id}", perm(model.PermAddressesManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		var in core.AddressInput
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		s.acceptOperation(w,r,in.RequestID,core.OperationPayload{Kind:"address.update",Routing:&core.RoutingOperationInput{AddressID:id,Address:&in}},nil)
	})))
	mux.HandleFunc("DELETE /api/admin/addresses/{id}", perm(model.PermAddressesManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		var in struct{RequestID string `json:"requestId"`;ExpectedRevision int64 `json:"expectedRevision"`};if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return}
		s.acceptOperation(w,r,in.RequestID,core.OperationPayload{Kind:"address.delete",Routing:&core.RoutingOperationInput{AddressID:id,ExpectedRevision:in.ExpectedRevision}},nil)
	})))

	// Groups
	mux.HandleFunc("GET /api/admin/groups", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		v, err := s.Svc.Groups(r.Context(), principalFrom(r).OrgID)
		respond(w, r, v, err)
	}))
	mux.HandleFunc("POST /api/admin/groups", perm(model.PermGroupsManage, func(w http.ResponseWriter, r *http.Request) {
		var in core.GroupInput
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		s.acceptOperation(w,r,in.RequestID,core.OperationPayload{Kind:"group.create",Routing:&core.RoutingOperationInput{Group:&in}},nil)
	}))
	mux.HandleFunc("PATCH /api/admin/groups/{id}", perm(model.PermGroupsManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		var in core.GroupInput
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		s.acceptOperation(w,r,in.RequestID,core.OperationPayload{Kind:"group.update",Routing:&core.RoutingOperationInput{GroupID:id,Group:&in}},nil)
	})))
	mux.HandleFunc("DELETE /api/admin/groups/{id}", perm(model.PermGroupsManage, withID("id", func(w http.ResponseWriter, r *http.Request, p *principal, id int64) {
		var in struct{RequestID string `json:"requestId"`;ExpectedRevision int64 `json:"expectedRevision"`};if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return}
		s.acceptOperation(w,r,in.RequestID,core.OperationPayload{Kind:"group.delete",Routing:&core.RoutingOperationInput{GroupID:id,ExpectedRevision:in.ExpectedRevision}},nil)
	})))
}
