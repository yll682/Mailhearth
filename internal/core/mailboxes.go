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
	"mailhearth/internal/provider"
)

const mailboxSelect = `SELECT b.id, b.org_id, b.connection_id, COALESCE(mc.label, ''), b.kind, b.address, b.address_key, b.domain_id, b.domain_binding_id, b.display_name, b.owner_member_id, COALESCE(m.display_name, ''), EXISTS(SELECT 1 FROM credentials c WHERE c.mailbox_id=b.id AND c.purpose='mail' AND c.state='active'), (SELECT MAX(c.updated_at) FROM credentials c WHERE c.mailbox_id=b.id AND c.purpose='mail' AND c.state='active'), b.status, b.imported, b.management_mode, b.remote_state, b.revision, b.access_revision, b.sent_copy_mode, b.folder_mapping_json, b.settings_json, b.created_at, b.updated_at,
	(SELECT COUNT(1) FROM mailbox_access a WHERE a.mailbox_id = b.id),
	COALESCE((SELECT json_group_object(ep.protocol,json_object('readiness',CASE
		WHEN mc.enabled=0 OR b.status!='active' OR ep.network_mode='disabled' THEN 'disabled'
		WHEN ep.username IS NULL OR ep.username='' OR cr.id IS NULL OR cr.state!='active' OR cr.secret_enc='' OR
			COALESCE(json_extract(CASE WHEN ep.network_mode='inherit' THEN mc.protocol_defaults_json ELSE ep.network_override_json END,CASE WHEN ep.network_mode='inherit' THEN '$.'||ep.protocol||'.enabled' ELSE '$.enabled' END),0)=0 THEN 'unconfigured'
		WHEN ep.check_status='passed' AND json_extract(ep.checked_versions_json,'$.connectionRevision')=mc.revision
			AND json_extract(ep.checked_versions_json,'$.endpointRevision')=ep.revision
			AND json_extract(ep.checked_versions_json,'$.credentialId')=cr.id
			AND json_extract(ep.checked_versions_json,'$.credentialGeneration')=cr.generation THEN 'ready'
		ELSE 'unverified' END,'checkStatus',ep.check_status))
		FROM mailbox_endpoints ep LEFT JOIN credentials cr ON cr.id=ep.credential_id AND cr.mailbox_id=b.id AND cr.connection_id=b.connection_id AND cr.purpose='mail'
		WHERE ep.mailbox_id=b.id),'{}')
	FROM mailboxes b LEFT JOIN members m ON m.id = b.owner_member_id LEFT JOIN mail_connections mc ON mc.id = b.connection_id`

func scanMailbox(row interface{ Scan(...any) error }) (*model.Mailbox, error) {
	var b model.Mailbox
	var domainID, owner, domainBindingID sql.NullInt64
	var credAt sql.NullString
	var imported, managementMode, remoteState, sentCopyMode, folderMapping, settings,protocols string
	if err := row.Scan(&b.ID, &b.OrgID, &b.ConnectionID, &b.ConnectionLabel, &b.Kind, &b.Address, &b.AddressKey,
		&domainID, &domainBindingID, &b.DisplayName, &owner, &b.OwnerName, &b.HasCredential, &credAt,
		&b.Status, &imported, &managementMode, &remoteState, &b.Revision, &b.AccessRevision, &sentCopyMode, &folderMapping,
		&settings, &b.CreatedAt, &b.UpdatedAt, &b.AccessCount,&protocols); err != nil {
		return nil, err
	}
	b.DomainID = nullInt(domainID)
	b.DomainBindingID = nullInt(domainBindingID)
	b.OwnerMemberID = nullInt(owner)
	b.CredentialAt = nullStr(credAt)
	b.Imported = imported == "1"
	b.ManagementMode = managementMode
	b.RemoteState = remoteState
	b.SentCopyMode = sentCopyMode
	if err:=json.Unmarshal([]byte(protocols),&b.Protocols);err!=nil{return nil,err}
	if err := json.Unmarshal([]byte(folderMapping), &b.FolderMapping); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(settings), &b.Settings); err != nil {
		return nil, err
	}
	if b.Settings.SieveRules == nil {
		b.Settings.SieveRules = []model.SieveRule{}
	}
	return &b, nil
}
// Mailboxes lists all mailboxes.
func (s *Service) Mailboxes(ctx context.Context, orgID int64) ([]model.Mailbox, error) {
	rows, err := s.DB.QueryContext(ctx, mailboxSelect+` WHERE b.org_id = ? ORDER BY b.kind, b.address`, orgID)
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

func (s *Service) mailboxByAddress(ctx context.Context, q db.Querier, orgID, connectionID int64, addr string) (*model.Mailbox, error) {
	c,err:=s.MailConnection(ctx,orgID,connectionID);if err!=nil{return nil,err};_,key,err:=canonicalMailboxAddress(c.ProviderKind,addr);if err!=nil{return nil,err}
	b, err := scanMailbox(q.QueryRowContext(ctx, mailboxSelect+` WHERE b.org_id = ? AND b.connection_id=? AND b.address_key = ?`, orgID,connectionID,key))
	if db.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return b, err
}

// CreateMailboxRequest 使用明确的连接与域名关联创建邮箱。
type CreateMailboxRequest struct {
	RequestID string `json:"requestId"`
	ConnectionID int64 `json:"connectionId"`
	DomainBindingID int64 `json:"domainBindingId"`
	CredentialMode string `json:"credentialMode"`
	SentCopyMode string `json:"sentCopyMode"`
	Credentials []EnteredCredential `json:"credentials,omitempty"`
	Endpoints EndpointInputs `json:"endpoints"`
	FolderMapping model.FolderMapping `json:"folderMapping"`
	Kind          string `json:"kind"` // personal | shared
	DomainID      int64  `json:"domainId"`
	LocalPart     string `json:"localPart"`
	DisplayName   string `json:"displayName"`
	OwnerMemberID int64  `json:"ownerMemberId"` // personal only
}

// CreateMailbox 通过 Operation 创建并验证所属服务商的邮箱。
func (s *Service) CreateMailbox(ctx context.Context, orgID, actor int64, req CreateMailboxRequest) (*model.Mailbox, error) {
	if req.ConnectionID<1 || req.DomainBindingID<1{return nil,provider.Errorf("invalid","创建邮箱需要 connectionId 和 domainBindingId")}
	if req.DomainID>0{binding,err:=s.DomainBinding(ctx,orgID,req.DomainBindingID);if err!=nil{return nil,err};if binding.DomainID!=req.DomainID || binding.ConnectionID!=req.ConnectionID{return nil,ErrNotFound}}
	var owner *int64;if req.OwnerMemberID!=0{owner=&req.OwnerMemberID}
	in:=ManagedMailboxCreateInput{Mode:"create",ConnectionID:req.ConnectionID,DomainBindingID:req.DomainBindingID,Kind:req.Kind,LocalPart:req.LocalPart,DisplayName:req.DisplayName,OwnerMemberID:owner,CredentialMode:req.CredentialMode,SentCopyMode:req.SentCopyMode,Credentials:req.Credentials,Endpoints:req.Endpoints,FolderMapping:req.FolderMapping}
	op,err:=s.runLocalManagementOperation(ctx,orgID,actor,req.RequestID,OperationPayload{Kind:"mailbox.create",Create:&in});if err!=nil{return nil,err}
	var result struct{MailboxID int64 `json:"mailboxId"`};if err:=json.Unmarshal(op.Result,&result);err!=nil{return nil,err};if result.MailboxID<1{return nil,provider.Errorf("internal","邮箱创建结果缺少 mailboxId")};return s.Mailbox(ctx,orgID,result.MailboxID)
}

// SplitLocal returns the local part of an address.
func SplitLocal(addr string) string {
	l, _ := SplitAddress(addr)
	return l
}

// EnsureCredential 通过 Operation 验证并接入 managed 凭据。
func (s *Service) EnsureCredential(ctx context.Context, orgID, actor, mailboxID int64) error {
	mb, err := s.Mailbox(ctx, orgID, mailboxID)
	if err != nil {
		return err
	}
	_,err=s.runLocalManagementOperation(ctx,orgID,actor,"",OperationPayload{Kind:"mailbox.connect",MailboxID:mb.ID,RemoteMailbox:&RemoteMailboxInput{ExpectedRevision:mb.Revision,CredentialMode:"managed"}});return err
}

// RotateCredential 通过 Operation 验证新凭据并核查旧凭据撤销。
func (s *Service) RotateCredential(ctx context.Context, orgID, actor, mailboxID int64) error {
	mb, err := s.Mailbox(ctx, orgID, mailboxID)
	if err != nil {
		return err
	}
	_,err=s.runLocalManagementOperation(ctx,orgID,actor,"",OperationPayload{Kind:"mailbox.rotate",MailboxID:mb.ID,RemoteMailbox:&RemoteMailboxInput{ExpectedRevision:mb.Revision}});return err
}

// ResetMailboxPassword 完成密码重置并通过一次性领取接口返回主密码。
func (s *Service) ResetMailboxPassword(ctx context.Context, orgID, actor, mailboxID int64) (string, error) {
	mb, err := s.Mailbox(ctx, orgID, mailboxID)
	if err != nil {
		return "", err
	}
	op,err:=s.runLocalManagementOperation(ctx,orgID,actor,"",OperationPayload{Kind:"mailbox.resetPassword",MailboxID:mb.ID,RemoteMailbox:&RemoteMailboxInput{ExpectedRevision:mb.Revision}});if err!=nil{return "",err};return s.ClaimOperationSecret(ctx,orgID,actor,op.ID)
}

// SuspendMailbox 暂停本地访问并保留服务器凭据。
func (s *Service) SuspendMailbox(ctx context.Context, orgID, actor, mailboxID int64, expectedRevision ...int64) error {
	mb,err:=s.Mailbox(ctx,orgID,mailboxID);if err!=nil{return err};revision:=mb.Revision;if len(expectedRevision)>1{return provider.Errorf("invalid","只能提供一个邮箱版本")};if len(expectedRevision)==1{revision=expectedRevision[0]}
	_,err=s.runLocalManagementOperation(ctx,orgID,actor,"",OperationPayload{Kind:"mailbox.suspend",MailboxID:mailboxID,ExpectedRevision:revision});return err
}


// BindMailbox 将已登记邮箱关联到指定成员。
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
	in:=UpdateMailboxInput{ExpectedRevision:mb.Revision};if ownerID>0{kind:=model.MailboxPersonal;in.Kind=&kind;in.OwnerMemberID=&ownerID};if name:=strings.TrimSpace(displayName);name!=""{in.DisplayName=&name}
	return s.UpdateMailbox(ctx,orgID,actor,mailboxID,in)
}


func randomPassword() string { return secretsRandomPassword() }

// UpdateMailboxInput edits display name, kind and owner.
type UpdateMailboxInput struct {
	RequestID string `json:"requestId"`
	ExpectedRevision int64 `json:"expectedRevision"`
	DisplayName   *string `json:"displayName"`
	Kind          *string `json:"kind"`
	OwnerMemberID *int64  `json:"ownerMemberId"` // 0 clears
}

// UpdateMailbox applies edits; converting to shared clears the owner.
func (s *Service) UpdateMailbox(ctx context.Context, orgID, actor, id int64, in UpdateMailboxInput) (*model.Mailbox, error) {
	if _,err:=s.runLocalManagementOperation(ctx,orgID,actor,in.RequestID,OperationPayload{Kind:"mailbox.update",MailboxID:id,MailboxEdit:&in});err!=nil{return nil,err}
	return s.Mailbox(ctx, orgID, id)
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
	err=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		if _,err:=tx.ExecContext(ctx,`INSERT INTO mailbox_access(mailbox_id,member_id,level,granted_by,granted_at) VALUES (?,?,?,?,?) ON CONFLICT(mailbox_id,member_id) DO UPDATE SET level=excluded.level,granted_by=excluded.granted_by,granted_at=excluded.granted_at`,mailboxID,memberID,level,actor,db.Now());err!=nil{return err}
		_,err:=tx.ExecContext(ctx,`UPDATE mailboxes SET revision=revision+1,access_revision=access_revision+1,updated_at=? WHERE id=? AND org_id=?`,db.Now(),mailboxID,orgID);return err
	})
	if err!=nil {
		return nil, err
	}
	s.cancelMailRequests(memberID,mailboxID)
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
	if err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		if _,err:=tx.ExecContext(ctx,`DELETE FROM mailbox_access WHERE mailbox_id=? AND member_id=?`,mailboxID,memberID);err!=nil{return err}
		_,err:=tx.ExecContext(ctx,`UPDATE mailboxes SET access_revision=access_revision+1,revision=revision+1,updated_at=? WHERE id=?`,db.Now(),mailboxID);return err
	});err!=nil{return err}
	s.cancelMailRequests(memberID,mailboxID)
	s.audit(ctx, orgID, actor, "mailbox.revoke", "mailbox", fmt.Sprint(mailboxID), map[string]any{"member": memberID})
	return nil
}

// --- identities ---

// Identities lists sending identities of a mailbox.
func (s *Service) Identities(ctx context.Context, mailboxID int64) ([]model.Identity, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, mailbox_id, address, display_name, reply_to, signature_html, is_default, authorization_source, authorization_status, authorization_checked_at, revision FROM identities WHERE mailbox_id = ? ORDER BY is_default DESC, id`, mailboxID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Identity{}
	for rows.Next() {
		var i model.Identity
		var def int
		var checked sql.NullString
		if err := rows.Scan(&i.ID, &i.MailboxID, &i.Address, &i.DisplayName, &i.ReplyTo, &i.SignatureHTML, &def, &i.AuthorizationSource, &i.AuthorizationStatus, &checked, &i.Revision); err != nil {
			return nil, err
		}
		i.IsDefault = def == 1
		i.AuthorizationCheckedAt=nullStr(checked)
		out = append(out, i)
	}
	return out, rows.Err()
}

// IdentityInput edits an identity.
type IdentityInput struct {
	ExpectedRevision int64 `json:"expectedRevision"`
	Address       string `json:"address"`
	DisplayName   string `json:"displayName"`
	ReplyTo       string `json:"replyTo"`
	SignatureHTML string `json:"signatureHtml"`
	IsDefault     bool   `json:"isDefault"`
}

// UpsertIdentity creates or updates an identity (id 0 creates).
func (s *Service) UpsertIdentity(ctx context.Context, orgID, actor, mailboxID, id int64, in IdentityInput) (*model.Identity, error) {
	mb,err:=s.Mailbox(ctx,orgID,mailboxID);if err!=nil{return nil,err}
	connection,err:=s.MailConnection(ctx,orgID,mb.ConnectionID);if err!=nil{return nil,err}
	addr,_,err:=canonicalMailboxAddress(connection.ProviderKind,in.Address)
	if err != nil {
		return nil, err
	}
	replyTo := strings.TrimSpace(in.ReplyTo)
	if replyTo != "" {
		if replyTo,_,err=canonicalMailboxAddress(provider.Manual,replyTo); err != nil {
			return nil, err
		}
	}
	sig := sanitizeSignature(in.SignatureHTML)
	now := db.Now()
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if id!=0 && in.ExpectedRevision<1{return provider.Errorf("invalid","需要 expectedRevision")}
		if err:=requireIdentityManagement(ctx,tx,orgID,actor,mailboxID);err!=nil{return err}
		if in.IsDefault {
			if _, err := tx.ExecContext(ctx, `UPDATE identities SET is_default=0,revision=revision+1 WHERE mailbox_id=? AND id!=? AND is_default=1`, mailboxID,id); err != nil {
				return err
			}
		}
		def := 0
		if in.IsDefault {
			def = 1
		}
		if id == 0 {
			var exists bool;if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM identities WHERE mailbox_id=? AND address=?)`,mailboxID,addr).Scan(&exists);err!=nil{return err};if exists{return provider.Errorf("identity_exists","发件身份已登记")}
			res, err := tx.ExecContext(ctx, `INSERT INTO identities(mailbox_id,address,display_name,reply_to,signature_html,is_default,created_at,authorization_source,authorization_status) VALUES (?,?,?,?,?,?,?,'admin','unverified')`,
				mailboxID, addr, strings.TrimSpace(in.DisplayName), replyTo, sig, def, now)
			if err != nil {
				return err
			}
			id,err=res.LastInsertId();if err!=nil{return err}
		}else{
			res,err:=tx.ExecContext(ctx,`UPDATE identities SET authorization_status=CASE WHEN address!=? THEN 'unverified' ELSE authorization_status END,authorization_source=CASE WHEN address!=? THEN 'admin' ELSE authorization_source END,authorization_checked_at=CASE WHEN address!=? THEN NULL ELSE authorization_checked_at END,address=?,display_name=?,reply_to=?,signature_html=?,is_default=CASE WHEN ?=1 THEN 1 ELSE is_default END,revision=revision+1 WHERE id=? AND mailbox_id=? AND revision=?`,addr,addr,addr,addr,strings.TrimSpace(in.DisplayName),replyTo,sig,def,id,mailboxID,in.ExpectedRevision);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","发件身份已更新")}
		}
		var defaults int;if err:=tx.QueryRowContext(ctx,`SELECT COUNT(*) FROM identities WHERE mailbox_id=? AND is_default=1`,mailboxID).Scan(&defaults);err!=nil{return err}
		if defaults==0{if _,err:=tx.ExecContext(ctx,`UPDATE identities SET is_default=1 WHERE id=(SELECT MIN(id) FROM identities WHERE mailbox_id=?)`,mailboxID);err!=nil{return err}}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.cancelMailRequests(0,mailboxID)
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
func (s *Service) DeleteIdentity(ctx context.Context, orgID, actor, mailboxID, id int64,expectedRevision ...int64) error {
	if _, err := s.Mailbox(ctx, orgID, mailboxID); err != nil {
		return err
	}
	if len(expectedRevision)!=1 || expectedRevision[0]<1{return provider.Errorf("invalid","需要 expectedRevision")}
	err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		if err:=requireIdentityManagement(ctx,tx,orgID,actor,mailboxID);err!=nil{return err}
		var revision int64;var isDefault bool
		err:=tx.QueryRowContext(ctx,`SELECT revision,is_default FROM identities WHERE id=? AND mailbox_id=?`,id,mailboxID).Scan(&revision,&isDefault);if db.IsNotFound(err){return ErrNotFound};if err!=nil{return err}
		if revision!=expectedRevision[0]{return provider.Errorf("revision_conflict","发件身份已更新")};if isDefault{return invalid("默认身份不能删除")}
		var linked bool;if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM provider_resources WHERE identity_id=?)`,id).Scan(&linked);err!=nil{return err};if linked{return provider.Errorf("external_action_required","发件身份仍有关联的服务商资源")}
		if _,err:=tx.ExecContext(ctx,`DELETE FROM identities WHERE id=? AND mailbox_id=? AND revision=?`,id,mailboxID,revision);err!=nil{return err}
		_,err=tx.ExecContext(ctx,`INSERT INTO audit_log(org_id,actor_member_id,action,target_type,target_id,detail_json,created_at) VALUES (?,?,'identity.delete','identity',?,?,?)`,orgID,actor,fmtID(id),toJSON(struct{MailboxID int64 `json:"mailboxId"`}{mailboxID}),db.Now());return err
	});if err!=nil{return err};s.cancelMailRequests(0,mailboxID);return nil
}

// --- credential resolution for the mail layer ---

// MailboxContext is what the mail API needs to act on a mailbox.
type MailboxContext struct {
	Mailbox *model.Mailbox
	Level   string
	Cred    imappool.Cred
	SMTP *ResolvedEndpoint
	ManageSieve *ResolvedEndpoint
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
	mc,err:=s.CheckMailboxAccess(ctx,orgID,memberID,mailboxID);if err!=nil{return nil,err}
	imap,err:=s.ResolveEndpoint(ctx,orgID,mailboxID,provider.ProtocolIMAP);if err!=nil{return nil,err}
	mc.Cred=imap.IMAPCredential();return mc,nil
}

func (s *Service) CheckMailboxAccess(ctx context.Context,orgID,memberID,mailboxID int64) (*MailboxContext,error) {
	member,err:=s.Member(ctx,orgID,memberID)
	if err!=nil{return nil,err}
	if member.Status!=model.MemberActive{return nil,ErrForbidden}
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
	var enabled bool
	if err:=s.DB.QueryRowContext(ctx,`SELECT enabled FROM mail_connections WHERE id=? AND org_id=?`,mb.ConnectionID,orgID).Scan(&enabled);err!=nil{return nil,err}
	if !enabled{return nil,provider.Errorf("endpoint_disabled","邮件连接已停用")}
	return &MailboxContext{Mailbox:mb,Level:level},nil
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
		ORDER BY CASE WHEN b.owner_member_id = ? THEN 0 ELSE 1 END, b.display_name, b.address`, orgID, memberID, memberID, memberID)
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
			if err:=s.DB.QueryRowContext(ctx, `SELECT level FROM mailbox_access WHERE mailbox_id = ? AND member_id = ?`, out[i].ID, memberID).Scan(&out[i].Level);err!=nil{return nil,err}
		}
		out[i].Identities,err=s.Identities(ctx,out[i].ID);if err!=nil{return nil,err}
	}
	return out, nil
}

// SaveMailboxSettings stores settings JSON (rules/vacation).
func (s *Service) SaveMailboxSettings(ctx context.Context, mailboxID int64, settings model.MailboxSettings) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE mailboxes SET settings_json = ?, updated_at = ? WHERE id = ?`, toJSON(settings), db.Now(), mailboxID)
	return err
}
