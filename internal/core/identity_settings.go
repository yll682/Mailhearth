package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"mailhearth/internal/model"
	"mailhearth/internal/provider"
	"mailhearth/internal/db"
)

type IdentitySettingsInput struct {
	ExpectedRevision int64 `json:"expectedRevision"`
	DisplayName string `json:"displayName"`
	ReplyTo string `json:"replyTo"`
	SignatureHTML string `json:"signatureHtml"`
	IsDefault bool `json:"isDefault"`
}

func (s *Service) UpdateIdentitySettings(ctx context.Context,orgID,actor,mailboxID,id int64,in IdentitySettingsInput) (*model.Identity,error) {
	mc,err:=s.CheckMailboxAccess(ctx,orgID,actor,mailboxID);if err!=nil{return nil,err};if mc.Level!=model.AccessFull{return nil,ErrForbidden}
	if in.ExpectedRevision<1{return nil,provider.Errorf("invalid","需要 expectedRevision")}
	replyTo:=strings.TrimSpace(in.ReplyTo)
	if replyTo!=""{address,_,err:=canonicalMailboxAddress(provider.Manual,replyTo);if err!=nil{return nil,err};replyTo=address}
	err=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		var current bool
		if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM members m JOIN mailboxes b ON b.org_id=m.org_id WHERE m.id=? AND m.org_id=? AND m.status='active' AND b.id=? AND b.status='active' AND b.access_revision=? AND (b.owner_member_id=m.id OR EXISTS(SELECT 1 FROM mailbox_access a WHERE a.mailbox_id=b.id AND a.member_id=m.id AND a.level='full')))`,actor,orgID,mailboxID,mc.Mailbox.AccessRevision).Scan(&current);err!=nil{return err};if !current{return ErrForbidden}
		var exists bool
		if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM identities WHERE id=? AND mailbox_id=? AND revision=?)`,id,mailboxID,in.ExpectedRevision).Scan(&exists);err!=nil{return err};if !exists{return provider.Errorf("revision_conflict","发件身份已更新")}
		if in.IsDefault{if _,err:=tx.ExecContext(ctx,`UPDATE identities SET is_default=0,revision=revision+1 WHERE mailbox_id=? AND is_default=1 AND id!=?`,mailboxID,id);err!=nil{return err}}
		_,err:=tx.ExecContext(ctx,`UPDATE identities SET display_name=?,reply_to=?,signature_html=?,is_default=CASE WHEN ? THEN 1 ELSE is_default END,revision=revision+1 WHERE mailbox_id=? AND id=?`,strings.TrimSpace(in.DisplayName),replyTo,sanitizeSignature(in.SignatureHTML),in.IsDefault,mailboxID,id);return err
	});if err!=nil{return nil,err}
	s.cancelMailRequests(0,mailboxID)
	identities,err:=s.Identities(ctx,mailboxID);if err!=nil{return nil,err}
	for _,identity:=range identities{if identity.ID==id{return &identity,nil}}
	return nil,ErrNotFound
}

func (s *Service) AuthorizeIdentity(ctx context.Context,orgID,actor,mailboxID,id,revision int64,allowed bool) (*model.Identity,error) {
	if revision<1{return nil,provider.Errorf("invalid","需要 expectedRevision")}
	status:="denied";if allowed{status="allowed"}
	err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		if err:=requireIdentityManagement(ctx,tx,orgID,actor,mailboxID);err!=nil{return err}
		res,err:=tx.ExecContext(ctx,`UPDATE identities SET authorization_source='admin',authorization_status=?,authorization_checked_at=?,revision=revision+1 WHERE id=? AND mailbox_id=? AND revision=?`,status,db.Now(),id,mailboxID,revision);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","发件身份已更新")}
		_,err=tx.ExecContext(ctx,`INSERT INTO audit_log(org_id,actor_member_id,action,target_type,target_id,detail_json,created_at) VALUES (?,?,'identity.authorize','identity',?,?,?)`,orgID,actor,fmtID(id),toJSON(struct{MailboxID int64 `json:"mailboxId"`;Status string `json:"status"`}{mailboxID,status}),db.Now());return err
	});if err!=nil{return nil,err}
	s.cancelMailRequests(0,mailboxID)
	identities,err:=s.Identities(ctx,mailboxID);if err!=nil{return nil,err};for _,identity:=range identities{if identity.ID==id{return &identity,nil}};return nil,ErrNotFound
}

func requireIdentityManagement(ctx context.Context,q db.Querier,orgID,actor,mailboxID int64) error {
	var kind,raw string
	err:=q.QueryRowContext(ctx,`SELECT b.kind,r.permissions_json FROM mailboxes b JOIN members m ON m.org_id=b.org_id JOIN roles r ON r.id=m.role_id AND r.org_id=m.org_id WHERE b.id=? AND b.org_id=? AND m.id=? AND m.status='active'`,mailboxID,orgID,actor).Scan(&kind,&raw)
	if db.IsNotFound(err){return ErrForbidden};if err!=nil{return err}
	var permissions []string;if err:=json.Unmarshal([]byte(raw),&permissions);err!=nil{return err}
	required:=model.PermMailboxesManage;if kind==model.MailboxShared{required=model.PermSharedManage}
	if !HasPermission(permissions,required){return ErrForbidden};return nil
}
