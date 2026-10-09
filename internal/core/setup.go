package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
	"mailhearth/internal/secrets"
)

type SetupStatus struct {
	NeedsSetup bool   `json:"needsSetup"`
	Step       string `json:"step"`
	OrgName    string `json:"orgName,omitempty"`
	DevStack   bool   `json:"devStack"`
}

func (s *Service) Status(ctx context.Context) (*SetupStatus, error) {
	st := &SetupStatus{DevStack: s.Cfg.DevStack}
	org, err := s.Org(ctx)
	if err == ErrNoSetup {
		st.NeedsSetup, st.Step = true, "org"
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	st.OrgName = org.Name
	done, err := db.GetSetting(ctx, s.DB, "setup.completed")
	if err != nil {
		return nil, err
	}
	if done == "1" {
		st.Step = "done"
		return st, nil
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM mail_connections WHERE org_id=?`, org.ID).Scan(&count); err != nil {
		return nil, err
	}
	st.NeedsSetup = true
	st.Step = "mailboxes"
	if count == 0 {
		st.Step = "connection"
	}
	return st, nil
}

func (s *Service) CompleteConnectionSetup(ctx context.Context, orgID, actor, connectionID int64, mailboxID *int64) error {
	c, err := s.MailConnection(ctx, orgID, connectionID)
	if err != nil {
		return err
	}
	if !c.Enabled {
		return provider.Errorf("endpoint_disabled", "连接已经停用")
	}
	var mailbox *model.Mailbox
	var endpoint *ResolvedEndpoint
	if mailboxID != nil {
		mailbox, err = s.Mailbox(ctx, orgID, *mailboxID)
		if err != nil {
			return err
		}
		if mailbox.ConnectionID != connectionID {
			return ErrNotFound
		}
		if mailbox.Kind != model.MailboxPersonal || mailbox.Status != model.MailboxActive || (mailbox.OwnerMemberID != nil && *mailbox.OwnerMemberID != actor) {
			return provider.Errorf("invalid", "需要选择可以绑定的 active personal 邮箱")
		}
		endpoint, err = s.ResolveEndpoint(ctx, orgID, mailbox.ID, provider.ProtocolIMAP)
		if err != nil {
			return err
		}
		candidate := candidateEndpoint{protocol: provider.ProtocolIMAP, resolved: endpoint}
		if err := s.validateCandidateEndpoint(ctx, &candidate); err != nil {
			return err
		}
	}
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		var permissionsJSON string
		if err := tx.QueryRowContext(ctx, `SELECT r.permissions_json FROM members m JOIN roles r ON r.id=m.role_id WHERE m.id=? AND m.org_id=? AND m.status='active'`, actor, orgID).Scan(&permissionsJSON); err != nil {
			if db.IsNotFound(err) {
				return ErrForbidden
			}
			return err
		}
		var permissions []string
		if err := json.Unmarshal([]byte(permissionsJSON), &permissions); err != nil {
			return err
		}
		if !HasPermission(permissions, model.PermOrgManage) {
			return ErrForbidden
		}
		var current bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail_connections WHERE id=? AND org_id=? AND revision=? AND enabled=1)`, connectionID, orgID, c.Revision).Scan(&current); err != nil {
			return err
		}
		if !current {
			return provider.Errorf("revision_conflict", "连接配置已经更新")
		}
		if mailbox != nil {
			var pending bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operation_locks WHERE org_id=? AND resource_key IN (?,?))`, orgID, "mailbox:"+fmtID(mailbox.ID), "connection:"+fmtID(connectionID)).Scan(&pending); err != nil {
				return err
			}
			if pending {
				return provider.Errorf("operation_in_progress", "邮箱或连接仍有进行中的操作")
			}
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mailbox_endpoints e JOIN credentials c ON c.id=e.credential_id WHERE e.mailbox_id=? AND e.protocol='imap' AND e.revision=? AND e.credential_id=? AND c.generation=? AND c.state='active')`, mailbox.ID, endpoint.EndpointRevision, endpoint.CredentialID, endpoint.CredentialGeneration).Scan(&current); err != nil {
				return err
			}
			if !current {
				return provider.Errorf("revision_conflict", "邮箱协议配置已经更新")
			}
			res, err := tx.ExecContext(ctx, `UPDATE mailboxes SET owner_member_id=?,revision=revision+1,access_revision=access_revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=? AND kind='personal' AND status='active' AND (owner_member_id IS NULL OR owner_member_id=?)`, actor, db.Now(), mailbox.ID, orgID, mailbox.Revision, actor)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n != 1 {
				return provider.Errorf("revision_conflict", "邮箱归属已经更新")
			}
		}
		return db.SetSetting(ctx, tx, "setup.completed", "1")
	})
	if err != nil {
		return err
	}
	if mailbox != nil {
		s.cancelMailRequests(0, mailbox.ID)
		if s.Pool != nil {
			s.Pool.InvalidateMailbox(mailbox.ID)
		}
	}
	return nil
}

type InitRequest struct {
	OrgName    string `json:"orgName"`
	AdminName  string `json:"adminName"`
	AdminEmail string `json:"adminEmail"`
	Password   string `json:"password"`
}

func (s *Service) Init(ctx context.Context, req InitRequest) (*model.Member, error) {
	if _, err := s.Org(ctx); err == nil {
		return nil, fmt.Errorf("%w: setup already completed", ErrConflict)
	} else if err != ErrNoSetup {
		return nil, err
	}
	orgName, adminName := strings.TrimSpace(req.OrgName), strings.TrimSpace(req.AdminName)
	if orgName == "" || adminName == "" {
		return nil, invalid("organisation name and your name are required")
	}
	email, err := NormalizeEmail(req.AdminEmail)
	if err != nil {
		return nil, err
	}
	if err := checkPasswordStrength(req.Password); err != nil {
		return nil, err
	}
	hash, err := secrets.HashPassword(req.Password)
	if err != nil {
		return nil, err
	}
	var memberID, orgID int64
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		var existing bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organizations)`).Scan(&existing); err != nil {
			return err
		}
		if existing {
			return ErrConflict
		}
		now := db.Now()
		res, err := tx.ExecContext(ctx, `INSERT INTO organizations(name,created_at) VALUES (?,?)`, orgName, now)
		if err != nil {
			return err
		}
		orgID, err = res.LastInsertId()
		if err != nil {
			return err
		}
		if err := s.ensureBuiltinRoles(ctx, tx, orgID); err != nil {
			return err
		}
		owner, err := s.roleByKey(ctx, tx, orgID, model.RoleOwner)
		if err != nil {
			return err
		}
		res, err = tx.ExecContext(ctx, `INSERT INTO members(org_id,display_name,login_email,password_hash,role_id,status,created_at,updated_at) VALUES (?,?,?,?,?,'active',?,?)`, orgID, adminName, email, hash, owner.ID, now, now)
		if err != nil {
			return err
		}
		memberID, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return nil, err
	}
	m, err := s.MemberByID(ctx, memberID)
	if err != nil {
		return nil, err
	}
	s.audit(ctx, orgID, memberID, "org.create", "org", fmtID(orgID), map[string]any{"name": orgName})
	return m, nil
}

type Discovery struct {
	ConnectionID int64                                `json:"connectionId"`
	SnapshotID   int64                                `json:"snapshotId"`
	Resources    []provider.DiscoverySnapshotResource `json:"resources"`
	Domains      []provider.DiscoverySnapshotResource `json:"domains"`
	Users        []string                             `json:"users"`
	Rules        []provider.DiscoverySnapshotResource `json:"rules"`
}

// Connect 创建明确的 Purelymail 连接，发现结果仅用于选择导入。
func (s *Service) Connect(ctx context.Context, orgID, actor int64, token string) (*Discovery, error) {
	c, err := s.CreateConnection(ctx, orgID, actor, CreateConnectionInput{ProviderKind: provider.Purelymail, Label: "Purelymail", APIAuth: &provider.APIAuth{APIKey: token}})
	if err != nil {
		return nil, err
	}
	return s.Discover(ctx, orgID, actor, c.ID)
}

func (s *Service) Discover(ctx context.Context, orgID int64, selection ...int64) (*Discovery, error) {
	if len(selection) != 2 || selection[0] < 1 || selection[1] < 1 {
		return nil, provider.Errorf("invalid", "需要 actorMemberId 和 connectionId")
	}
	op, err := s.runLocalManagementOperation(ctx, orgID, selection[0], uuid.NewString(), OperationPayload{Kind: "connection.discover", ConnectionID: selection[1]})
	if err != nil {
		return nil, err
	}
	d := &Discovery{ConnectionID: selection[1], Domains: []provider.DiscoverySnapshotResource{}, Users: []string{}, Rules: []provider.DiscoverySnapshotResource{}}
	if err := json.Unmarshal(op.Result, d); err != nil {
		return nil, err
	}
	for _, resource := range d.Resources {
		switch resource.Resource.ResourceType {
		case "domain":
			d.Domains = append(d.Domains, resource)
		case "mailbox":
			d.Users = append(d.Users, resource.Summary["address"])
		case "routing_rule", "alias", "rewrite":
			d.Rules = append(d.Rules, resource)
		}
	}
	return d, nil
}

type ImportResult struct {
	Domains          int      `json:"domains"`
	MailboxesNew     int      `json:"mailboxesNew"`
	MailboxesKept    int      `json:"mailboxesKept"`
	MailboxesGone    int      `json:"mailboxesGone"`
	AddressesNew     int      `json:"addressesNew"`
	AddressesUpdated int      `json:"addressesUpdated"`
	AddressesGone    int      `json:"addressesGone"`
	Warnings         []string `json:"warnings"`
}

func (s *Service) Sync(ctx context.Context, orgID, actor int64, connectionID ...int64) (*ImportResult, error) {
	if len(connectionID) != 1 || connectionID[0] < 1 {
		return nil, provider.Errorf("invalid", "需要 connectionId")
	}
	if _, err := s.runLocalManagementOperation(ctx, orgID, actor, uuid.NewString(), OperationPayload{Kind: "connection.sync", ConnectionID: connectionID[0]}); err != nil {
		return nil, err
	}
	res := &ImportResult{Warnings: []string{}}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM domain_bindings WHERE connection_id=?`, connectionID[0]).Scan(&res.Domains); err != nil {
		return nil, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM mailboxes WHERE connection_id=? AND remote_state='present'`, connectionID[0]).Scan(&res.MailboxesKept); err != nil {
		return nil, err
	}
	return res, nil
}

func (s *Service) CompleteSetup(ctx context.Context, orgID, actor int64, bindMailbox string, connectionID ...int64) (*ImportResult, error) {
	if len(connectionID) != 1 || connectionID[0] < 1 {
		return nil, provider.Errorf("invalid", "需要 connectionId")
	}
	var mailboxID *int64
	if bindMailbox != "" {
		mb, err := s.mailboxByAddress(ctx, s.DB, orgID, connectionID[0], bindMailbox)
		if err != nil {
			return nil, err
		}
		mailboxID = &mb.ID
	}
	if err := s.CompleteConnectionSetup(ctx, orgID, actor, connectionID[0], mailboxID); err != nil {
		return nil, err
	}
	res := &ImportResult{Warnings: []string{}}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM domain_bindings WHERE connection_id=?`, connectionID[0]).Scan(&res.Domains); err != nil {
		return nil, err
	}
	return res, nil
}

type Overview struct {
	Org            *model.Organization   `json:"org"`
	Connections    []OverviewConnection  `json:"connections"`
	Members        map[string]int        `json:"members"`
	Mailboxes      map[string]int        `json:"mailboxes"`
	Domains        []model.Domain        `json:"domains"`
	DomainBindings []model.DomainBinding `json:"domainBindings"`
	Addresses      int                   `json:"addresses"`
	Groups         int                   `json:"groups"`
	Unconnected    []model.Mailbox       `json:"unconnected"`
	Unassigned     []model.Mailbox       `json:"unassigned"`
	RecentAudit    []model.AuditEntry    `json:"recentAudit"`
	Pool           map[string]int        `json:"pool"`
	PendingSetup   bool                  `json:"pendingSetup"`
}

type OverviewConnection struct {
	ID                 int64                 `json:"id"`
	ProviderKind       provider.ProviderKind `json:"providerKind"`
	Label              string                `json:"label"`
	Enabled            bool                  `json:"enabled"`
	LastSyncAt         *string               `json:"lastSyncAt"`
	LastAPICheckStatus *string               `json:"lastApiCheckStatus,omitempty"`
	LastAPIErrorCode   *string               `json:"lastApiErrorCode,omitempty"`
}

func (s *Service) Overview(ctx context.Context, orgID int64) (*Overview, error) {
	o := &Overview{Members: map[string]int{}, Mailboxes: map[string]int{}, Pool: map[string]int{}, Connections: []OverviewConnection{}, Unconnected: []model.Mailbox{}, Unassigned: []model.Mailbox{}}
	var err error
	o.Org, err = s.Org(ctx)
	if err != nil {
		return nil, err
	}
	connections, err := s.Connections(ctx, orgID)
	if err != nil {
		return nil, err
	}
	for _, c := range connections {
		status := c.LastAPICheckStatus
		o.Connections = append(o.Connections, OverviewConnection{ID: c.ID, ProviderKind: c.ProviderKind, Label: c.Label, Enabled: c.Enabled, LastSyncAt: c.LastSyncAt, LastAPICheckStatus: &status, LastAPIErrorCode: c.LastAPIErrorCode})
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT status,COUNT(*) FROM members WHERE org_id=? GROUP BY status`, orgID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			rows.Close()
			return nil, err
		}
		o.Members[key] = count
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = s.DB.QueryContext(ctx, `SELECT kind||'.'||status,COUNT(*) FROM mailboxes WHERE org_id=? GROUP BY kind,status`, orgID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			rows.Close()
			return nil, err
		}
		o.Mailboxes[key] = count
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	o.Domains, err = s.Domains(ctx, orgID)
	if err != nil {
		return nil, err
	}
	o.DomainBindings, err = s.DomainBindings(ctx, orgID, 0, 0)
	if err != nil {
		return nil, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM addresses WHERE org_id=? AND kind!='primary'`, orgID).Scan(&o.Addresses); err != nil {
		return nil, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM groups WHERE org_id=?`, orgID).Scan(&o.Groups); err != nil {
		return nil, err
	}
	all, err := s.Mailboxes(ctx, orgID)
	if err != nil {
		return nil, err
	}
	for _, mb := range all {
		if mb.Status != model.MailboxActive {
			continue
		}
		if !mb.HasCredential {
			o.Unconnected = append(o.Unconnected, mb)
		}
		if mb.Kind == model.MailboxPersonal && mb.OwnerMemberID == nil {
			o.Unassigned = append(o.Unassigned, mb)
		}
	}
	o.RecentAudit, err = s.Audit(ctx, orgID, 0, 10)
	if err != nil {
		return nil, err
	}
	if s.Pool != nil {
		open, idle, watchers := s.Pool.Stats()
		o.Pool["open"], o.Pool["idle"], o.Pool["watchers"] = open, idle, watchers
	}
	return o, nil
}
