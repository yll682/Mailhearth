package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"mailhearth/internal/db"
	"mailhearth/internal/mailproto/mimeutil"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
	"mailhearth/internal/secrets"
)

func secretsRandomPassword() string { return secrets.RandomPassword() }

func sanitizeSignature(html string) string {
	html = strings.TrimSpace(html)
	if len(html) > 20000 {
		html = html[:20000]
	}
	return mimeutil.SanitizeSignature(html)
}

const addressSelect = `SELECT a.id,a.domain_id,d.name,a.local_part,a.address,a.kind,a.mailbox_id,a.group_id,a.targets_json,a.pm_rule_id,a.is_prefix,a.is_catchall,a.note,a.created_at,a.org_id,a.connection_id,a.domain_binding_id,a.address_key,a.management_mode,a.sync_state,a.revision,a.desired_targets_json,a.observed_targets_json FROM addresses a JOIN domains d ON d.id=a.domain_id`

func scanAddress(row interface{ Scan(...any) error }) (*model.Address, error) {
	var a model.Address
	var mailbox, group, rule, binding sql.NullInt64
	var targets, desired, observed string
	if err := row.Scan(&a.ID, &a.DomainID, &a.Domain, &a.LocalPart, &a.Address, &a.Kind, &mailbox, &group, &targets, &rule, &a.IsPrefix, &a.IsCatchall, &a.Note, &a.CreatedAt, &a.OrgID, &a.ConnectionID, &binding, &a.AddressKey, &a.ManagementMode, &a.SyncState, &a.Revision, &desired, &observed); err != nil {
		return nil, err
	}
	a.MailboxID, a.GroupID, a.PMRuleID, a.DomainBindingID = nullInt(mailbox), nullInt(group), nullInt(rule), nullInt(binding)
	if err := json.Unmarshal([]byte(targets), &a.Targets); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(desired), &a.DesiredTargets); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(observed), &a.ObservedTargets); err != nil {
		return nil, err
	}
	if a.Targets == nil {
		a.Targets = []string{}
	}
	return &a, nil
}

func (s *Service) Addresses(ctx context.Context, orgID int64) ([]model.Address, error) {
	rows, err := s.DB.QueryContext(ctx, addressSelect+` WHERE a.org_id=? ORDER BY a.connection_id,d.name,a.is_catchall,a.local_part`, orgID)
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

func (s *Service) Address(ctx context.Context, orgID, id int64) (*model.Address, error) {
	a, err := scanAddress(s.DB.QueryRowContext(ctx, addressSelect+` WHERE a.org_id=? AND a.id=?`, orgID, id))
	if db.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return a, err
}

type AddressInput struct {
	RequestID        string   `json:"requestId"`
	ConnectionID     int64    `json:"connectionId"`
	DomainBindingID  int64    `json:"domainBindingId"`
	ExpectedRevision int64    `json:"expectedRevision"`
	Mode             string   `json:"mode"`
	DomainID         int64    `json:"domainId"`
	LocalPart        string   `json:"localPart"`
	Kind             string   `json:"kind"`
	MailboxID        int64    `json:"mailboxId"`
	Targets          []string `json:"targets"`
	Note             string   `json:"note"`
}

func operationResultID(op *OperationView, key string) (int64, error) {
	var result map[string]json.RawMessage
	if err := json.Unmarshal(op.Result, &result); err != nil {
		return 0, err
	}
	var id int64
	if len(result[key]) == 0 {
		return 0, provider.Errorf("invalid_operation_result", "操作结果缺少对象标识")
	}
	if err := json.Unmarshal(result[key], &id); err != nil {
		return 0, err
	}
	if id < 1 {
		return 0, provider.Errorf("invalid_operation_result", "操作结果中的对象标识无效")
	}
	return id, nil
}

func (s *Service) CreateAddress(ctx context.Context, orgID, actor int64, in AddressInput) (*model.Address, error) {
	op, err := s.runLocalManagementOperation(ctx, orgID, actor, in.RequestID, OperationPayload{Kind: "address.create", Routing: &RoutingOperationInput{Address: &in}})
	if err != nil {
		return nil, err
	}
	id, err := operationResultID(op, "addressId")
	if err != nil {
		return nil, err
	}
	return s.Address(ctx, orgID, id)
}

func (s *Service) UpdateAddress(ctx context.Context, orgID, actor, id int64, in AddressInput) (*model.Address, error) {
	_, err := s.runLocalManagementOperation(ctx, orgID, actor, in.RequestID, OperationPayload{Kind: "address.update", Routing: &RoutingOperationInput{AddressID: id, Address: &in}})
	if err != nil {
		return nil, err
	}
	return s.Address(ctx, orgID, id)
}

func (s *Service) DeleteAddress(ctx context.Context, orgID, actor, id int64, expectedRevision ...int64) error {
	if len(expectedRevision) != 1 || expectedRevision[0] < 1 {
		return provider.Errorf("invalid", "需要 expectedRevision")
	}
	_, err := s.runLocalManagementOperation(ctx, orgID, actor, uuid.NewString(), OperationPayload{Kind: "address.delete", Routing: &RoutingOperationInput{AddressID: id, ExpectedRevision: expectedRevision[0]}})
	return err
}

func (s *Service) SetMailboxForwarding(ctx context.Context, orgID, actor, mailboxID int64, in ForwardingInput) (*ForwardingView, error) {
	kind := "mailbox.forwarding.set"
	if len(in.Targets) == 0 {
		kind = "mailbox.forwarding.delete"
	}
	_, err := s.runLocalManagementOperation(ctx, orgID, actor, in.RequestID, OperationPayload{Kind: kind, MailboxID: mailboxID, Forwarding: &in})
	if err != nil {
		return nil, err
	}
	return s.MailboxForwarding(ctx, orgID, mailboxID)
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

func (s *Service) groupAddress(ctx context.Context, orgID, groupID int64) (*model.Address, error) {
	a, err := scanAddress(s.DB.QueryRowContext(ctx, addressSelect+` WHERE a.org_id=? AND a.group_id=?`, orgID, groupID))
	if db.IsNotFound(err) {
		return nil, nil
	}
	return a, err
}

func (s *Service) loadGroup(ctx context.Context, orgID int64, row interface{ Scan(...any) error }) (*model.Group, error) {
	var g model.Group
	if err := row.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt, &g.Revision); err != nil {
		return nil, err
	}
	g.MemberIDs = []int64{}
	rows, err := s.DB.QueryContext(ctx, `SELECT member_id FROM group_members WHERE group_id=? ORDER BY member_id`, g.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		g.MemberIDs = append(g.MemberIDs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	g.Address, err = s.groupAddress(ctx, orgID, g.ID)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (s *Service) Groups(ctx context.Context, orgID int64) ([]model.Group, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM groups WHERE org_id=? ORDER BY name,id`, orgID)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []model.Group{}
	for _, id := range ids {
		g, err := s.Group(ctx, orgID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, nil
}

func (s *Service) Group(ctx context.Context, orgID, id int64) (*model.Group, error) {
	g, err := s.loadGroup(ctx, orgID, s.DB.QueryRowContext(ctx, `SELECT id,name,description,created_at,revision FROM groups WHERE org_id=? AND id=?`, orgID, id))
	if db.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return g, err
}

type GroupInput struct {
	RequestID        string  `json:"requestId"`
	ExpectedRevision int64   `json:"expectedRevision"`
	ConnectionID     int64   `json:"connectionId"`
	DomainBindingID  int64   `json:"domainBindingId"`
	Mode             string  `json:"mode"`
	Name             string  `json:"name"`
	Description      string  `json:"description"`
	MemberIDs        []int64 `json:"memberIds"`
	AddressDomainID  int64   `json:"addressDomainId"`
	AddressLocal     string  `json:"addressLocal"`
	RemoveAddress    bool    `json:"removeAddress"`
}

func (s *Service) CreateGroup(ctx context.Context, orgID, actor int64, in GroupInput) (*model.Group, error) {
	op, err := s.runLocalManagementOperation(ctx, orgID, actor, in.RequestID, OperationPayload{Kind: "group.create", Routing: &RoutingOperationInput{Group: &in}})
	if err != nil {
		return nil, err
	}
	id, err := operationResultID(op, "groupId")
	if err != nil {
		return nil, err
	}
	return s.Group(ctx, orgID, id)
}

func (s *Service) UpdateGroup(ctx context.Context, orgID, actor, id int64, in GroupInput) (*model.Group, error) {
	_, err := s.runLocalManagementOperation(ctx, orgID, actor, in.RequestID, OperationPayload{Kind: "group.update", Routing: &RoutingOperationInput{GroupID: id, Group: &in}})
	if err != nil {
		return nil, err
	}
	return s.Group(ctx, orgID, id)
}

func (s *Service) AddGroupMember(ctx context.Context, orgID, actor, groupID, memberID int64) error {
	g, err := s.Group(ctx, orgID, groupID)
	if err != nil {
		return err
	}
	members := append([]int64{}, g.MemberIDs...)
	exists := false
	for _, id := range members {
		if id == memberID {
			exists = true
		}
	}
	if !exists {
		members = append(members, memberID)
	}
	_, err = s.UpdateGroup(ctx, orgID, actor, groupID, GroupInput{RequestID: uuid.NewString(), ExpectedRevision: g.Revision, Name: g.Name, Description: g.Description, MemberIDs: members})
	return err
}

func (s *Service) RemoveGroupMember(ctx context.Context, orgID, actor, groupID, memberID int64) error {
	g, err := s.Group(ctx, orgID, groupID)
	if err != nil {
		return err
	}
	members := []int64{}
	for _, id := range g.MemberIDs {
		if id != memberID {
			members = append(members, id)
		}
	}
	_, err = s.UpdateGroup(ctx, orgID, actor, groupID, GroupInput{RequestID: uuid.NewString(), ExpectedRevision: g.Revision, Name: g.Name, Description: g.Description, MemberIDs: members})
	return err
}

func (s *Service) SyncGroupAddress(ctx context.Context, orgID, actor, groupID int64) error {
	g, err := s.Group(ctx, orgID, groupID)
	if err != nil {
		return err
	}
	if g.Address == nil {
		return nil
	}
	_, err = s.UpdateGroup(ctx, orgID, actor, groupID, GroupInput{RequestID: uuid.NewString(), ExpectedRevision: g.Revision, Name: g.Name, Description: g.Description, MemberIDs: g.MemberIDs})
	return err
}

func (s *Service) DeleteGroup(ctx context.Context, orgID, actor, id int64, expectedRevision ...int64) error {
	if len(expectedRevision) != 1 || expectedRevision[0] < 1 {
		return provider.Errorf("invalid", "需要 expectedRevision")
	}
	_, err := s.runLocalManagementOperation(ctx, orgID, actor, uuid.NewString(), OperationPayload{Kind: "group.delete", Routing: &RoutingOperationInput{GroupID: id, ExpectedRevision: expectedRevision[0]}})
	return err
}
