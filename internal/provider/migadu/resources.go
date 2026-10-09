package migadu

import (
	"context"
	"encoding/json"
	"net/http"
	"mailhearth/internal/provider"
)

type identityDTO struct {
	LocalPart string `json:"local_part"`
	Address string `json:"address"`
	Name string `json:"name"`
	PasswordUse string `json:"password_use"`
	MaySend *bool `json:"may_send"`
}

func senderIdentityInfo(req provider.GetMailboxRequest,i identityDTO) (provider.SenderIdentityInfo,error) {
	if i.LocalPart=="" || i.Address!=i.LocalPart+"@"+req.Domain{return provider.SenderIdentityInfo{},provider.Errorf("upstream_failed","Migadu identity 地址字段不完整")}
	if i.PasswordUse!="none" && i.PasswordUse!="mailbox" && i.PasswordUse!="custom"{return provider.SenderIdentityInfo{},provider.Errorf("upstream_failed","Migadu identity password_use 无效")}
	return provider.SenderIdentityInfo{Domain:req.Domain,MailboxLocalPart:req.LocalPart,LocalPart:i.LocalPart,Address:i.Address,DisplayName:i.Name,PasswordUse:i.PasswordUse,MaySend:i.MaySend},nil
}

func (a *Adapter) ReadResource(ctx context.Context,req provider.ReadResourceRequest) (provider.DiscoverySnapshotResource,error) {
	r:=req.Resource;out:=provider.DiscoverySnapshotResource{Resource:r,Summary:map[string]string{}};out.Resource.State=provider.ResourcePresent
	domain:=r.RemoteLocator["domain"];if domain==""{return out,provider.Errorf("invalid","远程引用缺少 domain")};if _,err:=provider.CanonicalDomain(domain);err!=nil{return out,err}
	base:="/domains/"+pathSegment(domain)
	switch r.ResourceType{
	case "domain":info,err:=a.GetDomain(ctx,provider.GetDomainRequest{Domain:domain});if err!=nil{return out,err};out.Domain=&info;out.Summary["name"]=info.Name
	case "mailbox":
		local:=r.RemoteLocator["localPart"];if local==""{return out,provider.Errorf("invalid","邮箱引用缺少 localPart")};info,err:=a.GetMailbox(ctx,provider.GetMailboxRequest{Domain:domain,LocalPart:local});if err!=nil{return out,err};out.Summary["address"]=info.Address
	case "alias":
		local:=r.RemoteLocator["aliasLocalPart"];if local==""{return out,provider.Errorf("invalid","alias 引用缺少 aliasLocalPart")}
		var value struct{Address string `json:"address"`;LocalPart string `json:"local_part"`;Destinations json.RawMessage `json:"destinations"`}
		if err:=a.call(ctx,http.MethodGet,base+"/aliases/"+pathSegment(local),nil,&value);err!=nil{return out,err};if value.LocalPart!=local || value.Address!=local+"@"+domain{return out,provider.Errorf("upstream_failed","Migadu alias 的远程引用不一致")}
		targets,err:=decodeDestinations(value.Destinations);if err!=nil{return out,err};out.AddressRule=&provider.AddressRuleInfo{RemoteKey:r.RemoteKey,RemoteLocator:r.RemoteLocator,Domain:domain,LocalPart:local,Targets:targets};out.Summary["address"]=value.Address
	case "external_rule":
		name:=r.RemoteLocator["rewriteName"];if name==""{return out,provider.Errorf("invalid","rewrite 引用缺少 rewriteName")}
		var value struct{Name string `json:"name"`;Pattern string `json:"local_part_rule"`;Destinations json.RawMessage `json:"destinations"`}
		if err:=a.call(ctx,http.MethodGet,base+"/rewrites/"+pathSegment(name),nil,&value);err!=nil{return out,err};if value.Name!=name || value.Pattern==""{return out,provider.Errorf("upstream_failed","Migadu rewrite 的远程引用不一致")}
		targets,err:=decodeDestinations(value.Destinations);if err!=nil{return out,err};out.AddressRule=&provider.AddressRuleInfo{RemoteKey:r.RemoteKey,RemoteLocator:r.RemoteLocator,Domain:domain,Name:name,Pattern:value.Pattern,Targets:targets};out.Summary["name"]=name;out.Summary["pattern"]=value.Pattern
	case "identity","forwarding":
		mailbox:=r.RemoteLocator["mailboxLocalPart"];if mailbox==""{return out,provider.Errorf("invalid","远程引用缺少父邮箱 localPart")};path:=base+"/mailboxes/"+pathSegment(mailbox)
		if r.ResourceType=="identity"{
			local:=r.RemoteLocator["identityLocalPart"];if local==""{return out,provider.Errorf("invalid","identity 引用缺少 identityLocalPart")};var value identityDTO
			if err:=a.call(ctx,http.MethodGet,path+"/identities/"+pathSegment(local),nil,&value);err!=nil{return out,err};info,err:=senderIdentityInfo(provider.GetMailboxRequest{Domain:domain,LocalPart:mailbox},value);if err!=nil{return out,err};if info.LocalPart!=local{return out,provider.Errorf("upstream_failed","Migadu identity 的远程引用不一致")};out.Identity=&info;out.Summary["address"]=info.Address;out.Summary["passwordUse"]=info.PasswordUse
		}else{
			target:=r.RemoteLocator["targetAddress"];if _,err:=provider.ValidateAddress(target);err!=nil{return out,err};var value forwardingDTO
			if err:=a.call(ctx,http.MethodGet,path+"/forwardings/"+pathSegment(target),nil,&value);err!=nil{return out,err};if value.Address!=target{return out,provider.Errorf("upstream_failed","Migadu forwarding 的远程引用不一致")};out.Summary["address"]=target;out.Summary["mailboxAddress"]=mailbox+"@"+domain;out.Summary["status"]=value.status();out.Summary["deliveryMode"]="unverified";out.Forwarding=&provider.ForwardingInfo{Domain:domain,LocalPart:mailbox,Targets:[]string{target},DeliveryMode:"unverified",StatusByTarget:map[string]string{target:value.status()}}
		}
	default:return out,provider.Errorf("unsupported_operation","此资源类型没有独立读取方法")
	}
	return out,nil
}
