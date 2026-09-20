// Package model holds the organisation-level domain types shared by the
// services and the HTTP API. These are the words administrators see:
// members, mailboxes, addresses, shared mailboxes, groups and roles.
package model

// Permission keys. A role is a named set of these.
const (
	PermOrgOwner        = "org.owner"        // transfer ownership, delete organisation
	PermOrgManage       = "org.manage"       // organisation settings, Purelymail connection, roles
	PermDomainsManage   = "domains.manage"   // add/remove domains, DNS checks
	PermMembersManage   = "members.manage"   // invite, edit, disable, offboard members
	PermMailboxesManage = "mailboxes.manage" // create/bind/rotate personal mailboxes
	PermSharedManage    = "shared.manage"    // shared mailboxes and access grants
	PermAddressesManage = "addresses.manage" // aliases, forwards, catch-all
	PermGroupsManage    = "groups.manage"    // groups and distribution addresses
	PermAuditRead       = "audit.read"       // read the audit log
	PermBillingRead     = "billing.read"     // see Purelymail credit
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

// Address kinds.
const (
	AddressPrimary  = "primary"  // the login address of a mailbox
	AddressAlias    = "alias"    // routing rule -> exactly one mailbox in this org
	AddressForward  = "forward"  // routing rule -> one or more arbitrary targets
	AddressGroup    = "group"    // routing rule maintained from group membership
	AddressCatchall = "catchall" // *@domain
	AddressPrefix   = "prefix"   // prefix*@domain
)

// Access levels for shared mailboxes.
const (
	AccessFull = "full" // read, send, organise
	AccessSend = "send" // read and send, cannot delete
	AccessRead = "read" // read only
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

type Mailbox struct {
	ID            int64           `json:"id"`
	Kind          string          `json:"kind"`
	Address       string          `json:"address"` // the Purelymail user, e.g. alice@acme.com
	DomainID      *int64          `json:"domainId"`
	DisplayName   string          `json:"displayName"`
	OwnerMemberID *int64          `json:"ownerMemberId"`
	OwnerName     string          `json:"ownerName"`
	HasCredential bool            `json:"hasCredential"`
	CredentialAt  *string         `json:"credentialAt"`
	Status        string          `json:"status"`
	Imported      bool            `json:"imported"`
	Settings      MailboxSettings `json:"settings"`
	AccessCount   int             `json:"accessCount"`
	CreatedAt     string          `json:"createdAt"`
	UpdatedAt     string          `json:"updatedAt"`
}

// MailboxSettings is stored as JSON on the mailbox row.
type MailboxSettings struct {
	SieveRules []SieveRule `json:"sieveRules"`
	Vacation   *Vacation   `json:"vacation,omitempty"`
	SieveSync  string      `json:"sieveSync,omitempty"` // last successful upload time
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
	ID            int64  `json:"id"`
	MailboxID     int64  `json:"mailboxId"`
	Address       string `json:"address"`
	DisplayName   string `json:"displayName"`
	ReplyTo       string `json:"replyTo"`
	SignatureHTML string `json:"signatureHtml"`
	IsDefault     bool   `json:"isDefault"`
}

type Address struct {
	ID         int64    `json:"id"`
	DomainID   int64    `json:"domainId"`
	Domain     string   `json:"domain"`
	LocalPart  string   `json:"localPart"`
	Address    string   `json:"address"`
	Kind       string   `json:"kind"`
	MailboxID  *int64   `json:"mailboxId"`
	GroupID    *int64   `json:"groupId"`
	Targets    []string `json:"targets"`
	PMRuleID   *int64   `json:"pmRuleId"`
	IsPrefix   bool     `json:"isPrefix"`
	IsCatchall bool     `json:"isCatchall"`
	Note       string   `json:"note"`
	CreatedAt  string   `json:"createdAt"`
}

type Group struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	MemberIDs   []int64  `json:"memberIds"`
	Address     *Address `json:"address"`
	CreatedAt   string   `json:"createdAt"`
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

// --- Sieve rule model (compiled to Sieve by internal/mailproto/sieve) ---

type SieveCondition struct {
	Field  string `json:"field"`            // from | to | subject | body | header | size
	Header string `json:"header,omitempty"` // when field == header
	Op     string `json:"op"`               // contains | not_contains | is | matches | over | under
	Value  string `json:"value"`
}

type SieveAction struct {
	Type    string `json:"type"`              // move | copy | flag | markread | forward | redirect | discard | stop
	Folder  string `json:"folder,omitempty"`  // move/copy
	Address string `json:"address,omitempty"` // forward/redirect
	Flag    string `json:"flag,omitempty"`    // flag (e.g. \Flagged)
}

type SieveRule struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Enabled    bool             `json:"enabled"`
	Match      string           `json:"match"` // all | any
	Conditions []SieveCondition `json:"conditions"`
	Actions    []SieveAction    `json:"actions"`
}

type Vacation struct {
	Enabled bool   `json:"enabled"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Days    int    `json:"days"`
}
