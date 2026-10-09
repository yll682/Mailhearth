package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
	"mailhearth/internal/secrets"
)

const memberSelect = `SELECT m.id, m.display_name, m.login_email, m.role_id, r.key, r.name, m.title, m.department, m.status,
	m.password_hash != '', m.created_at, m.updated_at, m.last_login_at, m.departed_at, m.revision
	FROM members m JOIN roles r ON r.id = m.role_id`

func scanMember(row interface{ Scan(...any) error }) (*model.Member, error) {
	var m model.Member
	var last, departed sql.NullString
	if err := row.Scan(&m.ID, &m.DisplayName, &m.LoginEmail, &m.RoleID, &m.RoleKey, &m.RoleName, &m.Title, &m.Department, &m.Status,
		&m.HasPassword, &m.CreatedAt, &m.UpdatedAt, &last, &departed, &m.Revision); err != nil {
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
	ExpectedRevision int64  `json:"expectedRevision"`
	DisplayName      string `json:"displayName"`
	LoginEmail       string `json:"loginEmail"`
	RoleID           int64  `json:"roleId"`
	Title            string `json:"title"`
	Department       string `json:"department"`
}

// NewMailboxSpec optionally creates a mailbox during onboarding.
type NewMailboxSpec struct {
	DomainID  int64  `json:"domainId"`
	LocalPart string `json:"localPart"`
}

// CreateMemberRequest onboards a person.
type CreateMemberRequest struct {
	MemberInput
	RequestID               string                     `json:"requestId"`
	MailboxAction           string                     `json:"mailboxAction"`
	MailboxID               int64                      `json:"mailboxId,omitempty"`
	MailboxExpectedRevision int64                      `json:"mailboxExpectedRevision,omitempty"`
	Create                  *ManagedMailboxCreateInput `json:"create,omitempty"`
	Attach                  *AttachMailboxInput        `json:"attach,omitempty"`
	GroupExpectedRevisions  map[int64]int64            `json:"groupExpectedRevisions,omitempty"`
	SharedExpectedRevisions map[int64]int64            `json:"sharedExpectedRevisions,omitempty"`
	NewMailbox              *NewMailboxSpec            `json:"-"`
	BindMailboxID           int64                      `json:"-"`
	SendInvite              bool                       `json:"sendInvite"`      // mint an invite link
	Password                string                     `json:"password"`        // or set an initial password directly
	GroupIDs                []int64                    `json:"groupIds"`        // add to groups
	SharedMailboxes         []int64                    `json:"sharedMailboxes"` // grant full access to shared mailboxes
}

// CreateMemberResult returns the new member and any invite link.
type CreateMemberResult struct {
	Operation  *OperationView `json:"operation,omitempty"`
	Member     *model.Member  `json:"member"`
	Mailbox    *model.Mailbox `json:"mailbox"`
	InviteLink string         `json:"inviteLink,omitempty"`
	Warnings   []string       `json:"warnings,omitempty"`
}

// CreateMember onboards a member: record, optional mailbox, invite.
func (s *Service) CreateMember(ctx context.Context, orgID, actor int64, req CreateMemberRequest) (*CreateMemberResult, error) {
	if req.MailboxAction == "" && req.NewMailbox == nil && req.BindMailboxID == 0 && req.Create == nil && req.Attach == nil && req.MailboxID == 0 {
		req.MailboxAction = "none"
	}
	if req.RequestID == "" {
		req.RequestID = uuid.NewString()
	}
	op, _, err := s.QueueOperation(ctx, orgID, actor, req.RequestID, OperationPayload{Kind: "member.create", MemberCreate: &req}, nil)
	if err != nil {
		return nil, err
	}
	if !s.operationsStarted.Load() && op.Status == "queued" {
		var sealed string
		if err := s.DB.QueryRowContext(ctx, `SELECT payload_enc FROM operations WHERE id=?`, op.ID).Scan(&sealed); err != nil {
			return nil, err
		}
		if err := s.executeOperation(ctx, orgID, actor, op.ID, sealed); err != nil {
			return nil, err
		}
		op, err = s.Operation(ctx, orgID, op.ID)
		if err != nil {
			return nil, err
		}
	}
	var body string
	if err := s.DB.QueryRowContext(ctx, `SELECT result_json FROM operation_steps WHERE operation_id=? AND step_key='member.prepare'`, op.ID).Scan(&body); err != nil {
		return nil, err
	}
	var plan memberCreationPlan
	if err := json.Unmarshal([]byte(body), &plan); err != nil {
		return nil, err
	}
	member, err := s.Member(ctx, orgID, plan.MemberID)
	if err != nil {
		return nil, err
	}
	out := &CreateMemberResult{Member: member, Operation: op}
	if op.Status == "succeeded" {
		var result struct {
			MailboxID  int64  `json:"mailboxId"`
			InviteLink string `json:"inviteLink"`
		}
		if err := json.Unmarshal(op.Result, &result); err != nil {
			return nil, err
		}
		out.InviteLink = result.InviteLink
		if result.MailboxID > 0 {
			out.Mailbox, err = s.Mailbox(ctx, orgID, result.MailboxID)
			if err != nil {
				return nil, err
			}
		}
	}
	return out, nil
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
	if err := requireManagementPermission(ctx, s.DB, orgID, actor, model.PermMembersManage); err != nil {
		return nil, err
	}
	m, err := s.Member(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if in.ExpectedRevision < 1 || in.ExpectedRevision != m.Revision {
		return nil, provider.Errorf("revision_conflict", "需要成员当前 expectedRevision")
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
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := requireManagementPermission(ctx, tx, orgID, actor, model.PermMembersManage); err != nil {
			return err
		}
		if err := requireResourceAvailable(ctx, tx, orgID, "member:"+fmtID(id), "member-login:"+login); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE members SET display_name = ?, login_email = ?, role_id = ?, title = ?, department = ?, revision=revision+1,updated_at = ? WHERE id = ? AND org_id=? AND revision=?`, name, login, roleID, strings.TrimSpace(in.Title), strings.TrimSpace(in.Department), db.Now(), id, orgID, in.ExpectedRevision)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return provider.Errorf("revision_conflict", "成员已经更新")
		}
		return nil
	})
	if err != nil {
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
	in := MemberStatusInput{ExpectedRevision: m.Revision, Enabled: &enable}
	if _, err := s.runLocalManagementOperation(ctx, orgID, actor, "", OperationPayload{Kind: "member.status", MemberID: id, MemberStatus: &in}); err != nil {
		return nil, err
	}
	return s.Member(ctx, orgID, id)
}

// CreateInvite mints a single-use invite link for a member.
func (s *Service) CreateInvite(ctx context.Context, orgID, actor, memberID int64) (string, error) {
	if err := requireManagementPermission(ctx, s.DB, orgID, actor, model.PermMembersManage); err != nil {
		return "", err
	}
	m, err := s.Member(ctx, orgID, memberID)
	if err != nil {
		return "", err
	}
	if m.Status == model.MemberDeparted {
		return "", invalid("departed members cannot be invited; create a new member instead")
	}
	token := secrets.RandomToken(32)
	now := time.Now().UTC()
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := requireManagementPermission(ctx, tx, orgID, actor, model.PermMembersManage); err != nil {
			return err
		}
		if err := requireResourceAvailable(ctx, tx, orgID, "member:"+fmtID(memberID)); err != nil {
			return err
		}
		var current bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM members WHERE org_id=? AND id=? AND revision=? AND status IN ('active','invited'))`, orgID, memberID, m.Revision).Scan(&current); err != nil {
			return err
		}
		if !current {
			return invalid("成员状态或版本已经更新")
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM invites WHERE member_id=? AND accepted_at IS NULL`, memberID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO invites(org_id,member_id,token_hash,created_by,created_at,expires_at) VALUES (?,?,?,?,?,?)`, orgID, memberID, secrets.HashToken(token), actor, now.Format(time.RFC3339), now.Add(s.Cfg.InviteTTL).Format(time.RFC3339))
		return err
	})
	if err != nil {
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
		var orgID int64
		if err := tx.QueryRowContext(ctx, `SELECT org_id FROM members WHERE id=?`, memberID).Scan(&orgID); err != nil {
			return err
		}
		if err := requireResourceAvailable(ctx, tx, orgID, "member:"+fmtID(memberID)); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE invites SET accepted_at = ? WHERE id = ? AND member_id=? AND accepted_at IS NULL AND expires_at>?`, now, inviteID, memberID, now)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return invalid("邀请已经使用或过期")
		}
		q := `UPDATE members SET password_hash = ?, status = 'active',revision=revision+1, updated_at = ? WHERE id = ? AND status IN ('invited','active')`
		args := []any{hash, now, memberID}
		if n := strings.TrimSpace(displayName); n != "" {
			q = `UPDATE members SET password_hash = ?, status = 'active',revision=revision+1, updated_at = ?, display_name = ? WHERE id = ? AND status IN ('invited','active')`
			args = []any{hash, now, n, memberID}
		}
		res, err = tx.ExecContext(ctx, q, args...)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
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
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := requireResourceAvailable(ctx, tx, orgID, "member:"+fmtID(memberID)); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE members SET password_hash=?,status=CASE WHEN status='invited' THEN 'active' ELSE status END,revision=revision+1,updated_at=? WHERE id=? AND org_id=? AND status!='departed'`, hash, db.Now(), memberID, orgID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrNotFound
		}
		if revokeOthers {
			if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE member_id=?`, memberID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if revokeOthers {
		s.cancelMailRequests(memberID, 0)
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
		err = s.DB.QueryRowContext(ctx, `SELECT m.id,m.password_hash,m.status FROM members m WHERE m.id=(SELECT MIN(b.owner_member_id) FROM mailboxes b WHERE LOWER(b.address)=? HAVING COUNT(DISTINCT b.owner_member_id)=1)`, email).Scan(&id, &hash, &status)
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
	err := s.DB.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO sessions(member_id,token_hash,created_at,expires_at,last_seen_at,ip,user_agent) SELECT id,?,?,?,?,?,? FROM members WHERE id=? AND status='active'`, secrets.HashToken(token), now.Format(time.RFC3339), now.Add(s.Cfg.SessionTTL).Format(time.RFC3339), now.Format(time.RFC3339), ip, ua, memberID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrForbidden
		}
		_, err = tx.ExecContext(ctx, `UPDATE members SET last_login_at=? WHERE id=? AND status='active'`, db.Now(), memberID)
		return err
	})
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
	if err == nil {
		s.requestMu.Lock()
		for _, request := range s.requests {
			if request.sessionHash == secrets.HashToken(token) {
				request.cancel()
			}
		}
		s.requestMu.Unlock()
	}
	return err
}

// RevokeSessions logs a member out everywhere.
func (s *Service) RevokeSessions(ctx context.Context, memberID int64) error {
	s.cancelMailRequests(memberID, 0)
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE member_id = ?`, memberID)
	return err
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
		if err := requireResourceAvailable(ctx, tx, orgID, "member:"+fmtID(actor), "member:"+fmtID(toMemberID)); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE members SET role_id=?,revision=revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=? AND status='active' AND role_id=?`, admin.ID, db.Now(), actor, orgID, from.Revision, owner.ID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("%w: 所有者状态或版本已经更新", ErrConflict)
		}
		res, err = tx.ExecContext(ctx, `UPDATE members SET role_id=?,revision=revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=? AND status='active'`, owner.ID, db.Now(), toMemberID, orgID, to.Revision)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("%w: 接收成员状态或版本已经更新", ErrConflict)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.audit(ctx, orgID, actor, "org.transfer", "member", fmt.Sprint(toMemberID), map[string]any{"from": actor})
	return nil
}

// DeleteMember 删除没有关联资源或历史的离职、受邀成员。
func (s *Service) DeleteMember(ctx context.Context, orgID, actor, id int64, expectedRevision ...int64) error {
	if err := requireManagementPermission(ctx, s.DB, orgID, actor, model.PermMembersManage); err != nil {
		return err
	}
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
	if len(expectedRevision) != 1 || expectedRevision[0] < 1 || expectedRevision[0] != m.Revision {
		return provider.Errorf("revision_conflict", "需要成员当前 expectedRevision")
	}
	if m.Status != model.MemberInvited && m.Status != model.MemberDeparted {
		return provider.Errorf("invalid", "只能删除受邀或离职成员")
	}
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := requireManagementPermission(ctx, tx, orgID, actor, model.PermMembersManage); err != nil {
			return err
		}
		if err := requireResourceAvailable(ctx, tx, orgID, "member:"+fmtID(id), "member-login:"+m.LoginEmail); err != nil {
			return err
		}
		var dependencies struct {
			Mailboxes    int `json:"mailboxes"`
			Groups       int `json:"groups"`
			SharedAccess int `json:"sharedAccess"`
			History      int `json:"history"`
		}
		if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM mailboxes WHERE owner_member_id=?),(SELECT COUNT(*) FROM group_members WHERE member_id=?),(SELECT COUNT(*) FROM mailbox_access WHERE member_id=?),(SELECT COUNT(*) FROM message_state WHERE assignee_member_id=?)+(SELECT COUNT(*) FROM mail_activity WHERE member_id=?)+(SELECT COUNT(*) FROM operations WHERE actor_member_id=?)+(SELECT COUNT(*) FROM submissions WHERE member_id=?)`, id, id, id, id, id, id, id).Scan(&dependencies.Mailboxes, &dependencies.Groups, &dependencies.SharedAccess, &dependencies.History); err != nil {
			return err
		}
		if dependencies.Mailboxes+dependencies.Groups+dependencies.SharedAccess+dependencies.History > 0 {
			failure := provider.Errorf("member_in_use", "成员仍有关联资源或历史记录")
			failure.Details = dependencies
			return failure
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM members WHERE id=? AND org_id=? AND revision=? AND status IN ('invited','departed')`, id, orgID, expectedRevision[0])
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return provider.Errorf("revision_conflict", "成员状态或版本已经更新")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_log(org_id,actor_member_id,action,target_type,target_id,detail_json,created_at) VALUES (?,?,'member.delete','member',?,?,?)`, orgID, actor, fmtID(id), toJSON(map[string]any{"name": m.DisplayName, "login": m.LoginEmail}), db.Now())
		return err
	})
	if err == nil {
		s.cancelMailRequests(id, 0)
	}
	return err
}
