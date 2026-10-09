// Package model holds the organisation-level domain types shared by the
// services and the HTTP API. These are the words administrators see:
// members, mailboxes, addresses, shared mailboxes, groups and roles.
package model

// Permission keys. A role is a named set of these.
const (
	PermOrgOwner        = "org.owner"        // transfer ownership, delete organisation
	PermOrgManage       = "org.manage"       // organisation settings, connections, roles
	PermDomainsManage   = "domains.manage"   // add/remove domains, DNS checks
	PermMembersManage   = "members.manage"   // invite, edit, disable, offboard members
	PermMailboxesManage = "mailboxes.manage" // create/bind/rotate personal mailboxes
	PermSharedManage    = "shared.manage"    // shared mailboxes and access grants
	PermAddressesManage = "addresses.manage" // aliases, forwards, catch-all
	PermGroupsManage    = "groups.manage"    // groups and distribution addresses
	PermAuditRead       = "audit.read"       // read the audit log
	PermBillingRead     = "billing.read"     // see connection balance and usage
)

// AllPermissions lists every permission in display order.
var AllPermissions = []string{
	PermOrgOwner, PermOrgManage, PermDomainsManage, PermMembersManage, PermMailboxesManage,
	PermSharedManage, PermAddressesManage, PermGroupsManage, PermAuditRead, PermBillingRead,
}

// Builtin role keys.
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// Member statuses.
const (
	MemberInvited  = "invited"
	MemberActive   = "active"
	MemberDisabled = "disabled"
	MemberDeparted = "departed"
)

// Mailbox kinds and statuses.
const (
	MailboxPersonal = "personal"
	MailboxShared   = "shared"

	MailboxActive    = "active"
	MailboxSuspended = "suspended"
	MailboxArchived  = "archived"
)

// Mailbox remote and management states.
const (
	MailboxRemotePresent     = "present"
	MailboxRemoteMissing     = "missing"
	MailboxRemoteInaccessible = "inaccessible"
	MailboxRemoteUnknown     = "unknown"
	MailboxRemoteExternal    = "external"

	ManagementModeAPI      = "api"
	ManagementModeExternal = "external"
)

// Address kinds.
const (
	AddressPrimary     = "primary"
	AddressAlias       = "alias"
	AddressForward     = "forward"
	AddressGroup       = "group"
	AddressCatchall    = "catchall"
	AddressPrefix      = "prefix"
	AddressExternalRule = "external_rule"
)

// Access levels for shared mailboxes.
const (
	AccessFull = "full"
	AccessSend = "send"
	AccessRead = "read"
)

// Provider connection kinds.
const (
	ProviderPurelymail = "purelymail"
	ProviderMigadu     = "migadu"
	ProviderManual     = "manual"
)

type Organization struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
}

type Role struct {
	ID          int64    `json:"id"`
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
	Builtin     bool     `json:"builtin"`
	MemberCount int      `json:"memberCount"`
}

type Member struct {
	ID          int64   `json:"id"`
	DisplayName string  `json:"displayName"`
	LoginEmail  string  `json:"loginEmail"`
	RoleID      int64   `json:"roleId"`
	RoleKey     string  `json:"roleKey"`
	RoleName    string  `json:"roleName"`
	Title       string  `json:"title"`
	Department  string  `json:"department"`
	Status      string  `json:"status"`
	HasPassword bool    `json:"hasPassword"`
	Revision    int64   `json:"revision"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
	LastLoginAt *string `json:"lastLoginAt"`
	DepartedAt  *string `json:"departedAt"`
}

type DNSSummary struct {
	MX    bool `json:"mx"`
	SPF   bool `json:"spf"`
	DKIM  bool `json:"dkim"`
	DMARC bool `json:"dmarc"`
}

type Domain struct {
	ID                    int64       `json:"id"`
	Name                  string      `json:"name"`
	IsShared              bool        `json:"isShared"`
	AllowAccountReset     bool        `json:"allowAccountReset"`
	SymbolicSubaddressing bool        `json:"symbolicSubaddressing"`
	DNS                   *DNSSummary `json:"dns"`
	DNSCheckedAt          *string     `json:"dnsCheckedAt"`
	Status                string      `json:"status"`
	MailboxCount          int         `json:"mailboxCount"`
	AddressCount          int         `json:"addressCount"`
	CreatedAt             string      `json:"createdAt"`
}

type DomainBinding struct {
	ID                 int64             `json:"id"`
	DomainID           int64             `json:"domainId"`
	DomainName         string            `json:"domainName"`
	ConnectionID       int64             `json:"connectionId"`
	ConnectionLabel    string            `json:"connectionLabel"`
	ManagementMode     string            `json:"managementMode"`
	RemoteState        string            `json:"remoteState"`
	ProviderSettings   map[string]any    `json:"providerSettings,omitempty"`
	DNSStatus          map[string]string `json:"dnsStatus"`
	DNSCheckedAt       *string           `json:"dnsCheckedAt"`
	Revision           int64             `json:"revision"`
	CreatedAt          string            `json:"createdAt"`
	UpdatedAt          string            `json:"updatedAt"`
}

type MailConnection struct {
	ID                 int64             `json:"id"`
	OrgID              int64             `json:"orgId"`
	ProviderKind       string            `json:"providerKind"`
	Label              string            `json:"label"`
	Enabled            bool              `json:"enabled"`
	Revision           int64             `json:"revision"`
	APIBaseURL         string            `json:"apiBaseUrl,omitempty"`
	APIUsername        string            `json:"apiUsername,omitempty"`
	APICredentialID    *int64            `json:"apiCredentialId,omitempty"`
	DomainScope        DomainScope       `json:"domainScope"`
	ProtocolDefaults   ProtocolTemplates `json:"protocolDefaults"`
	LastAPICheckAt     *string           `json:"lastApiCheckAt,omitempty"`
	LastAPICheckStatus string            `json:"lastApiCheckStatus"`
	LastAPIErrorCode   *string           `json:"lastApiErrorCode,omitempty"`
	LastSyncAt         *string           `json:"lastSyncAt,omitempty"`
	CreatedAt          string            `json:"createdAt"`
	UpdatedAt          string            `json:"updatedAt"`
}

type DomainScope struct {
	Mode    string   `json:"mode"`
	Domains []string `json:"domains,omitempty"`
}

type ProtocolTemplates struct {
	IMAP        *ProtocolTemplate `json:"imap"`
	SMTP        *ProtocolTemplate `json:"smtp"`
	ManageSieve *ProtocolTemplate `json:"managesieve"`
}

type ProtocolTemplate struct {
	Enabled     bool   `json:"enabled"`
	Host        string `json:"host,omitempty"`
	Port        int    `json:"port,omitempty"`
	TLSMode     string `json:"tlsMode,omitempty"`
	CABundleID  *int64 `json:"caBundleId,omitempty"`
}

type Mailbox struct {
	ID             int64           `json:"id"`
	OrgID          int64           `json:"orgId"`
	ConnectionID   int64           `json:"connectionId"`
	ConnectionLabel string         `json:"connectionLabel"`
	Kind           string          `json:"kind"`
	Address        string          `json:"address"`
	AddressKey     string          `json:"addressKey"`
	DomainID       *int64          `json:"domainId"`
	DomainBindingID *int64         `json:"domainBindingId,omitempty"`
	DisplayName    string          `json:"displayName"`
	OwnerMemberID  *int64          `json:"ownerMemberId"`
	OwnerName      string          `json:"ownerName"`
	Status         string          `json:"status"`
	Imported       bool            `json:"imported"`
	ManagementMode string          `json:"managementMode"`
	RemoteState    string          `json:"remoteState"`
	PMUser        string          `json:"-"`
	HasCredential bool            `json:"-"`
	CredentialAt  *string         `json:"-"`
	CredentialLabel string        `json:"-"`
	Revision       int64           `json:"revision"`
	AccessRevision int64           `json:"accessRevision"`
	SentCopyMode   string          `json:"sentCopyMode"`
	FolderMapping  FolderMapping   `json:"folderMapping"`
	Protocols MailboxProtocols `json:"protocols"`
	Settings       MailboxSettings `json:"settings"`
	AccessCount    int             `json:"accessCount"`
	CreatedAt      string          `json:"createdAt"`
	UpdatedAt      string          `json:"updatedAt"`
}

type ProtocolStatus struct {
	Readiness string `json:"readiness"`
	CheckStatus string `json:"checkStatus"`
}

type MailboxProtocols struct {
	IMAP *ProtocolStatus `json:"imap"`
	SMTP *ProtocolStatus `json:"smtp"`
	ManageSieve *ProtocolStatus `json:"managesieve"`
}

type FolderMapping struct {
	Sent    *string `json:"sent"`
	Drafts  *string `json:"drafts"`
	Trash   *string `json:"trash"`
	Junk    *string `json:"junk"`
	Archive *string `json:"archive"`
}

type MailboxSettings struct {
	SieveRules []SieveRule `json:"sieveRules"`
	Vacation   *Vacation   `json:"vacation,omitempty"`
	SieveSync  string      `json:"sieveSync,omitempty"`
	SieveScriptName string `json:"sieveScriptName,omitempty"`
	SieveScriptHash string `json:"sieveScriptHash,omitempty"`
}

type MailboxAccess struct {
	ID         int64  `json:"id"`
	MailboxID  int64  `json:"mailboxId"`
	MemberID   int64  `json:"memberId"`
	MemberName string `json:"memberName"`
	Level      string `json:"level"`
	GrantedAt  string `json:"grantedAt"`
}

type Identity struct {
	ID                     int64  `json:"id"`
	MailboxID              int64  `json:"mailboxId"`
	Address                string `json:"address"`
	DisplayName            string `json:"displayName"`
	ReplyTo                string `json:"replyTo"`
	SignatureHTML          string `json:"signatureHtml"`
	IsDefault              bool   `json:"isDefault"`
	AuthorizationSource    string `json:"authorizationSource"`
	AuthorizationStatus    string `json:"authorizationStatus"`
	AuthorizationCheckedAt *string `json:"authorizationCheckedAt"`
	Revision               int64  `json:"revision"`
}

type Address struct {
	ID             int64    `json:"id"`
	OrgID          int64    `json:"orgId"`
	ConnectionID   int64    `json:"connectionId"`
	DomainID       int64    `json:"domainId"`
	DomainBindingID *int64  `json:"domainBindingId,omitempty"`
	Domain         string   `json:"domain"`
	LocalPart      string   `json:"localPart"`
	Address        string   `json:"address"`
	AddressKey     string   `json:"addressKey"`
	Kind           string   `json:"kind"`
	MailboxID      *int64   `json:"mailboxId"`
	GroupID        *int64   `json:"groupId"`
	Targets        []string `json:"targets"`
	DesiredTargets []string `json:"desiredTargets"`
	ObservedTargets []string `json:"observedTargets"`
	ManagementMode string   `json:"managementMode"`
	SyncState      string   `json:"syncState"`
	Revision       int64    `json:"revision"`
	PMRuleID      *int64   `json:"-"`
	IsPrefix       bool     `json:"isPrefix"`
	IsCatchall     bool     `json:"isCatchall"`
	Note           string   `json:"note"`
	CreatedAt      string   `json:"createdAt"`
}

type Group struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	MemberIDs   []int64  `json:"memberIds"`
	Address     *Address `json:"address"`
	Revision    int64    `json:"revision"`
	CreatedAt   string   `json:"createdAt"`
}

type MailboxForwarding struct {
	ID           int64                `json:"id"`
	MailboxID    int64                `json:"mailboxId"`
	Targets      []string             `json:"targets"`
	DeliveryMode string               `json:"deliveryMode"`
	RemoteStatus map[string]any       `json:"remoteStatus"`
	SyncState    string               `json:"syncState"`
	Revision     int64                `json:"revision"`
	CreatedAt    string               `json:"createdAt"`
	UpdatedAt    string               `json:"updatedAt"`
}

type AuditEntry struct {
	ID         int64  `json:"id"`
	ActorID    *int64 `json:"actorId"`
	ActorName  string `json:"actorName"`
	Action     string `json:"action"`
	TargetType string `json:"targetType"`
	TargetID   string `json:"targetId"`
	Detail     any    `json:"detail"`
	CreatedAt  string `json:"createdAt"`
}

type ProviderResource struct {
	ID                 int64  `json:"id"`
	ConnectionID       int64  `json:"connectionId"`
	ResourceType       string `json:"resourceType"`
	RemoteKey          string `json:"remoteKey"`
	RemoteLocatorJSON  string `json:"remoteLocatorJson"`
	Purpose            string `json:"purpose"`
	OwnedByMailhearth  bool   `json:"ownedByMailhearth"`
	LastSeenAt         *string `json:"lastSeenAt"`
	RemoteState        string `json:"remoteState"`
	DomainBindingID    *int64 `json:"domainBindingId,omitempty"`
	MailboxID          *int64 `json:"mailboxId,omitempty"`
	AddressID          *int64 `json:"addressId,omitempty"`
	IdentityID         *int64 `json:"identityId,omitempty"`
	CredentialID       *int64 `json:"credentialId,omitempty"`
	MailboxForwardingID *int64 `json:"mailboxForwardingId,omitempty"`
}

type Operation struct {
	ID              string  `json:"id"`
	OrgID           int64   `json:"orgId"`
	ActorMemberID   *int64  `json:"actorMemberId"`
	Kind            string  `json:"kind"`
	RequestID       string  `json:"requestId"`
	RequestDigest   string  `json:"requestDigest"`
	Status          string  `json:"status"`
	Result          any     `json:"result"`
	ErrorCode       *string `json:"errorCode,omitempty"`
	CreatedAt       string  `json:"createdAt"`
	UpdatedAt       string  `json:"updatedAt"`
}

type OperationStep struct {
	OperationID    string  `json:"operationId"`
	StepKey        string  `json:"stepKey"`
	Sequence       int     `json:"sequence"`
	Status         string  `json:"status"`
	RemoteRefJSON  string  `json:"remoteRefJson"`
	ResultJSON     string  `json:"resultJson"`
	ErrorCode      *string `json:"errorCode,omitempty"`
	StartedAt      *string `json:"startedAt,omitempty"`
	FinishedAt     *string `json:"finishedAt,omitempty"`
}

type Submission struct {
	ID                   string  `json:"id"`
	MailboxID            int64   `json:"mailboxId"`
	MemberID             int64   `json:"memberId"`
	RequestID            string  `json:"requestId"`
	MessageID            string  `json:"messageId"`
	Status               string  `json:"status"`
	SMTPStatus           string  `json:"smtpStatus"`
	SentStatus           string  `json:"sentStatus"`
	Error                *string `json:"error,omitempty"`
	CreatedAt            string  `json:"createdAt"`
	UpdatedAt            string  `json:"updatedAt"`
}

// --- Sieve rule model (compiled to Sieve by internal/mailproto/sieve) ---

type SieveCondition struct {
	Field  string `json:"field"`
	Header string `json:"header,omitempty"`
	Op     string `json:"op"`
	Value  string `json:"value"`
}

type SieveAction struct {
	Type    string `json:"type"`
	Folder  string `json:"folder,omitempty"`
	Address string `json:"address,omitempty"`
	Flag    string `json:"flag,omitempty"`
}

type SieveRule struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Enabled    bool             `json:"enabled"`
	Match      string           `json:"match"`
	Conditions []SieveCondition `json:"conditions"`
	Actions    []SieveAction    `json:"actions"`
}

type Vacation struct {
	Enabled bool   `json:"enabled"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Days    int    `json:"days"`
}
