package core

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
)

// MailboxPlan says what happens to one mailbox when its owner leaves.
type MailboxPlan struct {
	MailboxID int64  `json:"mailboxId"`
	Action    string `json:"action"` // handover | shared | suspend | keep
	// handover: the member who takes over the mailbox (owner + history).
	NewOwnerID int64 `json:"newOwnerId"`
	// shared: members granted access to the now-shared mailbox.
	GrantMemberIDs []int64 `json:"grantMemberIds"`
	// Optional forwarding of new mail for the address to these targets.
	ForwardTo []string `json:"forwardTo"`
}

// OffboardRequest offboards a member.
type OffboardRequest struct {
	Plans            []MailboxPlan `json:"plans"`
	RemoveFromGroups bool          `json:"removeFromGroups"`
	RevokeShared     bool          `json:"revokeShared"`
}

// OffboardResult reports what happened.
type OffboardResult struct {
	Member   *model.Member `json:"member"`
	Warnings []string      `json:"warnings"`
}

// Offboard disables the member, revokes their sessions and applies the
// per-mailbox plan. Every mailbox credential the person could have used is
// rotated so that access truly ends even for external mail clients.
func (s *Service) Offboard(ctx context.Context, orgID, actor, memberID int64, req OffboardRequest) (*OffboardResult, error) {
	m, err := s.Member(ctx, orgID, memberID)
	if err != nil {
		return nil, err
	}
	if m.RoleKey == model.RoleOwner {
		return nil, invalid("transfer ownership before offboarding the owner")
	}
	if actor == memberID {
		return nil, invalid("you cannot offboard yourself")
	}
	res := &OffboardResult{Warnings: []string{}}
	owned, err := s.ownedMailboxes(ctx, orgID, memberID)
	if err != nil {
		return nil, err
	}
	plans := map[int64]MailboxPlan{}
	for _, p := range req.Plans {
		plans[p.MailboxID] = p
	}
	for _, mb := range owned {
		p, ok := plans[mb.ID]
		if !ok {
			p = MailboxPlan{MailboxID: mb.ID, Action: "suspend"}
		}
		if err := s.applyMailboxPlan(ctx, orgID, actor, &mb, p); err != nil {
			res.Warnings = append(res.Warnings, mb.Address+": "+err.Error())
		}
	}
	if req.RevokeShared {
		s.DB.ExecContext(ctx, `DELETE FROM mailbox_access WHERE member_id = ?`, memberID)
	}
	if req.RemoveFromGroups {
		rows, err := s.DB.QueryContext(ctx, `SELECT group_id FROM group_members WHERE member_id = ?`, memberID)
		if err == nil {
			var gids []int64
			for rows.Next() {
				var g int64
				rows.Scan(&g)
				gids = append(gids, g)
			}
			rows.Close()
			for _, g := range gids {
				if err := s.RemoveGroupMember(ctx, orgID, actor, g, memberID); err != nil {
					res.Warnings = append(res.Warnings, fmt.Sprintf("group %d: %v", g, err))
				}
			}
		}
	}
	now := db.Now()
	if _, err := s.DB.ExecContext(ctx, `UPDATE members SET status = 'departed', departed_at = ?, updated_at = ? WHERE id = ?`, now, now, memberID); err != nil {
		return nil, err
	}
	s.RevokeSessions(ctx, memberID)
	s.audit(ctx, orgID, actor, "member.offboard", "member", fmt.Sprint(memberID), map[string]any{"plans": req.Plans, "warnings": res.Warnings})
	res.Member, err = s.Member(ctx, orgID, memberID)
	return res, err
}

func (s *Service) ownedMailboxes(ctx context.Context, orgID, memberID int64) ([]model.Mailbox, error) {
	rows, err := s.DB.QueryContext(ctx, mailboxSelect+` WHERE b.org_id = ? AND b.owner_member_id = ?`, orgID, memberID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Mailbox
	for rows.Next() {
		b, err := scanMailbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

func (s *Service) applyMailboxPlan(ctx context.Context, orgID, actor int64, mb *model.Mailbox, p MailboxPlan) error {
	switch p.Action {
	case "handover":
		if p.NewOwnerID == 0 {
			return invalid("choose who takes over the mailbox")
		}
		if _, err := s.Member(ctx, orgID, p.NewOwnerID); err != nil {
			return invalid("new owner not found")
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE mailboxes SET owner_member_id = ?, updated_at = ? WHERE id = ?`, p.NewOwnerID, db.Now(), mb.ID); err != nil {
			return err
		}
		if err := s.RotateCredential(ctx, orgID, actor, mb.ID); err != nil {
			return err
		}
		if err := s.lockOutOtherClients(ctx, orgID, mb.ID); err != nil {
			return err
		}
		s.audit(ctx, orgID, actor, "mailbox.handover", "mailbox", fmt.Sprint(mb.ID), map[string]any{"address": mb.Address, "newOwner": p.NewOwnerID})
	case "shared":
		if _, err := s.DB.ExecContext(ctx, `UPDATE mailboxes SET kind = 'shared', owner_member_id = NULL, updated_at = ? WHERE id = ?`, db.Now(), mb.ID); err != nil {
			return err
		}
		for _, mid := range p.GrantMemberIDs {
			if _, err := s.GrantAccess(ctx, orgID, actor, mb.ID, mid, model.AccessFull); err != nil {
				return err
			}
		}
		if err := s.RotateCredential(ctx, orgID, actor, mb.ID); err != nil {
			return err
		}
		if err := s.lockOutOtherClients(ctx, orgID, mb.ID); err != nil {
			return err
		}
		s.audit(ctx, orgID, actor, "mailbox.toshared", "mailbox", fmt.Sprint(mb.ID), map[string]any{"address": mb.Address, "grants": p.GrantMemberIDs})
	case "keep":
		if err := s.RotateCredential(ctx, orgID, actor, mb.ID); err != nil {
			return err
		}
		if err := s.lockOutOtherClients(ctx, orgID, mb.ID); err != nil {
			return err
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE mailboxes SET owner_member_id = NULL, updated_at = ? WHERE id = ?`, db.Now(), mb.ID); err != nil {
			return err
		}
	default: // suspend
		if err := s.SuspendMailbox(ctx, orgID, actor, mb.ID); err != nil {
			return err
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE mailboxes SET owner_member_id = NULL, updated_at = ? WHERE id = ?`, db.Now(), mb.ID); err != nil {
			return err
		}
	}
	if len(p.ForwardTo) > 0 {
		if _, err := s.SetMailboxForwarding(ctx, orgID, actor, mb.ID, p.ForwardTo); err != nil {
			return err
		}
	}
	return nil
}

// lockOutOtherClients resets the Purelymail password so any phone or
// desktop client the departed person configured stops working.
func (s *Service) lockOutOtherClients(ctx context.Context, orgID, mailboxID int64) error {
	mb, err := s.Mailbox(ctx, orgID, mailboxID)
	if err != nil {
		return err
	}
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return err
	}
	pw := randomPassword()
	return upstream("purelymail modify user", api.ModifyUser(ctx, modifyPassword(mb.Address, pw)))
}

// --- team collaboration state on shared mailboxes ---

// MessageState is the team view of a message.
type MessageState struct {
	Key          string     `json:"key"`
	AssigneeID   *int64     `json:"assigneeId"`
	AssigneeName string     `json:"assigneeName"`
	Status       string     `json:"status"`
	UpdatedAt    string     `json:"updatedAt"`
	Activity     []Activity `json:"activity"`
}

// Activity is one row of the message's history.
type Activity struct {
	ID         int64  `json:"id"`
	MemberID   *int64 `json:"memberId"`
	MemberName string `json:"memberName"`
	Action     string `json:"action"`
	Detail     string `json:"detail"`
	CreatedAt  string `json:"createdAt"`
}

// MessageKey derives a stable key: the Message-ID header when present.
func MessageKey(messageID, folder string, uidValidity, uid uint32) string {
	if id := strings.Trim(strings.TrimSpace(messageID), "<>"); id != "" {
		return "mid:" + strings.ToLower(id)
	}
	return fmt.Sprintf("uid:%s/%d/%d", folder, uidValidity, uid)
}

// RecordActivity appends an activity row.
func (s *Service) RecordActivity(ctx context.Context, mailboxID, memberID int64, key, action, detail string) error {
	if len(detail) > 2000 {
		detail = detail[:2000]
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO mail_activity(mailbox_id, message_key, member_id, action, detail, created_at) VALUES (?,?,?,?,?,?)`, mailboxID, key, memberID, action, detail, db.Now())
	return err
}

// GetMessageState returns state + activity for a message.
func (s *Service) GetMessageState(ctx context.Context, mailboxID int64, key string) (*MessageState, error) {
	st := &MessageState{Key: key, Status: "open", Activity: []Activity{}}
	var assignee sql.NullInt64
	var name sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT ms.assignee_member_id, m.display_name, ms.status, ms.updated_at FROM message_state ms LEFT JOIN members m ON m.id = ms.assignee_member_id WHERE ms.mailbox_id = ? AND ms.message_key = ?`, mailboxID, key).
		Scan(&assignee, &name, &st.Status, &st.UpdatedAt)
	if err != nil && !db.IsNotFound(err) {
		return nil, err
	}
	st.AssigneeID = nullInt(assignee)
	if name.Valid {
		st.AssigneeName = name.String
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT a.id, a.member_id, COALESCE(m.display_name, ''), a.action, a.detail, a.created_at FROM mail_activity a LEFT JOIN members m ON m.id = a.member_id WHERE a.mailbox_id = ? AND a.message_key = ? ORDER BY a.id`, mailboxID, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a Activity
		var mid sql.NullInt64
		if err := rows.Scan(&a.ID, &mid, &a.MemberName, &a.Action, &a.Detail, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.MemberID = nullInt(mid)
		st.Activity = append(st.Activity, a)
	}
	return st, rows.Err()
}

// StatesFor returns states for many keys (message list badges).
func (s *Service) StatesFor(ctx context.Context, mailboxID int64, keys []string) (map[string]*MessageState, error) {
	out := map[string]*MessageState{}
	if len(keys) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(keys)+1)
	args = append(args, mailboxID)
	ph := make([]string, len(keys))
	for i, k := range keys {
		ph[i] = "?"
		args = append(args, k)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT ms.message_key, ms.assignee_member_id, COALESCE(m.display_name,''), ms.status, ms.updated_at FROM message_state ms LEFT JOIN members m ON m.id = ms.assignee_member_id WHERE ms.mailbox_id = ? AND ms.message_key IN (`+strings.Join(ph, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		st := &MessageState{Activity: []Activity{}}
		var assignee sql.NullInt64
		if err := rows.Scan(&st.Key, &assignee, &st.AssigneeName, &st.Status, &st.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		st.AssigneeID = nullInt(assignee)
		out[st.Key] = st
	}
	rows.Close()
	// Latest reply/forward per key for "handled by" badges.
	rows, err = s.DB.QueryContext(ctx, `SELECT a.message_key, a.member_id, COALESCE(m.display_name,''), a.action, a.detail, a.created_at FROM mail_activity a LEFT JOIN members m ON m.id = a.member_id
		WHERE a.mailbox_id = ? AND a.message_key IN (`+strings.Join(ph, ",")+`) AND a.action IN ('replied','forwarded') ORDER BY a.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var a Activity
		var mid sql.NullInt64
		if err := rows.Scan(&key, &mid, &a.MemberName, &a.Action, &a.Detail, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.MemberID = nullInt(mid)
		st := out[key]
		if st == nil {
			st = &MessageState{Key: key, Status: "open", Activity: []Activity{}}
			out[key] = st
		}
		st.Activity = append(st.Activity, a)
	}
	return out, rows.Err()
}

// Assign sets or clears the assignee.
func (s *Service) Assign(ctx context.Context, mailboxID, actor int64, key string, assignee *int64) error {
	var a any
	detail := "unassigned"
	if assignee != nil && *assignee != 0 {
		a = *assignee
		var name string
		if err := s.DB.QueryRowContext(ctx, `SELECT display_name FROM members WHERE id = ?`, *assignee).Scan(&name); err != nil {
			return invalid("member not found")
		}
		detail = name
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO message_state(mailbox_id, message_key, assignee_member_id, status, updated_at) VALUES (?,?,?,'open',?)
		ON CONFLICT(mailbox_id, message_key) DO UPDATE SET assignee_member_id = excluded.assignee_member_id, updated_at = excluded.updated_at`, mailboxID, key, a, db.Now()); err != nil {
		return err
	}
	action := "assigned"
	if a == nil {
		action = "unassigned"
	}
	return s.RecordActivity(ctx, mailboxID, actor, key, action, detail)
}

// SetStatus marks a message open or resolved.
func (s *Service) SetStatus(ctx context.Context, mailboxID, actor int64, key, status string) error {
	if status != "open" && status != "resolved" {
		return invalid("status must be open or resolved")
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO message_state(mailbox_id, message_key, status, updated_at) VALUES (?,?,?,?)
		ON CONFLICT(mailbox_id, message_key) DO UPDATE SET status = excluded.status, updated_at = excluded.updated_at`, mailboxID, key, status, db.Now()); err != nil {
		return err
	}
	action := "resolved"
	if status == "open" {
		action = "reopened"
	}
	return s.RecordActivity(ctx, mailboxID, actor, key, action, "")
}

// AddNote appends an internal note.
func (s *Service) AddNote(ctx context.Context, mailboxID, actor int64, key, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return invalid("note text is required")
	}
	return s.RecordActivity(ctx, mailboxID, actor, key, "note", text)
}

// MailboxMembers lists people who can act on a mailbox (for assignment).
func (s *Service) MailboxMembers(ctx context.Context, orgID, mailboxID int64) ([]model.Member, error) {
	rows, err := s.DB.QueryContext(ctx, memberSelect+` WHERE m.org_id = ? AND m.status = 'active' AND (m.id = (SELECT owner_member_id FROM mailboxes WHERE id = ?) OR m.id IN (SELECT member_id FROM mailbox_access WHERE mailbox_id = ?)) ORDER BY m.display_name`, orgID, mailboxID, mailboxID)
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

// Contact is an autocomplete suggestion.
type Contact struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Kind    string `json:"kind"` // member | shared | group | alias
}

// SuggestContacts searches organisation addresses for the composer.
func (s *Service) SuggestContacts(ctx context.Context, orgID int64, q string, limit int) ([]Contact, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	like := "%" + q + "%"
	rows, err := s.DB.QueryContext(ctx, `
		SELECT COALESCE(m.display_name, b.display_name), b.pm_user, CASE WHEN b.kind = 'shared' THEN 'shared' ELSE 'member' END
		FROM mailboxes b LEFT JOIN members m ON m.id = b.owner_member_id
		WHERE b.org_id = ? AND b.status = 'active' AND (b.pm_user LIKE ? OR LOWER(COALESCE(m.display_name, b.display_name)) LIKE ?)
		UNION ALL
		SELECT COALESCE(g.name, a.local_part), a.address, CASE WHEN a.group_id IS NOT NULL THEN 'group' ELSE 'alias' END
		FROM addresses a LEFT JOIN groups g ON g.id = a.group_id
		WHERE a.org_id = ? AND a.kind IN ('alias','group','forward') AND (a.address LIKE ? OR LOWER(COALESCE(g.name, '')) LIKE ?)
		LIMIT ?`, orgID, like, like, orgID, like, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Contact{}
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.Name, &c.Address, &c.Kind); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
