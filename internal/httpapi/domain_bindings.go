package httpapi

import (
	"net/http"
	"strconv"

	"mailhearth/internal/core"
	"mailhearth/internal/model"
)

func (s *Server) routesDomainBindings(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/domain-bindings",s.requirePerm(model.PermDomainsManage,func(w http.ResponseWriter,r *http.Request){
		var connectionID,domainID int64
		for name,target:=range map[string]*int64{"connectionId":&connectionID,"domainId":&domainID}{if value:=r.URL.Query().Get(name);value!=""{id,err:=strconv.ParseInt(value,10,64);if err!=nil || id<1{s.fail(w,r,&core.ValidationError{Msg:"筛选 ID 无效"});return};*target=id}}
		out,err:=s.Svc.DomainBindings(r.Context(),principalFrom(r).OrgID,connectionID,domainID);if err!=nil{s.fail(w,r,err);return};writeJSON(w,http.StatusOK,out)
	}))
	mux.HandleFunc("POST /api/admin/domain-bindings",s.requirePerm(model.PermDomainsManage,func(w http.ResponseWriter,r *http.Request){
		var in core.DomainBindingInput;if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return}
		kind:="domain.register";if in.Mode=="create"{kind="domain.create"}
		s.acceptOperation(w,r,in.RequestID,core.OperationPayload{Kind:kind,Domain:&in},nil)
	}))
	for _,route:=range []struct{method,path,kind string}{{"PATCH","","domain.configure"},{"POST","/recheck","domain.recheck"},{"POST","/activate","domain.activate"},{"POST","/delete-remote","domain.delete"}}{
		mux.HandleFunc(route.method+" /api/admin/domain-bindings/{id}"+route.path,s.requirePerm(model.PermDomainsManage,func(w http.ResponseWriter,r *http.Request){
			id,err:=pathInt(r,"id");if err!=nil{s.fail(w,r,err);return}
			var in core.DomainBindingInput;if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return};in.BindingID=id
			s.acceptOperation(w,r,in.RequestID,core.OperationPayload{Kind:route.kind,Domain:&in},nil)
		}))
	}
	mux.HandleFunc("GET /api/admin/domain-bindings/{id}/dns-records",s.requirePerm(model.PermDomainsManage,func(w http.ResponseWriter,r *http.Request){
		id,err:=pathInt(r,"id");if err!=nil{s.fail(w,r,err);return};p:=principalFrom(r)
		out,err:=s.Svc.DomainDNSRecords(r.Context(),p.OrgID,id);if err!=nil{s.fail(w,r,err);return};writeJSON(w,http.StatusOK,out)
	}))
	mux.HandleFunc("DELETE /api/admin/domain-bindings/{id}",s.requirePerm(model.PermDomainsManage,func(w http.ResponseWriter,r *http.Request){
		id,err:=pathInt(r,"id");if err!=nil{s.fail(w,r,err);return};var in struct{ExpectedRevision int64 `json:"expectedRevision"`};if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return};p:=principalFrom(r)
		if err:=s.Svc.DeleteDomainBinding(r.Context(),p.OrgID,p.Member.ID,id,in.ExpectedRevision);err!=nil{s.fail(w,r,err);return};w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("GET /api/admin/mailboxes/{id}/capabilities",s.requireAuth(func(w http.ResponseWriter,r *http.Request){
		id,err:=pathInt(r,"id");if err!=nil{s.fail(w,r,err);return};p:=principalFrom(r);mb,err:=s.Svc.Mailbox(r.Context(),p.OrgID,id);if err!=nil{s.fail(w,r,err);return}
		out,err:=s.Svc.ObjectCapabilities(r.Context(),p.OrgID,p.Member.ID,mb.ConnectionID,id);if err!=nil{s.fail(w,r,err);return};writeJSON(w,http.StatusOK,out)
	}))
}
