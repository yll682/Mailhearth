package httpapi

import (
	"net/http"
	"mailhearth/internal/core"
	"mailhearth/internal/model"
)

func (s *Server) acceptOperation(w http.ResponseWriter,r *http.Request,requestID string,payload core.OperationPayload,keys []string) {
	p:=principalFrom(r);op,repeated,err:=s.Svc.QueueOperation(r.Context(),p.OrgID,p.Member.ID,requestID,payload,keys)
	if err!=nil{s.fail(w,r,err);return};status:=http.StatusAccepted;if repeated{status=http.StatusOK};writeJSON(w,status,op)
}

func (s *Server) routesOperations(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/admin/operations/{id}/report-credential-cleanup",s.requireAuth(func(w http.ResponseWriter,r *http.Request){
		var in core.CredentialCleanupInput;if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return};p:=principalFrom(r);result,err:=s.Svc.ReportLostCredentialCleanup(r.Context(),p.OrgID,p.Member.ID,r.PathValue("id"),in);if err!=nil{s.fail(w,r,err);return};writeJSON(w,http.StatusOK,result)
	}))
	mux.HandleFunc("POST /api/admin/operations/{id}/confirm-external",s.requireAuth(func(w http.ResponseWriter,r *http.Request){
		var in core.ConfirmExternalInput;if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return};p:=principalFrom(r)
		result,err:=s.Svc.ConfirmExternalOperation(r.Context(),p.OrgID,p.Member.ID,r.PathValue("id"),in);if err!=nil{s.fail(w,r,err);return};writeJSON(w,http.StatusOK,result)
	}))
	mux.HandleFunc("POST /api/admin/operations/{id}/claim-secret",s.requireAuth(func(w http.ResponseWriter,r *http.Request){
		w.Header().Set("Cache-Control","no-store")
		var in struct{};if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return};p:=principalFrom(r)
		secret,err:=s.Svc.ClaimOperationSecret(r.Context(),p.OrgID,p.Member.ID,r.PathValue("id"));if err!=nil{s.fail(w,r,err);return};writeJSON(w,http.StatusOK,map[string]string{"password":secret})
	}))
	mux.HandleFunc("POST /api/admin/connections/{id}/sync",s.requirePerm(model.PermOrgManage,func(w http.ResponseWriter,r *http.Request){
		id,err:=pathInt(r,"id");if err!=nil{s.fail(w,r,err);return};var in struct{RequestID string `json:"requestId"`};if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return};if _,err:=s.Svc.MailConnection(r.Context(),principalFrom(r).OrgID,id);err!=nil{s.fail(w,r,err);return};s.acceptOperation(w,r,in.RequestID,core.OperationPayload{Kind:"connection.sync",ConnectionID:id},nil)
	}))
	mux.HandleFunc("GET /api/admin/operations",s.requireAuth(func(w http.ResponseWriter,r *http.Request){
		p:=principalFrom(r);v,err:=s.Svc.OperationsForMember(r.Context(),p.OrgID,p.Member.ID);if err!=nil{s.fail(w,r,err);return};writeJSON(w,http.StatusOK,v)
	}))
	mux.HandleFunc("GET /api/admin/operations/{id}",s.requireAuth(func(w http.ResponseWriter,r *http.Request){
		p:=principalFrom(r)
		op,err:=s.Svc.OperationForMember(r.Context(),p.OrgID,p.Member.ID,r.PathValue("id"));if err!=nil{s.fail(w,r,err);return};writeJSON(w,http.StatusOK,op)
	}))
	for _,action:=range []string{"cancel","retry","reconcile"}{
		mux.HandleFunc("POST /api/admin/operations/{id}/"+action,s.requireAuth(func(w http.ResponseWriter,r *http.Request){
			var in struct{};if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return};p:=principalFrom(r)
			v,err:=s.Svc.ControlOperation(r.Context(),p.OrgID,p.Member.ID,r.PathValue("id"),action);if err!=nil{s.fail(w,r,err);return};status:=http.StatusOK;if action=="retry"{status=http.StatusAccepted};writeJSON(w,status,v)
		}))
	}
	mux.HandleFunc("POST /api/admin/connections/{id}/discover",s.requirePerm(model.PermOrgManage,func(w http.ResponseWriter,r *http.Request){
		id,err:=pathInt(r,"id");if err!=nil{s.fail(w,r,err);return}
		var in struct{RequestID string `json:"requestId"`};if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return}
		if _,err:=s.Svc.MailConnection(r.Context(),principalFrom(r).OrgID,id);err!=nil{s.fail(w,r,err);return}
		s.acceptOperation(w,r,in.RequestID,core.OperationPayload{Kind:"connection.discover",ConnectionID:id},[]string{"connection:"+r.PathValue("id")})
	}))
	mux.HandleFunc("POST /api/admin/connections/{id}/import",s.requirePerm(model.PermOrgManage,func(w http.ResponseWriter,r *http.Request){
		id,err:=pathInt(r,"id");if err!=nil{s.fail(w,r,err);return}
		var in struct{RequestID string `json:"requestId"`;SnapshotID int64 `json:"snapshotId"`;SelectedResources []core.SelectedResource `json:"selectedResources"`}
		if err:=readJSON(r,&in);err!=nil{s.fail(w,r,err);return}
		if _,err:=s.Svc.MailConnection(r.Context(),principalFrom(r).OrgID,id);err!=nil{s.fail(w,r,err);return}
		s.acceptOperation(w,r,in.RequestID,core.OperationPayload{Kind:"connection.import",ConnectionID:id,Import:&core.ImportSelection{SnapshotID:in.SnapshotID,SelectedResources:in.SelectedResources}},[]string{"connection:"+r.PathValue("id")})
	}))
}
