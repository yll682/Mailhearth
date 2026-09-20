package core

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/secrets"
)

const memberSelect = `SELECT m.id, m.display_name, m.login_email, m.role_id, r.key, r.name, m.title, m.department, m.status,
	m.password_hash != '', m.created_at, m.updated_at, m.last_login_at, m.departed_at
	FROM members m JOIN roles r ON r.id = m.role_id`

func scanMember(row interface{ Scan(...any) error }) (*model.Member, error) {
	var m model.Member
	var last, departed sql.NullString
	if err := row.Scan(&m.ID, &m.DisplayName, &m.LoginEmail, &m.RoleID, &m.RoleKey, &m.RoleName, &m.Title, &m.Department, &m.Status,
		&m.HasPassword, &m.CreatedAt, &m.UpdatedAt, &last, &departed); err != nil {
		return nil, err
	}
	m.LastLoginAt = nullStr(last)
	m.DepartedAt = nullStr(departed)
	return &m, nil
}

// Members lists members of the organisation.
func (s *Service) Members(ctx context.Context, orgID int64) ([]model.Member, error) {
	rows, err := s.DB.QueryContext(ctx, memberSelect+` WHERE m.org_id = ? ORDER BY CASE m.status WHEN 'active' THEN 0 WHEN 'invited' THEN 1 WHEN 'disabled' THEN 2 ELSE 3 END, m.display_name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Member{}
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// Member fetches one member.
func (s *Service) Member(ctx context.Context, orgID, id int64) (*model.Member, error) {
	m, err := scanMember(s.DB.QueryRowContext(ctx, memberSelect+` WHERE m.org_id = ? AND m.id = ?`, orgID, id))
	if db.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return m, err
}

// MemberByID fetches a member without an org filter (sessions).
func (s *Service) MemberByID(ctx context.Context, id int64) (*model.Member, error) {
	m, err := scanMember(s.DB.QueryRowContext(ctx, memberSelect+` WHERE m.id = ?`, id))
	if db.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return m, err
}

// MemberInput is the create/update payload.
type MemberInput struct {
	DisplayName string `json:"displayName"`
	LoginEmail  string `json:"loginEmail"`
	RoleID      int64  `json:"roleId"`
	Title       string `json:"title"`
	Department  string `json:"department"`
}

// NewMailboxSpec optionally creates a mailbox during onboarding.
type NewMailboxSpec struct {
	DomainID  int64  `json:"domainId"`
	LocalPart string `json:"localPart"`
}

// CreateMemberRequest onboards a person.
type CreateMemberRequest struct {
	MemberInput
	NewMailbox      *NewMailboxSpec `json:"newMailbox"`      // create a fresh Purelymail user
	BindMailboxID   int64           `json:"bindMailboxId"`   // or attach an existing imported mailbox
	SendInvite      bool            `json:"sendInvite"`      // mint an invite link
	Password        string          `json:"password"`        // or set an initial password directly
	GroupIDs        []int64         `json:"groupIds"`        // add to groups
	SharedMailboxes []int64         `json:"sharedMailboxes"` // grant full access to shared mailboxes
}

// CreateMemberResult returns the new member and any invite link.
type CreateMemberResult struct {
	Member     *model.Member  `json:"member"`
	Mailbox    *model.Mailbox `json:"mailbox"`
	InviteLink string         `json:"inviteLink,omitempty"`
	Warnings   []string       `json:"warnings,omitempty"`
}

// CreateMember onboards a member: record, optional mailbox, invite.
func (s *Service) CreateMember(ctx context.Context, orgID, actor int64, req CreateMemberRequest) (*CreateMemberResult, error) {
	name := strings.TrimSpace(req.DisplayName)
	if name == "" {
		return nil, invalid("display name is required")
	}
	if req.RoleID == 0 {
		r, err := s.roleByKey(ctx, s.DB, orgID, model.RoleMember)
		if err != nil {
			return nil, err
		}
		req.RoleID = r.ID
	}
	role, err := s.Role(ctx, orgID, req.RoleID)
	if err != nil {
		return nil, invalid("role not found")
	}
	if role.Key == model.RoleOwner {
		return nil, invalid("use ownership transfer to create another owner")
	}
	loginEmail := strings.ToLower(strings.TrimSpace(req.LoginEmail))
	var mailboxAddr string
	if req.NewMailbox != nil {
		local := strings.ToLower(strings.TrimSpace(req.NewMailbox.LocalPart))
		dom, err := s.Domain(ctx, orgID, req.NewMailbox.DomainID)
		if err != nil {
			return nil, invalid("domain not found")
		}
		if !validLocalPart(local) {
			return nil, invalid("%q is not a valid mailbox name", local)
		}
		mailboxAddr = local + "@" + dom.Name
	} else if req.BindMailboxID != 0 {
		mb, err := s.Mailbox(ctx, orgID, req.BindMailboxID)
		if err != nil {
			return nil, invalid("mailbox not found")
		}
		if mb.OwnerMemberID != nil {
			return nil, invalid("mailbox %s already belongs to another member", mb.Address)
		}
		if mb.Kind != model.MailboxPersonal {
			return nil, invalid("shared mailboxes cannot be bound to a person; grant access instead")
		}
		mailboxAddr = mb.Address
	}
	if loginEmail == "" {
		loginEmail = mailboxAddr
	}
	if loginEmail, err = NormalizeEmail(loginEmail); err != nil {
		return nil, err
	}
	var pwHash string
	if req.Password != "" {
		if err := checkPasswordStrength(req.Password); err != nil {
			return nil, err
		}
		if pwHash, err = secrets.HashPassword(req.Password); err != nil {
			return nil, err
		}
	}
	status := model.MemberInvited
	if pwHash != "" {
		status = model.MemberActive
	}
	now := db.Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO members(org_id, display_name, login_email, password_hash, role_id, title, department, status, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, orgID, name, loginEmail, pwHash, req.RoleID, strings.TrimSpace(req.Title), strings.TrimSpace(req.Department), status, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("%w: a member with login %s already exists", ErrConflict, loginEmail)
		}
		return nil, err
	}
	memberID, _ := res.LastInsertId()
	s.audit(ctx, orgID, actor, "member.create", "member", fmt.Sprint(memberID), map[string]any{"name": name, "login": loginEmail, "role": role.Key})

	out := &CreateMemberResult{}
	if req.NewMailbox != nil {
		mb, err := s.CreateMailbox(ctx, orgID, actor, CreateMailboxRequest{Kind: model.MailboxPersonal, DomainID: req.NewMailbox.DomainID, LocalPart: req.NewMailbox.LocalPart, DisplayName: name, OwnerMemberID: memberID})
		if err != nil {
			out.Warnings = append(out.Warnings, "member created but mailbox creation failed: "+err.Error())
		} else {
			out.Mailbox = mb
		}
	} else if req.BindMailboxID != 0 {
		mb, err := s.BindMailbox(ctx, orgID, actor, req.BindMailboxID, memberID, name)
		if err != nil {
			out.Warnings = append(out.Warnings, "member created but mailbox binding failed: "+err.Error())
		} else {
			out.Mailbox = mb
		}
	}
	for _, gid := range req.GroupIDs {
		if err := s.AddGroupMember(ctx, orgID, actor, gid, memberID); err != nil {
			out.Warnings = append(out.Warnings, "group assignment failed: "+err.Error())
		}
	}
	for _, mid := range req.SharedMailboxes {
		if _, err := s.GrantAccess(ctx, orgID, actor, mid, memberID, model.AccessFull); err != nil {
			out.Warnings = append(out.Warnings, "shared mailbox access failed: "+err.Error())
		}
	}
	if req.SendInvite || pwHash == "" {
		link, err := s.CreateInvite(ctx, orgID, actor, memberID)
		if err != nil {
			out.Warnings = append(out.Warnings, "invite creation failed: "+err.Error())
		} else {
			out.InviteLink = link
		}
	}
	out.Member, err = s.Member(ctx, orgID, memberID)
	return out, err
}

func checkPasswordStrength(pw string) error {
	if len(pw) < 10 {
		return invalid("password must be at least 10 characters")
	}
	if len(pw) > 200 {
		return invalid("password is too long")
	}
	return nil
}

// UpdateMember edits profile fields and role.
func (s *Service) UpdateMember(ctx context.Context, orgID, actor, id int64, in MemberInput) (*model.Member, error) {
	m, err := s.Member(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.DisplayName)
	if name == "" {
		name = m.DisplayName
	}
	login := m.LoginEmail
	if strings.TrimSpace(in.LoginEmail) != "" {
		if login, err = NormalizeEmail(in.LoginEmail); err != nil {
			return nil, err
		}
	}
	roleID := m.RoleID
	if in.RoleID != 0 && in.RoleID != m.RoleID {
		newRole, err := s.Role(ctx, orgID, in.RoleID)
		if err != nil {
			return nil, invalid("role not found")
		}
		if newRole.Key == model.RoleOwner || m.RoleKey == model.RoleOwner {
			return nil, invalid("ownership is changed with the transfer action")
		}
		if actor == id {
			return nil, invalid("you cannot change your own role")
		}
		roleID = in.RoleID
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE members SET display_name = ?, login_email = ?, role_id = ?, title = ?, department = ?, updated_at = ? WHERE id = ?`,
		name, login, roleID, strings.TrimSpace(in.Title), strings.TrimSpace(in.Department), db.Now(), id); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("%w: login %s is already used", ErrConflict, login)
		}
		return nil, err
	}
	s.audit(ctx, orgID, actor, "member.update", "member", fmt.Sprint(id), map[string]any{"name": name, "login": login, "roleId": roleID})
	return s.Member(ctx, orgID, id)
}

// SetMemberStatus enables or disables a member (disabling revokes sessions).
func (s *Service) SetMemberStatus(ctx context.Context, orgID, actor, id int64, enable bool) (*model.Member, error) {
	m, err := s.Member(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if m.RoleKey == model.RoleOwner && !enable {
		return nil, invalid("the owner cannot be disabled")
	}
	if actor == id && !enable {
		return nil, invalid("you cannot disable yourself")
	}
	status := model.MemberDisabled
	if enable {
		status = model.MemberActive
		if !m.HasPassword {
			status = model.MemberInvited
		}
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE members SET status = ?, updated_at = ? WHERE id = ?`, status, db.Now(), id); err != nil {
		return nil, err
	}
	if !enable {
		s.RevokeSessions(ctx, id)
	}
	s.audit(ctx, orgID, actor, "member.status", "member", fmt.Sprint(id), map[string]any{"status": status})
	return s.Member(ctx, orgID, id)
}

// CreateInvite mints a single-use invite link for a member.
func (s *Service) CreateInvite(ctx context.Context, orgID, actor, memberID int64) (string, error) {
	m, err := s.Member(ctx, orgID, memberID)
	if err != nil {
		return "", err
	}
	if m.Status == model.MemberDeparted {
		return "", invalid("departed members cannot be invited; create a new member instead")
	}
	token := secrets.RandomToken(32)
	now := time.Now().UTC()
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM invites WHERE member_id = ? AND accepted_at IS NULL`, memberID); err != nil {
		return "", err
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO invites(org_id, member_id, token_hash, created_by, created_at, expires_at) VALUES (?,?,?,?,?,?)`,
		orgID, memberID, secrets.HashToken(token), actor, now.Format(time.RFC3339), now.Add(s.Cfg.InviteTTL).Format(time.RFC3339)); err != nil {
		return "", err
	}
	s.audit(ctx, orgID, actor, "member.invite", "member", fmt.Sprint(memberID), nil)
	return s.inviteLink(token), nil
}

func (s *Service) inviteLink(token string) string {
	base := s.Cfg.BaseURL
	if base == "" {
		return "/invite/" + token
	}
	return base + "/invite/" + token
}

// InviteInfo describes a pending invite to the person opening the link.
type InviteInfo struct {
	MemberName string `json:"memberName"`
	LoginEmail string `json:"loginEmail"`
	OrgName    string `json:"orgName"`
	ExpiresAt  string `json:"expiresAt"`
}

func (s *Service) lookupInvite(ctx context.Context, token string) (int64, int64, *InviteInfo, error) {
	var inviteID, memberID int64
	var info InviteInfo
	var expires string
	err := s.DB.QueryRowContext(ctx, `SELECT i.id, i.member_id, i.expires_at, m.display_name, m.login_email, o.name
		FROM invites i JOIN members m ON m.id = i.member_id JOIN organizations o ON o.id = i.org_id
		WHERE i.token_hash = ? AND i.accepted_at IS NULL`, secrets.HashToken(token)).Scan(&inviteID, &memberID, &expires, &info.MemberName, &info.LoginEmail, &info.OrgName)
	if db.IsNotFound(err) {
		return 0, 0, nil, ErrNotFound
	}
	if err != nil {
		return 0, 0, nil, err
	}
	if db.ParseTime(expires).Before(time.Now()) {
		return 0, 0, nil, invalid("this invite link has expired; ask your administrator for a new one")
	}
	info.ExpiresAt = expires
	return inviteID, memberID, &info, nil
}

// Invite returns invite details for the accept page.
func (s *Service) Invite(ctx context.Context, token string) (*InviteInfo, error) {
	_, _, info, err := s.lookupInvite(ctx, token)
	return info, err
}

// AcceptInvite sets the password and activates the member.
func (s *Service) AcceptInvite(ctx context.Context, token, password, displayName string) (*model.Member, error) {
	inviteID, memberID, _, err := s.lookupInvite(ctx, token)
	if err != nil {
		return nil, err
	}
	if err := checkPasswordStrength(password); err != nil {
		return nil, err
	}
	hash, err := secrets.HashPassword(password)
	if err != nil {
		return nil, err
	}
	now := db.Now()
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE invites SET accepted_at = ? WHERE id = ?`, now, inviteID); err != nil {
			return err
		}
		q := `UPDATE members SET password_hash = ?, status = 'active', updated_at = ? WHERE id = ? AND status IN ('invited','active')`
		args := []any{hash, now, memberID}
		if n := strings.TrimSpace(displayName); n != "" {
			q = `UPDATE members SET password_hash = ?, status = 'active', updated_at = ?, display_name = ? WHERE id = ? AND status IN ('invited','active')`
			args = []any{hash, now, n, memberID}
		}
		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return invalid("this account can no longer be activated")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.MemberByID(ctx, memberID)
}

// SetPassword sets a member's password (admin reset or self-service).
func (s *Service) SetPassword(ctx context.Context, orgID, actor, memberID int64, password string, revokeOthers bool) error {
	if err := checkPasswordStrength(password); err != nil {
		return err
	}
	hash, err := secrets.HashPassword(password)
	if err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE members SET password_hash = ?, status = CASE WHEN status = 'invited' THEN 'active' ELSE status END, updated_at = ? WHERE id = ? AND org_id = ?`, hash, db.Now(), memberID, orgID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if revokeOthers {
		s.RevokeSessions(ctx, memberID)
	}
	s.audit(ctx, orgID, actor, "member.password", "member", fmt.Sprint(memberID), map[string]any{"self": actor == memberID})
	return nil
}

// VerifyLogin checks credentials and returns the member.
func (s *Service) VerifyLogin(ctx context.Context, email, password string) (*model.Member, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var id int64
	var hash, status string
	err := s.DB.QueryRowContext(ctx, `SELECT id, password_hash, status FROM members WHERE login_email = ?`, email).Scan(&id, &hash, &status)
	if err != nil && !db.IsNotFound(err) {
		return nil, err
	}
	// Also allow logging in with any owned mailbox address.
	if db.IsNotFound(err) {
		err = s.DB.QueryRowContext(ctx, `SELECT m.id, m.password_hash, m.status FROM members m JOIN mailboxes b ON b.owner_member_id = m.id WHERE b.pm_user = ?`, email).Scan(&id, &hash, &status)
		if db.IsNotFound(err) {
			secrets.VerifyPassword("$argon2id$v=19$m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", password) // constant-time-ish
			return nil, ErrForbidden
		}
		if err != nil {
			return nil, err
		}
	}
	if hash == "" || !secrets.VerifyPassword(hash, password) {
		return nil, ErrForbidden
	}
	if status != model.MemberActive {
		return nil, fmt.Errorf("%w: account is %s", ErrForbidden, status)
	}
	s.DB.ExecContext(ctx, `UPDATE members SET last_login_at = ? WHERE id = ?`, db.Now(), id)
	return s.MemberByID(ctx, id)
}

// --- sessions ---

// Session is an authenticated browser session.
type Session struct {
	ID        int64
	MemberID  int64
	ExpiresAt time.Time
}

// CreateSession issues a session token.
func (s *Service) CreateSession(ctx context.Context, memberID int64, ip, ua string) (string, error) {
	token := secrets.RandomToken(32)
	now := time.Now().UTC()
	if len(ua) > 300 {
		ua = ua[:300]
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO sessions(member_id, token_hash, created_at, expires_at, last_seen_at, ip, user_agent) VALUES (?,?,?,?,?,?,?)`,
		memberID, secrets.HashToken(token), now.Format(time.RFC3339), now.Add(s.Cfg.SessionTTL).Format(time.RFC3339), now.Format(time.RFC3339), ip, ua)
	if err != nil {
		return "", err
	}
	return token, nil
}

// LookupSession resolves a token to a member (nil when invalid).
func (s *Service) LookupSession(ctx context.Context, token string) (*model.Member, error) {
	if token == "" {
		return nil, nil
	}
	var sid, memberID int64
	var expires, lastSeen string
	err := s.DB.QueryRowContext(ctx, `SELECT id, member_id, expires_at, last_seen_at FROM sessions WHERE token_hash = ?`, secrets.HashToken(token)).Scan(&sid, &memberID, &expires, &lastSeen)
	if db.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if db.ParseTime(expires).Before(time.Now()) {
		s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sid)
		return nil, nil
	}
	if time.Since(db.ParseTime(lastSeen)) > 5*time.Minute {
		s.DB.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?`, db.Now(), time.Now().UTC().Add(s.Cfg.SessionTTL).Format(time.RFC3339), sid)
	}
	m, err := s.MemberByID(ctx, memberID)
	if err != nil {
		return nil, nil
	}
	if m.Status != model.MemberActive {
		return nil, nil
	}
	return m, nil
}

// DeleteSession logs a session out.
func (s *Service) DeleteSession(ctx context.Context, token string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, secrets.HashToken(token))
	return err
}

// RevokeSessions logs a member out everywhere.
func (s *Service) RevokeSessions(ctx context.Context, memberID int64) {
	s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE member_id = ?`, memberID)
}

// Permissions returns the member's permission list and role key.
func (s *Service) Permissions(ctx context.Context, memberID int64) ([]string, string, error) {
	return s.memberPermissions(ctx, memberID)
}

// TransferOwnership makes another active member the owner and demotes the
// current owner to administrator.
func (s *Service) TransferOwnership(ctx context.Context, orgID, actor, toMemberID int64) error {
	from, err := s.Member(ctx, orgID, actor)
	if err != nil {
		return err
	}
	if from.RoleKey != model.RoleOwner {
		return ErrForbidden
	}
	to, err := s.Member(ctx, orgID, toMemberID)
	if err != nil {
		return err
	}
	if to.Status != model.MemberActive {
		return invalid("the new owner must be an active member")
	}
	owner, err := s.roleByKey(ctx, s.DB, orgID, model.RoleOwner)
	if err != nil {
		return err
	}
	admin, err := s.roleByKey(ctx, s.DB, orgID, model.RoleAdmin)
	if err != nil {
		return err
	}
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE members SET role_id = ?, updated_at = ? WHERE id = ?`, admin.ID, db.Now(), actor); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE members SET role_id = ?, updated_at = ? WHERE id = ?`, owner.ID, db.Now(), toMemberID)
		return err
	})
	if err != nil {
		return err
	}
	s.audit(ctx, orgID, actor, "org.transfer", "member", fmt.Sprint(toMemberID), map[string]any{"from": actor})
	return nil
}

// DeleteMember removes a departed/invited member with no mailboxes.
func (s *Service) DeleteMember(ctx context.Context, orgID, actor, id int64) error {
	m, err := s.Member(ctx, orgID, id)
	if err != nil {
		return err
	}
	if m.RoleKey == model.RoleOwner {
		return invalid("the owner cannot be deleted")
	}
	if actor == id {
		return invalid("you cannot delete yourself")
	}
	var owned int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM mailboxes WHERE owner_member_id = ?`, id).Scan(&owned)
	if owned > 0 {
		return invalid("offboard this member first so their %d mailbox(es) are handed over", owned)
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM members WHERE id = ?`, id); err != nil {
		return err
	}
	s.audit(ctx, orgID, actor, "member.delete", "member", fmt.Sprint(id), map[string]any{"name": m.DisplayName, "login": m.LoginEmail})
	return nil
}
