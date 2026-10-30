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
	MailboxRemotePresent      = "present"
	MailboxRemoteMissing      = "missing"
	MailboxRemoteInaccessible = "inaccessible"
	MailboxRemoteUnknown      = "unknown"
	MailboxRemoteExternal     = "external"

	ManagementModeAPI      = "api"
	ManagementModeExternal = "external"
)

// Address kinds.
const (
	AddressPrimary      = "primary"
	AddressAlias        = "alias"
	AddressForward      = "forward"
	AddressGroup        = "group"
	AddressCatchall     = "catchall"
	AddressPrefix       = "prefix"
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
	Status      string  `json:"status" tstype:"\"invited\" | \"active\" | \"disabled\" | \"departed\""`
	HasPassword bool    `json:"hasPassword"`
	Revision    int64   `json:"revision"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
	LastLoginAt *string `json:"lastLoginAt"`
	DepartedAt  *string `json:"departedAt"`
}

type Domain struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	MailboxCount int    `json:"mailboxCount"`
	AddressCount int    `json:"addressCount"`
	CreatedAt    string `json:"createdAt"`
}

type DomainBinding struct {
	ID               int64             `json:"id"`
	DomainID         int64             `json:"domainId"`
	DomainName       string            `json:"domainName"`
	ConnectionID     int64             `json:"connectionId"`
	ConnectionLabel  string            `json:"connectionLabel"`
	ManagementMode   string            `json:"managementMode" tstype:"\"api\" | \"external\""`
	RemoteState      string            `json:"remoteState"`
	ProviderSettings map[string]any    `json:"providerSettings" tstype:"{ allowAccountReset?: boolean; symbolicSubaddressing?: boolean; [key: string]: unknown }"`
	DNSStatus        map[string]string `json:"dnsStatus"`
	DNSCheckedAt     *string           `json:"dnsCheckedAt"`
	Revision         int64             `json:"revision"`
	CreatedAt        string            `json:"createdAt"`
	UpdatedAt        string            `json:"updatedAt"`
}

type Mailbox struct {
	ID              int64            `json:"id"`
	OrgID           int64            `json:"orgId"`
	ConnectionID    int64            `json:"connectionId"`
	ConnectionLabel string           `json:"connectionLabel"`
	Kind            string           `json:"kind" tstype:"\"personal\" | \"shared\""`
	Address         string           `json:"address"`
	AddressKey      string           `json:"addressKey"`
	DomainID        *int64           `json:"domainId"`
	DomainBindingID *int64           `json:"domainBindingId,omitempty"`
	DisplayName     string           `json:"displayName"`
	OwnerMemberID   *int64           `json:"ownerMemberId"`
	OwnerName       string           `json:"ownerName"`
	Status          string           `json:"status" tstype:"\"active\" | \"suspended\" | \"archived\""`
	Imported        bool             `json:"imported"`
	ManagementMode  string           `json:"managementMode" tstype:"\"api\" | \"external\""`
	RemoteState     string           `json:"remoteState"`
	PMUser          string           `json:"-"`
	HasCredential   bool             `json:"-"`
	CredentialAt    *string          `json:"-"`
	CredentialLabel string           `json:"-"`
	Revision        int64            `json:"revision"`
	AccessRevision  int64            `json:"accessRevision"`
	SentCopyMode    string           `json:"sentCopyMode" tstype:"\"append\" | \"server\""`
	FolderMapping   FolderMapping    `json:"folderMapping"`
	Protocols       MailboxProtocols `json:"protocols"`
	Settings        MailboxSettings  `json:"settings"`
	AccessCount     int              `json:"accessCount"`
	CreatedAt       string           `json:"createdAt"`
	UpdatedAt       string           `json:"updatedAt"`
}

type ProtocolStatus struct {
	Readiness   string `json:"readiness" tstype:"\"ready\" | \"unconfigured\" | \"unverified\" | \"disabled\""`
	CheckStatus string `json:"checkStatus" tstype:"\"never\" | \"passed\" | \"failed\" | \"stale\""`
}

type MailboxProtocols struct {
	IMAP        *ProtocolStatus `json:"imap"`
	SMTP        *ProtocolStatus `json:"smtp"`
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
	SieveRules      []SieveRule `json:"sieveRules"`
	Vacation        *Vacation   `json:"vacation,omitempty"`
	SieveSync       string      `json:"sieveSync,omitempty"`
	SieveScriptName string      `json:"sieveScriptName,omitempty"`
	SieveScriptHash string      `json:"sieveScriptHash,omitempty"`
}

type MailboxAccess struct {
	ID         int64  `json:"id"`
	MailboxID  int64  `json:"mailboxId"`
	MemberID   int64  `json:"memberId"`
	MemberName string `json:"memberName"`
	Level      string `json:"level" tstype:"\"full\" | \"send\" | \"read\""`
	GrantedAt  string `json:"grantedAt"`
}

type Identity struct {
	ID                     int64   `json:"id"`
	MailboxID              int64   `json:"mailboxId"`
	Address                string  `json:"address"`
	DisplayName            string  `json:"displayName"`
	ReplyTo                string  `json:"replyTo"`
	SignatureHTML          string  `json:"signatureHtml"`
	IsDefault              bool    `json:"isDefault"`
	AuthorizationSource    string  `json:"authorizationSource" tstype:"\"admin\" | \"provider\""`
	AuthorizationStatus    string  `json:"authorizationStatus" tstype:"\"allowed\" | \"unverified\" | \"denied\""`
	AuthorizationCheckedAt *string `json:"authorizationCheckedAt"`
	Revision               int64   `json:"revision"`
}

type Address struct {
	ID              int64    `json:"id"`
	OrgID           int64    `json:"orgId"`
	ConnectionID    int64    `json:"connectionId"`
	DomainID        int64    `json:"domainId"`
	DomainBindingID *int64   `json:"domainBindingId,omitempty"`
	Domain          string   `json:"domain"`
	LocalPart       string   `json:"localPart"`
	Address         string   `json:"address"`
	AddressKey      string   `json:"addressKey"`
	Kind            string   `json:"kind" tstype:"\"primary\" | \"alias\" | \"forward\" | \"group\" | \"catchall\" | \"prefix\" | \"external_rule\""`
	MailboxID       *int64   `json:"mailboxId"`
	GroupID         *int64   `json:"groupId"`
	Targets         []string `json:"targets"`
	DesiredTargets  []string `json:"desiredTargets"`
	ObservedTargets []string `json:"observedTargets"`
	ManagementMode  string   `json:"managementMode" tstype:"\"api\" | \"external\""`
	SyncState       string   `json:"syncState"`
	Revision        int64    `json:"revision"`
	IsPrefix        bool     `json:"isPrefix"`
	IsCatchall      bool     `json:"isCatchall"`
	Note            string   `json:"note"`
	CreatedAt       string   `json:"createdAt"`
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
	ID           int64          `json:"id"`
	MailboxID    int64          `json:"mailboxId"`
	Targets      []string       `json:"targets"`
	DeliveryMode string         `json:"deliveryMode" tstype:"\"redirect\" | \"copy\" | \"unverified\""`
	RemoteStatus map[string]any `json:"remoteStatus" tstype:"{ desiredTargets?: string[]; observedTargets?: string[]; statusByTarget?: Record<string, string>; remoteStates?: Record<string, string>; systemVerified?: boolean; verificationSource?: string }"`
	SyncState    string         `json:"syncState"`
	Revision     int64          `json:"revision"`
	CreatedAt    string         `json:"createdAt"`
	UpdatedAt    string         `json:"updatedAt"`
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

type OperationStep struct {
	OperationID   string  `json:"operationId"`
	StepKey       string  `json:"stepKey"`
	Sequence      int     `json:"sequence"`
	Status        string  `json:"status"`
	RemoteRefJSON string  `json:"remoteRefJson"`
	ResultJSON    string  `json:"resultJson"`
	ErrorCode     *string `json:"errorCode,omitempty"`
	StartedAt     *string `json:"startedAt,omitempty"`
	FinishedAt    *string `json:"finishedAt,omitempty"`
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
	Match      string           `json:"match" tstype:"\"all\" | \"any\""`
	Conditions []SieveCondition `json:"conditions"`
	Actions    []SieveAction    `json:"actions"`
}

type Vacation struct {
	Enabled bool   `json:"enabled"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Days    int    `json:"days"`
}
