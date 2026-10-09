package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"mailhearth/internal/db"
	"mailhearth/internal/provider"
)

type MailboxRetirementCredential struct {
	ID int64 `json:"credentialId"`
	Source string `json:"source"`
	State string `json:"state"`
	ExternalRevocationRequired bool `json:"externalRevocationRequired"`
}

type MailboxRetirementReference struct {
	ID int64 `json:"resourceId"`
	Type string `json:"resourceType"`
	RemoteKey string `json:"remoteKey"`
	Purpose string `json:"purpose"`
	Owned bool `json:"ownedByMailhearth"`
	Locator json.RawMessage `json:"locator"`
}

func (s *Service) MailboxRetirementCredentials(ctx context.Context,orgID,mailboxID int64) ([]MailboxRetirementCredential,error) {
	if _,err:=s.Mailbox(ctx,orgID,mailboxID);err!=nil{return nil,err}
	rows,err:=s.DB.QueryContext(ctx,`SELECT id,source,state FROM credentials WHERE mailbox_id=? ORDER BY id`,mailboxID);if err!=nil{return nil,err};defer rows.Close()
	result:=[]MailboxRetirementCredential{}
	for rows.Next(){var credential MailboxRetirementCredential;if err:=rows.Scan(&credential.ID,&credential.Source,&credential.State);err!=nil{return nil,err};credential.ExternalRevocationRequired=credential.State!="revoked";result=append(result,credential)}
	return result,rows.Err()
}

func (s *Service) DeleteMailbox(ctx context.Context,orgID,actor,id int64,confirm string,expectedRevision ...int64) error {
	if len(expectedRevision)!=1 || expectedRevision[0]<1{return provider.Errorf("invalid","需要 expectedRevision")}
	err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		var address string;var revision,connectionID int64;var owner sql.NullInt64
		err:=tx.QueryRowContext(ctx,`SELECT address,revision,connection_id,owner_member_id FROM mailboxes WHERE id=? AND org_id=?`,id,orgID).Scan(&address,&revision,&connectionID,&owner);if db.IsNotFound(err){return ErrNotFound};if err!=nil{return err}
		if revision!=expectedRevision[0]{return provider.Errorf("revision_conflict","邮箱配置已更新")};if confirm!=address{return provider.Errorf("invalid","请输入完整邮箱地址")}
		var count int
		if err:=tx.QueryRowContext(ctx,`SELECT (SELECT COUNT(*) FROM message_state WHERE mailbox_id=?)+(SELECT COUNT(*) FROM mail_activity WHERE mailbox_id=?)+(SELECT COUNT(*) FROM submissions WHERE mailbox_id=?)`,id,id,id).Scan(&count);err!=nil{return err}
		if count>0{return provider.Errorf("mailbox_has_history","邮箱包含历史记录，请归档邮箱")}
		if owner.Valid{return provider.Errorf("mailbox_in_use","请解除邮箱所有者关联")}
		if err:=tx.QueryRowContext(ctx,`SELECT (SELECT COUNT(*) FROM mailbox_access WHERE mailbox_id=?)+(SELECT COUNT(*) FROM addresses WHERE connection_id=? AND kind!='primary' AND (mailbox_id=? OR EXISTS(SELECT 1 FROM json_each(targets_json) WHERE value=?)))+(SELECT COUNT(*) FROM mailbox_forwardings WHERE mailbox_id=?)+(SELECT COUNT(*) FROM operations WHERE target_mailbox_id=? AND status IN ('queued','running','unknown','needs_action'))`,id,connectionID,id,address,id,id).Scan(&count);err!=nil{return err}
		if count>0{return provider.Errorf("mailbox_in_use","邮箱仍有授权、地址规则、转发或未完成操作")}
		var retryable bool;if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM operations WHERE org_id=? AND target_mailbox_id=? AND status='failed' AND payload_enc IS NOT NULL)`,orgID,id).Scan(&retryable);err!=nil{return err};if retryable{return provider.Errorf("mailbox_in_use","邮箱仍有需要处理或取消的失败操作")}
		credentials:=[]MailboxRetirementCredential{}
		rows,err:=tx.QueryContext(ctx,`SELECT id,source,state FROM credentials WHERE mailbox_id=? ORDER BY id`,id);if err!=nil{return err}
		for rows.Next(){var credential MailboxRetirementCredential;if err:=rows.Scan(&credential.ID,&credential.Source,&credential.State);err!=nil{rows.Close();return err};credential.ExternalRevocationRequired=credential.State!="revoked";credentials=append(credentials,credential)};err=rows.Err();rows.Close();if err!=nil{return err}
		references:=[]MailboxRetirementReference{}
		rows,err=tx.QueryContext(ctx,`SELECT id,resource_type,remote_key,purpose,owned_by_mailhearth,remote_locator_json FROM provider_resources WHERE mailbox_id=? OR identity_id IN (SELECT id FROM identities WHERE mailbox_id=?) OR credential_id IN (SELECT id FROM credentials WHERE mailbox_id=?) OR address_id IN (SELECT id FROM addresses WHERE mailbox_id=? AND kind='primary') ORDER BY id`,id,id,id,id);if err!=nil{return err}
		for rows.Next(){var reference MailboxRetirementReference;var locator string;if err:=rows.Scan(&reference.ID,&reference.Type,&reference.RemoteKey,&reference.Purpose,&reference.Owned,&locator);err!=nil{rows.Close();return err};reference.Locator=json.RawMessage(locator);references=append(references,reference)};err=rows.Err();rows.Close();if err!=nil{return err}
		detail,err:=json.Marshal(struct{Address string `json:"address"`;ConnectionID int64 `json:"connectionId"`;Credentials []MailboxRetirementCredential `json:"credentials"`;References []MailboxRetirementReference `json:"references"`}{address,connectionID,credentials,references});if err!=nil{return err}
		if _,err:=tx.ExecContext(ctx,`INSERT INTO audit_log(org_id,actor_member_id,action,target_type,target_id,detail_json,created_at) VALUES (?,?,'mailbox.unregister','mailbox',?,?,?)`,orgID,actor,fmtID(id),string(detail),db.Now());err!=nil{return err}
		if _,err:=tx.ExecContext(ctx,`UPDATE operations SET result_json=json_set(result_json,'$.unregisteredMailboxId',?),target_mailbox_id=NULL,updated_at=? WHERE org_id=? AND target_mailbox_id=? AND status IN ('succeeded','cancelled')`,id,db.Now(),orgID,id);err!=nil{return err}
		if _,err:=tx.ExecContext(ctx,`DELETE FROM provider_resources WHERE mailbox_id=? OR identity_id IN (SELECT id FROM identities WHERE mailbox_id=?) OR credential_id IN (SELECT id FROM credentials WHERE mailbox_id=?) OR address_id IN (SELECT id FROM addresses WHERE mailbox_id=? AND kind='primary')`,id,id,id,id);err!=nil{return err}
		if _,err:=tx.ExecContext(ctx,`DELETE FROM identities WHERE mailbox_id=?`,id);err!=nil{return err}
		if _,err:=tx.ExecContext(ctx,`DELETE FROM addresses WHERE mailbox_id=? AND kind='primary'`,id);err!=nil{return err}
		if _,err:=tx.ExecContext(ctx,`DELETE FROM mailbox_endpoints WHERE mailbox_id=?`,id);err!=nil{return err}
		if _,err:=tx.ExecContext(ctx,`DELETE FROM credentials WHERE mailbox_id=?`,id);err!=nil{return err}
		_,err=tx.ExecContext(ctx,`DELETE FROM mailboxes WHERE id=? AND org_id=? AND revision=?`,id,orgID,revision);return err
	});if err!=nil{return err}
	s.cancelMailRequests(0,id);if s.Pool!=nil{s.Pool.InvalidateMailbox(id)};return nil
}

func (s *Service) ArchiveMailbox(ctx context.Context,orgID,actor,id,revision int64) error {
	_,err:=s.runLocalManagementOperation(ctx,orgID,actor,"",OperationPayload{Kind:"mailbox.archive",MailboxID:id,ExpectedRevision:revision});return err
}

func (s *Service) ReactivateMailbox(ctx context.Context,orgID,actor,mailboxID int64,expectedRevision ...int64) error {
	if len(expectedRevision)!=1 || expectedRevision[0]<1{return provider.Errorf("invalid","需要 expectedRevision")}
	_,err:=s.runLocalManagementOperation(ctx,orgID,actor,"",OperationPayload{Kind:"mailbox.reactivate",MailboxID:mailboxID,ExpectedRevision:expectedRevision[0]});return err
}

func (s *Service) reactivateMailboxProtocols(ctx context.Context,orgID,actor,mailboxID,expectedRevision int64) error {
	ctx,cancel:=context.WithTimeout(ctx,120*time.Second);defer cancel()
	mb,err:=s.Mailbox(ctx,orgID,mailboxID);if err!=nil{return err};if mb.Revision!=expectedRevision{return provider.Errorf("revision_conflict","邮箱配置已更新")}
	if mb.Status!="suspended"{return provider.Errorf("invalid","仅允许恢复已暂停的邮箱")}
	view,err:=s.MailboxEndpoints(ctx,orgID,mailboxID);if err!=nil{return err}
	var imapConfigured bool
	for _,endpoint:=range view.Endpoints{if endpoint.Protocol==provider.ProtocolIMAP{imapConfigured=endpoint.NetworkMode!="disabled" && endpoint.EffectiveNetwork.Enabled}}
	if !imapConfigured{return provider.Errorf("endpoint_unconfigured","恢复邮箱需要配置 IMAP")}
	var checked []*ResolvedEndpoint
	capabilities:=map[string]string{}
	for _,endpoint:=range view.Endpoints{
		if endpoint.NetworkMode=="disabled" || !endpoint.EffectiveNetwork.Enabled{if endpoint.Protocol==provider.ProtocolIMAP{return provider.Errorf("endpoint_disabled","恢复邮箱需要启用 IMAP")};continue}
		resolved,err:=s.ResolveEndpoint(ctx,orgID,mailboxID,endpoint.Protocol);if err!=nil{return err}
		candidate:=&candidateEndpoint{protocol:endpoint.Protocol,resolved:resolved}
		if err:=s.validateCandidateEndpoint(ctx,candidate);err!=nil{return err};checked=append(checked,resolved);capabilities[endpoint.Protocol]=candidate.capabilities
	}
	if len(checked)==0{return provider.Errorf("endpoint_unconfigured","邮箱协议尚未配置")}
	err=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		for _,endpoint:=range checked{
			var valid bool
			if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM mailbox_endpoints ep JOIN credentials cr ON cr.id=ep.credential_id JOIN mail_connections c ON c.id=cr.connection_id WHERE ep.mailbox_id=? AND ep.protocol=? AND ep.revision=? AND cr.id=? AND cr.generation=? AND cr.state='active' AND c.enabled=1 AND c.revision=?)`,mailboxID,endpoint.Protocol,endpoint.EndpointRevision,endpoint.CredentialID,endpoint.CredentialGeneration,endpoint.ConnectionRevision).Scan(&valid);err!=nil{return err};if !valid{return provider.Errorf("revision_conflict","邮箱协议配置已更新")}
			versions,err:=json.Marshal(CheckedVersions{endpoint.ConnectionRevision,endpoint.EndpointRevision,endpoint.CredentialID,endpoint.CredentialGeneration});if err!=nil{return err}
			if _,err:=tx.ExecContext(ctx,`UPDATE mailbox_endpoints SET check_status='passed',checked_at=?,checked_versions_json=?,capabilities_json=?,last_error_code=NULL,updated_at=? WHERE mailbox_id=? AND protocol=?`,db.Now(),string(versions),capabilities[endpoint.Protocol],db.Now(),mailboxID,endpoint.Protocol);err!=nil{return err}
		}
		res,err:=tx.ExecContext(ctx,`UPDATE mailboxes SET status='active',revision=revision+1,access_revision=access_revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=? AND status='suspended'`,db.Now(),mailboxID,orgID,expectedRevision);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","邮箱配置已更新")}
		return completeLocalOperation(ctx,tx,struct{MailboxID int64 `json:"mailboxId"`}{mailboxID})
	});if err!=nil{return err}
	s.cancelMailRequests(0,mailboxID);if s.Pool!=nil{s.Pool.InvalidateMailbox(mailboxID)}
	s.audit(ctx,orgID,actor,"mailbox.reactivate","mailbox",fmtID(mailboxID),nil);return nil
}
