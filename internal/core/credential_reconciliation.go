package core

import (
	"context"
	"database/sql"
	"strings"
	"strconv"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

func (s *Service) reconciliationMailbox(ctx context.Context,orgID int64,id string,payload OperationPayload) (*model.Mailbox,error) {
	mailboxID,err:=memberCreationMailboxID(ctx,s.DB,id,payload.MailboxID);if err!=nil{return nil,err};if mailboxID<1{return nil,provider.Errorf("verification_required","远程核查需要已预留的邮箱 ID")};return s.Mailbox(ctx,orgID,mailboxID)
}

func (s *Service) verifySavedManagedCredential(ctx context.Context,orgID int64,mb *model.Mailbox,credentialID int64,revocation bool) (string,error) {
	c,err:=s.MailConnection(ctx,orgID,mb.ConnectionID);if err!=nil{return "",err};if c.ProviderKind!=provider.Purelymail{return "",provider.Errorf("verification_required","该服务商的 managed 凭据核查尚未经过远程核验")}
	var sealed,state string;var generation int64
	if err:=s.DB.QueryRowContext(ctx,`SELECT c.secret_enc,c.generation,c.state FROM credentials c WHERE c.id=? AND c.org_id=? AND c.mailbox_id=? AND c.connection_id=? AND c.source='purelymail_app_password' AND c.purpose='mail' AND EXISTS(SELECT 1 FROM provider_resources p WHERE p.credential_id=c.id AND p.owned_by_mailhearth=1 AND p.purpose='login_credential')`,credentialID,orgID,mb.ID,c.ID).Scan(&sealed,&generation,&state);err!=nil{if db.IsNotFound(err){return "",provider.Errorf("verification_required","缺少可核查的 owned managed 凭据")};return "",err}
	if sealed=="" || (revocation && state!="revocation_pending"){return "",provider.Errorf("verification_required","凭据秘密或撤销状态不足以独立核验")}
	secret,err:=s.Box.Open(sealed);if err!=nil{return "",provider.Errorf("credential_decryption_failed","待核查凭据解密失败")}
	view,err:=s.MailboxEndpoints(ctx,orgID,mb.ID);if err!=nil{return "",err};tested:=0;accepted:=0;rejected:=0
	for _,endpoint:=range view.Endpoints{
		if endpoint.NetworkMode=="disabled" || !endpoint.EffectiveNetwork.Enabled{continue}
		if endpoint.Credential!=nil{var source string;if err:=s.DB.QueryRowContext(ctx,`SELECT source FROM credentials WHERE id=?`,endpoint.Credential.ID).Scan(&source);err!=nil{return "",err};if source=="entered"{continue}}
		resolved:=&ResolvedEndpoint{Protocol:endpoint.Protocol,Network:endpoint.EffectiveNetwork,Username:mb.Address,Secret:secret,OrgID:orgID,ConnectionID:c.ID,MailboxID:mb.ID,ConnectionRevision:c.Revision,EndpointRevision:endpoint.Revision,CredentialID:credentialID,CredentialGeneration:generation}
		if err:=s.prepareEndpointNetwork(ctx,resolved);err!=nil{return "",err};candidate:=candidateEndpoint{protocol:endpoint.Protocol,resolved:resolved};err:=s.validateCandidateEndpoint(ctx,&candidate);tested++
		if err==nil{accepted++;continue};if operationErrorCode(err)=="mailbox_auth_failed"{rejected++;continue};return "",err
	}
	if tested==0{return "",provider.Errorf("verification_required","没有可以独立认证核查的 managed 协议")}
	if revocation{if rejected==tested{return "succeeded",nil};if accepted==tested{return "failed",nil};return "",provider.Errorf("verification_required","各协议对旧凭据的认证结果不一致")}
	if accepted==tested{return "succeeded",nil};return "",provider.Errorf("verification_required","已保存的候选凭据尚未通过全部协议认证核查")
}

func (s *Service) reconcileCredentialStep(ctx context.Context,orgID int64,id,key string,payload OperationPayload) (string,error) {
	if payload.Offboard!=nil{for _,mailbox:=range payload.Offboard.Mailboxes{if strings.HasPrefix(key,"mailbox."+fmtID(mailbox.ID)+"."){payload.MailboxID=mailbox.ID;break}}}
	mb,err:=s.reconciliationMailbox(ctx,orgID,id,payload);if err!=nil{return "",err}
	if strings.HasSuffix(key,"credential.create"){
		var credentialID sql.NullInt64;if err:=s.DB.QueryRowContext(ctx,`SELECT json_extract(result_json,'$.credentialId') FROM operation_steps WHERE operation_id=? AND step_key=?`,id,key).Scan(&credentialID);err!=nil{return "",err};if !credentialID.Valid{return "",provider.Errorf("verification_required","服务商凭据响应未保存，需要管理员核查服务商侧的候选凭据")}
		return s.verifySavedManagedCredential(ctx,orgID,mb,credentialID.Int64,false)
	}
	position:=strings.LastIndex(key,"credential.revoke.");if position<0{return "",provider.Errorf("verification_required","凭据步骤缺少独立核验信息")};credentialID,err:=strconv.ParseInt(key[position+len("credential.revoke."):],10,64);if err!=nil || credentialID<1{return "",provider.Errorf("invalid","撤销凭据 ID 无效")}
	return s.verifySavedManagedCredential(ctx,orgID,mb,credentialID,true)
}

func (s *Service) reconcilePrimaryPassword(ctx context.Context,orgID int64,id string,payload OperationPayload) (string,error) {
	mb,err:=s.reconciliationMailbox(ctx,orgID,id,payload);if err!=nil{return "",err};c,err:=s.MailConnection(ctx,orgID,mb.ConnectionID);if err!=nil{return "",err};if c.ProviderKind!=provider.Purelymail{return "",provider.Errorf("verification_required","主密码认证核查需要完成服务商的远程核验")}
	var sealed string;if err:=s.DB.QueryRowContext(ctx,`SELECT secret_enc FROM operation_private WHERE operation_id=? AND field_key='reset_password'`,id).Scan(&sealed);err!=nil{return "",err};secret,err:=s.Box.Open(sealed);if err!=nil{return "",provider.Errorf("credential_decryption_failed","主密码候选解密失败")}
	view,err:=s.MailboxEndpoints(ctx,orgID,mb.ID);if err!=nil{return "",err}
	for _,endpoint:=range view.Endpoints{if endpoint.Protocol!=provider.ProtocolIMAP || endpoint.NetworkMode=="disabled" || !endpoint.EffectiveNetwork.Enabled{continue};resolved:=&ResolvedEndpoint{Protocol:provider.ProtocolIMAP,Network:endpoint.EffectiveNetwork,Username:mb.Address,Secret:secret,OrgID:orgID,ConnectionID:c.ID,MailboxID:mb.ID,ConnectionRevision:c.Revision,EndpointRevision:endpoint.Revision}
		if err:=s.prepareEndpointNetwork(ctx,resolved);err!=nil{return "",err};candidate:=candidateEndpoint{protocol:provider.ProtocolIMAP,resolved:resolved};err:=s.validateCandidateEndpoint(ctx,&candidate);if err==nil{return "succeeded",nil};if operationErrorCode(err)=="mailbox_auth_failed"{return "failed",nil};return "",err
	}
	return "",provider.Errorf("verification_required","主密码核查需要启用的 IMAP 网络配置")
}
