package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
)

var builtinRoles = []model.Role{
	{Key: model.RoleOwner, Name: "Owner", Description: "Full control including the Purelymail connection and ownership transfer.", Permissions: model.AllPermissions, Builtin: true},
	{Key: model.RoleAdmin, Name: "Administrator", Description: "Manages people, mailboxes, addresses and domains.", Permissions: func() []string {
		var p []string
		for _, x := range model.AllPermissions {
			if x != model.PermOrgOwner {
				p = append(p, x)
			}
		}
		return p
	}(), Builtin: true},
	{Key: model.RoleMember, Name: "Member", Description: "Uses their own and shared mailboxes.", Permissions: []string{}, Builtin: true},
}

func (s *Service) ensureBuiltinRoles(ctx context.Context, q db.Querier, orgID int64) error {
	for _, r := range builtinRoles {
		if _, err := q.ExecContext(ctx, `INSERT INTO roles(org_id, key, name, description, permissions_json, builtin) VALUES (?,?,?,?,?,1)
			ON CONFLICT(org_id, key) DO UPDATE SET permissions_json = excluded.permissions_json`, orgID, r.Key, r.Name, r.Description, toJSON(r.Permissions)); err != nil {
			return err
		}
	}
	return nil
}

func scanRole(row interface{ Scan(...any) error }) (*model.Role, error) {
	var r model.Role
	var perms string
	var builtin int
	if err := row.Scan(&r.ID, &r.Key, &r.Name, &r.Description, &perms, &builtin, &r.MemberCount); err != nil {
		return nil, err
	}
	r.Builtin = builtin == 1
	json.Unmarshal([]byte(perms), &r.Permissions)
	if r.Permissions == nil {
		r.Permissions = []string{}
	}
	return &r, nil
}

const roleSelect = `SELECT r.id, r.key, r.name, r.description, r.permissions_json, r.builtin,
	(SELECT COUNT(1) FROM members m WHERE m.role_id = r.id) FROM roles r`

// Roles lists roles.
func (s *Service) Roles(ctx context.Context, orgID int64) ([]model.Role, error) {
	rows, err := s.DB.QueryContext(ctx, roleSelect+` WHERE r.org_id = ? ORDER BY r.builtin DESC, r.id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Role{}
	for rows.Next() {
		r, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// Role fetches a role by id.
func (s *Service) Role(ctx context.Context, orgID, id int64) (*model.Role, error) {
	r, err := scanRole(s.DB.QueryRowContext(ctx, roleSelect+` WHERE r.org_id = ? AND r.id = ?`, orgID, id))
	if db.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *Service) roleByKey(ctx context.Context, q db.Querier, orgID int64, key string) (*model.Role, error) {
	r, err := scanRole(q.QueryRowContext(ctx, roleSelect+` WHERE r.org_id = ? AND r.key = ?`, orgID, key))
	if db.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return r, err
}

func validatePermissions(perms []string) ([]string, error) {
	allowed := map[string]bool{}
	for _, p := range model.AllPermissions {
		allowed[p] = true
	}
	out := []string{}
	seen := map[string]bool{}
	for _, p := range perms {
		p = strings.TrimSpace(p)
		if !allowed[p] {
			return nil, invalid("unknown permission %q", p)
		}
		if p == model.PermOrgOwner {
			return nil, invalid("the owner permission cannot be granted to custom roles")
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, nil
}

// RoleInput creates or updates a custom role.
type RoleInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

// CreateRole adds a custom role.
func (s *Service) CreateRole(ctx context.Context, orgID, actor int64, in RoleInput) (*model.Role, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, invalid("role name is required")
	}
	perms, err := validatePermissions(in.Permissions)
	if err != nil {
		return nil, err
	}
	key := "custom-" + strings.ToLower(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, name))
	res, err := s.DB.ExecContext(ctx, `INSERT INTO roles(org_id, key, name, description, permissions_json, builtin) VALUES (?,?,?,?,?,0)`, orgID, key, name, strings.TrimSpace(in.Description), toJSON(perms))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("%w: a role with a similar name exists", ErrConflict)
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	s.audit(ctx, orgID, actor, "role.create", "role", fmt.Sprint(id), map[string]any{"name": name, "permissions": perms})
	return s.Role(ctx, orgID, id)
}

// UpdateRole changes a custom role.
func (s *Service) UpdateRole(ctx context.Context, orgID, actor, id int64, in RoleInput) (*model.Role, error) {
	r, err := s.Role(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if r.Builtin {
		return nil, invalid("built-in roles cannot be edited")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, invalid("role name is required")
	}
	perms, err := validatePermissions(in.Permissions)
	if err != nil {
		return nil, err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE roles SET name = ?, description = ?, permissions_json = ? WHERE id = ?`, name, strings.TrimSpace(in.Description), toJSON(perms), id); err != nil {
		return nil, err
	}
	s.audit(ctx, orgID, actor, "role.update", "role", fmt.Sprint(id), map[string]any{"name": name, "permissions": perms})
	return s.Role(ctx, orgID, id)
}

// DeleteRole removes a custom role that no member uses.
func (s *Service) DeleteRole(ctx context.Context, orgID, actor, id int64) error {
	r, err := s.Role(ctx, orgID, id)
	if err != nil {
		return err
	}
	if r.Builtin {
		return invalid("built-in roles cannot be deleted")
	}
	if r.MemberCount > 0 {
		return invalid("reassign the %d member(s) using this role first", r.MemberCount)
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM roles WHERE id = ?`, id); err != nil {
		return err
	}
	s.audit(ctx, orgID, actor, "role.delete", "role", fmt.Sprint(id), map[string]any{"name": r.Name})
	return nil
}

// HasPermission reports whether a permission set includes perm (owners have
// everything).
func HasPermission(perms []string, perm string) bool {
	for _, p := range perms {
		if p == model.PermOrgOwner || p == perm {
			return true
		}
	}
	return false
}

func (s *Service) memberPermissions(ctx context.Context, memberID int64) ([]string, string, error) {
	var perms, key string
	err := s.DB.QueryRowContext(ctx, `SELECT r.permissions_json, r.key FROM members m JOIN roles r ON r.id = m.role_id WHERE m.id = ?`, memberID).Scan(&perms, &key)
	if err != nil {
		if db.IsNotFound(err) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}
	var out []string
	json.Unmarshal([]byte(perms), &out)
	return out, key, nil
}

var _ = sql.ErrNoRows
