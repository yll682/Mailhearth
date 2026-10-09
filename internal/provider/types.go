package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"net"
	"strings"
	"sort"
	"golang.org/x/net/idna"
)

// ProviderKind is the persisted connection type.
type ProviderKind string

const (
	Purelymail ProviderKind = "purelymail"
	Migadu     ProviderKind = "migadu"
	Manual     ProviderKind = "manual"
)

// Protocol names are the fixed template keys.
const (
	ProtocolIMAP        = "imap"
	ProtocolSMTP        = "smtp"
	ProtocolManageSieve = "managesieve"
)

// ProtocolTemplate is one saved connection default.
type ProtocolTemplate struct {
	Enabled     bool   `json:"enabled"`
	Host        string `json:"host,omitempty"`
	Port        int    `json:"port,omitempty"`
	TLSMode     string `json:"tlsMode,omitempty"`
	CABundleID  *int64 `json:"caBundleId,omitempty"`
}

// ProtocolTemplates is the complete three-key template set.
type ProtocolTemplates struct {
	IMAP        ProtocolTemplate `json:"imap"`
	SMTP        ProtocolTemplate `json:"smtp"`
	ManageSieve ProtocolTemplate `json:"managesieve"`
}

func (p *ProtocolTemplates) UnmarshalJSON(data []byte) error {
	var in struct {
		IMAP *ProtocolTemplate `json:"imap"`
		SMTP *ProtocolTemplate `json:"smtp"`
		ManageSieve *ProtocolTemplate `json:"managesieve"`
	}
	decoder:=json.NewDecoder(bytes.NewReader(data));decoder.DisallowUnknownFields()
	if err:=decoder.Decode(&in);err!=nil{return Errorf("invalid","协议模板 JSON 无效")}
	var trailing any;if err:=decoder.Decode(&trailing);err!=io.EOF{return Errorf("invalid","协议模板只能包含一个 JSON 值")}
	if in.IMAP==nil || in.SMTP==nil || in.ManageSieve==nil{return Errorf("invalid","协议模板必须包含 imap、smtp、managesieve")}
	*p=ProtocolTemplates{IMAP:*in.IMAP,SMTP:*in.SMTP,ManageSieve:*in.ManageSieve};return nil
}

// DomainScope limits discovery and management.
type DomainScope struct {
	Mode    string   `json:"mode"`
	Domains []string `json:"domains,omitempty"`
}

// APIAuth is the management credential input for one connection.
type APIAuth struct {
	APIKey   string `json:"apiKey,omitempty"`
	Username string `json:"username,omitempty"`
}

// ResourceState is persisted for remote objects.
type ResourceState string

const (
	ResourcePresent      ResourceState = "present"
	ResourceMissing      ResourceState = "missing"
	ResourceInaccessible ResourceState = "inaccessible"
	ResourceUnknown      ResourceState = "unknown"
	ResourceExternal     ResourceState = "external"
)

// ResourcePurpose explains a remote object association.
type ResourcePurpose string

const (
	PurposeDomain         ResourcePurpose = "domain"
	PurposeMailbox        ResourcePurpose = "mailbox"
	PurposeRouting        ResourcePurpose = "routing"
	PurposeForwarding     ResourcePurpose = "forwarding"
	PurposeSenderIdentity ResourcePurpose = "sender_identity"
	PurposeLoginCredential ResourcePurpose = "login_credential"
)

// Resource associates a local object with a remote object.
type Resource struct {
	ConnectionID      int64             `json:"connectionId"`
	ResourceType      string            `json:"resourceType"`
	RemoteKey         string            `json:"remoteKey"`
	RemoteLocator     map[string]string `json:"remoteLocator,omitempty"`
	Purpose           ResourcePurpose   `json:"purpose"`
	OwnedByMailhearth bool              `json:"ownedByMailhearth"`
	State             ResourceState     `json:"remoteState"`
}

// Capability is the complete UI execution capability.
type CapabilityConstraints struct {
	SameDomainOnly bool `json:"sameDomainOnly,omitempty"`
	SameConnectionOnly bool `json:"sameConnectionOnly,omitempty"`
	AllowedDeliveryModes []string `json:"allowedDeliveryModes,omitempty"`
	RequiredSieveExtensions []string `json:"requiredSieveExtensions,omitempty"`
}

type Capability struct {
	Key              string         `json:"key"`
	Support          string         `json:"support"`
	Readiness        string         `json:"readiness"`
	PermissionAllowed bool          `json:"permissionAllowed"`
	Constraints      CapabilityConstraints `json:"constraints"`
	ReasonCode       string         `json:"reasonCode,omitempty"`
}

// TypedError carries a public error code and a user-readable message.
type TypedError struct {
	Code    string
	Message string
	Err     error
	OperationID *string
	Details any
}

func (e *TypedError) Error() string { return e.Message }
func (e *TypedError) Unwrap() error { return e.Err }

func Errorf(code, format string, args ...any) *TypedError {
	return &TypedError{Code: code, Message: fmt.Sprintf(format, args...)}
}

var ErrUnsupported = errors.New("provider operation unsupported")

// ValidateProtocolTemplate enforces the fixed structure.
func ValidateProtocolTemplate(p ProtocolTemplate) error {
	if !p.Enabled {
		if p.Host != "" || p.Port != 0 || p.TLSMode != "" || p.CABundleID != nil {
			return Errorf("invalid", "disabled protocol template must be empty")
		}
		return nil
	}
	if strings.ContainsAny(p.Host, "/?#@\\\r\n\x00") || strings.TrimSpace(p.Host) != p.Host || p.Host == "" || (strings.Contains(p.Host,":") && net.ParseIP(p.Host)==nil) {
		return Errorf("invalid", "invalid protocol host %q", p.Host)
	}
	if p.Port < 1 || p.Port > 65535 {
		return Errorf("invalid", "protocol port must be between 1 and 65535")
	}
	if p.TLSMode != "tls" && p.TLSMode != "starttls" {
		return Errorf("invalid", "协议 TLS mode 必须为 tls 或 starttls")
	}
	if p.CABundleID!=nil && *p.CABundleID<1{return Errorf("invalid","CA bundle ID 必须为正整数")}
	return nil
}

// ValidateProtocolTemplates checks the full set.
func ValidateProtocolTemplates(t ProtocolTemplates) error {
	if err := ValidateProtocolTemplate(t.IMAP); err != nil {
		return fmt.Errorf("imap: %w", err)
	}
	if err := ValidateProtocolTemplate(t.SMTP); err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	if err := ValidateProtocolTemplate(t.ManageSieve); err != nil {
		return fmt.Errorf("managesieve: %w", err)
	}
	return nil
}

// DefaultTemplates returns the provider defaults from the implementation spec.
func DefaultTemplates(kind ProviderKind) ProtocolTemplates {
	switch kind {
	case Purelymail:
		return ProtocolTemplates{
			IMAP:        enabledTemplate("imap.purelymail.com", 993, "tls"),
			SMTP:        enabledTemplate("smtp.purelymail.com", 465, "tls"),
			ManageSieve: enabledTemplate("mailserver.purelymail.com", 4190, "starttls"),
		}
	case Migadu:
		return ProtocolTemplates{
			IMAP:        enabledTemplate("imap.migadu.com", 993, "tls"),
			SMTP:        enabledTemplate("smtp.migadu.com", 465, "tls"),
			ManageSieve: ProtocolTemplate{},
		}
	case Manual:
		return ProtocolTemplates{}
	}
	return ProtocolTemplates{}
}

func enabledTemplate(host string, port int, tlsMode string) ProtocolTemplate {
	return ProtocolTemplate{Enabled: true, Host: host, Port: port, TLSMode: tlsMode}
}

// ValidateAddress uses the standard library and rejects display names.
func ValidateAddress(address string) (string, error) {
	if strings.ContainsAny(address,"\r\n\x00") { return "", Errorf("invalid", "邮件地址含有无效字符") }
	a, err := mail.ParseAddress(address)
	if err != nil || a.Address != address {
		return "", Errorf("invalid", "%q is not a valid email address", address)
	}
	return address, nil
}

// NormalizeDomain performs ASCII lowering. IDNA conversion is in core where
// the text dependency is available.
func NormalizeDomain(domain string) string { return strings.ToLower(strings.TrimSpace(domain)) }

func CanonicalDomain(domain string) (string,error) {
	value,err:=idna.Lookup.ToASCII(strings.ToLower(strings.TrimSpace(domain)))
	if err!=nil || value=="" || strings.ContainsAny(value,"/@\r\n\x00") || !strings.Contains(value,".") { return "",Errorf("invalid","域名无效") }
	return value,nil
}

func NormalizeScope(scope DomainScope) (DomainScope,error) {
	if scope.Mode=="all" && len(scope.Domains)==0 { return DomainScope{Mode:"all"},nil }
	if scope.Mode!="selected" || len(scope.Domains)==0 { return DomainScope{},Errorf("invalid","域名范围必须为 all 或非空 selected") }
	set:=map[string]bool{}
	for _,domain:=range scope.Domains { value,err:=CanonicalDomain(domain); if err!=nil { return DomainScope{},err }; set[value]=true }
	out:=DomainScope{Mode:"selected",Domains:make([]string,0,len(set))}
	for domain:=range set { out.Domains=append(out.Domains,domain) }
	sort.Strings(out.Domains)
	return out,nil
}

// JSONString is a helper for deterministic JSON serialization.
func JSONString(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Validation result used by connection tests.
type ConnectionTest struct {
	Protocol string `json:"protocol"`
	Stage    string `json:"stage"`
	OK       bool   `json:"ok"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message,omitempty"`
}

// DiscoverySnapshotResource is one safe discovery item.
type DiscoverySnapshotResource struct {
	Resource Resource      `json:"resource"`
	Summary  map[string]string `json:"summary,omitempty"`
	AddressRule *AddressRuleInfo `json:"addressRule,omitempty"`
	Identity *SenderIdentityInfo `json:"identity,omitempty"`
	Domain *DomainInfo `json:"domain,omitempty"`
	Forwarding *ForwardingInfo `json:"forwarding,omitempty"`
}

type ReadResourceRequest struct { Resource Resource `json:"resource"` }
type ResourceReader interface { ReadResource(context.Context,ReadResourceRequest) (DiscoverySnapshotResource,error) }

// DiscoverySnapshot is persisted without secrets.
type DiscoverySnapshot struct {
	ConnectionID      int64                      `json:"connectionId"`
	ConnectionRevision int64                      `json:"connectionRevision"`
	Scope             DomainScope                `json:"scope"`
	Resources         []DiscoverySnapshotResource `json:"resources"`
	Complete          bool                       `json:"complete"`
}

// Request and result types used by all management adapters.
type ValidateConnectionRequest struct {
	APIBaseURL string   `json:"apiBaseUrl"`
	APIKey     string   `json:"apiKey"`
	Username   string   `json:"username,omitempty"`
	Protocols  ProtocolTemplates `json:"protocolDefaults"`
}

type ValidateConnectionResult struct {
	OK       bool             `json:"ok"`
	Code     string           `json:"code,omitempty"`
	Message  string           `json:"message,omitempty"`
	Checks   []ConnectionTest `json:"checks,omitempty"`
}

type DiscoverRequest struct {
	Scope DomainScope `json:"scope"`
}

type DiscoverResult struct {
	Snapshot DiscoverySnapshot `json:"snapshot"`
}

type DomainRecord struct {
	Type     string `json:"type"`
	Host     string `json:"host"`
	Value    string `json:"value"`
	Priority int    `json:"priority,omitempty"`
	Purpose  string `json:"purpose"`
}

type DNSStatus struct {
	MX    string `json:"mx"`
	SPF   string `json:"spf"`
	DKIM  string `json:"dkim"`
	DMARC string `json:"dmarc"`
}

type GetDomainRequest struct {
	Domain string `json:"domain"`
}

type DomainInfo struct {
	Name                  string    `json:"name"`
	IsShared              bool      `json:"isShared"`
	AllowAccountReset     *bool     `json:"allowAccountReset,omitempty"`
	SymbolicSubaddressing *bool     `json:"symbolicSubaddressing,omitempty"`
	DNS                   DNSStatus `json:"dns"`
}

type CreateDomainRequest struct {
	Domain string `json:"domain"`
}

type CreateDomainResult struct {
	Domain DomainInfo `json:"domain"`
}

type UpdateDomainRequest struct {
	Domain                string `json:"domain"`
	AllowAccountReset     *bool  `json:"allowAccountReset,omitempty"`
	SymbolicSubaddressing *bool  `json:"symbolicSubaddressing,omitempty"`
	RecheckDNS            bool   `json:"recheckDns,omitempty"`
}

type UpdateDomainResult struct {
	Domain DomainInfo `json:"domain"`
}

type DeleteDomainRequest struct {
	Domain string `json:"domain"`
}

type DeleteDomainResult struct{}

type GetDNSRecordsRequest struct {
	Domain string `json:"domain"`
}

type GetDNSRecordsResult struct {
	Records []DomainRecord `json:"records"`
}

type CheckDNSRequest struct {
	Domain string `json:"domain"`
}

type CheckDNSResult struct {
	Status DNSStatus `json:"status"`
}

type ActivateDomainRequest struct {
	Domain string `json:"domain"`
}

type ActivateDomainResult struct {
	Domain DomainInfo `json:"domain"`
}

type MailboxIdentity struct {
	LocalPart string `json:"localPart"`
	Address   string `json:"address"`
}

type MailboxInfo struct {
	Domain     string           `json:"domain"`
	LocalPart  string           `json:"localPart"`
	Address    string           `json:"address"`
	Identities []MailboxIdentity `json:"identities,omitempty"`
}

type GetMailboxRequest struct {
	Domain    string `json:"domain"`
	LocalPart string `json:"localPart"`
}

type CreateMailboxRequest struct {
	Domain       string `json:"domain"`
	LocalPart    string `json:"localPart"`
	Password     string `json:"password,omitempty"`
	DisplayName  string `json:"displayName,omitempty"`
}

type UpdateMailboxRequest struct {
	Domain      string `json:"domain"`
	LocalPart   string `json:"localPart"`
	NewPassword string `json:"newPassword,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

type DeleteMailboxRequest struct {
	Domain    string `json:"domain"`
	LocalPart string `json:"localPart"`
}

type CredentialInfo struct {
	LocalPart string `json:"localPart,omitempty"`
	Password  string `json:"password,omitempty"`
}

type CreateCredentialRequest struct {
	Domain    string `json:"domain"`
	LocalPart string `json:"localPart"`
}

type RevokeCredentialRequest struct {
	Domain    string `json:"domain"`
	LocalPart string `json:"localPart"`
	RemoteKey string `json:"remoteKey"`
	Secret string `json:"-"`
}

type AddressRuleInfo struct {
	RemoteKey string `json:"remoteKey"`
	RemoteLocator map[string]string `json:"remoteLocator"`
	Domain    string   `json:"domain"`
	LocalPart string   `json:"localPart"`
	Prefix    bool     `json:"prefix"`
	Catchall  bool     `json:"catchall"`
	Targets   []string `json:"targets"`
	Pattern string `json:"pattern,omitempty"`
	Name string `json:"name,omitempty"`
}

type AddressRuleRequest struct {
	Domain    string   `json:"domain"`
	LocalPart string   `json:"localPart"`
	Prefix    bool     `json:"prefix"`
	Catchall  bool     `json:"catchall"`
	Targets   []string `json:"targets"`
}

type ForwardingInfo struct {
	Domain        string         `json:"domain"`
	LocalPart     string         `json:"localPart"`
	Targets       []string       `json:"targets"`
	DeliveryMode  string         `json:"deliveryMode"`
	StatusByTarget map[string]string `json:"statusByTarget"`
}

type ForwardingRequest struct {
	Domain       string   `json:"domain"`
	LocalPart    string   `json:"localPart"`
	Targets      []string `json:"targets"`
	DeliveryMode string   `json:"deliveryMode"`
}

type SenderIdentityInfo struct {
	Domain        string `json:"domain"`
	MailboxLocalPart string `json:"mailboxLocalPart"`
	LocalPart     string `json:"localPart"`
	Address       string `json:"address"`
	DisplayName string `json:"displayName,omitempty"`
	PasswordUse string `json:"passwordUse,omitempty"`
	MaySend *bool `json:"maySend,omitempty"`
}

type AuthorizeSenderIdentityRequest struct {
	Domain            string `json:"domain"`
	MailboxLocalPart  string `json:"mailboxLocalPart"`
	IdentityLocalPart string `json:"identityLocalPart"`
}

// Provider is the fixed public management interface.
type Provider interface {
	Kind() ProviderKind
	ValidateConnection(ctx context.Context, req ValidateConnectionRequest) (ValidateConnectionResult, error)
	Discover(ctx context.Context, req DiscoverRequest) (DiscoverResult, error)
	GetDomain(ctx context.Context, req GetDomainRequest) (DomainInfo, error)
	CreateDomain(ctx context.Context, req CreateDomainRequest) (CreateDomainResult, error)
	UpdateDomain(ctx context.Context, req UpdateDomainRequest) (UpdateDomainResult, error)
	DeleteDomain(ctx context.Context, req DeleteDomainRequest) (DeleteDomainResult, error)
	GetDNSRecords(ctx context.Context, req GetDNSRecordsRequest) (GetDNSRecordsResult, error)
	CheckDNS(ctx context.Context, req CheckDNSRequest) (CheckDNSResult, error)
	ActivateDomain(ctx context.Context, req ActivateDomainRequest) (ActivateDomainResult, error)
	GetMailbox(ctx context.Context, req GetMailboxRequest) (MailboxInfo, error)
	CreateMailbox(ctx context.Context, req CreateMailboxRequest) (MailboxInfo, error)
	UpdateMailbox(ctx context.Context, req UpdateMailboxRequest) (MailboxInfo, error)
	DeleteMailbox(ctx context.Context, req DeleteMailboxRequest) error
	ResetMailboxPassword(ctx context.Context, req UpdateMailboxRequest) (CredentialInfo, error)
	CreateCredential(ctx context.Context, req CreateCredentialRequest) (CredentialInfo, error)
	RevokeCredential(ctx context.Context, req RevokeCredentialRequest) error
	GetAddressRule(ctx context.Context, req AddressRuleRequest) (AddressRuleInfo, error)
	CreateAddressRule(ctx context.Context, req AddressRuleRequest) (AddressRuleInfo, error)
	UpdateAddressRule(ctx context.Context, req AddressRuleRequest) (AddressRuleInfo, error)
	DeleteAddressRule(ctx context.Context, req AddressRuleRequest) error
	GetForwarding(ctx context.Context, req GetMailboxRequest) (ForwardingInfo, error)
	SetForwarding(ctx context.Context, req ForwardingRequest) (ForwardingInfo, error)
	DeleteForwarding(ctx context.Context, req GetMailboxRequest) error
	ListSenderIdentities(ctx context.Context, req GetMailboxRequest) ([]SenderIdentityInfo, error)
	AuthorizeSenderIdentity(ctx context.Context, req AuthorizeSenderIdentityRequest) (SenderIdentityInfo, error)
}
