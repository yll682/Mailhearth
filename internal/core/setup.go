package core

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/purelymail"
	"mailhearth/internal/secrets"
)

// SetupStatus tells the SPA which onboarding step to show.
type SetupStatus struct {
	NeedsSetup bool   `json:"needsSetup"`
	Step       string `json:"step"` // org | connect | import | done
	OrgName    string `json:"orgName,omitempty"`
	DevStack   bool   `json:"devStack"`
}

// Status reports the installation state.
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
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM purelymail_accounts WHERE org_id = ?`, org.ID).Scan(&n)
	if n == 0 {
		st.NeedsSetup, st.Step = true, "connect"
		return st, nil
	}
	done, _ := db.GetSetting(ctx, s.DB, "setup.completed")
	if done != "1" {
		st.NeedsSetup, st.Step = true, "import"
		return st, nil
	}
	st.Step = "done"
	return st, nil
}

// InitRequest creates the organisation and its owner.
type InitRequest struct {
	OrgName    string `json:"orgName"`
	AdminName  string `json:"adminName"`
	AdminEmail string `json:"adminEmail"`
	Password   string `json:"password"`
}

// Init performs the first setup step. It refuses to run twice.
func (s *Service) Init(ctx context.Context, req InitRequest) (*model.Member, error) {
	if _, err := s.Org(ctx); err == nil {
		return nil, fmt.Errorf("%w: setup already completed", ErrConflict)
	}
	orgName := strings.TrimSpace(req.OrgName)
	adminName := strings.TrimSpace(req.AdminName)
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
	var memberID int64
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		now := db.Now()
		res, err := tx.ExecContext(ctx, `INSERT INTO organizations(name, created_at) VALUES (?, ?)`, orgName, now)
		if err != nil {
			return err
		}
		orgID, _ := res.LastInsertId()
		if err := s.ensureBuiltinRoles(ctx, tx, orgID); err != nil {
			return err
		}
		owner, err := s.roleByKey(ctx, tx, orgID, model.RoleOwner)
		if err != nil {
			return err
		}
		res, err = tx.ExecContext(ctx, `INSERT INTO members(org_id, display_name, login_email, password_hash, role_id, status, created_at, updated_at) VALUES (?,?,?,?,?,'active',?,?)`,
			orgID, adminName, email, hash, owner.ID, now, now)
		if err != nil {
			return err
		}
		memberID, _ = res.LastInsertId()
		return nil
	})
	if err != nil {
		return nil, err
	}
	m, err := s.MemberByID(ctx, memberID)
	if err != nil {
		return nil, err
	}
	org, _ := s.Org(ctx)
	s.audit(ctx, org.ID, memberID, "org.create", "org", fmt.Sprint(org.ID), map[string]any{"name": orgName})
	return m, nil
}

// ConnectionInfo describes the stored Purelymail connection (never the token).
type ConnectionInfo struct {
	Connected  bool    `json:"connected"`
	TokenHint  string  `json:"tokenHint"`
	Credit     string  `json:"credit"`
	LastSyncAt *string `json:"lastSyncAt"`
	LastError  string  `json:"lastError"`
	APIURL     string  `json:"apiUrl"`
}

// Connection returns the connection summary.
func (s *Service) Connection(ctx context.Context, orgID int64) (*ConnectionInfo, error) {
	info := &ConnectionInfo{APIURL: s.Cfg.PurelymailAPIURL}
	var last sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT token_hint, credit, last_sync_at, last_error FROM purelymail_accounts WHERE org_id = ? ORDER BY id LIMIT 1`, orgID).Scan(&info.TokenHint, &info.Credit, &last, &info.LastError)
	if db.IsNotFound(err) {
		return info, nil
	}
	if err != nil {
		return nil, err
	}
	info.Connected = true
	info.LastSyncAt = nullStr(last)
	return info, nil
}

// Discovery summarises what exists on the Purelymail account.
type Discovery struct {
	Credit   string                   `json:"credit"`
	Domains  []purelymail.Domain      `json:"domains"`
	Users    []string                 `json:"users"`
	Rules    []purelymail.RoutingRule `json:"rules"`
	Existing struct {
		Mailboxes int `json:"mailboxes"`
		Addresses int `json:"addresses"`
	} `json:"existing"`
}

// Connect validates and stores the API token, returning what was found.
func (s *Service) Connect(ctx context.Context, orgID, actor int64, token string) (*Discovery, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, invalid("API token is required")
	}
	api := s.NewPM(token)
	credit, err := api.CheckAccountCredit(ctx)
	if err != nil {
		if purelymail.IsInvalidToken(err) {
			return nil, invalid("Purelymail rejected this API token")
		}
		return nil, upstream("purelymail", err)
	}
	enc, err := s.Box.Seal(token)
	if err != nil {
		return nil, err
	}
	now := db.Now()
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM purelymail_accounts WHERE org_id = ?`, orgID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO purelymail_accounts(org_id, label, api_token_enc, token_hint, credit, created_at, updated_at) VALUES (?,?,?,?,?,?,?)`,
			orgID, "Purelymail", enc, secrets.Hint(token), credit, now, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.audit(ctx, orgID, actor, "connection.set", "org", fmt.Sprint(orgID), map[string]any{"hint": secrets.Hint(token)})
	return s.Discover(ctx, orgID)
}

// Discover lists the account contents without changing anything.
func (s *Service) Discover(ctx context.Context, orgID int64) (*Discovery, error) {
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return nil, err
	}
	d := &Discovery{Domains: []purelymail.Domain{}, Users: []string{}, Rules: []purelymail.RoutingRule{}}
	if d.Credit, err = api.CheckAccountCredit(ctx); err != nil {
		return nil, upstream("purelymail credit", err)
	}
	if d.Domains, err = api.ListDomains(ctx, true); err != nil {
		return nil, upstream("purelymail list domains", err)
	}
	if d.Users, err = api.ListUsers(ctx); err != nil {
		return nil, upstream("purelymail list users", err)
	}
	if d.Rules, err = api.ListRoutingRules(ctx); err != nil {
		return nil, upstream("purelymail list routing rules", err)
	}
	if d.Domains == nil {
		d.Domains = []purelymail.Domain{}
	}
	if d.Users == nil {
		d.Users = []string{}
	}
	if d.Rules == nil {
		d.Rules = []purelymail.RoutingRule{}
	}
	s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM mailboxes WHERE org_id = ?`, orgID).Scan(&d.Existing.Mailboxes)
	s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM addresses WHERE org_id = ? AND kind != 'primary'`, orgID).Scan(&d.Existing.Addresses)
	return d, nil
}

// ImportResult summarises a sync.
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

// Sync imports domains, users and routing rules into the organisation
// model. It is idempotent and never modifies the Purelymail account.
func (s *Service) Sync(ctx context.Context, orgID, actor int64) (*ImportResult, error) {
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return nil, err
	}
	d, err := s.Discover(ctx, orgID)
	if err != nil {
		s.DB.ExecContext(ctx, `UPDATE purelymail_accounts SET last_error = ?, updated_at = ? WHERE org_id = ?`, err.Error(), db.Now(), orgID)
		return nil, err
	}
	res := &ImportResult{Warnings: []string{}}
	now := db.Now()
	domainIDs := map[string]int64{}
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		// Domains
		for _, dom := range d.Domains {
			id, err := s.upsertDomain(ctx, tx, orgID, dom)
			if err != nil {
				return err
			}
			domainIDs[strings.ToLower(dom.Name)] = id
			res.Domains++
		}
		// Users -> mailboxes
		seenUsers := map[string]bool{}
		for _, u := range d.Users {
			u = strings.ToLower(strings.TrimSpace(u))
			if u == "" {
				continue
			}
			seenUsers[u] = true
			local, domain := SplitAddress(u)
			domID, ok := domainIDs[domain]
			if !ok {
				// Users on domains not returned by listDomains (should not happen); still import.
				id, err := s.upsertDomain(ctx, tx, orgID, purelymail.Domain{Name: domain})
				if err != nil {
					return err
				}
				domainIDs[domain] = id
				domID = id
			}
			var existing int64
			err := tx.QueryRowContext(ctx, `SELECT id FROM mailboxes WHERE org_id = ? AND pm_user = ?`, orgID, u).Scan(&existing)
			if err != nil && !db.IsNotFound(err) {
				return err
			}
			if existing == 0 {
				r, err := tx.ExecContext(ctx, `INSERT INTO mailboxes(org_id, kind, pm_user, domain_id, display_name, status, imported, created_at, updated_at) VALUES (?,?,?,?,?,'active',1,?,?)`,
					orgID, model.MailboxPersonal, u, domID, local, now, now)
				if err != nil {
					return err
				}
				existing, _ = r.LastInsertId()
				if _, err := tx.ExecContext(ctx, `INSERT INTO identities(mailbox_id, address, display_name, is_default, created_at) VALUES (?,?,?,1,?)`, existing, u, local, now); err != nil {
					return err
				}
				res.MailboxesNew++
			} else {
				if _, err := tx.ExecContext(ctx, `UPDATE mailboxes SET status = CASE WHEN status = 'archived' THEN 'active' ELSE status END, domain_id = ?, updated_at = ? WHERE id = ?`, domID, now, existing); err != nil {
					return err
				}
				res.MailboxesKept++
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO addresses(org_id, domain_id, local_part, address, kind, mailbox_id, created_at, updated_at) VALUES (?,?,?,?,'primary',?,?,?)
				ON CONFLICT(org_id, address) DO UPDATE SET mailbox_id = excluded.mailbox_id, domain_id = excluded.domain_id, updated_at = excluded.updated_at`, orgID, domID, local, u, existing, now, now); err != nil {
				return err
			}
		}
		// Mailboxes that disappeared upstream
		rows, err := tx.QueryContext(ctx, `SELECT id, pm_user FROM mailboxes WHERE org_id = ? AND status != 'archived'`, orgID)
		if err != nil {
			return err
		}
		var gone []int64
		for rows.Next() {
			var id int64
			var user string
			rows.Scan(&id, &user)
			if !seenUsers[user] {
				gone = append(gone, id)
			}
		}
		rows.Close()
		for _, id := range gone {
			if _, err := tx.ExecContext(ctx, `UPDATE mailboxes SET status = 'archived', credential_enc = '', updated_at = ? WHERE id = ?`, now, id); err != nil {
				return err
			}
			res.MailboxesGone++
		}
		// Routing rules -> addresses
		seenRules := map[int64]bool{}
		for _, r := range d.Rules {
			seenRules[r.ID] = true
			domain := strings.ToLower(r.DomainName)
			domID, ok := domainIDs[domain]
			if !ok {
				res.Warnings = append(res.Warnings, fmt.Sprintf("rule %d targets unknown domain %s", r.ID, domain))
				continue
			}
			local := strings.ToLower(r.MatchUser)
			full := local + "@" + domain
			kind := model.AddressForward
			switch {
			case r.Catchall:
				local, full, kind = "", "*@"+domain, model.AddressCatchall
			case r.Prefix:
				full, kind = local+"*@"+domain, model.AddressPrefix
			}
			var mailboxID any
			if kind == model.AddressForward && len(r.TargetAddresses) == 1 {
				var mid int64
				if err := tx.QueryRowContext(ctx, `SELECT id FROM mailboxes WHERE org_id = ? AND pm_user = ?`, orgID, strings.ToLower(r.TargetAddresses[0])).Scan(&mid); err == nil {
					kind, mailboxID = model.AddressAlias, mid
				}
			}
			var existingID int64
			var existingKind string
			var existingGroup sql.NullInt64
			err := tx.QueryRowContext(ctx, `SELECT id, kind, group_id FROM addresses WHERE org_id = ? AND address = ?`, orgID, full).Scan(&existingID, &existingKind, &existingGroup)
			if err != nil && !db.IsNotFound(err) {
				return err
			}
			targets := toJSON(r.TargetAddresses)
			switch {
			case existingID == 0:
				if _, err := tx.ExecContext(ctx, `INSERT INTO addresses(org_id, domain_id, local_part, address, kind, mailbox_id, targets_json, pm_rule_id, is_prefix, is_catchall, note, created_at, updated_at)
					VALUES (?,?,?,?,?,?,?,?,?,?,'',?,?)`, orgID, domID, local, full, kind, mailboxID, targets, r.ID, boolInt(r.Prefix), boolInt(r.Catchall), now, now); err != nil {
					return err
				}
				res.AddressesNew++
			case existingKind == model.AddressPrimary:
				// A rule on a mailbox's own address: forwarding overrides delivery.
				if _, err := tx.ExecContext(ctx, `UPDATE addresses SET kind = 'forward', targets_json = ?, pm_rule_id = ?, updated_at = ? WHERE id = ?`, targets, r.ID, now, existingID); err != nil {
					return err
				}
				res.AddressesUpdated++
			case existingGroup.Valid:
				if _, err := tx.ExecContext(ctx, `UPDATE addresses SET targets_json = ?, pm_rule_id = ?, updated_at = ? WHERE id = ?`, targets, r.ID, now, existingID); err != nil {
					return err
				}
				res.AddressesUpdated++
			default:
				if _, err := tx.ExecContext(ctx, `UPDATE addresses SET kind = ?, mailbox_id = ?, targets_json = ?, pm_rule_id = ?, updated_at = ? WHERE id = ?`, kind, mailboxID, targets, r.ID, now, existingID); err != nil {
					return err
				}
				res.AddressesUpdated++
			}
		}
		// Rule-backed addresses that disappeared upstream
		rows, err = tx.QueryContext(ctx, `SELECT id, pm_rule_id, kind, mailbox_id FROM addresses WHERE org_id = ? AND pm_rule_id IS NOT NULL`, orgID)
		if err != nil {
			return err
		}
		type goneAddr struct {
			id, mailbox int64
			kind        string
		}
		var goneAddrs []goneAddr
		for rows.Next() {
			var id, ruleID int64
			var kind string
			var mailbox sql.NullInt64
			rows.Scan(&id, &ruleID, &kind, &mailbox)
			if !seenRules[ruleID] {
				goneAddrs = append(goneAddrs, goneAddr{id: id, kind: kind, mailbox: mailbox.Int64})
			}
		}
		rows.Close()
		for _, g := range goneAddrs {
			// A forward that sat on a mailbox address reverts to primary.
			var isPrimaryAddr int
			tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM mailboxes b JOIN addresses a ON a.address = b.pm_user WHERE a.id = ?`, g.id).Scan(&isPrimaryAddr)
			if isPrimaryAddr > 0 {
				if _, err := tx.ExecContext(ctx, `UPDATE addresses SET kind = 'primary', targets_json = '[]', pm_rule_id = NULL, note = '', updated_at = ? WHERE id = ?`, now, g.id); err != nil {
					return err
				}
			} else if g.kind == model.AddressGroup {
				if _, err := tx.ExecContext(ctx, `UPDATE addresses SET pm_rule_id = NULL, targets_json = '[]', updated_at = ? WHERE id = ?`, now, g.id); err != nil {
					return err
				}
			} else {
				if _, err := tx.ExecContext(ctx, `DELETE FROM addresses WHERE id = ?`, g.id); err != nil {
					return err
				}
			}
			res.AddressesGone++
		}
		_, err = tx.ExecContext(ctx, `UPDATE purelymail_accounts SET credit = ?, last_sync_at = ?, last_error = '', updated_at = ? WHERE org_id = ?`, d.Credit, now, now, orgID)
		return err
	})
	if err != nil {
		return nil, err
	}
	_ = api
	s.audit(ctx, orgID, actor, "connection.sync", "org", fmt.Sprint(orgID), res)
	return res, nil
}

// CompleteSetup runs the first sync, optionally binds the owner to one of
// the imported mailboxes, and marks setup as done.
func (s *Service) CompleteSetup(ctx context.Context, orgID, actor int64, bindMailbox string) (*ImportResult, error) {
	res, err := s.Sync(ctx, orgID, actor)
	if err != nil {
		return nil, err
	}
	if bindMailbox = strings.ToLower(strings.TrimSpace(bindMailbox)); bindMailbox != "" {
		owner, err := s.Member(ctx, orgID, actor)
		if err != nil {
			return nil, err
		}
		mb, err := s.mailboxByAddress(ctx, s.DB, orgID, bindMailbox)
		if err != nil {
			res.Warnings = append(res.Warnings, "mailbox "+bindMailbox+" not found")
		} else if _, err := s.BindMailbox(ctx, orgID, actor, mb.ID, actor, owner.DisplayName); err != nil {
			res.Warnings = append(res.Warnings, "could not connect "+bindMailbox+": "+err.Error())
		}
	}
	if err := db.SetSetting(ctx, s.DB, "setup.completed", "1"); err != nil {
		return nil, err
	}
	return res, nil
}

// Overview is the admin dashboard payload.
type Overview struct {
	Org          *model.Organization `json:"org"`
	Connection   *ConnectionInfo     `json:"connection"`
	Members      map[string]int      `json:"members"`
	Mailboxes    map[string]int      `json:"mailboxes"`
	Domains      []model.Domain      `json:"domains"`
	Addresses    int                 `json:"addresses"`
	Groups       int                 `json:"groups"`
	Unconnected  []model.Mailbox     `json:"unconnected"`
	Unassigned   []model.Mailbox     `json:"unassigned"`
	RecentAudit  []model.AuditEntry  `json:"recentAudit"`
	Pool         map[string]int      `json:"pool"`
	PendingSetup bool                `json:"pendingSetup"`
}

// Overview builds the dashboard.
func (s *Service) Overview(ctx context.Context, orgID int64) (*Overview, error) {
	o := &Overview{Members: map[string]int{}, Mailboxes: map[string]int{}, Pool: map[string]int{}}
	var err error
	if o.Org, err = s.Org(ctx); err != nil {
		return nil, err
	}
	if o.Connection, err = s.Connection(ctx, orgID); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT status, COUNT(1) FROM members WHERE org_id = ? GROUP BY status`, orgID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k string
		var n int
		rows.Scan(&k, &n)
		o.Members[k] = n
	}
	rows.Close()
	rows, err = s.DB.QueryContext(ctx, `SELECT kind || '.' || status, COUNT(1) FROM mailboxes WHERE org_id = ? GROUP BY kind, status`, orgID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k string
		var n int
		rows.Scan(&k, &n)
		o.Mailboxes[k] = n
	}
	rows.Close()
	if o.Domains, err = s.Domains(ctx, orgID); err != nil {
		return nil, err
	}
	s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM addresses WHERE org_id = ? AND kind != 'primary'`, orgID).Scan(&o.Addresses)
	s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM groups WHERE org_id = ?`, orgID).Scan(&o.Groups)
	all, err := s.Mailboxes(ctx, orgID)
	if err != nil {
		return nil, err
	}
	o.Unconnected, o.Unassigned = []model.Mailbox{}, []model.Mailbox{}
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
	if o.RecentAudit, err = s.Audit(ctx, orgID, 0, 10); err != nil {
		return nil, err
	}
	if s.Pool != nil {
		open, idle, watchers := s.Pool.Stats()
		o.Pool["open"], o.Pool["idle"], o.Pool["watchers"] = open, idle, watchers
	}
	return o, nil
}
