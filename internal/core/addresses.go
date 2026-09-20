package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"mailhearth/internal/db"
	"mailhearth/internal/mailproto/mimeutil"
	"mailhearth/internal/model"
	"mailhearth/internal/purelymail"
	"mailhearth/internal/secrets"
)

func secretsRandomPassword() string { return secrets.RandomPassword() }

func sanitizeSignature(html string) string {
	html = strings.TrimSpace(html)
	if html == "" {
		return ""
	}
	if len(html) > 20000 {
		html = html[:20000]
	}
	return mimeutil.SanitizeSignature(html)
}

const addressSelect = `SELECT a.id, a.domain_id, d.name, a.local_part, a.address, a.kind, a.mailbox_id, a.group_id, a.targets_json, a.pm_rule_id, a.is_prefix, a.is_catchall, a.note, a.created_at
	FROM addresses a JOIN domains d ON d.id = a.domain_id`

func scanAddress(row interface{ Scan(...any) error }) (*model.Address, error) {
	var a model.Address
	var mailboxID, groupID, ruleID sql.NullInt64
	var targets string
	var prefix, catchall int
	if err := row.Scan(&a.ID, &a.DomainID, &a.Domain, &a.LocalPart, &a.Address, &a.Kind, &mailboxID, &groupID, &targets, &ruleID, &prefix, &catchall, &a.Note, &a.CreatedAt); err != nil {
		return nil, err
	}
	a.MailboxID, a.GroupID, a.PMRuleID = nullInt(mailboxID), nullInt(groupID), nullInt(ruleID)
	a.IsPrefix, a.IsCatchall = prefix == 1, catchall == 1
	json.Unmarshal([]byte(targets), &a.Targets)
	if a.Targets == nil {
		a.Targets = []string{}
	}
	return &a, nil
}

// Addresses lists every address (primary ones included).
func (s *Service) Addresses(ctx context.Context, orgID int64) ([]model.Address, error) {
	rows, err := s.DB.QueryContext(ctx, addressSelect+` WHERE a.org_id = ? ORDER BY d.name, a.is_catchall, a.local_part`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Address{}
	for rows.Next() {
		a, err := scanAddress(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// Address fetches one address.
func (s *Service) Address(ctx context.Context, orgID, id int64) (*model.Address, error) {
	a, err := scanAddress(s.DB.QueryRowContext(ctx, addressSelect+` WHERE a.org_id = ? AND a.id = ?`, orgID, id))
	if db.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return a, err
}

// AddressInput creates or retargets an address.
type AddressInput struct {
	DomainID  int64    `json:"domainId"`
	LocalPart string   `json:"localPart"` // "" with kind catchall; prefix without the *
	Kind      string   `json:"kind"`      // alias | forward | catchall | prefix
	MailboxID int64    `json:"mailboxId"` // alias target
	Targets   []string `json:"targets"`   // forward/catchall/prefix targets
	Note      string   `json:"note"`
}

func (s *Service) resolveTargets(ctx context.Context, orgID int64, in AddressInput) ([]string, *int64, error) {
	switch in.Kind {
	case model.AddressAlias:
		if in.MailboxID == 0 {
			return nil, nil, invalid("choose the mailbox this alias delivers to")
		}
		mb, err := s.Mailbox(ctx, orgID, in.MailboxID)
		if err != nil {
			return nil, nil, invalid("mailbox not found")
		}
		id := mb.ID
		return []string{mb.Address}, &id, nil
	case model.AddressForward, model.AddressCatchall, model.AddressPrefix:
		var targets []string
		seen := map[string]bool{}
		for _, t := range in.Targets {
			addr, err := NormalizeEmail(t)
			if err != nil {
				return nil, nil, err
			}
			if !seen[addr] {
				seen[addr] = true
				targets = append(targets, addr)
			}
		}
		if len(targets) == 0 {
			return nil, nil, invalid("at least one target address is required")
		}
		var mbID *int64
		if len(targets) == 1 {
			if mb, err := s.mailboxByAddress(ctx, s.DB, orgID, targets[0]); err == nil {
				mbID = &mb.ID
			}
		}
		return targets, mbID, nil
	}
	return nil, nil, invalid("kind must be alias, forward, catchall or prefix")
}

// CreateAddress creates a routing rule on Purelymail and records it.
func (s *Service) CreateAddress(ctx context.Context, orgID, actor int64, in AddressInput) (*model.Address, error) {
	dom, err := s.Domain(ctx, orgID, in.DomainID)
	if err != nil {
		return nil, invalid("domain not found")
	}
	if dom.IsShared {
		return nil, invalid("routing rules are not available on shared Purelymail domains")
	}
	local := strings.ToLower(strings.TrimSpace(in.LocalPart))
	full := ""
	prefix, catchall := false, false
	switch in.Kind {
	case model.AddressCatchall:
		local, catchall, full = "", true, "*@"+dom.Name
	case model.AddressPrefix:
		if !validLocalPart(local) {
			return nil, invalid("%q is not a valid prefix", local)
		}
		prefix, full = true, local+"*@"+dom.Name
	default:
		if !validLocalPart(local) {
			return nil, invalid("%q is not a valid address name", local)
		}
		full = local + "@" + dom.Name
	}
	targets, mbID, err := s.resolveTargets(ctx, orgID, in)
	if err != nil {
		return nil, err
	}
	for _, t := range targets {
		if t == full {
			return nil, invalid("an address cannot forward to itself")
		}
	}
	if existing, err := s.addressByFull(ctx, orgID, full); err == nil {
		if existing.Kind == model.AddressPrimary {
			return nil, fmt.Errorf("%w: %s is a mailbox; adding a rule would stop delivery to it (use the mailbox forwarding action instead)", ErrConflict, full)
		}
		return nil, fmt.Errorf("%w: address %s already exists", ErrConflict, full)
	}
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return nil, err
	}
	ruleID, err := s.createRule(ctx, api, dom.Name, local, prefix, catchall, targets)
	if err != nil {
		return nil, err
	}
	now := db.Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO addresses(org_id, domain_id, local_part, address, kind, mailbox_id, targets_json, pm_rule_id, is_prefix, is_catchall, note, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, orgID, dom.ID, local, full, in.Kind, sqlNullInt(mbID), toJSON(targets), sqlNullInt(ruleID), boolInt(prefix), boolInt(catchall), strings.TrimSpace(in.Note), now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if in.Kind == model.AddressAlias && mbID != nil {
		s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO identities(mailbox_id, address, display_name, is_default, created_at) SELECT ?, ?, display_name, 0, ? FROM mailboxes WHERE id = ?`, *mbID, full, now, *mbID)
	}
	s.audit(ctx, orgID, actor, "address.create", "address", fmt.Sprint(id), map[string]any{"address": full, "kind": in.Kind, "targets": targets})
	return s.Address(ctx, orgID, id)
}

func sqlNullInt(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Service) addressByFull(ctx context.Context, orgID int64, full string) (*model.Address, error) {
	a, err := scanAddress(s.DB.QueryRowContext(ctx, addressSelect+` WHERE a.org_id = ? AND a.address = ?`, orgID, full))
	if db.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return a, err
}

// createRule creates a routing rule and returns its Purelymail id (looked
// up afterwards since the create call returns nothing).
func (s *Service) createRule(ctx context.Context, api purelymail.API, domain, local string, prefix, catchall bool, targets []string) (*int64, error) {
	if err := api.CreateRoutingRule(ctx, purelymail.CreateRoutingRuleRequest{DomainName: domain, MatchUser: local, Prefix: prefix, Catchall: catchall, TargetAddresses: targets}); err != nil {
		return nil, upstream("purelymail create routing rule", err)
	}
	rules, err := api.ListRoutingRules(ctx)
	if err != nil {
		return nil, nil
	}
	for _, r := range rules {
		if strings.EqualFold(r.DomainName, domain) && strings.EqualFold(r.MatchUser, local) && r.Prefix == prefix && r.Catchall == catchall {
			id := r.ID
			return &id, nil
		}
	}
	return nil, nil
}

func (s *Service) deleteRule(ctx context.Context, api purelymail.API, a *model.Address) error {
	if a.PMRuleID != nil {
		if err := api.DeleteRoutingRule(ctx, *a.PMRuleID); err != nil {
			var pe *purelymail.Error
			if !(asPMError(err, &pe) && pe.Code == "notFound") {
				return upstream("purelymail delete routing rule", err)
			}
		}
		return nil
	}
	// No stored id (e.g. older import): find it by shape.
	rules, err := api.ListRoutingRules(ctx)
	if err != nil {
		return upstream("purelymail list routing rules", err)
	}
	for _, r := range rules {
		if strings.EqualFold(r.DomainName, a.Domain) && strings.EqualFold(r.MatchUser, a.LocalPart) && r.Prefix == a.IsPrefix && r.Catchall == a.IsCatchall {
			return upstream("purelymail delete routing rule", api.DeleteRoutingRule(ctx, r.ID))
		}
	}
	return nil
}

func asPMError(err error, target **purelymail.Error) bool {
	e, ok := err.(*purelymail.Error)
	if !ok {
		if u, ok2 := err.(*UpstreamError); ok2 {
			return asPMError(u.Err, target)
		}
		return false
	}
	*target = e
	return true
}

// UpdateAddress retargets an address (rules are replaced atomically as far
// as Purelymail allows: delete then create).
func (s *Service) UpdateAddress(ctx context.Context, orgID, actor, id int64, in AddressInput) (*model.Address, error) {
	a, err := s.Address(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if a.Kind == model.AddressPrimary {
		return nil, invalid("a mailbox address cannot be retargeted; use forwarding on the mailbox")
	}
	if a.Kind == model.AddressGroup {
		return nil, invalid("group addresses follow the group's membership")
	}
	if in.Kind == "" {
		in.Kind = a.Kind
	}
	if in.Kind != a.Kind && (a.IsCatchall || a.IsPrefix || in.Kind == model.AddressCatchall || in.Kind == model.AddressPrefix) {
		return nil, invalid("change the address type by deleting and recreating it")
	}
	targets, mbID, err := s.resolveTargets(ctx, orgID, in)
	if err != nil {
		return nil, err
	}
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if err := s.deleteRule(ctx, api, a); err != nil {
		return nil, err
	}
	ruleID, err := s.createRule(ctx, api, a.Domain, a.LocalPart, a.IsPrefix, a.IsCatchall, targets)
	if err != nil {
		return nil, err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE addresses SET kind = ?, mailbox_id = ?, targets_json = ?, pm_rule_id = ?, note = ?, updated_at = ? WHERE id = ?`,
		in.Kind, sqlNullInt(mbID), toJSON(targets), sqlNullInt(ruleID), strings.TrimSpace(in.Note), db.Now(), id); err != nil {
		return nil, err
	}
	if in.Kind == model.AddressAlias && mbID != nil {
		s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO identities(mailbox_id, address, display_name, is_default, created_at) SELECT ?, ?, display_name, 0, ? FROM mailboxes WHERE id = ?`, *mbID, a.Address, db.Now(), *mbID)
	}
	s.audit(ctx, orgID, actor, "address.update", "address", fmt.Sprint(id), map[string]any{"address": a.Address, "kind": in.Kind, "targets": targets})
	return s.Address(ctx, orgID, id)
}

// DeleteAddress removes a rule-backed address.
func (s *Service) DeleteAddress(ctx context.Context, orgID, actor, id int64) error {
	a, err := s.Address(ctx, orgID, id)
	if err != nil {
		return err
	}
	if a.Kind == model.AddressPrimary {
		return invalid("a mailbox address is removed by deleting the mailbox")
	}
	if a.Kind == model.AddressGroup {
		return invalid("remove the address from the group instead")
	}
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return err
	}
	if err := s.deleteRule(ctx, api, a); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM addresses WHERE id = ?`, id); err != nil {
		return err
	}
	if a.MailboxID != nil {
		s.DB.ExecContext(ctx, `DELETE FROM identities WHERE mailbox_id = ? AND address = ? AND is_default = 0`, *a.MailboxID, a.Address)
	}
	s.audit(ctx, orgID, actor, "address.delete", "address", fmt.Sprint(id), map[string]any{"address": a.Address})
	return nil
}

// SetMailboxForwarding creates (or removes) a routing rule on a mailbox's
// own address. While active, mail for the address goes to the targets
// instead of the mailbox; the history stays in the mailbox.
func (s *Service) SetMailboxForwarding(ctx context.Context, orgID, actor, mailboxID int64, targets []string) (*model.Address, error) {
	mb, err := s.Mailbox(ctx, orgID, mailboxID)
	if err != nil {
		return nil, err
	}
	a, err := s.addressByFull(ctx, orgID, mb.Address)
	if err != nil {
		return nil, err
	}
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		if a.Kind == model.AddressPrimary {
			return a, nil
		}
		if err := s.deleteRule(ctx, api, a); err != nil {
			return nil, err
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE addresses SET kind = 'primary', targets_json = '[]', pm_rule_id = NULL, note = '', updated_at = ? WHERE id = ?`, db.Now(), a.ID); err != nil {
			return nil, err
		}
		s.audit(ctx, orgID, actor, "mailbox.forward.clear", "mailbox", fmt.Sprint(mailboxID), map[string]any{"address": mb.Address})
		return s.Address(ctx, orgID, a.ID)
	}
	clean, _, err := s.resolveTargets(ctx, orgID, AddressInput{Kind: model.AddressForward, Targets: targets})
	if err != nil {
		return nil, err
	}
	for _, t := range clean {
		if t == mb.Address {
			return nil, invalid("a mailbox cannot forward to itself")
		}
	}
	if a.Kind != model.AddressPrimary {
		if err := s.deleteRule(ctx, api, a); err != nil {
			return nil, err
		}
	}
	ruleID, err := s.createRule(ctx, api, a.Domain, a.LocalPart, false, false, clean)
	if err != nil {
		return nil, err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE addresses SET kind = 'forward', targets_json = ?, pm_rule_id = ?, note = 'Forwarding set on mailbox', updated_at = ? WHERE id = ?`, toJSON(clean), sqlNullInt(ruleID), db.Now(), a.ID); err != nil {
		return nil, err
	}
	s.audit(ctx, orgID, actor, "mailbox.forward", "mailbox", fmt.Sprint(mailboxID), map[string]any{"address": mb.Address, "targets": clean})
	return s.Address(ctx, orgID, a.ID)
}

// --- groups ---

func (s *Service) groupAddress(ctx context.Context, orgID, groupID int64) *model.Address {
	a, err := scanAddress(s.DB.QueryRowContext(ctx, addressSelect+` WHERE a.org_id = ? AND a.group_id = ?`, orgID, groupID))
	if err != nil {
		return nil
	}
	return a
}

func (s *Service) loadGroup(ctx context.Context, orgID int64, row interface{ Scan(...any) error }) (*model.Group, error) {
	var g model.Group
	if err := row.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt); err != nil {
		return nil, err
	}
	g.MemberIDs = []int64{}
	rows, err := s.DB.QueryContext(ctx, `SELECT member_id FROM group_members WHERE group_id = ? ORDER BY member_id`, g.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		g.MemberIDs = append(g.MemberIDs, id)
	}
	rows.Close()
	g.Address = s.groupAddress(ctx, orgID, g.ID)
	return &g, nil
}

// Groups lists groups.
func (s *Service) Groups(ctx context.Context, orgID int64) ([]model.Group, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, description, created_at FROM groups WHERE org_id = ? ORDER BY name`, orgID)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		id      int64
		name    string
		desc    string
		created string
	}
	for rows.Next() {
		var r struct {
			id      int64
			name    string
			desc    string
			created string
		}
		if err := rows.Scan(&r.id, &r.name, &r.desc, &r.created); err != nil {
			rows.Close()
			return nil, err
		}
		raw = append(raw, r)
	}
	rows.Close()
	out := []model.Group{}
	for _, r := range raw {
		g := model.Group{ID: r.id, Name: r.name, Description: r.desc, CreatedAt: r.created, MemberIDs: []int64{}}
		mrows, err := s.DB.QueryContext(ctx, `SELECT member_id FROM group_members WHERE group_id = ? ORDER BY member_id`, g.ID)
		if err != nil {
			return nil, err
		}
		for mrows.Next() {
			var id int64
			mrows.Scan(&id)
			g.MemberIDs = append(g.MemberIDs, id)
		}
		mrows.Close()
		g.Address = s.groupAddress(ctx, orgID, g.ID)
		out = append(out, g)
	}
	return out, nil
}

// Group fetches one group.
func (s *Service) Group(ctx context.Context, orgID, id int64) (*model.Group, error) {
	g, err := s.loadGroup(ctx, orgID, s.DB.QueryRowContext(ctx, `SELECT id, name, description, created_at FROM groups WHERE org_id = ? AND id = ?`, orgID, id))
	if db.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return g, err
}

// GroupInput creates/updates a group.
type GroupInput struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	MemberIDs   []int64 `json:"memberIds"`
	// Optional distribution address: mail to it reaches every member.
	AddressDomainID int64  `json:"addressDomainId"`
	AddressLocal    string `json:"addressLocal"`
	RemoveAddress   bool   `json:"removeAddress"`
}

// CreateGroup creates a group and optionally its distribution address.
func (s *Service) CreateGroup(ctx context.Context, orgID, actor int64, in GroupInput) (*model.Group, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, invalid("group name is required")
	}
	now := db.Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO groups(org_id, name, description, created_at, updated_at) VALUES (?,?,?,?,?)`, orgID, name, strings.TrimSpace(in.Description), now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("%w: group %s already exists", ErrConflict, name)
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	s.audit(ctx, orgID, actor, "group.create", "group", fmt.Sprint(id), map[string]any{"name": name})
	if err := s.setGroupMembers(ctx, orgID, id, in.MemberIDs); err != nil {
		return nil, err
	}
	if in.AddressDomainID != 0 && strings.TrimSpace(in.AddressLocal) != "" {
		if err := s.setGroupAddress(ctx, orgID, actor, id, in.AddressDomainID, in.AddressLocal); err != nil {
			g, _ := s.Group(ctx, orgID, id)
			return g, err
		}
	}
	return s.Group(ctx, orgID, id)
}

// UpdateGroup edits a group, its members and distribution address.
func (s *Service) UpdateGroup(ctx context.Context, orgID, actor, id int64, in GroupInput) (*model.Group, error) {
	g, err := s.Group(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = g.Name
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE groups SET name = ?, description = ?, updated_at = ? WHERE id = ?`, name, strings.TrimSpace(in.Description), db.Now(), id); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("%w: group %s already exists", ErrConflict, name)
		}
		return nil, err
	}
	if in.MemberIDs != nil {
		if err := s.setGroupMembers(ctx, orgID, id, in.MemberIDs); err != nil {
			return nil, err
		}
	}
	if in.RemoveAddress && g.Address != nil {
		if err := s.removeGroupAddress(ctx, orgID, actor, g); err != nil {
			return nil, err
		}
	} else if in.AddressDomainID != 0 && strings.TrimSpace(in.AddressLocal) != "" {
		if err := s.setGroupAddress(ctx, orgID, actor, id, in.AddressDomainID, in.AddressLocal); err != nil {
			return nil, err
		}
	} else if in.MemberIDs != nil {
		if err := s.SyncGroupAddress(ctx, orgID, actor, id); err != nil {
			return nil, err
		}
	}
	s.audit(ctx, orgID, actor, "group.update", "group", fmt.Sprint(id), map[string]any{"name": name, "members": in.MemberIDs})
	return s.Group(ctx, orgID, id)
}

func (s *Service) setGroupMembers(ctx context.Context, orgID, groupID int64, memberIDs []int64) error {
	return s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM group_members WHERE group_id = ?`, groupID); err != nil {
			return err
		}
		for _, mid := range memberIDs {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM members WHERE id = ? AND org_id = ?`, mid, orgID).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return invalid("member %d not found", mid)
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO group_members(group_id, member_id) VALUES (?,?)`, groupID, mid); err != nil {
				return err
			}
		}
		return nil
	})
}

// AddGroupMember adds one member and refreshes the distribution rule.
func (s *Service) AddGroupMember(ctx context.Context, orgID, actor, groupID, memberID int64) error {
	if _, err := s.Group(ctx, orgID, groupID); err != nil {
		return err
	}
	if _, err := s.Member(ctx, orgID, memberID); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO group_members(group_id, member_id) VALUES (?,?)`, groupID, memberID); err != nil {
		return err
	}
	return s.SyncGroupAddress(ctx, orgID, actor, groupID)
}

// RemoveGroupMember removes one member and refreshes the distribution rule.
func (s *Service) RemoveGroupMember(ctx context.Context, orgID, actor, groupID, memberID int64) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM group_members WHERE group_id = ? AND member_id = ?`, groupID, memberID); err != nil {
		return err
	}
	return s.SyncGroupAddress(ctx, orgID, actor, groupID)
}

// groupTargets returns the primary mailbox addresses of active group members.
func (s *Service) groupTargets(ctx context.Context, orgID, groupID int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT b.pm_user FROM group_members gm JOIN members m ON m.id = gm.member_id
		JOIN mailboxes b ON b.owner_member_id = m.id AND b.kind = 'personal' AND b.status = 'active'
		WHERE gm.group_id = ? AND m.status IN ('active','invited') AND b.org_id = ? ORDER BY b.pm_user`, groupID, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		rows.Scan(&a)
		out = append(out, a)
	}
	sort.Strings(out)
	return out, nil
}

func (s *Service) setGroupAddress(ctx context.Context, orgID, actor, groupID, domainID int64, local string) error {
	g, err := s.Group(ctx, orgID, groupID)
	if err != nil {
		return err
	}
	dom, err := s.Domain(ctx, orgID, domainID)
	if err != nil {
		return invalid("domain not found")
	}
	local = strings.ToLower(strings.TrimSpace(local))
	if !validLocalPart(local) {
		return invalid("%q is not a valid address name", local)
	}
	full := local + "@" + dom.Name
	if g.Address != nil && g.Address.Address == full {
		return s.SyncGroupAddress(ctx, orgID, actor, groupID)
	}
	if existing, err := s.addressByFull(ctx, orgID, full); err == nil {
		return fmt.Errorf("%w: address %s already exists (%s)", ErrConflict, full, existing.Kind)
	}
	if g.Address != nil {
		if err := s.removeGroupAddress(ctx, orgID, actor, g); err != nil {
			return err
		}
	}
	targets, err := s.groupTargets(ctx, orgID, groupID)
	if err != nil {
		return err
	}
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return err
	}
	var ruleID *int64
	if len(targets) > 0 {
		if ruleID, err = s.createRule(ctx, api, dom.Name, local, false, false, targets); err != nil {
			return err
		}
	}
	now := db.Now()
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO addresses(org_id, domain_id, local_part, address, kind, group_id, targets_json, pm_rule_id, created_at, updated_at) VALUES (?,?,?,?,'group',?,?,?,?,?)`,
		orgID, dom.ID, local, full, groupID, toJSON(targets), sqlNullInt(ruleID), now, now); err != nil {
		return err
	}
	s.audit(ctx, orgID, actor, "group.address", "group", fmt.Sprint(groupID), map[string]any{"address": full, "targets": targets})
	return nil
}

func (s *Service) removeGroupAddress(ctx context.Context, orgID, actor int64, g *model.Group) error {
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return err
	}
	if err := s.deleteRule(ctx, api, g.Address); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM addresses WHERE id = ?`, g.Address.ID); err != nil {
		return err
	}
	s.audit(ctx, orgID, actor, "group.address.remove", "group", fmt.Sprint(g.ID), map[string]any{"address": g.Address.Address})
	return nil
}

// SyncGroupAddress rewrites the distribution rule from current membership.
func (s *Service) SyncGroupAddress(ctx context.Context, orgID, actor, groupID int64) error {
	g, err := s.Group(ctx, orgID, groupID)
	if err != nil {
		return err
	}
	if g.Address == nil {
		return nil
	}
	targets, err := s.groupTargets(ctx, orgID, groupID)
	if err != nil {
		return err
	}
	if strings.Join(targets, ",") == strings.Join(g.Address.Targets, ",") && (g.Address.PMRuleID != nil || len(targets) == 0) {
		return nil
	}
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return err
	}
	if err := s.deleteRule(ctx, api, g.Address); err != nil {
		return err
	}
	var ruleID *int64
	if len(targets) > 0 {
		if ruleID, err = s.createRule(ctx, api, g.Address.Domain, g.Address.LocalPart, false, false, targets); err != nil {
			return err
		}
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE addresses SET targets_json = ?, pm_rule_id = ?, updated_at = ? WHERE id = ?`, toJSON(targets), sqlNullInt(ruleID), db.Now(), g.Address.ID)
	return err
}

// DeleteGroup removes a group and its address.
func (s *Service) DeleteGroup(ctx context.Context, orgID, actor, id int64) error {
	g, err := s.Group(ctx, orgID, id)
	if err != nil {
		return err
	}
	if g.Address != nil {
		if err := s.removeGroupAddress(ctx, orgID, actor, g); err != nil {
			return err
		}
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM groups WHERE id = ?`, id); err != nil {
		return err
	}
	s.audit(ctx, orgID, actor, "group.delete", "group", fmt.Sprint(id), map[string]any{"name": g.Name})
	return nil
}
