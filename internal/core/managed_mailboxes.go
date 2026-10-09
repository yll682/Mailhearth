package core

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

type ManagedMailboxCreateInput struct {
	RequestID string `json:"requestId"`
	Mode string `json:"mode"`
	ConnectionID int64 `json:"connectionId"`
	DomainBindingID int64 `json:"domainBindingId"`
	LocalPart string `json:"localPart"`
	Kind string `json:"kind"`
	DisplayName string `json:"displayName"`
	OwnerMemberID *int64 `json:"ownerMemberId"`
	CredentialMode string `json:"credentialMode"`
	SentCopyMode string `json:"sentCopyMode"`
	Credentials []EnteredCredential `json:"credentials,omitempty"`
	Endpoints EndpointInputs `json:"endpoints"`
	FolderMapping model.FolderMapping `json:"folderMapping"`
}

type RemoteMailboxInput struct {
	RequestID string `json:"requestId"`
	ExpectedRevision int64 `json:"expectedRevision"`
	ConfirmAddress string `json:"confirmAddress,omitempty"`
	RevokeAll bool `json:"revokeAll,omitempty"`
	CredentialMode string `json:"credentialMode,omitempty"`
	Credentials []EnteredCredential `json:"credentials,omitempty"`
	Endpoints EndpointInputs `json:"endpoints"`
}

func (s *Service) prepareMailboxManagement(ctx context.Context,orgID int64,payload *OperationPayload) (string,error) {
	if payload.Kind=="mailbox.create"{
		in:=payload.Create;if in==nil{return "",provider.Errorf("invalid","需要创建邮箱请求")}
		if in.CredentialMode==""{in.CredentialMode="managed"}
		if in.Mode!="create" || (in.CredentialMode!="managed" && in.CredentialMode!="entered"){return "",provider.Errorf("invalid","创建远程邮箱需要 create 和明确的凭据方式")}
		if in.CredentialMode=="managed" && (len(in.Credentials)>0 || in.Endpoints.IMAP!=nil || in.Endpoints.SMTP!=nil || in.Endpoints.ManageSieve!=nil || in.FolderMapping!=(model.FolderMapping{})){return "",provider.Errorf("invalid","managed 创建使用连接模板")}
		if in.Kind!=model.MailboxPersonal && in.Kind!=model.MailboxShared{return "",provider.Errorf("invalid","邮箱类型无效")};if in.Kind==model.MailboxShared && in.OwnerMemberID!=nil{return "",provider.Errorf("invalid","共享邮箱不能设置 owner")}
		in.LocalPart=strings.ToLower(strings.TrimSpace(in.LocalPart));if !validLocalPart(in.LocalPart){return "",provider.Errorf("invalid","邮箱 localPart 无效")}
		if in.SentCopyMode==""{in.SentCopyMode="append"};if in.SentCopyMode!="append" && in.SentCopyMode!="server"{return "",provider.Errorf("invalid","sentCopyMode 无效")}
		b,err:=s.DomainBinding(ctx,orgID,in.DomainBindingID);if err!=nil{return "",err};if b.ConnectionID!=in.ConnectionID{return "",ErrNotFound};if b.ManagementMode!="api" || b.RemoteState!="present"{return "",provider.Errorf("verification_required","域名关联需要确认可管理")}
		c,err:=s.MailConnection(ctx,orgID,in.ConnectionID);if err!=nil{return "",err};if !c.Enabled{return "",provider.Errorf("endpoint_disabled","连接已经停用")};if c.ProviderKind==provider.Manual{return "",provider.Errorf("external_action_required","手动连接使用 attach 登记已有邮箱")};if !c.APIConfigured || c.LastAPICheckStatus!="passed"{return "",provider.Errorf("verification_required","管理认证需要通过核验")};if c.ProviderKind==provider.Migadu && in.CredentialMode=="managed"{return "",provider.Errorf("verification_required","Migadu managed 凭据需要实施前核验")};if !scopeContainsDomain(c.DomainScope,b.DomainName){return "",provider.Errorf("target_constraint_failed","域名不在管理范围内")}
		if in.CredentialMode=="managed" && !c.ProtocolDefaults.IMAP.Enabled{return "",provider.Errorf("endpoint_unconfigured","managed 接入需要 IMAP 模板")}
		if in.CredentialMode=="entered"{if _,err:=s.candidateEndpoints(ctx,orgID,0,c,in.Credentials,in.Endpoints);err!=nil{return "",err}}
		if err:=validateFolderMappingInput(in.FolderMapping);err!=nil{return "",err}
		if in.OwnerMemberID!=nil{member,err:=s.Member(ctx,orgID,*in.OwnerMemberID);if err!=nil{return "",err};if member.Status!=model.MemberActive && member.Status!=model.MemberInvited{return "",provider.Errorf("invalid","owner 状态无效")}}
		var exists bool;if err:=s.DB.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM mailboxes WHERE connection_id=? AND address_key=?)`,in.ConnectionID,in.LocalPart+"@"+b.DomainName).Scan(&exists);err!=nil{return "",err};if exists{return "",provider.Errorf("idempotency_conflict","该连接已经登记此邮箱")}
		payload.ConnectionID=in.ConnectionID
		permission:=model.PermMailboxesManage;if in.Kind==model.MailboxShared{permission=model.PermSharedManage};return permission,nil
	}
	mb,err:=s.Mailbox(ctx,orgID,payload.MailboxID);if err!=nil{return "",err};payload.ConnectionID=mb.ConnectionID
	if payload.RemoteMailbox==nil || payload.RemoteMailbox.ExpectedRevision!=mb.Revision{return "",provider.Errorf("revision_conflict","需要当前邮箱 expectedRevision")}
	if payload.Kind=="mailbox.revokeRemoteAccess"{permission:=model.PermMailboxesManage;if mb.Kind==model.MailboxShared{permission=model.PermSharedManage};return permission,nil}
	if payload.Kind=="mailbox.connect" && payload.RemoteMailbox.CredentialMode!="managed"{return "",provider.Errorf("invalid","connect 需要明确选择 managed 或 entered 凭据")}
	if payload.Kind=="mailbox.connect" || payload.Kind=="mailbox.rotate"{if err:=s.checkManagedCredentialTargets(ctx,mb,payload.Kind=="mailbox.rotate");err!=nil{return "",err}}
	if mb.ManagementMode!="api" || mb.RemoteState!="present"{return "",provider.Errorf("external_action_required","此邮箱需要外部远程管理")}
	c,err:=s.MailConnection(ctx,orgID,mb.ConnectionID);if err!=nil{return "",err};if !c.Enabled{return "",provider.Errorf("endpoint_disabled","连接已停用")}
	if c.ProviderKind==provider.Manual{return "",provider.Errorf("external_action_required","手动连接的邮箱需要外部远程管理")};if !c.APIConfigured || c.LastAPICheckStatus!="passed"{return "",provider.Errorf("verification_required","管理认证需要通过核验")}
	_,domain:=SplitAddress(mb.Address);if !scopeContainsDomain(c.DomainScope,domain){return "",provider.Errorf("target_constraint_failed","邮箱域名不在管理范围内")}
	if c.ProviderKind==provider.Migadu && (payload.Kind=="mailbox.connect" || payload.Kind=="mailbox.rotate"){return "",provider.Errorf("verification_required","Migadu managed 凭据需要实施前核验")}
	if payload.Kind=="mailbox.deleteRemote" && payload.RemoteMailbox.ConfirmAddress!=mb.Address{return "",provider.Errorf("invalid","请输入完整邮箱地址")}
	if payload.RemoteMailbox.RevokeAll{return "",provider.Errorf("external_action_required","全部远程访问需要在服务商管理页面逐项核验并撤销")}
	permission:=model.PermMailboxesManage;if mb.Kind==model.MailboxShared{permission=model.PermSharedManage};return permission,nil
}

func (s *Service) operationPassword(ctx context.Context,key string) (string,error) {
	key=operationStepKey(ctx,key)
	id,ok:=ctx.Value(operationContextKey{}).(string);if !ok{return "",provider.Errorf("invalid","需要 Operation")}
	var sealed string
	err:=s.DB.QueryRowContext(ctx,`SELECT secret_enc FROM operation_private WHERE operation_id=? AND field_key=?`,id,key).Scan(&sealed)
	if err==nil{return s.Box.Open(sealed)};if !db.IsNotFound(err){return "",err}
	password:=randomPassword();sealed,err=s.Box.Seal(password);if err!=nil{return "",err}
	if _,err:=s.DB.ExecContext(ctx,`INSERT INTO operation_private(operation_id,field_key,secret_enc,created_at) VALUES (?,?,?,?)`,id,key,sealed,db.Now());err!=nil{return "",err};return password,nil
}

func (s *Service) createManagedMailbox(ctx context.Context,orgID int64,in *ManagedMailboxCreateInput) (*model.Mailbox,error) {
	b,err:=s.DomainBinding(ctx,orgID,in.DomainBindingID);if err!=nil{return nil,err}
	c,err:=s.MailConnection(ctx,orgID,in.ConnectionID);if err!=nil{return nil,err};if !c.Enabled{return nil,provider.Errorf("endpoint_disabled","连接已停用")};if b.ConnectionID!=c.ID || b.ManagementMode!="api" || b.RemoteState!="present" || !scopeContainsDomain(c.DomainScope,b.DomainName){return nil,provider.Errorf("verification_required","域名关联需要确认可管理")}
	address:=in.LocalPart+"@"+b.DomainName
	api,_,err:=s.connectionAdapter(ctx,orgID,in.ConnectionID);if err!=nil{return nil,err}
	password,err:=s.operationPassword(ctx,"mailbox_password");if err!=nil{return nil,err}
	if in.CredentialMode=="entered" && in.Endpoints.IMAP!=nil && in.Endpoints.IMAP.Username==address && in.Endpoints.IMAP.Credential!=nil{for _,credential:=range in.Credentials{if credential.ClientKey==in.Endpoints.IMAP.Credential.ClientKey{password=credential.Secret}}}
	id:=ctx.Value(operationContextKey{}).(string)
	var mailboxID int64
	err=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		if err:=checkOperationPermissionTx(ctx,tx,ctx.Value(operationContextKey{}).(string));err!=nil{return err}
		var current bool;if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM mail_connections c JOIN domain_bindings b ON b.connection_id=c.id WHERE c.id=? AND c.org_id=? AND c.enabled=1 AND c.revision=? AND b.id=? AND b.revision=?)`,c.ID,orgID,c.Revision,b.ID,b.Revision).Scan(&current);err!=nil{return err};if !current{return provider.Errorf("revision_conflict","连接或域名关联已经更新")}
		err:=tx.QueryRowContext(ctx,`SELECT json_extract(result_json,'$.mailboxId') FROM operation_steps WHERE operation_id=? AND step_key=? AND status='succeeded'`,id,operationStepKey(ctx,"mailbox.reserve")).Scan(&mailboxID);if err==nil{return nil};if !db.IsNotFound(err){return err}
		now:=db.Now();res,err:=tx.ExecContext(ctx,`INSERT INTO mailboxes(org_id,connection_id,domain_id,domain_binding_id,kind,address,address_key,display_name,owner_member_id,management_mode,remote_state,status,sent_copy_mode,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,'api','unknown','suspended',?,?,?)`,orgID,in.ConnectionID,b.DomainID,b.ID,in.Kind,address,address,in.DisplayName,in.OwnerMemberID,in.SentCopyMode,now,now);if err!=nil{return err};mailboxID,err=res.LastInsertId();if err!=nil{return err}
		if _,err:=tx.ExecContext(ctx,`INSERT INTO operation_locks(org_id,resource_key,operation_id) VALUES (?,?,?)`,orgID,"mailbox:"+fmtID(mailboxID),id);err!=nil{return err}
		for _,protocol:=range []string{provider.ProtocolIMAP,provider.ProtocolSMTP,provider.ProtocolManageSieve}{network,err:=templateFor(c.ProtocolDefaults,protocol);if err!=nil{return err};mode:="inherit";var username any=address;if !network.Enabled || in.CredentialMode=="entered"{mode="disabled";username=nil};if _,err:=tx.ExecContext(ctx,`INSERT INTO mailbox_endpoints(mailbox_id,protocol,network_mode,username,created_at,updated_at) VALUES (?,?,?,?,?,?)`,mailboxID,protocol,mode,username,now,now);err!=nil{return err}}
		if _,err:=tx.ExecContext(ctx,`UPDATE operations SET target_mailbox_id=? WHERE id=?`,mailboxID,id);err!=nil{return err}
		_,err=tx.ExecContext(ctx,`INSERT INTO operation_steps(operation_id,step_key,sequence,status,result_json,finished_at) SELECT ?,?,COALESCE(MAX(sequence),0)+1,'succeeded',?,? FROM operation_steps WHERE operation_id=?`,id,operationStepKey(ctx,"mailbox.reserve"),toJSON(map[string]int64{"mailboxId":mailboxID}),now,id);return err
	});if err!=nil{return nil,err}
	if err:=s.remoteOperationStep(ctx,"mailbox.create",map[string]string{"address":address},func()error{_,err:=api.CreateMailbox(ctx,provider.CreateMailboxRequest{Domain:b.DomainName,LocalPart:in.LocalPart,Password:password,DisplayName:in.DisplayName});return err});err!=nil{return nil,err}
	if _,err:=api.GetMailbox(ctx,provider.GetMailboxRequest{Domain:b.DomainName,LocalPart:in.LocalPart});err!=nil{return nil,err}
	if err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		if err:=checkOperationPermissionTx(ctx,tx,id);err!=nil{return err}
		if err:=saveDiscoveredResource(ctx,tx,in.ConnectionID,provider.Resource{ResourceType:"mailbox",RemoteKey:address,RemoteLocator:map[string]string{"address":address},Purpose:provider.PurposeMailbox},"mailbox_id",mailboxID);err!=nil{return err}
		if _,err:=tx.ExecContext(ctx,`UPDATE provider_resources SET owned_by_mailhearth=1 WHERE connection_id=? AND mailbox_id=? AND purpose='mailbox'`,in.ConnectionID,mailboxID);err!=nil{return err}
		_,err:=tx.ExecContext(ctx,`UPDATE mailboxes SET remote_state='present',updated_at=? WHERE id=? AND connection_id=?`,db.Now(),mailboxID,in.ConnectionID);return err
	});err!=nil{return nil,err}
	return s.Mailbox(ctx,orgID,mailboxID)
}

func (s *Service) managedCredential(ctx context.Context,orgID int64,mb *model.Mailbox,rotate bool) error {
	api,c,err:=s.connectionAdapter(ctx,orgID,mb.ConnectionID);if err!=nil{return err}
	if c.ProviderKind!=provider.Purelymail{return provider.Errorf("verification_required","managed 凭据需要实施前核验")}
	id:=ctx.Value(operationContextKey{}).(string)
	var candidateID int64;var sealed string
	err=s.DB.QueryRowContext(ctx,`SELECT c.id,c.secret_enc FROM credentials c JOIN operation_steps st ON st.operation_id=? AND st.step_key=? WHERE c.id=json_extract(st.result_json,'$.credentialId')`,id,operationStepKey(ctx,"credential.create")).Scan(&candidateID,&sealed)
	if err!=nil && !db.IsNotFound(err){return err}
	if candidateID==0{if err:=s.checkManagedCredentialTargets(ctx,mb,rotate);err!=nil{return err};if !rotate{ready,err:=s.managedEndpointsReady(ctx,mb.ID);if err!=nil{return err};if ready{return nil}}}
	local,domain:=SplitAddress(mb.Address)
	if err:=s.remoteOperationStep(ctx,"credential.create",map[string]any{"address":mb.Address,"mailboxId":mb.ID,"connectionId":mb.ConnectionID},func()error{
		credential,err:=api.CreateCredential(ctx,provider.CreateCredentialRequest{Domain:domain,LocalPart:local});if err!=nil{return err}
		if credential.Password==""{return provider.Errorf("upstream_failed","服务商未返回凭据秘密")}
		sealed,err=s.Box.Seal(credential.Password);if err!=nil{return &remoteOutcomeUnknown{err}}
		return s.DB.Tx(ctx,func(tx *sql.Tx)error{
			var generation int64;if err:=tx.QueryRowContext(ctx,`SELECT COALESCE(MAX(generation),0)+1 FROM credentials WHERE mailbox_id=? AND purpose='mail'`,mb.ID).Scan(&generation);err!=nil{return err}
			res,err:=tx.ExecContext(ctx,`INSERT INTO credentials(org_id,connection_id,mailbox_id,purpose,source,secret_enc,generation,state,hint,created_at,updated_at) VALUES (?,?,?,'mail','purelymail_app_password',?,?,'candidate','Mailhearth',?,?)`,orgID,mb.ConnectionID,mb.ID,sealed,generation,db.Now(),db.Now());if err!=nil{return err};candidateID,err=res.LastInsertId();if err!=nil{return err}
			if _,err:=tx.ExecContext(ctx,`UPDATE operation_steps SET result_json=? WHERE operation_id=? AND step_key=?`,toJSON(map[string]int64{"credentialId":candidateID}),id,operationStepKey(ctx,"credential.create"));err!=nil{return err}
			locator:=toJSON(map[string]string{"domain":domain,"mailboxLocalPart":local})
			_,err=tx.ExecContext(ctx,`INSERT INTO provider_resources(connection_id,resource_type,remote_key,remote_locator_json,purpose,owned_by_mailhearth,remote_state,credential_id,created_at,updated_at) VALUES (?,'app_password',?,?,'login_credential',1,'present',?,?,?)`,mb.ConnectionID,"credential:"+fmtID(candidateID),locator,candidateID,db.Now(),db.Now());return err
		})
	});err!=nil{return err}
	if candidateID==0{return provider.Errorf("internal","候选凭据未保存")}
	var generation int64;if err:=s.DB.QueryRowContext(ctx,`SELECT secret_enc,generation FROM credentials WHERE id=? AND mailbox_id=?`,candidateID,mb.ID).Scan(&sealed,&generation);err!=nil{return err}
	secret,err:=s.Box.Open(sealed);if err!=nil{return provider.Errorf("credential_decryption_failed","候选凭据解密失败")}
	view,err:=s.MailboxEndpoints(ctx,orgID,mb.ID);if err!=nil{return err}
	var checked []candidateEndpoint
	for _,ep:=range view.Endpoints{
		if ep.NetworkMode=="disabled" || !ep.EffectiveNetwork.Enabled{continue}
		if ep.Credential!=nil && ep.Credential.ID!=candidateID{var source string;if err:=s.DB.QueryRowContext(ctx,`SELECT source FROM credentials WHERE id=?`,ep.Credential.ID).Scan(&source);err!=nil{return err};if source=="entered"{continue}}
		candidate:=candidateEndpoint{protocol:ep.Protocol,resolved:&ResolvedEndpoint{Protocol:ep.Protocol,Network:ep.EffectiveNetwork,Username:mb.Address,Secret:secret,OrgID:orgID,ConnectionID:mb.ConnectionID,MailboxID:mb.ID,ConnectionRevision:c.Revision,EndpointRevision:ep.Revision+1,CredentialID:candidateID,CredentialGeneration:generation}}
		if err:=s.prepareEndpointNetwork(ctx,candidate.resolved);err!=nil{return err};if err:=s.validateCandidateEndpoint(ctx,&candidate);err!=nil{return err};checked=append(checked,candidate)
	}
	if len(checked)==0{return provider.Errorf("external_action_required","没有可以使用 managed 凭据的启用协议")}
	err=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		if err:=checkOperationPermissionTx(ctx,tx,id);err!=nil{return err}
		var current bool;if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM mail_connections WHERE id=? AND org_id=? AND revision=? AND enabled=1)`,c.ID,orgID,c.Revision).Scan(&current);err!=nil{return err};if !current{return provider.Errorf("revision_conflict","连接配置已经更新")}
		var active bool;if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM credentials WHERE id=? AND state='active')`,candidateID).Scan(&active);err!=nil{return err};if active{return validateManagedMailboxRevision(ctx,tx,orgID,id,mb.ID,mb.Revision)}
		res,err:=tx.ExecContext(ctx,`UPDATE mailboxes SET revision=revision+1,access_revision=access_revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=?`,db.Now(),mb.ID,orgID,mb.Revision);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","邮箱已更新")}
		if _,err:=tx.ExecContext(ctx,`UPDATE credentials SET state='revocation_pending',updated_at=? WHERE mailbox_id=? AND purpose='mail' AND source='purelymail_app_password' AND state='active' AND id!=?`,db.Now(),mb.ID,candidateID);err!=nil{return err}
		if _,err:=tx.ExecContext(ctx,`UPDATE credentials SET state='active',updated_at=? WHERE id=? AND state='candidate'`,db.Now(),candidateID);err!=nil{return err}
		for _,candidate:=range checked{ep:=candidate.resolved;versions:=toJSON(CheckedVersions{ConnectionRevision:ep.ConnectionRevision,EndpointRevision:ep.EndpointRevision,CredentialID:candidateID,CredentialGeneration:generation});res,err:=tx.ExecContext(ctx,`UPDATE mailbox_endpoints SET credential_id=?,username=?,revision=revision+1,check_status='passed',checked_at=?,checked_versions_json=?,capabilities_json=?,last_error_code=NULL,updated_at=? WHERE mailbox_id=? AND protocol=? AND revision=?`,candidateID,ep.Username,db.Now(),versions,candidate.capabilities,db.Now(),mb.ID,ep.Protocol,ep.EndpointRevision-1);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","协议配置已经更新")}}
		child:=context.WithValue(ctx,intermediateOperationKey{},true);child=context.WithValue(child,operationStepPrefixKey{},operationStepKey(ctx,"credential.activate."))
		return completeLocalOperation(child,tx,map[string]int64{"mailboxId":mb.ID,"mailboxRevision":mb.Revision+1,"credentialId":candidateID})
	});if err!=nil{return err};s.cancelMailRequests(0,mb.ID);if s.Pool!=nil{s.Pool.InvalidateMailbox(mb.ID)}
	rows,err:=s.DB.QueryContext(ctx,`SELECT c.id,c.secret_enc FROM credentials c WHERE c.mailbox_id=? AND c.state='revocation_pending' AND EXISTS(SELECT 1 FROM provider_resources p WHERE p.credential_id=c.id AND p.owned_by_mailhearth=1 AND p.purpose='login_credential') ORDER BY c.id`,mb.ID);if err!=nil{return err}
	type oldCredential struct{id int64;sealed string};var old []oldCredential
	for rows.Next(){var item oldCredential;if err:=rows.Scan(&item.id,&item.sealed);err!=nil{rows.Close();return err};old=append(old,item)};err=rows.Err();rows.Close();if err!=nil{return err}
	for _,item:=range old{password,err:=s.Box.Open(item.sealed);if err!=nil{return err};if err:=s.remoteOperationStep(ctx,"credential.revoke."+fmtID(item.id),map[string]int64{"credentialId":item.id},func()error{return api.RevokeCredential(ctx,provider.RevokeCredentialRequest{Domain:domain,LocalPart:local,RemoteKey:"credential:"+fmtID(item.id),Secret:password})});err!=nil{return err}
		outcome,err:=s.verifySavedManagedCredential(ctx,orgID,mb,item.id,true);if err!=nil{return err};if outcome!="succeeded"{return provider.Errorf("verification_required","旧凭据仍能够认证，撤销结果需要独立核查")}
		if err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{if err:=checkOperationPermissionTx(ctx,tx,id);err!=nil{return err};if _,err:=tx.ExecContext(ctx,`UPDATE credentials SET state='revoked',secret_enc='',updated_at=? WHERE id=? AND state='revocation_pending'`,db.Now(),item.id);err!=nil{return err};_,err:=tx.ExecContext(ctx,`UPDATE provider_resources SET remote_state='missing',updated_at=? WHERE credential_id=? AND owned_by_mailhearth=1 AND purpose='login_credential'`,db.Now(),item.id);return err});err!=nil{return err}}
	return nil
}

func (s *Service) checkManagedCredentialTargets(ctx context.Context,mb *model.Mailbox,rotate bool) error {
	var eligible,owned bool
	if err:=s.DB.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM mailbox_endpoints ep JOIN mailboxes mb ON mb.id=ep.mailbox_id JOIN mail_connections mc ON mc.id=mb.connection_id LEFT JOIN credentials c ON c.id=ep.credential_id WHERE ep.mailbox_id=? AND ep.network_mode!='disabled' AND (CASE WHEN ep.network_mode='inherit' THEN json_extract(mc.protocol_defaults_json,'$.'||ep.protocol||'.enabled') ELSE json_extract(ep.network_override_json,'$.enabled') END)=1 AND (c.id IS NULL OR c.source!='entered')),EXISTS(SELECT 1 FROM credentials c JOIN provider_resources p ON p.credential_id=c.id WHERE c.mailbox_id=? AND c.purpose='mail' AND c.state IN ('active','revocation_pending') AND c.source='purelymail_app_password' AND p.purpose='login_credential' AND p.owned_by_mailhearth=1)`,mb.ID,mb.ID).Scan(&eligible,&owned);err!=nil{return err}
	if !eligible{return provider.Errorf("external_action_required","entered 凭据通过 endpoint 候选配置更新；当前没有使用 managed 凭据的启用协议")}
	if rotate && !owned{return provider.Errorf("external_action_required","没有属于 Mailhearth 的 managed 凭据可供轮换")}
	return nil
}

func (s *Service) managedEndpointsReady(ctx context.Context,mailboxID int64) (bool,error) {
	rows,err:=s.DB.QueryContext(ctx,`SELECT ep.protocol,c.id,c.source,c.state,ep.check_status,json_extract(ep.checked_versions_json,'$.connectionRevision')=mc.revision AND json_extract(ep.checked_versions_json,'$.endpointRevision')=ep.revision AND json_extract(ep.checked_versions_json,'$.credentialId')=c.id AND json_extract(ep.checked_versions_json,'$.credentialGeneration')=c.generation FROM mailbox_endpoints ep JOIN mailboxes mb ON mb.id=ep.mailbox_id JOIN mail_connections mc ON mc.id=mb.connection_id LEFT JOIN credentials c ON c.id=ep.credential_id AND c.mailbox_id=mb.id AND c.connection_id=mc.id AND c.purpose='mail' WHERE mb.id=? AND ep.network_mode!='disabled' AND (CASE WHEN ep.network_mode='inherit' THEN json_extract(mc.protocol_defaults_json,'$.'||ep.protocol||'.enabled') ELSE json_extract(ep.network_override_json,'$.enabled') END)=1`,mailboxID);if err!=nil{return false,err};defer rows.Close()
	managed:=0;ready:=true
	for rows.Next(){var protocol string;var id sql.NullInt64;var source,state sql.NullString;var status string;var versions sql.NullBool;if err:=rows.Scan(&protocol,&id,&source,&state,&status,&versions);err!=nil{return false,err};if source.Valid && source.String=="entered"{continue};managed++;if !id.Valid || !source.Valid || source.String!="purelymail_app_password" || state.String!="active" || status!="passed" || !versions.Valid || !versions.Bool{ready=false}}
	if err:=rows.Err();err!=nil{return false,err};return managed>0 && ready,nil
}

func (s *Service) executeMailboxManagement(ctx context.Context,orgID,actor int64,payload OperationPayload) (any,error) {
	if err:=validateMailboxManagementVersions(ctx,s.DB,orgID,payload);err!=nil{return nil,err}
	if payload.RemoteMailbox!=nil{
		if payload.Kind=="mailbox.deleteRemote"{plan,err:=readMailboxMutationPlan(ctx,s.DB,ctx.Value(operationContextKey{}).(string));if err!=nil{return nil,err};if err:=validateMailboxMutationProgress(ctx,s.DB,orgID,ctx.Value(operationContextKey{}).(string),plan);err!=nil{return nil,err}}else if err:=validateManagedMailboxRevision(ctx,s.DB,orgID,ctx.Value(operationContextKey{}).(string),payload.MailboxID,payload.RemoteMailbox.ExpectedRevision);err!=nil{return nil,err}
	}
	var mb *model.Mailbox;var err error
	if payload.Kind=="mailbox.create"{mb,err=s.createManagedMailbox(ctx,orgID,payload.Create)}else{mb,err=s.Mailbox(ctx,orgID,payload.MailboxID)};if err!=nil{return nil,err}
	connection,err:=s.MailConnection(ctx,orgID,mb.ConnectionID);if err!=nil{return nil,err};_,managedDomain:=SplitAddress(mb.Address);if !connection.Enabled{return nil,provider.Errorf("endpoint_disabled","连接已停用")};if !scopeContainsDomain(connection.DomainScope,managedDomain){return nil,provider.Errorf("target_constraint_failed","邮箱域名不在管理范围内")};if mb.ManagementMode!="api" || (mb.RemoteState!="present" && payload.Kind!="mailbox.deleteRemote"){return nil,provider.Errorf("external_action_required","此邮箱需要外部远程管理")}
	api,_,err:=s.connectionAdapter(ctx,orgID,mb.ConnectionID);if err!=nil{return nil,err};local,domain:=SplitAddress(mb.Address)
	switch payload.Kind{
	case "mailbox.create":
		if payload.Create.CredentialMode=="entered"{if err:=s.enteredCreationCredential(ctx,orgID,mb,payload.Create);err!=nil{return nil,err}}else if err:=s.managedCredential(ctx,orgID,mb,false);err!=nil{return nil,err}
	case "mailbox.connect","mailbox.rotate":if err:=s.managedCredential(ctx,orgID,mb,payload.Kind=="mailbox.rotate");err!=nil{return nil,err}
	case "mailbox.resetPassword":
		password,err:=s.operationPassword(ctx,"reset_password");if err!=nil{return nil,err}
		if err:=s.remoteOperationStep(ctx,"mailbox.resetPassword",map[string]string{"address":mb.Address},func()error{credential,err:=api.ResetMailboxPassword(ctx,provider.UpdateMailboxRequest{Domain:domain,LocalPart:local,NewPassword:password});if err!=nil{return err};if credential.Password!=password{return &remoteOutcomeUnknown{provider.Errorf("verification_required","服务商密码重置结果与候选密码不一致")}};return nil});err!=nil{return nil,err}
		sealed,err:=s.Box.Seal(password);if err!=nil{return nil,err};id:=ctx.Value(operationContextKey{}).(string)
		if _,err:=s.DB.ExecContext(ctx,`UPDATE operations SET claim_secret_enc=?,claim_secret_expires_at=? WHERE id=? AND secret_claimed_at IS NULL`,sealed,time.Now().UTC().Add(24*time.Hour).Format(time.RFC3339),id);err!=nil{return nil,err}
		view,err:=s.MailboxEndpoints(ctx,orgID,mb.ID);if err!=nil{return nil,err}
		for _,endpoint:=range view.Endpoints{if endpoint.NetworkMode=="disabled" || !endpoint.EffectiveNetwork.Enabled{continue};resolved,err:=s.ResolveEndpoint(ctx,orgID,mb.ID,endpoint.Protocol);if err!=nil{return nil,err};candidate:=candidateEndpoint{protocol:endpoint.Protocol,resolved:resolved};if err:=s.validateCandidateEndpoint(ctx,&candidate);err!=nil{return nil,err}}
	case "mailbox.deleteRemote":
		child:=context.WithValue(ctx,intermediateOperationKey{},true);if _,err:=s.executeMailboxMutation(child,orgID,actor,payload);err!=nil{return nil,err}
		if err:=s.remoteOperationStep(ctx,"mailbox.delete",map[string]string{"address":mb.Address},func()error{return api.DeleteMailbox(ctx,provider.DeleteMailboxRequest{Domain:domain,LocalPart:local})});err!=nil{return nil,err}
		_,readErr:=api.GetMailbox(ctx,provider.GetMailboxRequest{Domain:domain,LocalPart:local});var typed *provider.TypedError;if !errors.As(readErr,&typed) || typed.Code!="not_found"{return nil,&remoteOutcomeUnknown{provider.Errorf("verification_required","需要确认远程邮箱已经删除")}}
	}
	result:=map[string]int64{"mailboxId":mb.ID}
	if err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		if err:=validateMailboxManagementVersions(ctx,tx,orgID,payload);err!=nil{return err}
		if payload.Kind=="mailbox.create"{
			current,err:=scanMailbox(tx.QueryRowContext(ctx,mailboxSelect+` WHERE b.id=? AND b.org_id=?`,mb.ID,orgID));if err!=nil{return err}
			local,_:=SplitAddress(current.Address);now:=db.Now()
			if _,err:=tx.ExecContext(ctx,`INSERT INTO addresses(org_id,connection_id,domain_id,domain_binding_id,address_key,local_part,address,kind,mailbox_id,management_mode,sync_state,created_at,updated_at) VALUES (?,?,?,?,?,?,?,'primary',?,'api','synced',?,?)`,orgID,current.ConnectionID,current.DomainID,current.DomainBindingID,current.AddressKey,local,current.Address,current.ID,now,now);err!=nil{return err}
			if _,err:=tx.ExecContext(ctx,`INSERT INTO identities(mailbox_id,address,display_name,is_default,authorization_source,authorization_status,created_at,updated_at) VALUES (?,?,?,1,'admin','allowed',?,?)`,current.ID,current.Address,current.DisplayName,now,now);err!=nil{return err}
			if _,err:=tx.ExecContext(ctx,`UPDATE mailboxes SET status='active',revision=revision+1,access_revision=access_revision+1,updated_at=? WHERE id=? AND org_id=? AND status='suspended' AND remote_state='present'`,db.Now(),mb.ID,orgID);err!=nil{return err}
		}
		if payload.Kind=="mailbox.deleteRemote"{
			res,err:=tx.ExecContext(ctx,`UPDATE mailboxes SET status='archived',remote_state='missing',revision=revision+1,access_revision=access_revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=?`,db.Now(),mb.ID,orgID,mb.Revision);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","邮箱配置已经更新")}
			if _,err:=tx.ExecContext(ctx,`UPDATE provider_resources SET remote_state='missing',updated_at=? WHERE mailbox_id=? OR credential_id IN (SELECT id FROM credentials WHERE mailbox_id=? AND source!='entered')`,db.Now(),mb.ID,mb.ID);err!=nil{return err}
			if _,err:=tx.ExecContext(ctx,`UPDATE credentials SET state=CASE WHEN source='entered' THEN 'retired' ELSE 'revoked' END,secret_enc=CASE WHEN source='entered' THEN secret_enc ELSE '' END,updated_at=? WHERE mailbox_id=? AND purpose='mail'`,db.Now(),mb.ID);err!=nil{return err}
		}
		return completeLocalOperation(ctx,tx,result)
	});err!=nil{return nil,err}
	if payload.Kind=="mailbox.deleteRemote"{s.cancelMailRequests(0,mb.ID);if s.Pool!=nil{s.Pool.InvalidateMailbox(mb.ID)}}
	s.audit(ctx,orgID,actor,payload.Kind,"mailbox",fmtID(mb.ID),nil)
	return result,nil
}
