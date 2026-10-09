package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"mailhearth/internal/db"
	"mailhearth/internal/mailproto/mailops"
	"mailhearth/internal/mailproto/sieve"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
	"mailhearth/internal/secrets"
)

type EnteredCredential struct {
	ClientKey string `json:"clientKey"`
	Secret string `json:"secret"`
}

type EndpointCredentialRef struct {
	ClientKey string `json:"clientKey,omitempty"`
	CredentialID int64 `json:"credentialId,omitempty"`
}

type EndpointInput struct {
	NetworkMode string `json:"networkMode"`
	Network *provider.ProtocolTemplate `json:"network,omitempty"`
	AuthMode string `json:"authMode,omitempty"`
	Username string `json:"username,omitempty"`
	Credential *EndpointCredentialRef `json:"credential,omitempty"`
}

type EndpointInputs struct {
	IMAP *EndpointInput `json:"imap"`
	SMTP *EndpointInput `json:"smtp"`
	ManageSieve *EndpointInput `json:"managesieve"`
}

type AttachMailboxInput struct {
	RequestID string `json:"requestId"`
	Mode string `json:"mode"`
	ConnectionID int64 `json:"connectionId"`
	Kind string `json:"kind"`
	Address string `json:"address"`
	DisplayName string `json:"displayName"`
	OwnerMemberID *int64 `json:"ownerMemberId"`
	CredentialMode string `json:"credentialMode"`
	Credentials []EnteredCredential `json:"credentials"`
	Endpoints EndpointInputs `json:"endpoints"`
	SentCopyMode string `json:"sentCopyMode"`
	FolderMapping model.FolderMapping `json:"folderMapping"`
}

type candidateEndpoint struct {
	protocol string
	input *EndpointInput
	resolved *ResolvedEndpoint
	capabilities string
}

func (s *Service) candidateEndpoints(ctx context.Context,orgID,mailboxID int64,c *ConnectionView,credentials []EnteredCredential,in EndpointInputs) ([]candidateEndpoint,error) {
	if in.IMAP==nil || in.SMTP==nil || in.ManageSieve==nil{return nil,provider.Errorf("invalid","必须提供全部三个协议配置")}
	entered:=map[string]string{}
	for _,credential:=range credentials{if credential.ClientKey=="" || credential.Secret=="" || strings.ContainsAny(credential.ClientKey,"\x00\r\n"){return nil,provider.Errorf("invalid","凭据名称与秘密不能为空")};if _,ok:=entered[credential.ClientKey];ok{return nil,provider.Errorf("invalid","clientKey 必须唯一")};entered[credential.ClientKey]=credential.Secret}
	items:=[]candidateEndpoint{{protocol:provider.ProtocolIMAP,input:in.IMAP},{protocol:provider.ProtocolSMTP,input:in.SMTP},{protocol:provider.ProtocolManageSieve,input:in.ManageSieve}}
	used:=map[string]bool{}
	for i:=range items{
		item:=&items[i];ep:=item.input
		if ep.NetworkMode=="disabled"{if ep.Network!=nil || ep.AuthMode!="" || ep.Username!="" || ep.Credential!=nil{return nil,provider.Errorf("invalid","停用协议只能包含 networkMode")};if item.protocol==provider.ProtocolIMAP && mailboxID==0{return nil,provider.Errorf("invalid","登记邮箱必须启用 IMAP")};continue}
		if ep.AuthMode!="password"{return nil,provider.Errorf("unsupported_auth_mode","认证方式必须为 password")}
		if !validProtocolUsername(ep.Username){return nil,provider.Errorf("invalid","协议用户名无效")}
		var network provider.ProtocolTemplate
		if ep.NetworkMode=="inherit"{if ep.Network!=nil{return nil,provider.Errorf("invalid","inherit 不能包含 network")};var err error;network,err=templateFor(c.ProtocolDefaults,item.protocol);if err!=nil{return nil,err}}else if ep.NetworkMode=="override"{if ep.Network==nil{return nil,provider.Errorf("invalid","override 必须包含完整 network")};network=*ep.Network}else{return nil,provider.Errorf("invalid","networkMode 无效")}
		if !network.Enabled{return nil,provider.Errorf("invalid","启用协议需要启用的网络模板")}
		if err:=provider.ValidateProtocolTemplate(network);err!=nil{return nil,err}
		ref:=ep.Credential;if ref==nil || (ref.ClientKey=="" && ref.CredentialID==0) || (ref.ClientKey!="" && ref.CredentialID!=0){return nil,provider.Errorf("invalid","credential 必须引用一份凭据")}
		resolved:=&ResolvedEndpoint{Protocol:item.protocol,Network:network,Username:ep.Username,OrgID:orgID,ConnectionID:c.ID,MailboxID:mailboxID,ConnectionRevision:c.Revision,EndpointRevision:-1,CredentialGeneration:1}
		if ref.ClientKey!=""{secret,ok:=entered[ref.ClientKey];if !ok{return nil,provider.Errorf("invalid","clientKey 不属于本次请求")};resolved.Secret=secret;used[ref.ClientKey]=true}else{
			if mailboxID==0{return nil,provider.Errorf("invalid","新邮箱不能引用已有凭据")}
			var sealed string
			if err:=s.DB.QueryRowContext(ctx,`SELECT secret_enc,generation FROM credentials WHERE id=? AND connection_id=? AND mailbox_id=? AND purpose='mail' AND state='active'`,ref.CredentialID,c.ID,mailboxID).Scan(&sealed,&resolved.CredentialGeneration);err!=nil{if db.IsNotFound(err){return nil,ErrForbidden};return nil,err}
			secret,err:=s.Box.Open(sealed);if err!=nil{return nil,provider.Errorf("credential_decryption_failed","邮件凭据解密失败")};resolved.Secret=secret;resolved.CredentialID=ref.CredentialID
		}
		if err:=s.prepareEndpointNetwork(ctx,resolved);err!=nil{return nil,err}
		item.resolved=resolved
	}
	if len(used)!=len(entered){return nil,provider.Errorf("invalid","每份新凭据必须被协议引用")}
	return items,nil
}

func (s *Service) validateCandidateEndpoint(ctx context.Context,item *candidateEndpoint) error {
	if item.resolved==nil{return nil};ep:=item.resolved
	ctx,cancel:=context.WithTimeout(ctx,30*time.Second);defer cancel()
	var capabilities any=struct{}{}
	switch item.protocol{
	case provider.ProtocolIMAP:
		if s.Pool==nil{return provider.Errorf("internal","IMAP 连接管理器未启动")}
		cred:=ep.IMAPCredential()
		conn,err:=s.Pool.GetFresh(ctx,cred);if err!=nil{return err};defer s.Pool.Put(conn)
		if _,err:=mailops.ListFolders(ctx,conn);err!=nil{return err}
		capabilities=conn.C.Caps()
	case provider.ProtocolSMTP:
		if err:=mailops.ValidateSMTP(ctx,mailops.SMTPConfig{Addr:ep.Address(),TLSMode:ep.Network.TLSMode,TLSConfig:ep.TLSConfig,Dialer:ep.Dialer,Timeout:30*time.Second},ep.Username,ep.Secret);err!=nil{return err}
	case provider.ProtocolManageSieve:
		client,err:=sieve.DialWithDialer(ctx,ep.Address(),ep.Network.TLSMode,ep.TLSConfig,ep.Dialer);if err!=nil{return err};defer client.Close()
		if err:=client.Authenticate(ep.Username,ep.Secret);err!=nil{return err}
		if _,err:=client.ListScripts();err!=nil{return err};capabilities=client.Capabilities()
	}
	body,err:=json.Marshal(capabilities);if err!=nil{return err};item.capabilities=string(body);return nil
}

func (s *Service) AttachMailbox(ctx context.Context,orgID,actor int64,in AttachMailboxInput) (*model.Mailbox,error) {
	if _,ok:=ctx.Value(operationContextKey{}).(string);!ok{
		op,err:=s.runLocalManagementOperation(ctx,orgID,actor,in.RequestID,OperationPayload{Kind:"mailbox.attach",ConnectionID:in.ConnectionID,Attach:&in});if err!=nil{return nil,err}
		var result struct{MailboxID int64 `json:"mailboxId"`};if err:=json.Unmarshal(op.Result,&result);err!=nil{return nil,err};if result.MailboxID<1{return nil,provider.Errorf("internal","邮箱登记结果缺少 mailboxId")};return s.Mailbox(ctx,orgID,result.MailboxID)
	}
	if err:=validateFolderMappingInput(in.FolderMapping);err!=nil{return nil,err}
	if in.Mode!="attach" || in.CredentialMode!="entered"{return nil,provider.Errorf("invalid","登记已有邮箱必须选择 attach 和 entered")}
	if in.Kind!=model.MailboxPersonal && in.Kind!=model.MailboxShared{return nil,provider.Errorf("invalid","邮箱类型无效")}
	if in.Kind==model.MailboxShared && in.OwnerMemberID!=nil{return nil,provider.Errorf("invalid","共享邮箱不能设置 owner")}
	if in.OwnerMemberID!=nil{if _,err:=s.Member(ctx,orgID,*in.OwnerMemberID);err!=nil{return nil,err}}
	if in.SentCopyMode==""{in.SentCopyMode="append"};if in.SentCopyMode!="append" && in.SentCopyMode!="server"{return nil,provider.Errorf("invalid","sentCopyMode 无效")}
	c,err:=s.MailConnection(ctx,orgID,in.ConnectionID);if err!=nil{return nil,err};if !c.Enabled{return nil,provider.Errorf("endpoint_disabled","连接已停用")}
	address,key,err:=canonicalMailboxAddress(c.ProviderKind,in.Address);if err!=nil{return nil,err};local,domain:=SplitAddress(address)
	ctx,cancel:=context.WithTimeout(ctx,120*time.Second);defer cancel()
	items,err:=s.candidateEndpoints(ctx,orgID,0,c,in.Credentials,in.Endpoints);if err!=nil{return nil,err}
	for i:=range items{if err:=s.validateCandidateEndpoint(ctx,&items[i]);err!=nil{return nil,err}}
	if err:=s.validateCandidateFolderMapping(ctx,items,in.FolderMapping);err!=nil{return nil,err}
	sealed:=map[string]string{}
	for _,credential:=range in.Credentials{enc,err:=s.Box.Seal(credential.Secret);if err!=nil{return nil,err};sealed[credential.ClientKey]=enc}
	var mailboxID int64
	err=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		res,err:=tx.ExecContext(ctx,`UPDATE mail_connections SET revision=revision WHERE id=? AND org_id=? AND revision=? AND enabled=1`,c.ID,orgID,c.Revision);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","连接已经更新")}
		var existing int64;err=tx.QueryRowContext(ctx,`SELECT id FROM mailboxes WHERE connection_id=? AND address_key=?`,c.ID,key).Scan(&existing);if err==nil{return provider.Errorf("invalid","该连接已登记此邮箱")};if !db.IsNotFound(err){return err}
		now:=db.Now()
		if _,err:=tx.ExecContext(ctx,`INSERT INTO domains(org_id,name,created_at,updated_at) VALUES (?,?,?,?) ON CONFLICT(org_id,name) DO NOTHING`,orgID,domain,now,now);err!=nil{return err}
		var domainID int64;if err:=tx.QueryRowContext(ctx,`SELECT id FROM domains WHERE org_id=? AND name=?`,orgID,domain).Scan(&domainID);err!=nil{return err}
		mapping,err:=json.Marshal(in.FolderMapping);if err!=nil{return err}
		res,err=tx.ExecContext(ctx,`INSERT INTO mailboxes(org_id,connection_id,domain_id,kind,address,address_key,display_name,owner_member_id,status,management_mode,remote_state,sent_copy_mode,folder_mapping_json,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,'active','external','external',?,?,?,?)`,orgID,c.ID,domainID,in.Kind,address,key,strings.TrimSpace(in.DisplayName),in.OwnerMemberID,in.SentCopyMode,string(mapping),now,now);if err!=nil{return err};mailboxID,err=res.LastInsertId();if err!=nil{return err}
		credentialIDs:=map[string]int64{}
		for _,credential:=range in.Credentials{
			res,err:=tx.ExecContext(ctx,`INSERT INTO credentials(org_id,connection_id,mailbox_id,purpose,source,secret_enc,generation,state,hint,created_at,updated_at) VALUES (?,?,?,'mail','entered',?,1,'active',?,?,?)`,orgID,c.ID,mailboxID,sealed[credential.ClientKey],secrets.Hint(credential.Secret),now,now);if err!=nil{return err};id,err:=res.LastInsertId();if err!=nil{return err};credentialIDs[credential.ClientKey]=id
		}
		for _,item:=range items{
			var network,user,credentialID,checkedVersions,checkedAt any;status:="never";capabilities:="{}"
			if item.resolved!=nil{
				user=item.input.Username;cid:=credentialIDs[item.input.Credential.ClientKey];credentialID=cid
				if item.input.NetworkMode=="override"{body,err:=json.Marshal(item.input.Network);if err!=nil{return err};network=string(body)}
				versions,err:=json.Marshal(struct{ConnectionRevision int64 `json:"connectionRevision"`;EndpointRevision int64 `json:"endpointRevision"`;CredentialID int64 `json:"credentialId"`;CredentialGeneration int64 `json:"credentialGeneration"`}{c.Revision,1,cid,1});if err!=nil{return err};checkedVersions=string(versions);checkedAt=now;status="passed";capabilities=item.capabilities
			}
			if _,err:=tx.ExecContext(ctx,`INSERT INTO mailbox_endpoints(mailbox_id,protocol,network_mode,network_override_json,username,credential_id,revision,check_status,checked_at,checked_versions_json,capabilities_json,created_at,updated_at) VALUES (?,?,?,?,?,?,1,?,?,?,?,?,?)`,mailboxID,item.protocol,item.input.NetworkMode,network,user,credentialID,status,checkedAt,checkedVersions,capabilities,now,now);err!=nil{return err}
		}
		if _,err:=tx.ExecContext(ctx,`INSERT INTO addresses(org_id,connection_id,address_key,domain_id,local_part,address,kind,mailbox_id,management_mode,sync_state,created_at,updated_at) VALUES (?,?,?,?,?,?,'primary',?,'external','unknown',?,?)`,orgID,c.ID,key,domainID,local,address,mailboxID,now,now);err!=nil{return err}
		if _,err=tx.ExecContext(ctx,`INSERT INTO identities(mailbox_id,address,display_name,is_default,authorization_source,authorization_status,created_at) VALUES (?,?,?,1,'admin','allowed',?)`,mailboxID,address,in.DisplayName,now);err!=nil{return err}
		return completeLocalOperation(ctx,tx,struct{MailboxID int64 `json:"mailboxId"`}{mailboxID})
	});if err!=nil{return nil,err}
	s.audit(ctx,orgID,actor,"mailbox.attach","mailbox",fmtID(mailboxID),nil)
	return s.Mailbox(ctx,orgID,mailboxID)
}
