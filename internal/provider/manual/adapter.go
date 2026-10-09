package manual

import "context"

import "mailhearth/internal/provider"

type Adapter struct{}

var _ provider.Provider = (*Adapter)(nil)

func New(apiBaseURL, username, apiKey string) *Adapter { return &Adapter{} }

func (a *Adapter) Kind() provider.ProviderKind { return provider.Manual }

func external() error {
	return provider.Errorf("external_action_required", "manual connections require external administration")
}

func (a *Adapter) ValidateConnection(ctx context.Context, req provider.ValidateConnectionRequest) (provider.ValidateConnectionResult, error) {
	if req.APIKey!="" || req.APIBaseURL!="" || req.Username!="" { return provider.ValidateConnectionResult{}, provider.Errorf("invalid","手动连接不能包含 API 认证") }
	if err:=provider.ValidateProtocolTemplates(req.Protocols);err!=nil { return provider.ValidateConnectionResult{},err }
	return provider.ValidateConnectionResult{OK:true},nil
}

func (a *Adapter) Discover(ctx context.Context, req provider.DiscoverRequest) (provider.DiscoverResult, error) {
	return provider.DiscoverResult{}, external()
}

func (a *Adapter) GetDomain(ctx context.Context, req provider.GetDomainRequest) (provider.DomainInfo, error) {
	return provider.DomainInfo{}, external()
}

func (a *Adapter) CreateDomain(ctx context.Context, req provider.CreateDomainRequest) (provider.CreateDomainResult, error) {
	return provider.CreateDomainResult{}, external()
}

func (a *Adapter) UpdateDomain(ctx context.Context, req provider.UpdateDomainRequest) (provider.UpdateDomainResult, error) {
	return provider.UpdateDomainResult{}, external()
}

func (a *Adapter) DeleteDomain(ctx context.Context, req provider.DeleteDomainRequest) (provider.DeleteDomainResult, error) {
	return provider.DeleteDomainResult{}, external()
}

func (a *Adapter) GetDNSRecords(ctx context.Context, req provider.GetDNSRecordsRequest) (provider.GetDNSRecordsResult, error) {
	return provider.GetDNSRecordsResult{}, external()
}

func (a *Adapter) CheckDNS(ctx context.Context, req provider.CheckDNSRequest) (provider.CheckDNSResult, error) {
	return provider.CheckDNSResult{}, external()
}

func (a *Adapter) ActivateDomain(ctx context.Context, req provider.ActivateDomainRequest) (provider.ActivateDomainResult, error) {
	return provider.ActivateDomainResult{}, external()
}

func (a *Adapter) GetMailbox(ctx context.Context, req provider.GetMailboxRequest) (provider.MailboxInfo, error) {
	return provider.MailboxInfo{}, external()
}

func (a *Adapter) CreateMailbox(ctx context.Context, req provider.CreateMailboxRequest) (provider.MailboxInfo, error) {
	return provider.MailboxInfo{}, external()
}

func (a *Adapter) UpdateMailbox(ctx context.Context, req provider.UpdateMailboxRequest) (provider.MailboxInfo, error) {
	return provider.MailboxInfo{}, external()
}

func (a *Adapter) DeleteMailbox(ctx context.Context, req provider.DeleteMailboxRequest) error { return external() }

func (a *Adapter) ResetMailboxPassword(ctx context.Context, req provider.UpdateMailboxRequest) (provider.CredentialInfo, error) {
	return provider.CredentialInfo{}, external()
}

func (a *Adapter) CreateCredential(ctx context.Context, req provider.CreateCredentialRequest) (provider.CredentialInfo, error) {
	return provider.CredentialInfo{}, external()
}

func (a *Adapter) RevokeCredential(ctx context.Context, req provider.RevokeCredentialRequest) error { return external() }

func (a *Adapter) GetAddressRule(ctx context.Context, req provider.AddressRuleRequest) (provider.AddressRuleInfo, error) {
	return provider.AddressRuleInfo{}, external()
}

func (a *Adapter) CreateAddressRule(ctx context.Context, req provider.AddressRuleRequest) (provider.AddressRuleInfo, error) {
	return provider.AddressRuleInfo{}, external()
}

func (a *Adapter) UpdateAddressRule(ctx context.Context, req provider.AddressRuleRequest) (provider.AddressRuleInfo, error) {
	return provider.AddressRuleInfo{}, external()
}

func (a *Adapter) DeleteAddressRule(ctx context.Context, req provider.AddressRuleRequest) error { return external() }

func (a *Adapter) GetForwarding(ctx context.Context, req provider.GetMailboxRequest) (provider.ForwardingInfo, error) {
	return provider.ForwardingInfo{}, external()
}

func (a *Adapter) SetForwarding(ctx context.Context, req provider.ForwardingRequest) (provider.ForwardingInfo, error) {
	return provider.ForwardingInfo{}, external()
}

func (a *Adapter) DeleteForwarding(ctx context.Context, req provider.GetMailboxRequest) error { return external() }

func (a *Adapter) ListSenderIdentities(ctx context.Context, req provider.GetMailboxRequest) ([]provider.SenderIdentityInfo, error) {
	return nil, external()
}

func (a *Adapter) AuthorizeSenderIdentity(ctx context.Context, req provider.AuthorizeSenderIdentityRequest) (provider.SenderIdentityInfo, error) {
	return provider.SenderIdentityInfo{}, external()
}
