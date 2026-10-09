package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"mailhearth/internal/db"
	"mailhearth/internal/provider"
	"mailhearth/internal/secrets"
)

type CheckedVersions struct {
	ConnectionRevision int64 `json:"connectionRevision"`
	EndpointRevision int64 `json:"endpointRevision"`
	CredentialID int64 `json:"credentialId"`
	CredentialGeneration int64 `json:"credentialGeneration"`
}

type EndpointCredentialView struct {
	ID int64 `json:"id"`
	Configured bool `json:"configured"`
	Hint string `json:"hint"`
	Generation int64 `json:"generation"`
	State string `json:"state"`
}

type EndpointView struct {
	Protocol string `json:"protocol"`
	NetworkMode string `json:"networkMode"`
	NetworkOverride *provider.ProtocolTemplate `json:"networkOverride"`
	EffectiveNetwork provider.ProtocolTemplate `json:"effectiveNetwork"`
	Username *string `json:"username"`
	Credential *EndpointCredentialView `json:"credential"`
	Revision int64 `json:"revision"`
	CheckStatus string `json:"checkStatus"`
	CheckedAt *string `json:"checkedAt"`
	CheckedVersions *CheckedVersions `json:"checkedVersions"`
	Capabilities json.RawMessage `json:"capabilities"`
	LastErrorCode *string `json:"lastErrorCode"`
}

type MailboxEndpointsView struct {
	MailboxID int64 `json:"mailboxId"`
	Revision int64 `json:"revision"`
	ConnectionID int64 `json:"connectionId"`
	Endpoints []EndpointView `json:"endpoints"`
}

func (s *Service) MailboxEndpoints(ctx context.Context,orgID,mailboxID int64) (*MailboxEndpointsView,error) {
	mb,err:=s.Mailbox(ctx,orgID,mailboxID);if err!=nil{return nil,err}
	c,err:=s.MailConnection(ctx,orgID,mb.ConnectionID);if err!=nil{return nil,err}
	rows,err:=s.DB.QueryContext(ctx,`SELECT ep.protocol,ep.network_mode,ep.network_override_json,ep.username,ep.revision,ep.check_status,ep.checked_at,ep.checked_versions_json,ep.capabilities_json,ep.last_error_code,
		cr.id,cr.hint,cr.generation,cr.state,CASE WHEN cr.secret_enc!='' AND cr.state='active' THEN 1 ELSE 0 END
		FROM mailbox_endpoints ep LEFT JOIN credentials cr ON cr.id=ep.credential_id AND cr.mailbox_id=ep.mailbox_id AND cr.connection_id=? AND cr.purpose='mail'
		WHERE ep.mailbox_id=? ORDER BY CASE ep.protocol WHEN 'imap' THEN 1 WHEN 'smtp' THEN 2 ELSE 3 END`,c.ID,mailboxID)
	if err!=nil{return nil,err};defer rows.Close()
	out:=&MailboxEndpointsView{MailboxID:mailboxID,Revision:mb.Revision,ConnectionID:c.ID,Endpoints:[]EndpointView{}}
	for rows.Next(){
		var e EndpointView;var network,user,at,versions,code,hint,state sql.NullString;var id,generation sql.NullInt64;var configured bool;var capabilities string
		if err:=rows.Scan(&e.Protocol,&e.NetworkMode,&network,&user,&e.Revision,&e.CheckStatus,&at,&versions,&capabilities,&code,&id,&hint,&generation,&state,&configured);err!=nil{return nil,err}
		e.Capabilities=json.RawMessage(capabilities)
		e.Username,e.CheckedAt,e.LastErrorCode=nullStr(user),nullStr(at),nullStr(code)
		e.EffectiveNetwork,err=templateFor(c.ProtocolDefaults,e.Protocol);if err!=nil{return nil,err}
		if network.Valid{var override provider.ProtocolTemplate;if err:=json.Unmarshal([]byte(network.String),&override);err!=nil{return nil,err};e.NetworkOverride=&override;if e.NetworkMode=="override"{e.EffectiveNetwork=override}}
		if e.NetworkMode=="disabled"{e.EffectiveNetwork=provider.ProtocolTemplate{}}
		if id.Valid{e.Credential=&EndpointCredentialView{ID:id.Int64,Configured:configured,Hint:hint.String,Generation:generation.Int64,State:state.String}}
		if versions.Valid{
			var checked CheckedVersions;if err:=json.Unmarshal([]byte(versions.String),&checked);err!=nil{return nil,err};e.CheckedVersions=&checked
			if checked.ConnectionRevision!=c.Revision || checked.EndpointRevision!=e.Revision || checked.CredentialID!=id.Int64 || checked.CredentialGeneration!=generation.Int64{e.CheckStatus="stale"}
		}
		out.Endpoints=append(out.Endpoints,e)
	}
	return out,rows.Err()
}

type UpdateEndpointsInput struct {
	RequestID string `json:"requestId"`
	ExpectedRevision int64 `json:"expectedRevision"`
	Credentials []EnteredCredential `json:"credentials"`
	Endpoints EndpointInputs `json:"endpoints"`
}

func (s *Service) UpdateEndpoints(ctx context.Context,orgID,actor,mailboxID int64,in UpdateEndpointsInput) (*MailboxEndpointsView,error) {
	if _,ok:=ctx.Value(operationContextKey{}).(string);!ok{
		_,err:=s.runLocalManagementOperation(ctx,orgID,actor,in.RequestID,OperationPayload{Kind:"mailbox.endpoints",MailboxID:mailboxID,Endpoints:&in});if err!=nil{return nil,err};return s.MailboxEndpoints(ctx,orgID,mailboxID)
	}
	mb,err:=s.Mailbox(ctx,orgID,mailboxID);if err!=nil{return nil,err}
	if mb.Revision!=in.ExpectedRevision{return nil,provider.Errorf("revision_conflict","邮箱配置已经更新")}
	c,err:=s.MailConnection(ctx,orgID,mb.ConnectionID);if err!=nil{return nil,err};if !c.Enabled{return nil,provider.Errorf("endpoint_disabled","连接已停用")}
	ctx,cancel:=context.WithTimeout(ctx,120*time.Second);defer cancel()
	items,err:=s.candidateEndpoints(ctx,orgID,mailboxID,c,in.Credentials,in.Endpoints);if err!=nil{return nil,err}
	for i:=range items{if err:=s.validateCandidateEndpoint(ctx,&items[i]);err!=nil{return nil,err}}
	sealed:=map[string]string{}
	for _,credential:=range in.Credentials{value,err:=s.Box.Seal(credential.Secret);if err!=nil{return nil,err};sealed[credential.ClientKey]=value}
	err=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		res,err:=tx.ExecContext(ctx,`UPDATE mail_connections SET revision=revision WHERE id=? AND org_id=? AND revision=? AND enabled=1`,c.ID,orgID,c.Revision);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","连接配置已经更新")}
		res,err=tx.ExecContext(ctx,`UPDATE mailboxes SET revision=revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=?`,db.Now(),mailboxID,orgID,in.ExpectedRevision);if err!=nil{return err};n,err=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","邮箱配置已经更新")}
		now:=db.Now();var generation int64
		if err:=tx.QueryRowContext(ctx,`SELECT COALESCE(MAX(generation),0)+1 FROM credentials WHERE mailbox_id=? AND purpose='mail'`,mailboxID).Scan(&generation);err!=nil{return err}
		credentialIDs:=map[string]int64{}
		for _,credential:=range in.Credentials{
			res,err:=tx.ExecContext(ctx,`INSERT INTO credentials(org_id,connection_id,mailbox_id,purpose,source,secret_enc,generation,state,hint,created_at,updated_at) VALUES (?,?,?,'mail','entered',?,?,'active',?,?,?)`,orgID,c.ID,mailboxID,sealed[credential.ClientKey],generation,secrets.Hint(credential.Secret),now,now);if err!=nil{return err}
			id,err:=res.LastInsertId();if err!=nil{return err};credentialIDs[credential.ClientKey]=id
		}
		for _,item:=range items{
			var revision int64
			if err:=tx.QueryRowContext(ctx,`SELECT revision FROM mailbox_endpoints WHERE mailbox_id=? AND protocol=?`,mailboxID,item.protocol).Scan(&revision);err!=nil{return err}
			var network,user,credentialID,checkedAt,versions any;status:="never";capabilities:="{}"
			if item.resolved!=nil{
				cid:=item.resolved.CredentialID;gen:=item.resolved.CredentialGeneration
				if item.input.Credential.ClientKey!=""{cid=credentialIDs[item.input.Credential.ClientKey];gen=generation}
				var current bool
				if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM credentials WHERE id=? AND mailbox_id=? AND connection_id=? AND purpose='mail' AND state='active' AND generation=?)`,cid,mailboxID,c.ID,gen).Scan(&current);err!=nil{return err};if !current{return provider.Errorf("revision_conflict","邮件凭据已经更新")}
				user,credentialID=item.input.Username,cid
				if item.input.NetworkMode=="override"{body,err:=json.Marshal(item.input.Network);if err!=nil{return err};network=string(body)}
				body,err:=json.Marshal(CheckedVersions{c.Revision,revision+1,cid,gen});if err!=nil{return err}
				versions,checkedAt,status,capabilities=string(body),now,"passed",item.capabilities
			}
			if _,err:=tx.ExecContext(ctx,`UPDATE mailbox_endpoints SET network_mode=?,network_override_json=?,username=?,credential_id=?,revision=revision+1,check_status=?,checked_at=?,checked_versions_json=?,capabilities_json=?,last_error_code=NULL,updated_at=? WHERE mailbox_id=? AND protocol=?`,item.input.NetworkMode,network,user,credentialID,status,checkedAt,versions,capabilities,now,mailboxID,item.protocol);err!=nil{return err}
		}
		if _,err:=tx.ExecContext(ctx,`UPDATE credentials SET state='retired',updated_at=? WHERE mailbox_id=? AND purpose='mail' AND source='entered' AND state='active' AND NOT EXISTS(SELECT 1 FROM mailbox_endpoints WHERE credential_id=credentials.id)`,now,mailboxID);err!=nil{return err}
		return completeLocalOperation(ctx,tx,struct{MailboxID int64 `json:"mailboxId"`;Revision int64 `json:"revision"`}{mailboxID,in.ExpectedRevision+1})
	});if err!=nil{return nil,err}
	if s.Pool!=nil{s.Pool.InvalidateMailbox(mailboxID)}
	s.cancelMailRequests(0,mailboxID)
	s.audit(ctx,orgID,actor,"mailbox.endpoints","mailbox",fmtID(mailboxID),nil)
	return s.MailboxEndpoints(ctx,orgID,mailboxID)
}
