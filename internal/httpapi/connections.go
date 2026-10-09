package httpapi

import (
	"net/http"
	"mailhearth/internal/core"
	"mailhearth/internal/model"
)

func (s *Server) routesConnections(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/connections/{id}/billing",s.requirePerm(model.PermBillingRead,func(w http.ResponseWriter,r *http.Request){
		id,err:=pathInt(r,"id");if err!=nil{s.fail(w,r,err);return};p:=principalFrom(r);out,err:=s.Svc.ConnectionBilling(r.Context(),p.OrgID,p.Member.ID,id);if err!=nil{s.fail(w,r,err);return};w.Header().Set("Cache-Control","no-store");writeJSON(w,http.StatusOK,out)
	}))
	mux.HandleFunc("GET /api/admin/connections",s.requirePerm(model.PermOrgManage,func(w http.ResponseWriter,r *http.Request){
		p:=principalFrom(r);out,err:=s.Svc.Connections(r.Context(),p.OrgID);if err!=nil{s.fail(w,r,err);return};for i:=range out{out[i].Capabilities,err=s.Svc.ObjectCapabilities(r.Context(),p.OrgID,p.Member.ID,out[i].ID,0);if err!=nil{s.fail(w,r,err);return}};writeJSON(w,http.StatusOK,out)
	}))
	mux.HandleFunc("POST /api/admin/connections",s.requirePerm(model.PermOrgManage,func(w http.ResponseWriter,r *http.Request){
		var in core.CreateConnectionInput
		if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return}
		p:=principalFrom(r);out,err:=s.Svc.CreateConnection(r.Context(),p.OrgID,p.Member.ID,in)
		if err!=nil{s.fail(w,r,err);return};writeJSON(w,http.StatusCreated,out)
	}))
	mux.HandleFunc("GET /api/admin/connections/{id}",s.requirePerm(model.PermOrgManage,func(w http.ResponseWriter,r *http.Request){
		id,err:=pathInt(r,"id");if err!=nil{s.fail(w,r,err);return}
		p:=principalFrom(r);out,err:=s.Svc.MailConnection(r.Context(),p.OrgID,id);if err!=nil{s.fail(w,r,err);return};out.Capabilities,err=s.Svc.ObjectCapabilities(r.Context(),p.OrgID,p.Member.ID,id,0);if err!=nil{s.fail(w,r,err);return};writeJSON(w,http.StatusOK,out)
	}))
	mux.HandleFunc("PATCH /api/admin/connections/{id}",s.requirePerm(model.PermOrgManage,func(w http.ResponseWriter,r *http.Request){
		id,err:=pathInt(r,"id");if err!=nil{s.fail(w,r,err);return}
		var in core.UpdateConnectionInput
		if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return}
		if in.APIAuth!=nil || in.DomainScope!=nil || in.ProtocolDefaults!=nil{
			if _,err:=s.Svc.MailConnection(r.Context(),principalFrom(r).OrgID,id);err!=nil{s.fail(w,r,err);return}
			requestID:=in.RequestID;in.RequestID=""
			s.acceptOperation(w,r,requestID,core.OperationPayload{Kind:"connection.configure",ConnectionID:id,Connection:&in},[]string{"connection:"+r.PathValue("id")})
			return
		}
		p:=principalFrom(r);out,err:=s.Svc.UpdateConnection(r.Context(),p.OrgID,p.Member.ID,id,in)
		if err!=nil{s.fail(w,r,err);return};writeJSON(w,http.StatusOK,out)
	}))
	mux.HandleFunc("POST /api/admin/connections/{id}/test",s.requirePerm(model.PermOrgManage,func(w http.ResponseWriter,r *http.Request){
		id,err:=pathInt(r,"id");if err!=nil{s.fail(w,r,err);return}
		p:=principalFrom(r);out,err:=s.Svc.TestConnection(r.Context(),p.OrgID,p.Member.ID,id);if err!=nil{s.fail(w,r,err);return};writeJSON(w,http.StatusOK,out)
	}))
	mux.HandleFunc("DELETE /api/admin/connections/{id}",s.requirePerm(model.PermOrgManage,func(w http.ResponseWriter,r *http.Request){
		id,err:=pathInt(r,"id");if err!=nil{s.fail(w,r,err);return}
		var in struct{ExpectedRevision int64 `json:"expectedRevision"`;ConfirmLabel string `json:"confirmLabel"`}
		if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return}
		p:=principalFrom(r);if err:=s.Svc.DeleteConnection(r.Context(),p.OrgID,p.Member.ID,id,in.ExpectedRevision,in.ConfirmLabel);err!=nil{s.fail(w,r,err);return};w.WriteHeader(http.StatusNoContent)
	}))
}
