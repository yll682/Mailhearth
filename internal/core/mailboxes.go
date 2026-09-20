package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"mailhearth/internal/db"
	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/model"
	"mailhearth/internal/purelymail"
)

const mailboxSelect = `SELECT b.id, b.kind, b.pm_user, b.domain_id, b.display_name, b.owner_member_id, COALESCE(m.display_name, ''), b.credential_enc != '', b.credential_at,
	b.status, b.imported, b.settings_json, b.created_at, b.updated_at,
	(SELECT COUNT(1) FROM mailbox_access a WHERE a.mailbox_id = b.id)
	FROM mailboxes b LEFT JOIN members m ON m.id = b.owner_member_id`

func scanMailbox(row interface{ Scan(...any) error }) (*model.Mailbox, error) {
	var b model.Mailbox
	var domainID, owner sql.NullInt64
	var credAt sql.NullString
	var imported int
	var settings string
	if err := row.Scan(&b.ID, &b.Kind, &b.Address, &domainID, &b.DisplayName, &owner, &b.OwnerName, &b.HasCredential, &credAt,
		&b.Status, &imported, &settings, &b.CreatedAt, &b.UpdatedAt, &b.AccessCount); err != nil {
		return nil, err
	}
	b.DomainID = nullInt(domainID)
	b.OwnerMemberID = nullInt(owner)
	b.CredentialAt = nullStr(credAt)
	b.Imported = imported == 1
	json.Unmarshal([]byte(settings), &b.Settings)
	if b.Settings.SieveRules == nil {
		b.Settings.SieveRules = []model.SieveRule{}
	}
	return &b, nil
}

// Mailboxes lists all mailboxes.
func (s *Service) Mailboxes(ctx context.Context, orgID int64) ([]model.Mailbox, error) {
	rows, err := s.DB.QueryContext(ctx, mailboxSelect+` WHERE b.org_id = ? ORDER BY b.kind, b.pm_user`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Mailbox{}
	for rows.Next() {
		b, err := scanMailbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// Mailbox fetches one mailbox.
func (s *Service) Mailbox(ctx context.Context, orgID, id int64) (*model.Mailbox, error) {
	b, err := scanMailbox(s.DB.QueryRowContext(ctx, mailboxSelect+` WHERE b.org_id = ? AND b.id = ?`, orgID, id))
	if db.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return b, err
}

func (s *Service) mailboxByAddress(ctx context.Context, q db.Querier, orgID int64, addr string) (*model.Mailbox, error) {
	b, err := scanMailbox(q.QueryRowContext(ctx, mailboxSelect+` WHERE b.org_id = ? AND b.pm_user = ?`, orgID, strings.ToLower(addr)))
	if db.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return b, err
}

// CreateMailboxRequest creates a new Purelymail user.
type CreateMailboxRequest struct {
	Kind          string `json:"kind"` // personal | shared
	DomainID      int64  `json:"domainId"`
	LocalPart     string `json:"localPart"`
	DisplayName   string `json:"displayName"`
	OwnerMemberID int64  `json:"ownerMemberId"` // personal only
}

// CreateMailbox provisions a Purelymail user and an app password for it.
func (s *Service) CreateMailbox(ctx context.Context, orgID, actor int64, req CreateMailboxRequest) (*model.Mailbox, error) {
	kind := req.Kind
	if kind == "" {
		kind = model.MailboxPersonal
	}
	if kind != model.MailboxPersonal && kind != model.MailboxShared {
		return nil, invalid("kind must be personal or shared")
	}
	dom, err := s.Domain(ctx, orgID, req.DomainID)
	if err != nil {
		return nil, invalid("domain not found")
	}
	if dom.Status != "active" {
		return nil, invalid("domain %s is not active", dom.Name)
	}
	local := strings.ToLower(strings.TrimSpace(req.LocalPart))
	if !validLocalPart(local) {
		return nil, invalid("%q is not a valid mailbox name (letters, digits, dots, dashes)", local)
	}
	addr := local + "@" + dom.Name
	if _, err := s.mailboxByAddress(ctx, s.DB, orgID, addr); err == nil {
		return nil, fmt.Errorf("%w: mailbox %s already exists", ErrConflict, addr)
	}
	var ownerID any
	if kind == model.MailboxPersonal && req.OwnerMemberID != 0 {
		if _, err := s.Member(ctx, orgID, req.OwnerMemberID); err != nil {
			return nil, invalid("owner not found")
		}
		ownerID = req.OwnerMemberID
	}
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if err := api.CreateUser(ctx, purelymail.CreateUserRequest{
		UserName: local, DomainName: dom.Name, Password: randomPassword(),
		EnablePasswordReset: false, EnableSearchIndexing: true, SendWelcomeEmail: false,
	}); err != nil {
		return nil, upstream("purelymail create user", err)
	}
	appPw, err := api.CreateAppPassword(ctx, addr, "Mailhearth")
	credEnc := ""
	warn := ""
	if err != nil {
		warn = err.Error()
	} else if credEnc, err = s.Box.Seal(appPw); err != nil {
		return nil, err
	}
	now := db.Now()
	display := strings.TrimSpace(req.DisplayName)
	if display == "" {
		display = local
	}
	var id int64
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO mailboxes(org_id, kind, pm_user, domain_id, display_name, owner_member_id, credential_enc, credential_label, credential_at, status, imported, settings_json, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,'active',0,'{}',?,?)`, orgID, kind, addr, dom.ID, display, ownerID, credEnc, "Mailhearth", now, now, now)
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
		if _, err := tx.ExecContext(ctx, `INSERT INTO addresses(org_id, domain_id, local_part, address, kind, mailbox_id, created_at, updated_at) VALUES (?,?,?,?,'primary',?,?,?)
			ON CONFLICT(org_id, address) DO UPDATE SET mailbox_id = excluded.mailbox_id, kind = 'primary', updated_at = excluded.updated_at`, orgID, dom.ID, local, addr, id, now, now); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO identities(mailbox_id, address, display_name, is_default, created_at) VALUES (?,?,?,1,?)`, id, addr, display, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.audit(ctx, orgID, actor, "mailbox.create", "mailbox", fmt.Sprint(id), map[string]any{"address": addr, "kind": kind, "owner": req.OwnerMemberID, "credentialWarning": warn})
	if warn != "" {
		s.Log.Warn("mailbox created but app password failed", "address", addr, "err", warn)
	}
	return s.Mailbox(ctx, orgID, id)
}

func randomPassword() string { return secretsRandomPassword() }

// BindMailbox attaches an imported mailbox to a member and obtains an app
// password so Mailhearth can open it.
func (s *Service) BindMailbox(ctx context.Context, orgID, actor, mailboxID, ownerID int64, displayName string) (*model.Mailbox, error) {
	mb, err := s.Mailbox(ctx, orgID, mailboxID)
	if err != nil {
		return nil, err
	}
	if ownerID != 0 {
		if mb.OwnerMemberID != nil && *mb.OwnerMemberID != ownerID {
			return nil, invalid("mailbox %s already belongs to another member", mb.Address)
		}
		if _, err := s.Member(ctx, orgID, ownerID); err != nil {
			return nil, invalid("owner not found")
		}
	}
	if !mb.HasCredential {
		if err := s.EnsureCredential(ctx, orgID, actor, mailboxID); err != nil {
			return nil, err
		}
	}
	now := db.Now()
	if ownerID != 0 {
		if _, err := s.DB.ExecContext(ctx, `UPDATE mailboxes SET owner_member_id = ?, kind = 'personal', status = 'active', updated_at = ? WHERE id = ?`, ownerID, now, mailboxID); err != nil {
			return nil, err
		}
	}
	if d := strings.TrimSpace(displayName); d != "" {
		// Import names a mailbox and its identity after the local part. Once a
		// person owns it, their name is what recipients should see, so replace
		// that placeholder in both places.
		local := SplitLocal(mb.Address)
		s.DB.ExecContext(ctx, `UPDATE mailboxes SET display_name = ? WHERE id = ? AND (display_name = '' OR display_name = ?)`, d, mailboxID, local)
		s.DB.ExecContext(ctx, `UPDATE identities SET display_name = ? WHERE mailbox_id = ? AND is_default = 1 AND (display_name = '' OR display_name = ?)`, d, mailboxID, local)
	}
	s.audit(ctx, orgID, actor, "mailbox.bind", "mailbox", fmt.Sprint(mailboxID), map[string]any{"address": mb.Address, "owner": ownerID})
	return s.Mailbox(ctx, orgID, mailboxID)
}

// SplitLocal returns the local part of an address.
func SplitLocal(addr string) string {
	l, _ := SplitAddress(addr)
	return l
}

// EnsureCredential creates an app password when the mailbox has none.
func (s *Service) EnsureCredential(ctx context.Context, orgID, actor, mailboxID int64) error {
	mb, err := s.Mailbox(ctx, orgID, mailboxID)
	if err != nil {
		return err
	}
	if mb.HasCredential {
		return nil
	}
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return err
	}
	appPw, err := api.CreateAppPassword(ctx, mb.Address, "Mailhearth")
	if err != nil {
		return upstream("purelymail create app password", err)
	}
	enc, err := s.Box.Seal(appPw)
	if err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE mailboxes SET credential_enc = ?, credential_label = 'Mailhearth', credential_at = ?, updated_at = ? WHERE id = ?`, enc, db.Now(), db.Now(), mailboxID); err != nil {
		return err
	}
	s.audit(ctx, orgID, actor, "mailbox.credential", "mailbox", fmt.Sprint(mailboxID), map[string]any{"address": mb.Address})
	return nil
}

// RotateCredential replaces the app password (old one is revoked).
func (s *Service) RotateCredential(ctx context.Context, orgID, actor, mailboxID int64) error {
	mb, err := s.Mailbox(ctx, orgID, mailboxID)
	if err != nil {
		return err
	}
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return err
	}
	var oldEnc string
	s.DB.QueryRowContext(ctx, `SELECT credential_enc FROM mailboxes WHERE id = ?`, mailboxID).Scan(&oldEnc)
	appPw, err := api.CreateAppPassword(ctx, mb.Address, "Mailhearth")
	if err != nil {
		return upstream("purelymail create app password", err)
	}
	enc, err := s.Box.Seal(appPw)
	if err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE mailboxes SET credential_enc = ?, credential_label = 'Mailhearth', credential_at = ?, updated_at = ? WHERE id = ?`, enc, db.Now(), db.Now(), mailboxID); err != nil {
		return err
	}
	if oldEnc != "" {
		if old, err := s.Box.Open(oldEnc); err == nil && old != "" {
			if err := api.DeleteAppPassword(ctx, mb.Address, old); err != nil {
				s.Log.Warn("could not revoke old app password", "address", mb.Address, "err", err)
			}
		}
	}
	s.audit(ctx, orgID, actor, "mailbox.rotate", "mailbox", fmt.Sprint(mailboxID), map[string]any{"address": mb.Address})
	return nil
}

// ResetMailboxPassword sets a new Purelymail password for use in external
// clients and returns it once. Mailhearth's own app password is rotated
// afterwards in case the provider invalidated it.
func (s *Service) ResetMailboxPassword(ctx context.Context, orgID, actor, mailboxID int64) (string, error) {
	mb, err := s.Mailbox(ctx, orgID, mailboxID)
	if err != nil {
		return "", err
	}
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return "", err
	}
	pw := randomPassword()
	if err := api.ModifyUser(ctx, purelymail.ModifyUserRequest{UserName: mb.Address, NewPassword: &pw}); err != nil {
		return "", upstream("purelymail modify user", err)
	}
	if err := s.RotateCredential(ctx, orgID, actor, mailboxID); err != nil {
		s.Log.Warn("rotate after password reset failed", "address", mb.Address, "err", err)
	}
	s.audit(ctx, orgID, actor, "mailbox.password", "mailbox", fmt.Sprint(mailboxID), map[string]any{"address": mb.Address})
	return pw, nil
}

// SuspendMailbox locks everyone out: new random Purelymail password, app
// password revoked, credential cleared. Mail keeps arriving and is kept.
func (s *Service) SuspendMailbox(ctx context.Context, orgID, actor, mailboxID int64) error {
	mb, err := s.Mailbox(ctx, orgID, mailboxID)
	if err != nil {
		return err
	}
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return err
	}
	pw := randomPassword()
	if err := api.ModifyUser(ctx, purelymail.ModifyUserRequest{UserName: mb.Address, NewPassword: &pw}); err != nil {
		return upstream("purelymail modify user", err)
	}
	var oldEnc string
	s.DB.QueryRowContext(ctx, `SELECT credential_enc FROM mailboxes WHERE id = ?`, mailboxID).Scan(&oldEnc)
	if old, err := s.Box.Open(oldEnc); err == nil && old != "" {
		api.DeleteAppPassword(ctx, mb.Address, old)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE mailboxes SET credential_enc = '', credential_at = NULL, status = 'suspended', updated_at = ? WHERE id = ?`, db.Now(), mailboxID); err != nil {
		return err
	}
	s.audit(ctx, orgID, actor, "mailbox.suspend", "mailbox", fmt.Sprint(mailboxID), map[string]any{"address": mb.Address})
	return nil
}

// ReactivateMailbox obtains a fresh credential for a suspended mailbox.
func (s *Service) ReactivateMailbox(ctx context.Context, orgID, actor, mailboxID int64) error {
	if _, err := s.DB.ExecContext(ctx, `UPDATE mailboxes SET status = 'active', updated_at = ? WHERE id = ? AND org_id = ?`, db.Now(), mailboxID, orgID); err != nil {
		return err
	}
	if err := s.EnsureCredential(ctx, orgID, actor, mailboxID); err != nil {
		return err
	}
	s.audit(ctx, orgID, actor, "mailbox.reactivate", "mailbox", fmt.Sprint(mailboxID), nil)
	return nil
}

// UpdateMailboxInput edits display name, kind and owner.
type UpdateMailboxInput struct {
	DisplayName   *string `json:"displayName"`
	Kind          *string `json:"kind"`
	OwnerMemberID *int64  `json:"ownerMemberId"` // 0 clears
}

// UpdateMailbox applies edits; converting to shared clears the owner.
func (s *Service) UpdateMailbox(ctx context.Context, orgID, actor, id int64, in UpdateMailboxInput) (*model.Mailbox, error) {
	mb, err := s.Mailbox(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	kind := mb.Kind
	if in.Kind != nil {
		if *in.Kind != model.MailboxPersonal && *in.Kind != model.MailboxShared {
			return nil, invalid("kind must be personal or shared")
		}
		kind = *in.Kind
	}
	var ownerID any
	if mb.OwnerMemberID != nil {
		ownerID = *mb.OwnerMemberID
	}
	if in.OwnerMemberID != nil {
		if *in.OwnerMemberID == 0 {
			ownerID = nil
		} else {
			if _, err := s.Member(ctx, orgID, *in.OwnerMemberID); err != nil {
				return nil, invalid("owner not found")
			}
			ownerID = *in.OwnerMemberID
		}
	}
	if kind == model.MailboxShared {
		ownerID = nil
	}
	display := mb.DisplayName
	if in.DisplayName != nil && strings.TrimSpace(*in.DisplayName) != "" {
		display = strings.TrimSpace(*in.DisplayName)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE mailboxes SET display_name = ?, kind = ?, owner_member_id = ?, updated_at = ? WHERE id = ?`, display, kind, ownerID, db.Now(), id); err != nil {
		return nil, err
	}
	s.audit(ctx, orgID, actor, "mailbox.update", "mailbox", fmt.Sprint(id), map[string]any{"kind": kind, "owner": ownerID, "displayName": display})
	return s.Mailbox(ctx, orgID, id)
}

// DeleteMailbox deletes the Purelymail user and all its mail. Requires the
// caller to confirm with the exact address.
func (s *Service) DeleteMailbox(ctx context.Context, orgID, actor, id int64, confirm string) error {
	mb, err := s.Mailbox(ctx, orgID, id)
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(confirm), mb.Address) {
		return invalid("type the mailbox address exactly to confirm deletion")
	}
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return err
	}
	if err := api.DeleteUser(ctx, mb.Address); err != nil {
		return upstream("purelymail delete user", err)
	}
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM addresses WHERE mailbox_id = ? AND kind = 'primary'`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM mailboxes WHERE id = ?`, id)
		return err
	})
	if err != nil {
		return err
	}
	s.audit(ctx, orgID, actor, "mailbox.delete", "mailbox", fmt.Sprint(id), map[string]any{"address": mb.Address})
	return nil
}

// --- access grants (shared mailboxes) ---

// AccessList lists members with access to a mailbox.
func (s *Service) AccessList(ctx context.Context, orgID, mailboxID int64) ([]model.MailboxAccess, error) {
	if _, err := s.Mailbox(ctx, orgID, mailboxID); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT a.id, a.mailbox_id, a.member_id, m.display_name, a.level, a.granted_at FROM mailbox_access a JOIN members m ON m.id = a.member_id WHERE a.mailbox_id = ? ORDER BY m.display_name`, mailboxID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.MailboxAccess{}
	for rows.Next() {
		var a model.MailboxAccess
		if err := rows.Scan(&a.ID, &a.MailboxID, &a.MemberID, &a.MemberName, &a.Level, &a.GrantedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GrantAccess gives a member access to a mailbox at level.
func (s *Service) GrantAccess(ctx context.Context, orgID, actor, mailboxID, memberID int64, level string) (*model.MailboxAccess, error) {
	mb, err := s.Mailbox(ctx, orgID, mailboxID)
	if err != nil {
		return nil, err
	}
	if _, err := s.Member(ctx, orgID, memberID); err != nil {
		return nil, invalid("member not found")
	}
	switch level {
	case model.AccessFull, model.AccessSend, model.AccessRead:
	case "":
		level = model.AccessFull
	default:
		return nil, invalid("level must be full, send or read")
	}
	if mb.OwnerMemberID != nil && *mb.OwnerMemberID == memberID {
		return nil, invalid("the owner already has full access")
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO mailbox_access(mailbox_id, member_id, level, granted_by, granted_at) VALUES (?,?,?,?,?)
		ON CONFLICT(mailbox_id, member_id) DO UPDATE SET level = excluded.level, granted_by = excluded.granted_by, granted_at = excluded.granted_at`, mailboxID, memberID, level, actor, db.Now()); err != nil {
		return nil, err
	}
	s.audit(ctx, orgID, actor, "mailbox.grant", "mailbox", fmt.Sprint(mailboxID), map[string]any{"member": memberID, "level": level})
	list, err := s.AccessList(ctx, orgID, mailboxID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].MemberID == memberID {
			return &list[i], nil
		}
	}
	return nil, ErrNotFound
}

// RevokeAccess removes a grant.
func (s *Service) RevokeAccess(ctx context.Context, orgID, actor, mailboxID, memberID int64) error {
	if _, err := s.Mailbox(ctx, orgID, mailboxID); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM mailbox_access WHERE mailbox_id = ? AND member_id = ?`, mailboxID, memberID); err != nil {
		return err
	}
	s.audit(ctx, orgID, actor, "mailbox.revoke", "mailbox", fmt.Sprint(mailboxID), map[string]any{"member": memberID})
	return nil
}

// --- identities ---

// Identities lists sending identities of a mailbox.
func (s *Service) Identities(ctx context.Context, mailboxID int64) ([]model.Identity, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, mailbox_id, address, display_name, reply_to, signature_html, is_default FROM identities WHERE mailbox_id = ? ORDER BY is_default DESC, id`, mailboxID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Identity{}
	for rows.Next() {
		var i model.Identity
		var def int
		if err := rows.Scan(&i.ID, &i.MailboxID, &i.Address, &i.DisplayName, &i.ReplyTo, &i.SignatureHTML, &def); err != nil {
			return nil, err
		}
		i.IsDefault = def == 1
		out = append(out, i)
	}
	return out, rows.Err()
}

// IdentityInput edits an identity.
type IdentityInput struct {
	Address       string `json:"address"`
	DisplayName   string `json:"displayName"`
	ReplyTo       string `json:"replyTo"`
	SignatureHTML string `json:"signatureHtml"`
	IsDefault     bool   `json:"isDefault"`
}

// allowedIdentityAddress reports whether a mailbox may send as addr: its own
// address or any alias/group address routed to it.
func (s *Service) allowedIdentityAddress(ctx context.Context, orgID, mailboxID int64, addr string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM addresses WHERE org_id = ? AND address = ? AND (mailbox_id = ? OR EXISTS (
		SELECT 1 FROM mailboxes b WHERE b.id = ? AND (addresses.targets_json LIKE '%' || b.pm_user || '%')))`, orgID, strings.ToLower(addr), mailboxID, mailboxID).Scan(&n)
	return n > 0, err
}

// UpsertIdentity creates or updates an identity (id 0 creates).
func (s *Service) UpsertIdentity(ctx context.Context, orgID, actor, mailboxID, id int64, in IdentityInput) (*model.Identity, error) {
	if _, err := s.Mailbox(ctx, orgID, mailboxID); err != nil {
		return nil, err
	}
	addr, err := NormalizeEmail(in.Address)
	if err != nil {
		return nil, err
	}
	ok, err := s.allowedIdentityAddress(ctx, orgID, mailboxID, addr)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, invalid("%s is not an address of this mailbox; add it as an alias first", addr)
	}
	replyTo := strings.TrimSpace(in.ReplyTo)
	if replyTo != "" {
		if replyTo, err = NormalizeEmail(replyTo); err != nil {
			return nil, err
		}
	}
	sig := sanitizeSignature(in.SignatureHTML)
	now := db.Now()
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if in.IsDefault {
			if _, err := tx.ExecContext(ctx, `UPDATE identities SET is_default = 0 WHERE mailbox_id = ?`, mailboxID); err != nil {
				return err
			}
		}
		def := 0
		if in.IsDefault {
			def = 1
		}
		if id == 0 {
			res, err := tx.ExecContext(ctx, `INSERT INTO identities(mailbox_id, address, display_name, reply_to, signature_html, is_default, created_at) VALUES (?,?,?,?,?,?,?)
				ON CONFLICT(mailbox_id, address) DO UPDATE SET display_name = excluded.display_name, reply_to = excluded.reply_to, signature_html = excluded.signature_html, is_default = excluded.is_default`,
				mailboxID, addr, strings.TrimSpace(in.DisplayName), replyTo, sig, def, now)
			if err != nil {
				return err
			}
			id, _ = res.LastInsertId()
			if id == 0 {
				tx.QueryRowContext(ctx, `SELECT id FROM identities WHERE mailbox_id = ? AND address = ?`, mailboxID, addr).Scan(&id)
			}
			return nil
		}
		res, err := tx.ExecContext(ctx, `UPDATE identities SET address = ?, display_name = ?, reply_to = ?, signature_html = ?, is_default = CASE WHEN ? = 1 THEN 1 ELSE is_default END WHERE id = ? AND mailbox_id = ?`,
			addr, strings.TrimSpace(in.DisplayName), replyTo, sig, def, id, mailboxID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Guarantee one default.
	var defs int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM identities WHERE mailbox_id = ? AND is_default = 1`, mailboxID).Scan(&defs)
	if defs == 0 {
		s.DB.ExecContext(ctx, `UPDATE identities SET is_default = 1 WHERE id = (SELECT MIN(id) FROM identities WHERE mailbox_id = ?)`, mailboxID)
	}
	list, err := s.Identities(ctx, mailboxID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, ErrNotFound
}

// DeleteIdentity removes a non-default identity.
func (s *Service) DeleteIdentity(ctx context.Context, orgID, actor, mailboxID, id int64) error {
	if _, err := s.Mailbox(ctx, orgID, mailboxID); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM identities WHERE id = ? AND mailbox_id = ? AND is_default = 0`, id, mailboxID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return invalid("the default identity cannot be deleted")
	}
	return nil
}

// --- credential resolution for the mail layer ---

// MailboxContext is what the mail API needs to act on a mailbox.
type MailboxContext struct {
	Mailbox *model.Mailbox
	Level   string
	Cred    imappool.Cred
}

// CanDelete reports whether the level allows destructive actions.
func (m *MailboxContext) CanDelete() bool { return m.Level == model.AccessFull }

// CanSend reports whether the level allows sending.
func (m *MailboxContext) CanSend() bool {
	return m.Level == model.AccessFull || m.Level == model.AccessSend
}

// ResolveMailbox checks that member may use mailbox and returns the
// credential. Admin permissions do not grant mail access: reading a shared
// mailbox always requires an explicit grant (or ownership).
func (s *Service) ResolveMailbox(ctx context.Context, orgID, memberID, mailboxID int64) (*MailboxContext, error) {
	mb, err := s.Mailbox(ctx, orgID, mailboxID)
	if err != nil {
		return nil, err
	}
	level := ""
	if mb.OwnerMemberID != nil && *mb.OwnerMemberID == memberID {
		level = model.AccessFull
	} else {
		err := s.DB.QueryRowContext(ctx, `SELECT level FROM mailbox_access WHERE mailbox_id = ? AND member_id = ?`, mailboxID, memberID).Scan(&level)
		if err != nil && !db.IsNotFound(err) {
			return nil, err
		}
	}
	if level == "" {
		return nil, ErrForbidden
	}
	if mb.Status != model.MailboxActive {
		return nil, invalid("mailbox %s is %s", mb.Address, mb.Status)
	}
	var enc string
	if err := s.DB.QueryRowContext(ctx, `SELECT credential_enc FROM mailboxes WHERE id = ?`, mailboxID).Scan(&enc); err != nil {
		return nil, err
	}
	if enc == "" {
		return nil, invalid("mailbox %s is not connected yet; an administrator must connect it", mb.Address)
	}
	pw, err := s.Box.Open(enc)
	if err != nil {
		return nil, err
	}
	return &MailboxContext{Mailbox: mb, Level: level, Cred: imappool.Cred{User: mb.Address, Pass: pw}}, nil
}

// AccessibleMailbox is a mailbox as presented to a member.
type AccessibleMailbox struct {
	model.Mailbox
	Level      string           `json:"level"`
	Identities []model.Identity `json:"identities"`
}

// AccessibleMailboxes lists the mailboxes a member can open.
func (s *Service) AccessibleMailboxes(ctx context.Context, orgID, memberID int64) ([]AccessibleMailbox, error) {
	rows, err := s.DB.QueryContext(ctx, mailboxSelect+` WHERE b.org_id = ? AND b.status = 'active' AND (b.owner_member_id = ? OR b.id IN (SELECT mailbox_id FROM mailbox_access WHERE member_id = ?))
		ORDER BY CASE WHEN b.owner_member_id = ? THEN 0 ELSE 1 END, b.display_name, b.pm_user`, orgID, memberID, memberID, memberID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccessibleMailbox{}
	for rows.Next() {
		b, err := scanMailbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, AccessibleMailbox{Mailbox: *b})
	}
	rows.Close()
	for i := range out {
		if out[i].OwnerMemberID != nil && *out[i].OwnerMemberID == memberID {
			out[i].Level = model.AccessFull
		} else {
			s.DB.QueryRowContext(ctx, `SELECT level FROM mailbox_access WHERE mailbox_id = ? AND member_id = ?`, out[i].ID, memberID).Scan(&out[i].Level)
		}
		out[i].Identities, _ = s.Identities(ctx, out[i].ID)
	}
	return out, nil
}

// SaveMailboxSettings stores settings JSON (rules/vacation).
func (s *Service) SaveMailboxSettings(ctx context.Context, mailboxID int64, settings model.MailboxSettings) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE mailboxes SET settings_json = ?, updated_at = ? WHERE id = ?`, toJSON(settings), db.Now(), mailboxID)
	return err
}
