package purelymail

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"mailhearth/internal/provider"
	purelymailclient "mailhearth/internal/purelymail"
)

type Adapter struct {
	api purelymailclient.API
}

var _ provider.Provider = (*Adapter)(nil)

func New(apiBaseURL, username, apiKey string) *Adapter {
	return &Adapter{api: purelymailclient.New(apiBaseURL, apiKey)}
}

func (a *Adapter) Kind() provider.ProviderKind { return provider.Purelymail }

func (a *Adapter) ValidateConnection(ctx context.Context, req provider.ValidateConnectionRequest) (provider.ValidateConnectionResult, error) {
	if req.APIBaseURL == "" || req.APIKey == "" {
		return provider.ValidateConnectionResult{}, provider.Errorf("invalid", "Purelymail API base URL and key are required")
	}
	if err := provider.ValidateProtocolTemplates(req.Protocols); err != nil {
		return provider.ValidateConnectionResult{}, err
	}
	client := a.api
	if client == nil {
		client = purelymailclient.New(req.APIBaseURL, req.APIKey)
	}
	if _, err := client.CheckAccountCredit(ctx); err != nil {
		code := "upstream_failed"
		var remote *purelymailclient.Error
		if purelymailclient.IsInvalidToken(err) || (errors.As(err, &remote) && (remote.Status == 401 || remote.Status == 403)) {
			code = "provider_auth_failed"
		} else if errors.As(err, &remote) && remote.Status == 429 {
			code = "upstream_rate_limited"
		}
		return provider.ValidateConnectionResult{OK: false, Code: code, Message: "Purelymail 管理认证未通过"}, provider.Errorf(code, "Purelymail 管理认证未通过")
	}
	return provider.ValidateConnectionResult{OK: true}, nil
}

func (a *Adapter) Discover(ctx context.Context, req provider.DiscoverRequest) (provider.DiscoverResult, error) {
	domains, err := a.api.ListDomains(ctx, true)
	if err != nil {
		return provider.DiscoverResult{}, err
	}
	users, err := a.api.ListUsers(ctx)
	if err != nil {
		return provider.DiscoverResult{}, err
	}
	rules, err := a.api.ListRoutingRules(ctx)
	if err != nil {
		return provider.DiscoverResult{}, err
	}
	resources := make([]provider.DiscoverySnapshotResource, 0, len(domains)+len(users)+len(rules))
	for _, d := range domains {
		if !scopeContains(req.Scope, d.Name) {
			continue
		}
		info := domainInfo(d)
		resources = append(resources, provider.DiscoverySnapshotResource{
			Resource: provider.Resource{
				ResourceType: "domain", RemoteKey: strings.ToLower(d.Name),
				RemoteLocator: map[string]string{"domain": strings.ToLower(d.Name)},
				Purpose:       provider.PurposeDomain, State: provider.ResourcePresent,
			},
			Summary: map[string]string{"name": d.Name},
			Domain:  &info,
		})
	}
	for _, u := range users {
		_, domain, ok := strings.Cut(u, "@")
		if !ok || !scopeContains(req.Scope, domain) {
			continue
		}
		resources = append(resources, provider.DiscoverySnapshotResource{
			Resource: provider.Resource{
				ResourceType: "mailbox", RemoteKey: strings.ToLower(u),
				RemoteLocator: map[string]string{"address": u},
				Purpose:       provider.PurposeMailbox, State: provider.ResourcePresent,
			},
			Summary: map[string]string{"address": u},
		})
	}
	for _, r := range rules {
		if !scopeContains(req.Scope, r.DomainName) {
			continue
		}
		resources = append(resources, provider.DiscoverySnapshotResource{
			Resource: provider.Resource{
				ResourceType: "routing_rule", RemoteKey: fmt.Sprintf("%d", r.ID),
				RemoteLocator: map[string]string{"domain": r.DomainName, "localPart": r.MatchUser, "prefix": fmt.Sprint(r.Prefix), "catchall": fmt.Sprint(r.Catchall)},
				Purpose:       provider.PurposeRouting, State: provider.ResourcePresent,
			},
			Summary:     map[string]string{"address": ruleAddress(r)},
			AddressRule: &provider.AddressRuleInfo{RemoteKey: fmt.Sprint(r.ID), RemoteLocator: map[string]string{"domain": r.DomainName, "localPart": r.MatchUser, "prefix": fmt.Sprint(r.Prefix), "catchall": fmt.Sprint(r.Catchall)}, Domain: r.DomainName, LocalPart: r.MatchUser, Prefix: r.Prefix, Catchall: r.Catchall, Targets: append([]string{}, r.TargetAddresses...)},
		})
	}
	mailboxAddresses := map[string]bool{}
	for _, address := range users {
		mailboxAddresses[strings.ToLower(address)] = true
	}
	for i := range resources {
		item := &resources[i]
		rule := item.AddressRule
		if rule == nil || rule.Prefix || rule.Catchall || !mailboxAddresses[strings.ToLower(rule.LocalPart+"@"+rule.Domain)] {
			continue
		}
		item.Resource.Purpose = provider.PurposeForwarding
		item.Forwarding = ruleForwardingInfo(*rule)
		item.Summary["mailboxAddress"] = rule.LocalPart + "@" + rule.Domain
		item.Summary["deliveryMode"] = "unverified"
	}
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Resource.ResourceType != resources[j].Resource.ResourceType {
			return resources[i].Resource.ResourceType < resources[j].Resource.ResourceType
		}
		return resources[i].Resource.RemoteKey < resources[j].Resource.RemoteKey
	})
	return provider.DiscoverResult{Snapshot: provider.DiscoverySnapshot{
		ConnectionID: 0, ConnectionRevision: 0, Scope: req.Scope, Resources: resources, Complete: true,
	}}, nil
}

func ruleAddress(r purelymailclient.RoutingRule) string {
	switch {
	case r.Catchall:
		return "*@" + r.DomainName
	case r.Prefix:
		return r.MatchUser + "*@" + r.DomainName
	default:
		return r.MatchUser + "@" + r.DomainName
	}
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

func external(kind string) error {
	return provider.Errorf("external_action_required", "Purelymail does not expose %s", kind)
}

func unsupported(kind string) error {
	return provider.Errorf("unsupported_operation", "Purelymail does not support %s", kind)
}

func (a *Adapter) GetDomain(ctx context.Context, req provider.GetDomainRequest) (provider.DomainInfo, error) {
	list, err := a.api.ListDomains(ctx, true)
	if err != nil {
		return provider.DomainInfo{}, err
	}
	for _, d := range list {
		if strings.EqualFold(d.Name, req.Domain) {
			return domainInfo(d), nil
		}
	}
	return provider.DomainInfo{}, provider.Errorf("verification_required", "域名缺少单项不存在确认")
}

func domainInfo(d purelymailclient.Domain) provider.DomainInfo {
	return provider.DomainInfo{
		Name: d.Name, IsShared: d.IsShared,
		AllowAccountReset: &d.AllowAccountReset, SymbolicSubaddressing: &d.SymbolicSubaddressing,
		DNS: provider.DNSStatus{
			MX: boolDNS(d.DNSSummary.PassesMx), SPF: boolDNS(d.DNSSummary.PassesSpf),
			DKIM: boolDNS(d.DNSSummary.PassesDkim), DMARC: boolDNS(d.DNSSummary.PassesDmarc),
		},
	}
}

func boolDNS(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

func (a *Adapter) CreateDomain(ctx context.Context, req provider.CreateDomainRequest) (provider.CreateDomainResult, error) {
	if err := a.api.AddDomain(ctx, strings.ToLower(req.Domain)); err != nil {
		return provider.CreateDomainResult{}, err
	}
	info, err := a.GetDomain(ctx, provider.GetDomainRequest{Domain: req.Domain})
	if err != nil {
		return provider.CreateDomainResult{}, err
	}
	return provider.CreateDomainResult{Domain: info}, nil
}

func (a *Adapter) UpdateDomain(ctx context.Context, req provider.UpdateDomainRequest) (provider.UpdateDomainResult, error) {
	if err := a.api.UpdateDomainSettings(ctx, purelymailclient.UpdateDomainSettingsRequest{
		Name: req.Domain, AllowAccountReset: req.AllowAccountReset, SymbolicSubaddressing: req.SymbolicSubaddressing, RecheckDNS: req.RecheckDNS,
	}); err != nil {
		return provider.UpdateDomainResult{}, err
	}
	info, err := a.GetDomain(ctx, provider.GetDomainRequest{Domain: req.Domain})
	if err != nil {
		return provider.UpdateDomainResult{}, err
	}
	return provider.UpdateDomainResult{Domain: info}, nil
}

func (a *Adapter) DeleteDomain(ctx context.Context, req provider.DeleteDomainRequest) (provider.DeleteDomainResult, error) {
	if err := a.api.DeleteDomain(ctx, req.Domain); err != nil {
		return provider.DeleteDomainResult{}, err
	}
	return provider.DeleteDomainResult{}, nil
}

func (a *Adapter) GetDNSRecords(ctx context.Context, req provider.GetDNSRecordsRequest) (provider.GetDNSRecordsResult, error) {
	return provider.GetDNSRecordsResult{}, unsupported("DNS record listing")
}

func (a *Adapter) CheckDNS(ctx context.Context, req provider.CheckDNSRequest) (provider.CheckDNSResult, error) {
	if err := a.api.UpdateDomainSettings(ctx, purelymailclient.UpdateDomainSettingsRequest{Name: req.Domain, RecheckDNS: true}); err != nil {
		return provider.CheckDNSResult{}, err
	}
	info, err := a.GetDomain(ctx, provider.GetDomainRequest{Domain: req.Domain})
	if err != nil {
		return provider.CheckDNSResult{}, err
	}
	return provider.CheckDNSResult{Status: info.DNS}, nil
}

func (a *Adapter) ActivateDomain(ctx context.Context, req provider.ActivateDomainRequest) (provider.ActivateDomainResult, error) {
	return provider.ActivateDomainResult{}, unsupported("domain activation")
}

func (a *Adapter) GetMailbox(ctx context.Context, req provider.GetMailboxRequest) (provider.MailboxInfo, error) {
	want := strings.ToLower(req.LocalPart + "@" + req.Domain)
	_, err := a.api.GetUser(ctx, want)
	if err != nil {
		var remote *purelymailclient.Error
		if errors.As(err, &remote) {
			if remote.Status == 404 {
				return provider.MailboxInfo{}, provider.Errorf("not_found", "远程邮箱不存在")
			}
			if remote.Status == 401 || remote.Status == 403 {
				return provider.MailboxInfo{}, provider.Errorf("provider_auth_failed", "远程邮箱不可访问")
			}
		}
		return provider.MailboxInfo{}, provider.Errorf("upstream_failed", "远程邮箱读取失败")
	}
	return provider.MailboxInfo{Domain: req.Domain, LocalPart: req.LocalPart, Address: want}, nil
}

func (a *Adapter) CreateMailbox(ctx context.Context, req provider.CreateMailboxRequest) (provider.MailboxInfo, error) {
	password := req.Password
	if password == "" {
		return provider.MailboxInfo{}, provider.Errorf("invalid", "邮箱创建需要已保存的候选密码")
	}
	if err := a.api.CreateUser(ctx, purelymailclient.CreateUserRequest{
		UserName: req.LocalPart, DomainName: req.Domain, Password: password,
		EnablePasswordReset: false, EnableSearchIndexing: true, SendWelcomeEmail: false,
	}); err != nil {
		return provider.MailboxInfo{}, err
	}
	return provider.MailboxInfo{Domain: req.Domain, LocalPart: req.LocalPart, Address: req.LocalPart + "@" + req.Domain}, nil
}

func (a *Adapter) UpdateMailbox(ctx context.Context, req provider.UpdateMailboxRequest) (provider.MailboxInfo, error) {
	if req.NewPassword != "" {
		p := req.NewPassword
		if err := a.api.ModifyUser(ctx, purelymailclient.ModifyUserRequest{UserName: req.LocalPart + "@" + req.Domain, NewPassword: &p}); err != nil {
			return provider.MailboxInfo{}, err
		}
	}
	return provider.MailboxInfo{Domain: req.Domain, LocalPart: req.LocalPart, Address: req.LocalPart + "@" + req.Domain}, nil
}

func (a *Adapter) DeleteMailbox(ctx context.Context, req provider.DeleteMailboxRequest) error {
	return a.api.DeleteUser(ctx, req.LocalPart+"@"+req.Domain)
}

func (a *Adapter) ResetMailboxPassword(ctx context.Context, req provider.UpdateMailboxRequest) (provider.CredentialInfo, error) {
	password := req.NewPassword
	if password == "" {
		return provider.CredentialInfo{}, provider.Errorf("invalid", "密码重置需要已保存的候选密码")
	}
	p := password
	if err := a.api.ModifyUser(ctx, purelymailclient.ModifyUserRequest{UserName: req.LocalPart + "@" + req.Domain, NewPassword: &p}); err != nil {
		return provider.CredentialInfo{}, err
	}
	return provider.CredentialInfo{Password: password}, nil
}

func (a *Adapter) CreateCredential(ctx context.Context, req provider.CreateCredentialRequest) (provider.CredentialInfo, error) {
	password, err := a.api.CreateAppPassword(ctx, req.LocalPart+"@"+req.Domain, "Mailhearth")
	if err != nil {
		return provider.CredentialInfo{}, err
	}
	return provider.CredentialInfo{Password: password}, nil
}

func (a *Adapter) RevokeCredential(ctx context.Context, req provider.RevokeCredentialRequest) error {
	if req.Secret == "" {
		return provider.Errorf("invalid", "撤销凭据需要明确的应用密码")
	}
	return a.api.DeleteAppPassword(ctx, req.LocalPart+"@"+req.Domain, req.Secret)
}

func (a *Adapter) GetAddressRule(ctx context.Context, req provider.AddressRuleRequest) (provider.AddressRuleInfo, error) {
	rules, err := a.api.ListRoutingRules(ctx)
	if err != nil {
		return provider.AddressRuleInfo{}, err
	}
	for _, r := range rules {
		if strings.EqualFold(r.DomainName, req.Domain) && strings.EqualFold(r.MatchUser, req.LocalPart) && r.Prefix == req.Prefix && r.Catchall == req.Catchall {
			return provider.AddressRuleInfo{
				RemoteKey: fmt.Sprint(r.ID), RemoteLocator: map[string]string{"domain": r.DomainName, "localPart": r.MatchUser},
				Domain: r.DomainName, LocalPart: r.MatchUser, Prefix: r.Prefix, Catchall: r.Catchall, Targets: normalizedTargets(r.TargetAddresses),
			}, nil
		}
	}
	return provider.AddressRuleInfo{}, provider.Errorf("not_found", "routing rule not found")
}

func (a *Adapter) CreateAddressRule(ctx context.Context, req provider.AddressRuleRequest) (provider.AddressRuleInfo, error) {
	if err := a.api.CreateRoutingRule(ctx, purelymailclient.CreateRoutingRuleRequest{
		DomainName: req.Domain, Prefix: req.Prefix, MatchUser: req.LocalPart, TargetAddresses: normalizedTargets(req.Targets), Catchall: req.Catchall,
	}); err != nil {
		return provider.AddressRuleInfo{}, err
	}
	info, err := a.GetAddressRule(ctx, req)
	if err != nil {
		return provider.AddressRuleInfo{}, provider.Errorf("remote_result_unknown", "远程规则写入后的读取需要核查")
	}
	return info, nil
}

func (a *Adapter) UpdateAddressRule(ctx context.Context, req provider.AddressRuleRequest) (provider.AddressRuleInfo, error) {
	rules, err := a.api.ListRoutingRules(ctx)
	if err != nil {
		return provider.AddressRuleInfo{}, err
	}
	old, err := findRule(rules, req)
	if err != nil {
		return provider.AddressRuleInfo{}, err
	}
	if err := a.api.DeleteRoutingRule(ctx, old.ID); err != nil {
		return provider.AddressRuleInfo{}, err
	}
	created, err := a.CreateAddressRule(ctx, req)
	if err != nil {
		return provider.AddressRuleInfo{}, provider.Errorf("upstream_failed", "routing rule removed and recreation failed: %s", err)
	}
	return created, nil
}

func (a *Adapter) DeleteAddressRule(ctx context.Context, req provider.AddressRuleRequest) error {
	rules, err := a.api.ListRoutingRules(ctx)
	if err != nil {
		return err
	}
	r, err := findRule(rules, req)
	if err != nil {
		return err
	}
	return a.api.DeleteRoutingRule(ctx, r.ID)
}

func findRule(rules []purelymailclient.RoutingRule, req provider.AddressRuleRequest) (purelymailclient.RoutingRule, error) {
	for _, r := range rules {
		if strings.EqualFold(r.DomainName, req.Domain) && strings.EqualFold(r.MatchUser, req.LocalPart) && r.Prefix == req.Prefix && r.Catchall == req.Catchall {
			return r, nil
		}
	}
	return purelymailclient.RoutingRule{}, provider.Errorf("not_found", "routing rule not found")
}

func (a *Adapter) GetForwarding(ctx context.Context, req provider.GetMailboxRequest) (provider.ForwardingInfo, error) {
	return provider.ForwardingInfo{}, unsupported("mailbox forwarding")
}

func (a *Adapter) SetForwarding(ctx context.Context, req provider.ForwardingRequest) (provider.ForwardingInfo, error) {
	return provider.ForwardingInfo{}, unsupported("mailbox forwarding")
}

func (a *Adapter) DeleteForwarding(ctx context.Context, req provider.GetMailboxRequest) error {
	return unsupported("mailbox forwarding")
}

func (a *Adapter) ListSenderIdentities(ctx context.Context, req provider.GetMailboxRequest) ([]provider.SenderIdentityInfo, error) {
	return nil, nil
}

func (a *Adapter) AuthorizeSenderIdentity(ctx context.Context, req provider.AuthorizeSenderIdentityRequest) (provider.SenderIdentityInfo, error) {
	return provider.SenderIdentityInfo{}, unsupported("sender identity authorization")
}

func normalizedTargets(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
