package httpapi

import (
	"net/http"

	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

func (s *Server) routesResourceOptions(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/resource-options",s.requireAuth(func(w http.ResponseWriter,r *http.Request){
		p:=principalFrom(r);allowed:=false
		for _,permission:=range []string{model.PermOrgManage,model.PermDomainsManage,model.PermMembersManage,model.PermMailboxesManage,model.PermSharedManage,model.PermAddressesManage,model.PermGroupsManage}{if p.can(permission){allowed=true}}
		if !allowed{s.fail(w,r,provider.Errorf("forbidden","没有资源管理权限"));return}
		connections,err:=s.Svc.Connections(r.Context(),p.OrgID);if err!=nil{s.fail(w,r,err);return}
		type connectionOption struct{ID int64 `json:"id"`;ProviderKind provider.ProviderKind `json:"providerKind"`;Label string `json:"label"`;Enabled bool `json:"enabled"`;Revision int64 `json:"revision"`;ProtocolDefaults provider.ProtocolTemplates `json:"protocolDefaults"`;Capabilities map[string]provider.Capability `json:"capabilities"`}
		type bindingOption struct{ID int64 `json:"id"`;DomainID int64 `json:"domainId"`;DomainName string `json:"domainName"`;ConnectionID int64 `json:"connectionId"`;ConnectionLabel string `json:"connectionLabel"`;ManagementMode string `json:"managementMode"`;RemoteState string `json:"remoteState"`;Revision int64 `json:"revision"`}
		options:=[]connectionOption{}
		for _,c:=range connections{capabilities,err:=s.Svc.ObjectCapabilities(r.Context(),p.OrgID,p.Member.ID,c.ID,0);if err!=nil{s.fail(w,r,err);return};options=append(options,connectionOption{c.ID,c.ProviderKind,c.Label,c.Enabled,c.Revision,c.ProtocolDefaults,capabilities})}
		bindings,err:=s.Svc.DomainBindings(r.Context(),p.OrgID,0,0);if err!=nil{s.fail(w,r,err);return};domains:=[]bindingOption{};for _,b:=range bindings{domains=append(domains,bindingOption{b.ID,b.DomainID,b.DomainName,b.ConnectionID,b.ConnectionLabel,b.ManagementMode,b.RemoteState,b.Revision})}
		logical,err:=s.Svc.Domains(r.Context(),p.OrgID);if err!=nil{s.fail(w,r,err);return}
		type domainOption struct{ID int64 `json:"id"`;Name string `json:"name"`};domainOptions:=[]domainOption{};for _,domain:=range logical{domainOptions=append(domainOptions,domainOption{domain.ID,domain.Name})}
		writeJSON(w,http.StatusOK,struct{Connections []connectionOption `json:"connections"`;Bindings []bindingOption `json:"bindings"`;Domains []domainOption `json:"domains"`}{options,domains,domainOptions})
	}))
}
