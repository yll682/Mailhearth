package migadu

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mailhearth/internal/provider"
)

type Adapter struct {
	base     string
	username string
	apiKey   string
	http     *http.Client
}

var _ provider.Provider = (*Adapter)(nil)

func New(apiBaseURL, username, apiKey string) *Adapter {
	if apiBaseURL == "" {
		apiBaseURL = "https://api.migadu.com/v1"
	}
	return &Adapter{
		base: strings.TrimRight(apiBaseURL, "/"), username: username, apiKey: apiKey,
		http: &http.Client{Timeout: 40 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Host != via[0].Host {
				return http.ErrUseLastResponse
			}
			return nil
		}},
	}
}

func (a *Adapter) Kind() provider.ProviderKind { return provider.Migadu }

func (a *Adapter) ValidateConnection(ctx context.Context, req provider.ValidateConnectionRequest) (provider.ValidateConnectionResult, error) {
	if req.APIBaseURL == "" || req.Username == "" || req.APIKey == "" {
		return provider.ValidateConnectionResult{}, provider.Errorf("invalid", "Migadu API base URL, username and key are required")
	}
	if err := provider.ValidateProtocolTemplates(req.Protocols); err != nil {
		return provider.ValidateConnectionResult{}, err
	}
	var domains []struct {
		Name string `json:"name"`
	}
	if err := a.call(ctx, http.MethodGet, "/domains", nil, &domains); err != nil {
		return provider.ValidateConnectionResult{OK: false, Code: "provider_auth_failed", Message: err.Error()}, err
	}
	return provider.ValidateConnectionResult{OK: true}, nil
}

func (a *Adapter) call(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, reader)
	if err != nil {
		return err
	}
	req.SetBasicAuth(a.username, a.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return provider.Errorf("upstream_failed","Migadu API 请求失败")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code:="upstream_failed"
		if resp.StatusCode==401 || resp.StatusCode==403 { code="provider_auth_failed" }
		if resp.StatusCode==429 { code="upstream_rate_limited" }
		if resp.StatusCode==404 { code="not_found" }
		return provider.Errorf(code,"Migadu API 返回 HTTP %d",resp.StatusCode)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("migadu: %s: decode response: %w", path, err)
	}
	return nil
}

type HTTPError struct {
	Status int
	Path   string
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("migadu: http %d on %s: %s", e.Status, e.Path, e.Body)
}

func pathSegment(v string) string { return url.PathEscape(v) }

type forwardingDTO struct {
	Address string `json:"address"`
	BlockedAt *string `json:"blocked_at"`
	ConfirmationSentAt *string `json:"confirmation_sent_at"`
	ConfirmedAt *string `json:"confirmed_at"`
	Active *bool `json:"is_active"`
}

func (f forwardingDTO) status() string {
	if f.BlockedAt!=nil{return "blocked"}
	if f.ConfirmedAt==nil && f.ConfirmationSentAt!=nil{return "pending_confirmation"}
	if f.Active!=nil && *f.Active && f.ConfirmedAt!=nil{return "active"}
	return "unknown"
}

func decodeDestinations(raw json.RawMessage) ([]string,error) {
	var decoded any;if err:=json.Unmarshal(raw,&decoded);err!=nil{return nil,provider.Errorf("upstream_failed","Migadu destinations JSON 无效")}
	values:=[]string{}
	switch value:=decoded.(type){
	case []any:for _,item:=range value{text,ok:=item.(string);if !ok{return nil,provider.Errorf("upstream_failed","Migadu destinations 必须包含邮件地址")};values=append(values,text)}
	case string:
		if value==""{return values,nil}
		reader:=csv.NewReader(strings.NewReader(value));reader.TrimLeadingSpace=true
		var err error;values,err=reader.Read();if err!=nil{return nil,provider.Errorf("upstream_failed","Migadu destinations CSV 无效")};if _,err:=reader.Read();err!=io.EOF{return nil,provider.Errorf("upstream_failed","Migadu destinations 只能包含一行 CSV")}
	default:return nil,provider.Errorf("upstream_failed","Migadu destinations 格式无效")
	}
	for i,value:=range values{value=strings.TrimSpace(value);if _,err:=provider.ValidateAddress(value);err!=nil{return nil,provider.Errorf("upstream_failed","Migadu destinations 包含无效地址")};values[i]=value}
	return values,nil
}

type domainDTO struct {
	Name      string `json:"name"`
}

func domainInfo(d domainDTO) provider.DomainInfo {
	return provider.DomainInfo{
		Name: d.Name,
		DNS: provider.DNSStatus{MX:"unknown",SPF:"unknown",DKIM:"unknown",DMARC:"unknown"},
	}
}

func (a *Adapter) Discover(ctx context.Context, req provider.DiscoverRequest) (provider.DiscoverResult, error) {
	var domains []domainDTO
	if err := a.call(ctx, http.MethodGet, "/domains", nil, &domains); err != nil {
		return provider.DiscoverResult{}, err
	}
	resources := make([]provider.DiscoverySnapshotResource, 0, len(domains))
	for _, d := range domains {
		if !scopeContains(req.Scope, d.Name) {
			continue
		}
		info:=domainInfo(d)
		resources = append(resources, provider.DiscoverySnapshotResource{
			Resource: provider.Resource{
				ResourceType: "domain", RemoteKey: strings.ToLower(d.Name),
				RemoteLocator: map[string]string{"domain": strings.ToLower(d.Name)},
				Purpose:       provider.PurposeDomain, State: provider.ResourcePresent,
			},
			Summary: map[string]string{"name": d.Name},
			Domain:&info,
		})
		var mailboxes []mailboxDTO
		if err:=a.call(ctx,http.MethodGet,"/domains/"+pathSegment(d.Name)+"/mailboxes",nil,&mailboxes);err!=nil{return provider.DiscoverResult{},err}
		for _,mailbox:=range mailboxes{
			info,err:=mailboxInfo(mailbox);if err!=nil{return provider.DiscoverResult{},err}
			resources=append(resources,provider.DiscoverySnapshotResource{Resource:provider.Resource{ResourceType:"mailbox",RemoteKey:info.Address,RemoteLocator:map[string]string{"domain":info.Domain,"localPart":info.LocalPart,"address":info.Address},Purpose:provider.PurposeMailbox,State:provider.ResourcePresent},Summary:map[string]string{"address":info.Address}})
			identities,err:=a.listMailboxIdentities(ctx,provider.GetMailboxRequest{Domain:info.Domain,LocalPart:info.LocalPart});if err!=nil{return provider.DiscoverResult{},err}
			for _,identity:=range identities{purpose:=provider.PurposeSenderIdentity;if identity.PasswordUse=="custom"{purpose=provider.PurposeLoginCredential};value:=identity;resources=append(resources,provider.DiscoverySnapshotResource{Resource:provider.Resource{ResourceType:"identity",RemoteKey:info.Address+"/"+identity.LocalPart,RemoteLocator:map[string]string{"domain":info.Domain,"mailboxLocalPart":info.LocalPart,"identityLocalPart":identity.LocalPart,"address":identity.Address},Purpose:purpose,State:provider.ResourcePresent},Summary:map[string]string{"address":identity.Address,"passwordUse":identity.PasswordUse},Identity:&value})}
			var forwardings []forwardingDTO
			if err:=a.call(ctx,http.MethodGet,"/domains/"+pathSegment(info.Domain)+"/mailboxes/"+pathSegment(info.LocalPart)+"/forwardings",nil,&forwardings);err!=nil{return provider.DiscoverResult{},err}
			for _,forwarding:=range forwardings{if _,err:=provider.ValidateAddress(forwarding.Address);err!=nil{return provider.DiscoverResult{},provider.Errorf("upstream_failed","Migadu forwarding 地址无效")};resources=append(resources,provider.DiscoverySnapshotResource{Resource:provider.Resource{ResourceType:"forwarding",RemoteKey:info.Address+"/"+forwarding.Address,RemoteLocator:map[string]string{"domain":info.Domain,"mailboxLocalPart":info.LocalPart,"targetAddress":forwarding.Address},Purpose:provider.PurposeForwarding,State:provider.ResourcePresent},Summary:map[string]string{"address":forwarding.Address,"mailboxAddress":info.Address,"status":forwarding.status(),"deliveryMode":"unverified"},Forwarding:&provider.ForwardingInfo{Domain:info.Domain,LocalPart:info.LocalPart,Targets:[]string{forwarding.Address},DeliveryMode:"unverified",StatusByTarget:map[string]string{forwarding.Address:forwarding.status()}}})}
		}
		var aliases []struct{Address string `json:"address"`;LocalPart string `json:"local_part"`;Destinations json.RawMessage `json:"destinations"`}
		if err:=a.call(ctx,http.MethodGet,"/domains/"+pathSegment(d.Name)+"/aliases",nil,&aliases);err!=nil{return provider.DiscoverResult{},err}
		for _,alias:=range aliases{targets,err:=decodeDestinations(alias.Destinations);if err!=nil{return provider.DiscoverResult{},err};locator:=map[string]string{"domain":d.Name,"aliasLocalPart":alias.LocalPart,"address":alias.Address};resources=append(resources,provider.DiscoverySnapshotResource{Resource:provider.Resource{ResourceType:"alias",RemoteKey:alias.Address,RemoteLocator:locator,Purpose:provider.PurposeRouting,State:provider.ResourcePresent},Summary:map[string]string{"address":alias.Address},AddressRule:&provider.AddressRuleInfo{RemoteKey:alias.Address,RemoteLocator:locator,Domain:d.Name,LocalPart:alias.LocalPart,Targets:targets}})}
		var rewrites []struct{Name string `json:"name"`;Rule string `json:"local_part_rule"`;Destinations json.RawMessage `json:"destinations"`}
		if err:=a.call(ctx,http.MethodGet,"/domains/"+pathSegment(d.Name)+"/rewrites",nil,&rewrites);err!=nil{return provider.DiscoverResult{},err}
		for _,rewrite:=range rewrites{targets,err:=decodeDestinations(rewrite.Destinations);if err!=nil{return provider.DiscoverResult{},err};locator:=map[string]string{"domain":d.Name,"rewriteName":rewrite.Name};resources=append(resources,provider.DiscoverySnapshotResource{Resource:provider.Resource{ResourceType:"external_rule",RemoteKey:d.Name+"/"+rewrite.Name,RemoteLocator:locator,Purpose:provider.PurposeRouting,State:provider.ResourcePresent},Summary:map[string]string{"name":rewrite.Name,"pattern":rewrite.Rule},AddressRule:&provider.AddressRuleInfo{RemoteKey:d.Name+"/"+rewrite.Name,RemoteLocator:locator,Domain:d.Name,Name:rewrite.Name,Pattern:rewrite.Rule,Targets:targets}})}
	}
	return provider.DiscoverResult{Snapshot: provider.DiscoverySnapshot{
		ConnectionID: 0, ConnectionRevision: 0, Scope: req.Scope, Resources: resources, Complete: true,
	}}, nil
}

func scopeContains(scope provider.DomainScope, domain string) bool {
	domain = strings.ToLower(domain)
	if scope.Mode == "" || scope.Mode == "all" {
		return true
	}
	for _, d := range scope.Domains {
		if strings.EqualFold(d, domain) {
			return true
		}
	}
	return false
}

func (a *Adapter) GetDomain(ctx context.Context, req provider.GetDomainRequest) (provider.DomainInfo, error) {
	var d domainDTO
	if err := a.call(ctx, http.MethodGet, "/domains/"+pathSegment(req.Domain), nil, &d); err != nil {
		return provider.DomainInfo{}, err
	}
	return domainInfo(d), nil
}

func (a *Adapter) CreateDomain(ctx context.Context, req provider.CreateDomainRequest) (provider.CreateDomainResult, error) {
	var d domainDTO
	if err := a.call(ctx, http.MethodPost, "/domains", map[string]string{"name": req.Domain}, &d); err != nil {
		return provider.CreateDomainResult{}, err
	}
	return provider.CreateDomainResult{Domain: domainInfo(d)}, nil
}

func (a *Adapter) UpdateDomain(ctx context.Context, req provider.UpdateDomainRequest) (provider.UpdateDomainResult, error) {
	return provider.UpdateDomainResult{}, provider.Errorf("unsupported_operation", "Migadu domain settings must be managed in the Migadu console")
}

func (a *Adapter) DeleteDomain(ctx context.Context, req provider.DeleteDomainRequest) (provider.DeleteDomainResult, error) {
	return provider.DeleteDomainResult{}, provider.Errorf("external_action_required", "Migadu domains are deleted in the Migadu console")
}

func (a *Adapter) GetDNSRecords(ctx context.Context, req provider.GetDNSRecordsRequest) (provider.GetDNSRecordsResult, error) {
	var out struct {
		DKIM []dnsRecordDTO `json:"dkim"`
		DMARC dnsRecordDTO `json:"dmarc"`
		Verification dnsRecordDTO `json:"dns_verification"`
		MX []dnsRecordDTO `json:"mx_records"`
		SPF dnsRecordDTO `json:"spf"`
	}
	if err := a.call(ctx, http.MethodGet, "/domains/"+pathSegment(req.Domain)+"/records", nil, &out); err != nil {
		return provider.GetDNSRecordsResult{}, err
	}
	records:=[]provider.DomainRecord{}
	appendRecord:=func(record dnsRecordDTO,purpose string)error{
		if record.Type=="" || record.Name=="" || record.Value==""{return provider.Errorf("upstream_failed","Migadu DNS 记录字段不完整")}
		records=append(records,provider.DomainRecord{Type:strings.ToUpper(record.Type),Host:record.Name,Value:record.Value,Priority:record.Priority,Purpose:purpose});return nil
	}
	for _,record:=range out.DKIM{if err:=appendRecord(record,"dkim");err!=nil{return provider.GetDNSRecordsResult{},err}}
	for _,record:=range out.MX{if err:=appendRecord(record,"mx");err!=nil{return provider.GetDNSRecordsResult{},err}}
	for _,item:=range []struct{record dnsRecordDTO;purpose string}{{out.DMARC,"dmarc"},{out.Verification,"ownership"},{out.SPF,"spf"}}{if err:=appendRecord(item.record,item.purpose);err!=nil{return provider.GetDNSRecordsResult{},err}}
	return provider.GetDNSRecordsResult{Records:records},nil
}

type dnsRecordDTO struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Value string `json:"value"`
	Priority int `json:"priority"`
}

func (a *Adapter) CheckDNS(ctx context.Context, req provider.CheckDNSRequest) (provider.CheckDNSResult, error) {
	return provider.CheckDNSResult{},provider.Errorf("verification_required","Migadu diagnostics 响应需要真实环境核验")
}

func (a *Adapter) ActivateDomain(ctx context.Context, req provider.ActivateDomainRequest) (provider.ActivateDomainResult, error) {
	var d domainDTO
	if err := a.call(ctx, http.MethodGet, "/domains/"+pathSegment(req.Domain)+"/activate", nil, &d); err != nil {
		return provider.ActivateDomainResult{}, err
	}
	return provider.ActivateDomainResult{Domain: domainInfo(d)}, nil
}

type mailboxDTO struct {
	Domain     string `json:"domain_name"`
	LocalPart  string `json:"local_part"`
	Address string `json:"address"`
}

func mailboxInfo(m mailboxDTO) (provider.MailboxInfo,error) {
	if m.Domain=="" || m.LocalPart=="" || m.Address!=m.LocalPart+"@"+m.Domain{return provider.MailboxInfo{},provider.Errorf("upstream_failed","Migadu 返回的邮箱地址字段不完整")}
	return provider.MailboxInfo{Domain:m.Domain,LocalPart:m.LocalPart,Address:m.Address,Identities:[]provider.MailboxIdentity{}},nil
}

func (a *Adapter) GetMailbox(ctx context.Context, req provider.GetMailboxRequest) (provider.MailboxInfo, error) {
	var m mailboxDTO
	if err := a.call(ctx, http.MethodGet, "/domains/"+pathSegment(req.Domain)+"/mailboxes/"+pathSegment(req.LocalPart), nil, &m); err != nil {
		return provider.MailboxInfo{}, err
	}
	return mailboxInfo(m)
}

func (a *Adapter) CreateMailbox(ctx context.Context, req provider.CreateMailboxRequest) (provider.MailboxInfo, error) {
	password := req.Password
	if password==""{return provider.MailboxInfo{},provider.Errorf("invalid","邮箱创建需要已保存的候选密码")}
	var m mailboxDTO
	if err := a.call(ctx, http.MethodPost, "/domains/"+pathSegment(req.Domain)+"/mailboxes", map[string]string{
		"local_part": req.LocalPart, "name": req.DisplayName, "password": password,
	}, &m); err != nil {
		return provider.MailboxInfo{}, err
	}
	return mailboxInfo(m)
}

func (a *Adapter) UpdateMailbox(ctx context.Context, req provider.UpdateMailboxRequest) (provider.MailboxInfo, error) {
	body := map[string]string{}
	if req.DisplayName != "" {
		body["name"] = req.DisplayName
	}
	if req.NewPassword != "" {
		body["password"] = req.NewPassword
	}
	var m mailboxDTO
	if err := a.call(ctx, http.MethodPut, "/domains/"+pathSegment(req.Domain)+"/mailboxes/"+pathSegment(req.LocalPart), body, &m); err != nil {
		return provider.MailboxInfo{}, err
	}
	return mailboxInfo(m)
}

func (a *Adapter) DeleteMailbox(ctx context.Context, req provider.DeleteMailboxRequest) error {
	err := a.call(ctx, http.MethodDelete, "/domains/"+pathSegment(req.Domain)+"/mailboxes/"+pathSegment(req.LocalPart), nil, nil)
	return err
}

func (a *Adapter) ResetMailboxPassword(ctx context.Context, req provider.UpdateMailboxRequest) (provider.CredentialInfo, error) {
	password := req.NewPassword
	if password==""{return provider.CredentialInfo{},provider.Errorf("invalid","密码重置需要已保存的候选密码")}
	if err := a.call(ctx, http.MethodPut, "/domains/"+pathSegment(req.Domain)+"/mailboxes/"+pathSegment(req.LocalPart), map[string]string{"password": password}, nil); err != nil {
		return provider.CredentialInfo{}, err
	}
	return provider.CredentialInfo{Password: password}, nil
}

func (a *Adapter) CreateCredential(ctx context.Context, req provider.CreateCredentialRequest) (provider.CredentialInfo, error) {
	return provider.CredentialInfo{}, provider.Errorf("verification_required","Migadu 专用 identity 需要完成 V01、V02 验证")
}

func (a *Adapter) RevokeCredential(ctx context.Context, req provider.RevokeCredentialRequest) error {
	return provider.Errorf("verification_required","Migadu 专用 identity 撤销需要完成 V01、V02 验证")
}

func (a *Adapter) GetAddressRule(ctx context.Context, req provider.AddressRuleRequest) (provider.AddressRuleInfo, error) {
	if req.Catchall {
		var out struct {
			Targets []string `json:"catchall_destinations"`
		}
		if err := a.call(ctx, http.MethodGet, "/domains/"+pathSegment(req.Domain), nil, &out); err != nil {
			return provider.AddressRuleInfo{}, err
		}
		return provider.AddressRuleInfo{Domain: req.Domain, Catchall: true, Targets: out.Targets}, nil
	}
	if req.Prefix{return provider.AddressRuleInfo{},provider.Errorf("external_action_required","复杂 rewrite 通过 Migadu 管理页面查看")}
	var out struct{Targets []string `json:"destinations"`}
	if err:=a.call(ctx,http.MethodGet,"/domains/"+pathSegment(req.Domain)+"/aliases/"+pathSegment(req.LocalPart),nil,&out);err!=nil{return provider.AddressRuleInfo{},err}
	return provider.AddressRuleInfo{Domain:req.Domain,LocalPart:req.LocalPart,Targets:out.Targets},nil
}

func (a *Adapter) CreateAddressRule(ctx context.Context, req provider.AddressRuleRequest) (provider.AddressRuleInfo, error) {
	return provider.AddressRuleInfo{},provider.Errorf("verification_required","Migadu 地址管理需要完成真实投递验证")
}

func (a *Adapter) UpdateAddressRule(ctx context.Context, req provider.AddressRuleRequest) (provider.AddressRuleInfo, error) {
	return provider.AddressRuleInfo{},provider.Errorf("verification_required","Migadu 地址管理需要完成真实投递验证")
}

func (a *Adapter) DeleteAddressRule(ctx context.Context, req provider.AddressRuleRequest) error {
	return provider.Errorf("verification_required","Migadu 地址管理需要完成真实投递验证")
}

func (a *Adapter) GetForwarding(ctx context.Context, req provider.GetMailboxRequest) (provider.ForwardingInfo, error) {
	return provider.ForwardingInfo{}, provider.Errorf("unsupported_operation", "Migadu mailbox forwarding requires a V03 verification run")
}

func (a *Adapter) SetForwarding(ctx context.Context, req provider.ForwardingRequest) (provider.ForwardingInfo, error) {
	return provider.ForwardingInfo{}, provider.Errorf("verification_required", "Migadu forwarding write is unverified")
}

func (a *Adapter) DeleteForwarding(ctx context.Context, req provider.GetMailboxRequest) error {
	return provider.Errorf("verification_required", "Migadu forwarding delete is unverified")
}

func (a *Adapter) ListSenderIdentities(ctx context.Context, req provider.GetMailboxRequest) ([]provider.SenderIdentityInfo, error) {
	identities,err:=a.listMailboxIdentities(ctx,req);if err!=nil{return nil,err}
	out:=[]provider.SenderIdentityInfo{};for _,identity:=range identities{if identity.PasswordUse!="custom"{out=append(out,identity)}};return out,nil
}

func (a *Adapter) listMailboxIdentities(ctx context.Context,req provider.GetMailboxRequest) ([]provider.SenderIdentityInfo,error) {
	var identities []identityDTO
	if err:=a.call(ctx,http.MethodGet,"/domains/"+pathSegment(req.Domain)+"/mailboxes/"+pathSegment(req.LocalPart)+"/identities",nil,&identities);err!=nil{return nil,err}
	out:=make([]provider.SenderIdentityInfo,0,len(identities))
	for _,i:=range identities{
		info,err:=senderIdentityInfo(req,i);if err!=nil{return nil,err};out=append(out,info)
	}
	return out, nil
}

func (a *Adapter) AuthorizeSenderIdentity(ctx context.Context, req provider.AuthorizeSenderIdentityRequest) (provider.SenderIdentityInfo, error) {
	return provider.SenderIdentityInfo{},provider.Errorf("verification_required","Migadu 发件授权需要完成真实发送验证")
}
