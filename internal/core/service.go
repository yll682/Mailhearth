// Package core implements the organisation model on top of SQLite and the
// Purelymail API: members, mailboxes, addresses, shared mailboxes, groups,
// roles, onboarding and offboarding. Handlers call into this package; it
// never touches HTTP.
package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"regexp"
	"strings"

	"mailhearth/internal/config"
	"mailhearth/internal/db"
	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/model"
	"mailhearth/internal/purelymail"
	"mailhearth/internal/secrets"
)

// Sentinel errors mapped to HTTP statuses by the API layer.
var (
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
	ErrConflict  = errors.New("conflict")
	ErrNoSetup   = errors.New("setup required")
)

// ValidationError carries a user-facing message.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// UpstreamError wraps a Purelymail/IMAP failure so the UI can explain it.
type UpstreamError struct {
	Op  string
	Err error
}

func (e *UpstreamError) Error() string { return e.Op + ": " + e.Err.Error() }
func (e *UpstreamError) Unwrap() error { return e.Err }

func upstream(op string, err error) error {
	if err == nil {
		return nil
	}
	return &UpstreamError{Op: op, Err: err}
}

// Service is the application service.
type Service struct {
	DB    *db.DB
	Cfg   *config.Config
	Box   *secrets.Box
	Log   *slog.Logger
	Pool  *imappool.Pool
	NewPM func(token string) purelymail.API
}

// New wires a service.
func New(database *db.DB, cfg *config.Config, box *secrets.Box, pool *imappool.Pool, log *slog.Logger) *Service {
	s := &Service{DB: database, Cfg: cfg, Box: box, Pool: pool, Log: log}
	s.NewPM = func(token string) purelymail.API { return purelymail.New(cfg.PurelymailAPIURL, token) }
	return s
}

var (
	localPartRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._+-]{0,62}[a-z0-9])?$`)
	domainRe    = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
)

// NormalizeEmail lower-cases and validates an address.
func NormalizeEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", invalid("email address is required")
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s {
		return "", invalid("%q is not a valid email address", s)
	}
	return s, nil
}

// SplitAddress returns local part and domain.
func SplitAddress(addr string) (string, string) {
	i := strings.LastIndex(addr, "@")
	if i < 0 {
		return addr, ""
	}
	return addr[:i], addr[i+1:]
}

func validLocalPart(s string) bool { return localPartRe.MatchString(s) }
func validDomain(s string) bool    { return domainRe.MatchString(s) }

func nullStr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := s.String
	return &v
}

func nullInt(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// Org returns the single organisation of this installation.
func (s *Service) Org(ctx context.Context) (*model.Organization, error) {
	var o model.Organization
	err := s.DB.QueryRowContext(ctx, `SELECT id, name, created_at FROM organizations ORDER BY id LIMIT 1`).Scan(&o.ID, &o.Name, &o.CreatedAt)
	if db.IsNotFound(err) {
		return nil, ErrNoSetup
	}
	return &o, err
}

// UpdateOrg renames the organisation.
func (s *Service) UpdateOrg(ctx context.Context, actor int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return invalid("organisation name is required")
	}
	org, err := s.Org(ctx)
	if err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE organizations SET name = ? WHERE id = ?`, name, org.ID); err != nil {
		return err
	}
	s.audit(ctx, org.ID, actor, "org.update", "org", fmt.Sprint(org.ID), map[string]any{"name": name})
	return nil
}

// pm returns a Purelymail client for the organisation, or ErrNoSetup.
func (s *Service) pm(ctx context.Context, orgID int64) (purelymail.API, error) {
	var enc string
	err := s.DB.QueryRowContext(ctx, `SELECT api_token_enc FROM purelymail_accounts WHERE org_id = ? ORDER BY id LIMIT 1`, orgID).Scan(&enc)
	if db.IsNotFound(err) {
		return nil, ErrNoSetup
	}
	if err != nil {
		return nil, err
	}
	token, err := s.Box.Open(enc)
	if err != nil {
		return nil, err
	}
	return s.NewPM(token), nil
}

// audit records an administrative action.
func (s *Service) audit(ctx context.Context, orgID int64, actor int64, action, targetType, targetID string, detail any) {
	var actorID any
	if actor > 0 {
		actorID = actor
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO audit_log(org_id, actor_member_id, action, target_type, target_id, detail_json, created_at) VALUES (?,?,?,?,?,?,?)`,
		orgID, actorID, action, targetType, targetID, toJSON(detail), db.Now()); err != nil {
		s.Log.Warn("audit write failed", "err", err)
	}
}

// Audit lists recent audit entries.
func (s *Service) Audit(ctx context.Context, orgID int64, before int64, limit int) ([]model.AuditEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if before <= 0 {
		before = 1 << 62
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT a.id, a.actor_member_id, COALESCE(m.display_name, ''), a.action, a.target_type, a.target_id, a.detail_json, a.created_at
		FROM audit_log a LEFT JOIN members m ON m.id = a.actor_member_id
		WHERE a.org_id = ? AND a.id < ? ORDER BY a.id DESC LIMIT ?`, orgID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.AuditEntry{}
	for rows.Next() {
		var e model.AuditEntry
		var actor sql.NullInt64
		var detail string
		if err := rows.Scan(&e.ID, &actor, &e.ActorName, &e.Action, &e.TargetType, &e.TargetID, &detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.ActorID = nullInt(actor)
		var d any
		json.Unmarshal([]byte(detail), &d)
		e.Detail = d
		out = append(out, e)
	}
	return out, rows.Err()
}
