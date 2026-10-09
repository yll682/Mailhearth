package httpapi

import (
	"fmt"
	"net/http"

	"mailhearth/internal/core"
	"mailhearth/internal/model"
)

func (s *Server) routesEndpoints(mux *http.ServeMux) {
	withMailbox:=func(fn func(http.ResponseWriter,*http.Request,*principal,int64)) http.HandlerFunc {
		return s.requireAuth(func(w http.ResponseWriter,r *http.Request){
			id,err:=pathInt(r,"id");if err!=nil{s.fail(w,r,err);return}
			p:=principalFrom(r);mb,err:=s.Svc.Mailbox(r.Context(),p.OrgID,id);if err!=nil{s.fail(w,r,err);return}
			permission:=model.PermMailboxesManage;if mb.Kind==model.MailboxShared{permission=model.PermSharedManage}
			if !p.can(permission){s.fail(w,r,core.ErrForbidden);return}
			fn(w,r,p,id)
		})
	}
	mux.HandleFunc("GET /api/admin/mailboxes/{id}/endpoints",withMailbox(func(w http.ResponseWriter,r *http.Request,p *principal,id int64){
		out,err:=s.Svc.MailboxEndpoints(r.Context(),p.OrgID,id);if err!=nil{s.fail(w,r,err);return}
		writeJSON(w,http.StatusOK,out)
	}))
	mux.HandleFunc("PUT /api/admin/mailboxes/{id}/endpoints",withMailbox(func(w http.ResponseWriter,r *http.Request,p *principal,id int64){
		var in core.UpdateEndpointsInput;if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return}
		requestID:=in.RequestID;in.RequestID=""
		s.acceptOperation(w,r,requestID,core.OperationPayload{Kind:"mailbox.endpoints",MailboxID:id,Endpoints:&in},[]string{fmt.Sprintf("mailbox:%d",id)})
	}))
}
