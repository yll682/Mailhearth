package core

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"mailhearth/internal/db"
	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/mailproto/mailops"
	"mailhearth/internal/mailproto/sieve"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

type OperationPayload struct {
	Kind             string                     `json:"kind"`
	ConnectionID     int64                      `json:"connectionId"`
	Attach           *AttachMailboxInput        `json:"attach,omitempty"`
	Import           *ImportSelection           `json:"import,omitempty"`
	MailboxID        int64                      `json:"mailboxId,omitempty"`
	ExpectedRevision int64                      `json:"expectedRevision,omitempty"`
	Endpoints        *UpdateEndpointsInput      `json:"endpoints,omitempty"`
	Connection       *UpdateConnectionInput     `json:"connection,omitempty"`
	Domain           *DomainBindingInput        `json:"domain,omitempty"`
	Create           *ManagedMailboxCreateInput `json:"create,omitempty"`
	RemoteMailbox    *RemoteMailboxInput        `json:"remoteMailbox,omitempty"`
	Rules            *RulesOperationInput       `json:"rules,omitempty"`
	SessionHash      string                     `json:"sessionHash,omitempty"`
	DomainBindingID  int64                      `json:"domainBindingId,omitempty"`
	Offboard         *OffboardOperationInput    `json:"offboard,omitempty"`
	Routing          *RoutingOperationInput     `json:"routing,omitempty"`
	Forwarding       *ForwardingInput           `json:"forwarding,omitempty"`
	MemberCreate     *CreateMemberRequest       `json:"memberCreate,omitempty"`
	MailboxEdit      *UpdateMailboxInput        `json:"mailboxEdit,omitempty"`
	MemberID         int64                      `json:"memberId,omitempty"`
	MemberStatus     *MemberStatusInput         `json:"memberStatus,omitempty"`
	Management       *mailboxManagementPlan     `json:"management,omitempty"`
}

type OperationView struct {
	ActorID            int64                 `json:"-"`
	RequiredPermission string                `json:"-"`
	ID                 string                `json:"operationId"`
	Kind               string                `json:"kind"`
	Status             string                `json:"status"`
	Result             json.RawMessage       `json:"result"`
	ErrorCode          *string               `json:"errorCode"`
	Steps              []model.OperationStep `json:"steps"`
	CreatedAt          string                `json:"createdAt"`
	UpdatedAt          string                `json:"updatedAt"`
}

func (s *Service) Operation(ctx context.Context, orgID int64, id string) (*OperationView, error) {
	var o OperationView
	var result string
	var code sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id,kind,status,result_json,error_code,created_at,updated_at,required_permission,actor_member_id FROM operations WHERE org_id=? AND id=?`, orgID, id).Scan(&o.ID, &o.Kind, &o.Status, &result, &code, &o.CreatedAt, &o.UpdatedAt, &o.RequiredPermission, &o.ActorID)
	if db.IsNotFound(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	o.Result = json.RawMessage(result)
	o.ErrorCode = nullStr(code)
	o.Steps = []model.OperationStep{}
	rows, err := s.DB.QueryContext(ctx, `SELECT operation_id,step_key,sequence,status,remote_ref_json,result_json,error_code,started_at,finished_at FROM operation_steps WHERE operation_id=? ORDER BY sequence`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var step model.OperationStep
		var code, start, end sql.NullString
		if err := rows.Scan(&step.OperationID, &step.StepKey, &step.Sequence, &step.Status, &step.RemoteRefJSON, &step.ResultJSON, &code, &start, &end); err != nil {
			return nil, err
		}
		step.ErrorCode = nullStr(code)
		step.StartedAt = nullStr(start)
		step.FinishedAt = nullStr(end)
		o.Steps = append(o.Steps, step)
	}
	return &o, rows.Err()
}

func (s *Service) QueueOperation(ctx context.Context, orgID, actor int64, requestID string, payload OperationPayload, keys []string) (*OperationView, bool, error) {
	if id, err := uuid.Parse(requestID); err != nil || id.String() != requestID {
		return nil, false, provider.Errorf("invalid", "requestId 必须为 UUID")
	}
	if payload.Domain != nil {
		payload.DomainBindingID = payload.Domain.BindingID
	}
	rawPayload := payload
	rawPayload.SessionHash = ""
	raw, err := json.Marshal(rawPayload)
	if err != nil {
		return nil, false, err
	}
	rawDigest := s.Box.RequestDigest(raw)
	sessionHash := payload.SessionHash
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, false, err
	}
	payload.SessionHash = sessionHash
	var priorID string
	var priorRaw sql.NullString
	err = s.DB.QueryRowContext(ctx, `SELECT id,raw_request_digest FROM operations WHERE org_id=? AND actor_member_id=? AND request_id=?`, orgID, actor, requestID).Scan(&priorID, &priorRaw)
	if err == nil && priorRaw.Valid {
		if subtle.ConstantTimeCompare([]byte(rawDigest), []byte(priorRaw.String)) != 1 {
			return nil, false, provider.Errorf("idempotency_conflict", "requestId 已用于其他内容")
		}
		op, err := s.OperationForMember(ctx, orgID, actor, priorID)
		if err != nil {
			return nil, false, err
		}
		if err := s.DB.Tx(ctx, func(tx *sql.Tx) error { return checkOperationControllerPermissionTx(ctx, tx, priorID, actor) }); err != nil {
			return nil, false, err
		}
		return op, true, nil
	}
	if err != nil && !db.IsNotFound(err) {
		return nil, false, err
	}
	switch payload.Kind {
	case "connection.discover", "connection.import", "connection.configure", "connection.sync", "mailbox.attach", "mailbox.endpoints", "mailbox.update", "mailbox.suspend", "mailbox.archive", "mailbox.reactivate", "mailbox.create", "mailbox.connect", "mailbox.rotate", "mailbox.resetPassword", "mailbox.deleteRemote", "mailbox.revokeRemoteAccess", "mailbox.rules", "mailbox.forwarding.set", "mailbox.forwarding.delete", "member.create", "member.status", "member.offboard", "address.create", "address.update", "address.delete", "group.create", "group.update", "group.delete", "domain.register", "domain.create", "domain.configure", "domain.recheck", "domain.activate", "domain.delete":
	default:
		return nil, false, provider.Errorf("unsupported_operation", "操作类型无效")
	}
	member, err := s.Member(ctx, orgID, actor)
	if err != nil {
		return nil, false, err
	}
	if member.Status != model.MemberActive {
		return nil, false, ErrForbidden
	}
	required := model.PermOrgManage
	var routingPrepared *routingPlan
	var forwardingPrepared *forwardingPlan
	var memberPrepared *memberCreationPlan
	var mutationPrepared *mailboxMutationPlan
	var statusPrepared *memberStatusPlan
	var additionPrepared *mailboxAdditionPlan
	if payload.Kind == "member.status" {
		statusPrepared, err = s.prepareMemberStatus(ctx, orgID, actor, &payload)
		if err != nil {
			return nil, false, err
		}
		required = model.PermMembersManage
		keys = append(keys, memberStatusKeys(statusPrepared)...)
	}
	if mailboxMutationOperation(payload.Kind) {
		mutationPrepared, required, err = s.prepareMailboxMutation(ctx, orgID, actor, &payload)
		if err != nil {
			return nil, false, err
		}
		keys = append(keys, mailboxMutationKeys(mutationPrepared)...)
	}
	if payload.Kind == "member.create" {
		memberPrepared, err = s.prepareMemberCreation(ctx, orgID, actor, payload.MemberCreate)
		if err != nil {
			return nil, false, err
		}
		required = model.PermMembersManage
		keys = append(keys, memberCreationKeys(memberPrepared)...)
	}
	if strings.HasPrefix(payload.Kind, "mailbox.forwarding.") {
		forwardingPrepared, required, err = s.prepareForwarding(ctx, orgID, actor, &payload)
		if err != nil {
			return nil, false, err
		}
	}
	if strings.HasPrefix(payload.Kind, "address.") || strings.HasPrefix(payload.Kind, "group.") {
		var err error
		routingPrepared, err = s.prepareRouting(ctx, orgID, payload.Kind, payload.Routing)
		if err != nil {
			return nil, false, err
		}
		keys = append(keys, routingKeys(routingPrepared)...)
		required = model.PermAddressesManage
		if strings.HasPrefix(payload.Kind, "group.") {
			required = model.PermGroupsManage
		}
	}
	if payload.Kind == "member.offboard" {
		prepared, err := s.prepareOffboard(ctx, orgID, actor, payload.Offboard)
		if err != nil {
			return nil, false, err
		}
		payload.Offboard = prepared
		keys = append(keys, offboardOperationKeys(prepared)...)
		required = model.PermMembersManage
	}
	if payload.Kind == "mailbox.rules" {
		if err := validateRulesInput(payload.Rules); err != nil {
			return nil, false, err
		}
		mb, err := s.Mailbox(ctx, orgID, payload.MailboxID)
		if err != nil {
			return nil, false, err
		}
		if mb.Revision != payload.Rules.ExpectedRevision {
			return nil, false, provider.Errorf("revision_conflict", "邮箱配置已更新")
		}
		payload.ConnectionID = mb.ConnectionID
		required = permissionMailFull
		if err := s.requireOperationPermission(ctx, orgID, actor, mb.ID, required); err != nil {
			return nil, false, err
		}
		if err := s.preflightRules(ctx, orgID, mb.ID, payload.Rules); err != nil {
			return nil, false, err
		}
	}
	if payload.Create != nil || payload.RemoteMailbox != nil {
		var err error
		required, err = s.prepareMailboxManagement(ctx, orgID, &payload)
		if err != nil {
			return nil, false, err
		}
		if err := s.prepareMailboxManagementVersions(ctx, orgID, &payload); err != nil {
			return nil, false, err
		}
		if payload.Create != nil {
			binding, err := s.DomainBinding(ctx, orgID, payload.Create.DomainBindingID)
			if err != nil {
				return nil, false, err
			}
			keys = append(keys, "mailbox-address:"+fmtID(payload.ConnectionID)+":"+payload.Create.LocalPart+"@"+binding.DomainName, "domain-binding:"+fmtID(binding.ID))
		}
	}
	if strings.HasPrefix(payload.Kind, "domain.") {
		if payload.Domain == nil {
			return nil, false, provider.Errorf("invalid", "需要域名请求")
		}
		in := *payload.Domain
		in.RequestID = ""
		payload.Domain = &in
		if payload.Kind == "domain.configure" && in.ProviderSettings == nil {
			return nil, false, provider.Errorf("invalid", "需要 providerSettings")
		}
		if err := s.prepareDomainOperation(ctx, orgID, payload.Kind, payload.Domain); err != nil {
			return nil, false, err
		}
		payload.ConnectionID = payload.Domain.ConnectionID
		required = model.PermDomainsManage
		keys = append(keys, "domain-name:"+fmtID(payload.ConnectionID)+":"+payload.Domain.DomainName)
		if in.BindingID > 0 {
			keys = append(keys, "domain-binding:"+fmtID(in.BindingID))
		}
	}
	if payload.Kind == "mailbox.attach" {
		if payload.Attach == nil {
			return nil, false, provider.Errorf("invalid", "缺少邮箱配置")
		}
		attach := *payload.Attach
		attach.RequestID = ""
		payload.Attach = &attach
		required = model.PermMailboxesManage
		if attach.Kind == model.MailboxShared {
			required = model.PermSharedManage
		}
	}
	if mailboxAdditionOperation(payload.Kind) {
		additionPrepared, err = s.prepareMailboxAddition(ctx, orgID, actor, &payload)
		if err != nil {
			return nil, false, err
		}
		keys = append(keys, mailboxAdditionKeys(additionPrepared)...)
	}
	if payload.Kind == "mailbox.endpoints" {
		if payload.Endpoints == nil {
			return nil, false, provider.Errorf("invalid", "缺少协议配置")
		}
		endpoints := *payload.Endpoints
		endpoints.RequestID = ""
		payload.Endpoints = &endpoints
		mb, err := s.Mailbox(ctx, orgID, payload.MailboxID)
		if err != nil {
			return nil, false, err
		}
		payload.ConnectionID = mb.ConnectionID
		required = model.PermMailboxesManage
		if mb.Kind == model.MailboxShared {
			required = model.PermSharedManage
		}
	}
	if payload.Kind == "mailbox.reactivate" {
		mb, err := s.Mailbox(ctx, orgID, payload.MailboxID)
		if err != nil {
			return nil, false, err
		}
		payload.ConnectionID = mb.ConnectionID
		required = model.PermMailboxesManage
		if mb.Kind == model.MailboxShared {
			required = model.PermSharedManage
		}
		if payload.ExpectedRevision < 1 {
			return nil, false, provider.Errorf("invalid", "需要 expectedRevision")
		}
	}
	if payload.Connection != nil {
		connection := *payload.Connection
		connection.RequestID = ""
		payload.Connection = &connection
	}
	if payload.Import != nil {
		selection := *payload.Import
		selection.SelectedResources = append([]SelectedResource{}, selection.SelectedResources...)
		sort.Slice(selection.SelectedResources, func(i, j int) bool {
			a, b := selection.SelectedResources[i], selection.SelectedResources[j]
			if a.ResourceType != b.ResourceType {
				return a.ResourceType < b.ResourceType
			}
			return a.RemoteKey < b.RemoteKey
		})
		payload.Import = &selection
	}
	if err := s.requireOperationPermission(ctx, orgID, actor, payload.MailboxID, required); err != nil {
		return nil, false, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	digest := s.Box.RequestDigest(body)
	sealed, err := s.Box.Seal(string(body))
	if err != nil {
		return nil, false, err
	}
	additionalPermissions := []string{}
	if payload.MemberCreate != nil {
		if payload.MemberCreate.MailboxAction != "none" {
			additionalPermissions = append(additionalPermissions, model.PermMailboxesManage)
		}
		if len(payload.MemberCreate.GroupIDs) > 0 {
			additionalPermissions = append(additionalPermissions, model.PermGroupsManage)
		}
		if len(payload.MemberCreate.SharedMailboxes) > 0 {
			additionalPermissions = append(additionalPermissions, model.PermSharedManage)
		}
	}
	if payload.Forwarding != nil {
		additionalPermissions = append(additionalPermissions, model.PermAddressesManage)
	}
	sort.Strings(additionalPermissions)
	if mutationPrepared != nil {
		if len(mutationPrepared.Groups) > 0 {
			additionalPermissions = append(additionalPermissions, model.PermGroupsManage)
		}
		permission := model.PermMailboxesManage
		if mutationPrepared.Kind == model.MailboxShared {
			permission = model.PermSharedManage
		}
		additionalPermissions = append(additionalPermissions, permission)
		sort.Strings(additionalPermissions)
	}
	if statusPrepared != nil && len(statusPrepared.Groups) > 0 {
		additionalPermissions = append(additionalPermissions, model.PermGroupsManage)
		sort.Strings(additionalPermissions)
	}
	if additionPrepared != nil && len(additionPrepared.Groups) > 0 {
		additionalPermissions = append(additionalPermissions, model.PermGroupsManage)
	}
	addressPermission := routingPrepared != nil && strings.HasPrefix(payload.Kind, "group.") && len(routingPrepared.Rules) > 0
	if memberPrepared != nil {
		for _, group := range memberPrepared.Groups {
			addressPermission = addressPermission || len(group.Rules) > 0
		}
	}
	if mutationPrepared != nil {
		for _, group := range mutationPrepared.Groups {
			addressPermission = addressPermission || len(group.Rules) > 0
		}
	}
	if statusPrepared != nil {
		for _, group := range statusPrepared.Groups {
			addressPermission = addressPermission || len(group.Rules) > 0
		}
	}
	if additionPrepared != nil {
		for _, group := range additionPrepared.Groups {
			addressPermission = addressPermission || len(group.Rules) > 0
		}
	}
	if payload.Offboard != nil && len(payload.Offboard.Groups) > 0 {
		addressPermission = true
		additionalPermissions = append(additionalPermissions, model.PermGroupsManage)
	}
	if addressPermission {
		additionalPermissions = append(additionalPermissions, model.PermAddressesManage)
	}
	sort.Strings(additionalPermissions)
	additionalPermissions = uniqueStrings(additionalPermissions)
	for _, permission := range additionalPermissions {
		if err := s.requireOperationPermission(ctx, orgID, actor, 0, permission); err != nil {
			return nil, false, err
		}
	}
	id := uuid.NewString()
	repeated := false
	keys = append([]string{}, keys...)
	if payload.ConnectionID > 0 {
		keys = append(keys, "connection:"+fmtID(payload.ConnectionID))
	}
	if payload.MailboxID > 0 {
		keys = append(keys, "mailbox:"+fmtID(payload.MailboxID))
	}
	sort.Strings(keys)
	unique := keys[:0]
	for _, key := range keys {
		if len(unique) == 0 || unique[len(unique)-1] != key {
			unique = append(unique, key)
		}
	}
	keys = unique
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		var prior, priorDigest string
		err := tx.QueryRowContext(ctx, `SELECT id,request_digest FROM operations WHERE org_id=? AND actor_member_id=? AND request_id=?`, orgID, actor, requestID).Scan(&prior, &priorDigest)
		if err == nil {
			if subtle.ConstantTimeCompare([]byte(digest), []byte(priorDigest)) != 1 {
				return provider.Errorf("idempotency_conflict", "requestId 已用于其他内容")
			}
			id = prior
			repeated = true
			return nil
		}
		if !db.IsNotFound(err) {
			return err
		}
		for _, key := range keys {
			var existing string
			err := tx.QueryRowContext(ctx, `SELECT operation_id FROM operation_locks WHERE org_id=? AND resource_key=?`, orgID, key).Scan(&existing)
			if err == nil {
				conflict := provider.Errorf("operation_in_progress", "资源已有进行中的操作")
				conflict.OperationID = &existing
				return conflict
			}
			if !db.IsNotFound(err) {
				return err
			}
		}
		now := db.Now()
		if err := validateMailboxManagementVersions(ctx, tx, orgID, payload); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO operations(id,org_id,actor_member_id,kind,request_id,request_digest,payload_enc,status,created_at,updated_at,required_permission,target_connection_id,target_mailbox_id,raw_request_digest) VALUES (?,?,?,?,?,?,?,'queued',?,?,?,NULLIF(?,0),NULLIF(?,0),?)`, id, orgID, actor, payload.Kind, requestID, digest, sealed, now, now, required, payload.ConnectionID, payload.MailboxID, rawDigest); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE operations SET additional_permissions_json=? WHERE id=?`, toJSON(additionalPermissions), id); err != nil {
			return err
		}
		if err := checkOperationControllerPermissionTx(ctx, tx, id, actor); err != nil {
			return err
		}
		if payload.Offboard != nil {
			if err := s.commitOffboardDeparture(ctx, tx, orgID, actor, id, payload.Offboard); err != nil {
				return err
			}
		}
		if routingPrepared != nil {
			if err := s.reserveRouting(ctx, tx, orgID, id, payload.Routing, routingPrepared); err != nil {
				return err
			}
		}
		if forwardingPrepared != nil {
			if err := reserveForwarding(ctx, tx, orgID, id, forwardingPrepared); err != nil {
				return err
			}
		}
		if memberPrepared != nil {
			if err := reserveMemberCreation(ctx, tx, orgID, id, payload.MemberCreate, memberPrepared); err != nil {
				return err
			}
		}
		if mutationPrepared != nil {
			if err := reserveMailboxMutation(ctx, tx, orgID, id, payload.Kind, mutationPrepared); err != nil {
				return err
			}
		}
		if statusPrepared != nil {
			if err := reserveMemberStatus(ctx, tx, orgID, id, statusPrepared); err != nil {
				return err
			}
		}
		if additionPrepared != nil {
			if err := reserveMailboxAddition(ctx, tx, orgID, id, additionPrepared); err != nil {
				return err
			}
		}
		for _, key := range keys {
			if _, err := tx.ExecContext(ctx, `INSERT INTO operation_locks(org_id,resource_key,operation_id) VALUES (?,?,?)`, orgID, key, id); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO operation_steps(operation_id,step_key,sequence,status) VALUES (?,'execute',1,'pending')`, id)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	if payload.Offboard != nil && !repeated {
		s.cancelMailRequests(payload.Offboard.MemberID, 0)
	}
	if (payload.Kind == "mailbox.suspend" || payload.Kind == "mailbox.archive" || payload.Kind == "mailbox.deleteRemote") && !repeated {
		s.cancelMailRequests(0, payload.MailboxID)
		if s.Pool != nil {
			s.Pool.InvalidateMailbox(payload.MailboxID)
		}
	}
	if statusPrepared != nil && statusPrepared.Status == model.MemberDisabled && !repeated {
		s.cancelMailRequests(statusPrepared.MemberID, 0)
	}
	select {
	case s.operationWake <- struct{}{}:
	default:
	}
	out, err := s.Operation(ctx, orgID, id)
	return out, repeated, err
}

func operationErrorCode(err error) string {
	var typed *provider.TypedError
	if errors.As(err, &typed) {
		return typed.Code
	}
	var sieveAuth *sieve.AuthError
	if errors.As(err, &sieveAuth) {
		return sieveAuth.Code
	}
	var imapAuth *imappool.AuthError
	var smtpAuth *mailops.SendAuthError
	var validation *ValidationError
	var network *net.OpError
	if errors.As(err, &imapAuth) || errors.As(err, &smtpAuth) {
		return "mailbox_auth_failed"
	}
	if errors.As(err, &validation) {
		return "invalid"
	}
	if errors.Is(err, ErrForbidden) {
		return "forbidden"
	}
	if errors.Is(err, ErrNotFound) {
		return "not_found"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.As(err, &network) {
		return "upstream_failed"
	}
	return "internal"
}

func (s *Service) StartOperations(ctx context.Context) error {
	if !s.operationsStarted.CompareAndSwap(false, true) {
		return provider.Errorf("operation_in_progress", "操作执行器已经启动")
	}
	if err := s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE operation_steps SET status='unknown',error_code='process_interrupted',finished_at=? WHERE status='running'`, db.Now()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE operations SET status='unknown',error_code='process_interrupted',updated_at=? WHERE status='running'`, db.Now())
		return err
	}); err != nil {
		return err
	}
	s.operationWG.Add(4)
	for worker := 0; worker < 4; worker++ {
		go func() {
			defer s.operationWG.Done()
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				if ctx.Err() != nil {
					return
				}
				if err := s.expireOperationSecrets(ctx); err != nil {
					if ctx.Err() == nil {
						select {
						case s.operationErrors <- errors.New("主密码期限检查失败"):
						default:
						}
					}
					return
				}
				var id, sealed string
				var orgID, actor int64
				err := s.DB.QueryRowContext(ctx, `SELECT id,org_id,actor_member_id,payload_enc FROM operations WHERE status='queued' ORDER BY created_at,id LIMIT 1`).Scan(&id, &orgID, &actor, &sealed)
				if db.IsNotFound(err) {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
					case <-s.operationWake:
					}
					continue
				}
				if err != nil {
					if ctx.Err() == nil {
						select {
						case s.operationErrors <- errors.New("操作执行器读取失败"):
						default:
						}
					}
					return
				}
				if err := s.executeOperation(ctx, orgID, actor, id, sealed); err != nil {
					if ctx.Err() == nil {
						select {
						case s.operationErrors <- errors.New("操作进度保存失败"):
						default:
						}
					}
					return
				}
			}
		}()
	}
	return nil
}

func (s *Service) OperationErrors() <-chan error { return s.operationErrors }
func (s *Service) WaitOperations()               { s.operationWG.Wait() }

func (s *Service) executeOperation(ctx context.Context, orgID, actor int64, id, sealed string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE operations SET status='running',updated_at=? WHERE id=? AND status='queued'`, db.Now(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return nil
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE operation_steps SET status='running',started_at=? WHERE operation_id=? AND step_key='execute'`, db.Now(), id); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	var payload OperationPayload
	var result any
	plain, runErr := s.Box.Open(sealed)
	if runErr == nil {
		runErr = json.Unmarshal([]byte(plain), &payload)
	}
	if runErr == nil && payload.Domain != nil {
		payload.Domain.BindingID = payload.DomainBindingID
	}
	if runErr == nil {
		member, err := s.Member(ctx, orgID, actor)
		if err != nil {
			runErr = err
		} else if member.Status != model.MemberActive {
			runErr = ErrForbidden
		}
	}
	if runErr == nil {
		perms, _, err := s.Permissions(ctx, actor)
		if err != nil {
			runErr = err
		} else {
			required := model.PermOrgManage
			if strings.HasPrefix(payload.Kind, "address.") {
				required = model.PermAddressesManage
			}
			if strings.HasPrefix(payload.Kind, "group.") {
				required = model.PermGroupsManage
			}
			if payload.Kind == "member.offboard" || payload.Kind == "member.create" || payload.Kind == "member.status" {
				required = model.PermMembersManage
			}
			if payload.Forwarding != nil {
				required = model.PermMailboxesManage
				mb, err := s.Mailbox(ctx, orgID, payload.MailboxID)
				if err != nil {
					runErr = err
				} else if mb.Kind == model.MailboxShared {
					required = model.PermSharedManage
				}
			}
			if payload.Kind == "mailbox.attach" {
				required = model.PermMailboxesManage
				if payload.Attach != nil && payload.Attach.Kind == model.MailboxShared {
					required = model.PermSharedManage
				}
			}
			if payload.Kind == "mailbox.endpoints" || payload.Kind == "mailbox.reactivate" {
				required = model.PermMailboxesManage
				mb, err := s.Mailbox(ctx, orgID, payload.MailboxID)
				if err != nil {
					runErr = err
				} else if mb.Kind == model.MailboxShared {
					required = model.PermSharedManage
				}
			}
			if strings.HasPrefix(payload.Kind, "domain.") {
				required = model.PermDomainsManage
			}
			if payload.Create != nil {
				required = model.PermMailboxesManage
				if payload.Create.Kind == model.MailboxShared {
					required = model.PermSharedManage
				}
			}
			if payload.RemoteMailbox != nil {
				required = model.PermMailboxesManage
				mb, err := s.Mailbox(ctx, orgID, payload.MailboxID)
				if err != nil {
					runErr = err
				} else if mb.Kind == model.MailboxShared {
					required = model.PermSharedManage
				}
			}
			if mailboxMutationOperation(payload.Kind) {
				if err := s.DB.QueryRowContext(ctx, `SELECT required_permission FROM operations WHERE id=?`, id).Scan(&required); err != nil {
					runErr = err
				}
			}
			if payload.Kind == "mailbox.rules" {
				runErr = s.requireOperationPermission(ctx, orgID, actor, payload.MailboxID, permissionMailFull)
			} else if !HasPermission(perms, required) {
				runErr = ErrForbidden
			}
		}
	}
	if runErr == nil {
		ctx = context.WithValue(ctx, operationContextKey{}, id)
		switch payload.Kind {
		case "connection.discover":
			result, runErr = s.DiscoverConnection(ctx, orgID, payload.ConnectionID)
		case "connection.import":
			if payload.Import == nil {
				runErr = provider.Errorf("invalid", "缺少导入请求")
			} else {
				result, runErr = s.ImportConnection(ctx, orgID, payload.ConnectionID, *payload.Import)
			}
		case "connection.sync":
			result, runErr = s.SyncConnection(ctx, orgID, payload.ConnectionID)
		case "connection.configure":
			if payload.Connection == nil {
				runErr = provider.Errorf("invalid", "缺少连接配置")
			} else {
				result, runErr = s.ConfigureConnection(ctx, orgID, actor, payload.ConnectionID, *payload.Connection)
			}
		case "mailbox.attach", "mailbox.create":
			result, runErr = s.executeMailboxAddition(ctx, orgID, actor, payload)
		case "mailbox.endpoints":
			if payload.Endpoints == nil {
				runErr = provider.Errorf("invalid", "缺少协议配置")
			} else {
				result, runErr = s.UpdateEndpoints(ctx, orgID, actor, payload.MailboxID, *payload.Endpoints)
			}
		case "mailbox.update", "mailbox.suspend", "mailbox.archive", "mailbox.reactivate":
			result, runErr = s.executeMailboxMutation(ctx, orgID, actor, payload)
		case "domain.register", "domain.create", "domain.configure", "domain.recheck", "domain.activate", "domain.delete":
			result, runErr = s.executeDomainOperation(ctx, orgID, actor, payload.Kind, payload.Domain)
		case "mailbox.connect", "mailbox.rotate", "mailbox.resetPassword", "mailbox.deleteRemote":
			result, runErr = s.executeMailboxManagement(ctx, orgID, actor, payload)
		case "mailbox.rules":
			result, runErr = s.executeRulesOperation(ctx, orgID, actor, payload)
		case "member.offboard":
			result, runErr = s.executeOffboard(ctx, orgID, actor, payload.Offboard)
		case "mailbox.forwarding.set", "mailbox.forwarding.delete":
			result, runErr = s.executeForwarding(ctx, orgID, actor, payload)
		case "mailbox.revokeRemoteAccess":
			result, runErr = s.executeRemoteAccessRevocation(ctx, orgID, payload)
		case "member.create":
			result, runErr = s.executeMemberCreation(ctx, orgID, actor, payload.MemberCreate)
		case "member.status":
			result, runErr = s.executeMemberStatus(ctx, orgID, actor, payload)
		case "address.create", "address.update", "address.delete", "group.create", "group.update", "group.delete":
			result, runErr = s.executeRouting(ctx, orgID, actor, payload)
		default:
			runErr = provider.Errorf("unsupported_operation", "操作类型无效")
		}
	}
	var committed string
	if err := s.DB.QueryRowContext(context.Background(), `SELECT status FROM operations WHERE id=?`, id).Scan(&committed); err != nil {
		return err
	}
	if committed == "succeeded" {
		return nil
	}
	status := "succeeded"
	var code any
	if runErr != nil {
		status = "failed"
		code = operationErrorCode(runErr)
		result = struct{}{}
		var typed *provider.TypedError
		if errors.As(runErr, &typed) && typed.Details != nil {
			result = map[string]any{"errorDetails": typed.Details}
		}
		var unknown *remoteOutcomeUnknown
		if ctx.Err() != nil || errors.As(runErr, &unknown) {
			status = "unknown"
		}
	}
	if status == "failed" {
		var effects bool
		if err := s.DB.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND step_key!='execute' AND step_key!='sieve.prepare' AND status='succeeded')`, id).Scan(&effects); err != nil {
			return err
		}
		if effects {
			status = "needs_action"
		}
	}
	resultBody, err := json.Marshal(result)
	if err != nil {
		return err
	}
	stepStatus := status
	if stepStatus == "needs_action" {
		stepStatus = "failed"
	}
	return s.DB.Tx(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE operation_steps SET status=?,result_json=?,error_code=?,finished_at=? WHERE operation_id=? AND step_key='execute'`, stepStatus, string(resultBody), code, db.Now(), id); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE operations SET status=?,result_json=?,error_code=?,payload_enc=CASE WHEN ?='succeeded' THEN NULL ELSE payload_enc END,updated_at=? WHERE id=?`, status, string(resultBody), code, status, db.Now(), id); err != nil {
			return err
		}
		if status != "unknown" && status != "needs_action" {
			_, err := tx.Exec(`DELETE FROM operation_locks WHERE operation_id=?`, id)
			return err
		}
		return nil
	})
}

type operationContextKey struct{}

type operationStepPrefixKey struct{}

type intermediateOperationKey struct{}

func operationStepKey(ctx context.Context, key string) string {
	prefix, _ := ctx.Value(operationStepPrefixKey{}).(string)
	return prefix + key
}

func completeLocalOperation(ctx context.Context, tx *sql.Tx, result any) error {
	id, ok := ctx.Value(operationContextKey{}).(string)
	if !ok {
		return nil
	}
	if err := checkOperationPermissionTx(ctx, tx, id); err != nil {
		return err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if intermediate, _ := ctx.Value(intermediateOperationKey{}).(bool); intermediate {
		var values map[string]json.RawMessage
		if err := json.Unmarshal(body, &values); err != nil {
			return err
		}
		if value, ok := values["mailboxId"]; ok {
			var mailboxID, revision, orgID int64
			if err := json.Unmarshal(value, &mailboxID); err != nil {
				return err
			}
			if err := tx.QueryRowContext(ctx, `SELECT revision FROM mailboxes WHERE id=?`, mailboxID).Scan(&revision); err != nil {
				return err
			}
			values["mailboxRevision"], err = json.Marshal(revision)
			if err != nil {
				return err
			}
			body, err = json.Marshal(values)
			if err != nil {
				return err
			}
			var status string
			if err := tx.QueryRowContext(ctx, `SELECT org_id,status FROM operations WHERE id=?`, id).Scan(&orgID, &status); err != nil {
				return err
			}
			if status == "running" {
				key := "mailbox:" + fmtID(mailboxID)
				if err := requireResourceAvailable(ctx, tx, orgID, key); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO operation_locks(org_id,resource_key,operation_id) VALUES (?,?,?) ON CONFLICT(org_id,resource_key) DO NOTHING`, orgID, key, id); err != nil {
					return err
				}
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO operation_steps(operation_id,step_key,sequence,status,result_json,finished_at) SELECT ?,?,COALESCE(MAX(sequence),0)+1,'succeeded',?,? FROM operation_steps WHERE operation_id=? ON CONFLICT(operation_id,step_key) DO NOTHING`, id, operationStepKey(ctx, "local.commit"), string(body), db.Now(), id)
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE operation_steps SET status='succeeded',result_json=?,error_code=NULL,finished_at=? WHERE operation_id=? AND status='running'`, string(body), db.Now(), id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE operations SET status='succeeded',result_json=?,error_code=NULL,payload_enc=NULL,updated_at=? WHERE id=? AND status='running'`, string(body), db.Now(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return provider.Errorf("operation_in_progress", "操作状态已经变化")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM operation_private WHERE operation_id=?`, id); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM operation_locks WHERE operation_id=?`, id)
	return err
}

func checkOperationPermissionTx(ctx context.Context, tx *sql.Tx, id string) error {
	var memberStatus, permissions, required, additional string
	if err := tx.QueryRowContext(ctx, `SELECT m.status,r.permissions_json,o.required_permission,o.additional_permissions_json FROM operations o JOIN members m ON m.id=o.actor_member_id AND m.org_id=o.org_id JOIN roles r ON r.id=m.role_id WHERE o.id=?`, id).Scan(&memberStatus, &permissions, &required, &additional); err != nil {
		return err
	}
	var currentPermissions []string
	if err := json.Unmarshal([]byte(permissions), &currentPermissions); err != nil {
		return err
	}
	if memberStatus != model.MemberActive {
		return ErrForbidden
	}
	var extra []string
	if err := json.Unmarshal([]byte(additional), &extra); err != nil {
		return err
	}
	for _, permission := range extra {
		if !HasPermission(currentPermissions, permission) {
			return ErrForbidden
		}
	}
	if required == permissionMailFull {
		var orgID, actor, mailboxID int64
		if err := tx.QueryRowContext(ctx, `SELECT org_id,actor_member_id,target_mailbox_id FROM operations WHERE id=?`, id).Scan(&orgID, &actor, &mailboxID); err != nil {
			return err
		}
		if err := requireFullAccessTx(ctx, tx, orgID, actor, mailboxID); err != nil {
			return err
		}
	} else if !HasPermission(currentPermissions, required) {
		return ErrForbidden
	}
	return nil
}

func checkOperationControllerPermissionTx(ctx context.Context, tx *sql.Tx, id string, actor int64) error {
	var status, permissionsJSON, required, additional string
	var orgID int64
	var mailboxID sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT m.status,r.permissions_json,o.required_permission,o.additional_permissions_json,o.org_id,o.target_mailbox_id FROM operations o JOIN members m ON m.id=? AND m.org_id=o.org_id JOIN roles r ON r.id=m.role_id AND r.org_id=m.org_id WHERE o.id=?`, actor, id).Scan(&status, &permissionsJSON, &required, &additional, &orgID, &mailboxID)
	if db.IsNotFound(err) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	if status != model.MemberActive {
		return ErrForbidden
	}
	var permissions, extra []string
	if err := json.Unmarshal([]byte(permissionsJSON), &permissions); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(additional), &extra); err != nil {
		return err
	}
	for _, permission := range extra {
		if !HasPermission(permissions, permission) {
			return ErrForbidden
		}
	}
	if required == permissionMailFull {
		if !mailboxID.Valid {
			return ErrForbidden
		}
		return requireFullAccessTx(ctx, tx, orgID, actor, mailboxID.Int64)
	}
	if !HasPermission(permissions, required) {
		return ErrForbidden
	}
	return nil
}
