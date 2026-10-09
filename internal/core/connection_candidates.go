package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
	"mailhearth/internal/secrets"
)

type inheritedEndpointCandidate struct {
	mailboxID int64
	mailboxRevision int64
	protocol string
	endpointRevision int64
	username sql.NullString
	credentialID sql.NullInt64
	generation sql.NullInt64
	sealed sql.NullString
	state sql.NullString
}

func (s *Service) ConfigureConnection(ctx context.Context,orgID,actor,id int64,in UpdateConnectionInput) (*ConnectionView,error) {
	if err:=requireManagementPermission(ctx,s.DB,orgID,actor,model.PermOrgManage);err!=nil{return nil,err}
	if err:=requireResourceAvailable(ctx,s.DB,orgID,"connection:"+fmtID(id));err!=nil{return nil,err}
	c,err:=s.MailConnection(ctx,orgID,id);if err!=nil{return nil,err}
	if c.Revision!=in.ExpectedRevision{return nil,provider.Errorf("revision_conflict","连接配置已经更新")}
	if in.Label!=nil{c.Label=strings.TrimSpace(*in.Label);if utf8.RuneCountInString(c.Label)<1 || utf8.RuneCountInString(c.Label)>100{return nil,provider.Errorf("invalid","连接名称长度必须为 1–100")}}
	if in.Enabled!=nil{c.Enabled=*in.Enabled}
	if in.DomainScope!=nil{c.DomainScope,err=provider.NormalizeScope(*in.DomainScope);if err!=nil{return nil,err};if c.ProviderKind==provider.Manual && c.DomainScope.Mode!="all"{return nil,provider.Errorf("invalid","手动连接范围必须为 all")}}
	if in.ProtocolDefaults!=nil{if err:=provider.ValidateProtocolTemplates(*in.ProtocolDefaults);err!=nil{return nil,err};c.ProtocolDefaults=*in.ProtocolDefaults}
	ctx,cancel:=context.WithTimeout(ctx,30*time.Minute);defer cancel()
	var apiSeal string
	if in.APIAuth!=nil{
		input:=CreateConnectionInput{ProviderKind:c.ProviderKind,Label:c.Label,APIAuth:in.APIAuth,DomainScope:&c.DomainScope,ProtocolDefaults:&c.ProtocolDefaults}
		base,err:=validateConnectionInput(&input);if err!=nil{return nil,err}
		api,err:=provider.Create(c.ProviderKind,base,in.APIAuth.Username,in.APIAuth.APIKey);if err!=nil{return nil,err}
		result,err:=api.ValidateConnection(ctx,provider.ValidateConnectionRequest{APIBaseURL:base,Username:in.APIAuth.Username,APIKey:in.APIAuth.APIKey,Protocols:c.ProtocolDefaults});if err!=nil{return nil,err};if !result.OK{return nil,provider.Errorf("provider_auth_failed","管理认证未通过")}
		apiSeal,err=s.Box.Seal(in.APIAuth.APIKey);if err!=nil{return nil,err}
		c.APIBaseURL=&base;c.APIUsername=&in.APIAuth.Username
	}
	var inherited []inheritedEndpointCandidate
	if in.ProtocolDefaults!=nil{
		rows,err:=s.DB.QueryContext(ctx,`SELECT b.id,b.revision,ep.protocol,ep.revision,ep.username,ep.credential_id,cr.generation,cr.secret_enc,cr.state
			FROM mailboxes b JOIN mailbox_endpoints ep ON ep.mailbox_id=b.id LEFT JOIN credentials cr ON cr.id=ep.credential_id AND cr.mailbox_id=b.id AND cr.connection_id=b.connection_id AND cr.purpose='mail'
			WHERE b.org_id=? AND b.connection_id=? AND ep.network_mode='inherit' ORDER BY b.id,ep.protocol`,orgID,id);if err!=nil{return nil,err}
		for rows.Next(){var item inheritedEndpointCandidate;if err:=rows.Scan(&item.mailboxID,&item.mailboxRevision,&item.protocol,&item.endpointRevision,&item.username,&item.credentialID,&item.generation,&item.sealed,&item.state);err!=nil{rows.Close();return nil,err};inherited=append(inherited,item)}
		if err:=rows.Err();err!=nil{rows.Close();return nil,err};if err:=rows.Close();err!=nil{return nil,err}
		for i:=0;i<len(inherited);{
			mailboxID:=inherited[i].mailboxID;validationCtx,endValidation:=context.WithTimeout(ctx,120*time.Second)
			for i<len(inherited) && inherited[i].mailboxID==mailboxID{
				item:=inherited[i];i++
				network,err:=templateFor(c.ProtocolDefaults,item.protocol);if err!=nil{endValidation();return nil,err}
				if !network.Enabled{endValidation();return nil,provider.Errorf("invalid","继承该模板的协议需要明确停用")}
				if !item.credentialID.Valid || !item.sealed.Valid || item.sealed.String=="" || item.state.String!="active" || !item.username.Valid || item.username.String==""{continue}
				secret,err:=s.Box.Open(item.sealed.String);if err!=nil{endValidation();return nil,provider.Errorf("credential_decryption_failed","邮件凭据解密失败")}
				resolved:=&ResolvedEndpoint{Protocol:item.protocol,Network:network,Username:item.username.String,Secret:secret,OrgID:orgID,ConnectionID:id,MailboxID:mailboxID,ConnectionRevision:c.Revision+1,EndpointRevision:item.endpointRevision,CredentialID:item.credentialID.Int64,CredentialGeneration:item.generation.Int64}
				if err:=s.prepareEndpointNetwork(validationCtx,resolved);err!=nil{endValidation();return nil,err}
				if err:=s.validateCandidateEndpoint(validationCtx,&candidateEndpoint{protocol:item.protocol,resolved:resolved});err!=nil{endValidation();return nil,err}
			}
			endValidation()
		}
	}
	scope,err:=json.Marshal(c.DomainScope);if err!=nil{return nil,err};templates,err:=json.Marshal(c.ProtocolDefaults);if err!=nil{return nil,err}
	err=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		if err:=requireManagementPermission(ctx,tx,orgID,actor,model.PermOrgManage);err!=nil{return err}
		if err:=requireResourceAvailable(ctx,tx,orgID,"connection:"+fmtID(id));err!=nil{return err}
		if in.ProtocolDefaults!=nil{var count int;if err:=tx.QueryRowContext(ctx,`SELECT COUNT(*) FROM mailboxes b JOIN mailbox_endpoints ep ON ep.mailbox_id=b.id WHERE b.org_id=? AND b.connection_id=? AND ep.network_mode='inherit'`,orgID,id).Scan(&count);err!=nil{return err};if count!=len(inherited){return provider.Errorf("revision_conflict","受影响邮箱配置已经更新")}}
		res,err:=tx.ExecContext(ctx,`UPDATE mail_connections SET label=?,enabled=?,domain_scope_json=?,protocol_defaults_json=?,revision=revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=?`,c.Label,c.Enabled,string(scope),string(templates),db.Now(),id,orgID,in.ExpectedRevision);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","连接配置已经更新")}
		for _,item:=range inherited{
			var current bool
			if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM mailboxes b JOIN mailbox_endpoints ep ON ep.mailbox_id=b.id LEFT JOIN credentials cr ON cr.id=ep.credential_id WHERE b.id=? AND b.revision=? AND ep.protocol=? AND ep.revision=? AND ep.network_mode='inherit' AND ep.credential_id IS ? AND cr.generation IS ? AND cr.state IS ?)`,item.mailboxID,item.mailboxRevision,item.protocol,item.endpointRevision,item.credentialID,item.generation,item.state).Scan(&current);err!=nil{return err}
			if !current{return provider.Errorf("revision_conflict","受影响邮箱配置已经更新")}
		}
		if in.APIAuth!=nil{
			var previous sql.NullInt64;if err:=tx.QueryRowContext(ctx,`SELECT api_credential_id FROM mail_connections WHERE id=?`,id).Scan(&previous);err!=nil{return err}
			var generation int64;if err:=tx.QueryRowContext(ctx,`SELECT COALESCE(MAX(generation),0)+1 FROM credentials WHERE connection_id=? AND purpose='api'`,id).Scan(&generation);err!=nil{return err}
			res,err:=tx.ExecContext(ctx,`INSERT INTO credentials(org_id,connection_id,purpose,source,secret_enc,generation,state,hint,created_at,updated_at) VALUES (?,?,'api','entered',?,?,'active',?,?,?)`,orgID,id,apiSeal,generation,secrets.Hint(in.APIAuth.APIKey),db.Now(),db.Now());if err!=nil{return err};credentialID,err:=res.LastInsertId();if err!=nil{return err}
			if _,err:=tx.ExecContext(ctx,`UPDATE mail_connections SET api_base_url=?,api_username=?,api_credential_id=?,last_api_check_at=?,last_api_check_status='passed',last_api_error_code=NULL WHERE id=?`,c.APIBaseURL,c.APIUsername,credentialID,db.Now(),id);err!=nil{return err}
			if previous.Valid{if _,err:=tx.ExecContext(ctx,`UPDATE credentials SET state='retired',updated_at=? WHERE id=?`,db.Now(),previous.Int64);err!=nil{return err}}
		}
		if _,err:=tx.ExecContext(ctx,`UPDATE mailbox_endpoints SET check_status='stale' WHERE mailbox_id IN (SELECT id FROM mailboxes WHERE connection_id=?) AND check_status='passed'`,id);err!=nil{return err}
		if in.DomainScope!=nil && c.DomainScope.Mode=="selected"{
			domains,err:=json.Marshal(c.DomainScope.Domains);if err!=nil{return err}
			for _,table:=range []string{"domain_bindings","mailboxes","addresses"}{
				state:="";if table!="addresses"{state=",remote_state='external'"}
				if _,err:=tx.ExecContext(ctx,`UPDATE `+table+` SET management_mode='external'`+state+`,revision=revision+1,updated_at=? WHERE connection_id=? AND domain_id IN (SELECT id FROM domains WHERE name NOT IN (SELECT value FROM json_each(?)))`,db.Now(),id,string(domains));err!=nil{return err}
			}
			if _,err:=tx.ExecContext(ctx,`UPDATE provider_resources SET remote_state='external',updated_at=? WHERE connection_id=? AND (domain_binding_id IN (SELECT id FROM domain_bindings WHERE connection_id=? AND management_mode='external') OR mailbox_id IN (SELECT id FROM mailboxes WHERE connection_id=? AND management_mode='external') OR address_id IN (SELECT id FROM addresses WHERE connection_id=? AND management_mode='external'))`,db.Now(),id,id,id,id);err!=nil{return err}
		}
		return completeLocalOperation(ctx,tx,struct{ConnectionID int64 `json:"connectionId"`;Revision int64 `json:"revision"`}{id,c.Revision+1})
	});if err!=nil{return nil,err}
	if s.Pool!=nil{s.Pool.InvalidateConnection(id)}
	if err:=s.cancelConnectionMailRequests(id);err!=nil{return nil,err}
	s.audit(ctx,orgID,actor,"connection.configure","connection",fmtID(id),nil)
	return s.MailConnection(ctx,orgID,id)
}
