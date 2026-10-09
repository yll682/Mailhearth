package core

import (
	"context"
	"database/sql"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
	"mailhearth/internal/secrets"
)

func (s *Service) enteredCreationCredential(ctx context.Context,orgID int64,mb *model.Mailbox,in *ManagedMailboxCreateInput) error {
	id:=ctx.Value(operationContextKey{}).(string)
	var saved bool
	if err:=s.DB.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND step_key=? AND status='succeeded')`,id,operationStepKey(ctx,"credential.entered")).Scan(&saved);err!=nil{return err};if saved{return nil}
	c,err:=s.MailConnection(ctx,orgID,mb.ConnectionID);if err!=nil{return err}
	items,err:=s.candidateEndpoints(ctx,orgID,mb.ID,c,in.Credentials,in.Endpoints);if err!=nil{return err}
	for i:=range items{if err:=s.validateCandidateEndpoint(ctx,&items[i]);err!=nil{return err}}
	if err:=s.validateCandidateFolderMapping(ctx,items,in.FolderMapping);err!=nil{return err}
	sealed:=map[string]string{}
	for _,credential:=range in.Credentials{value,err:=s.Box.Seal(credential.Secret);if err!=nil{return err};sealed[credential.ClientKey]=value}
	return s.DB.Tx(ctx,func(tx *sql.Tx)error{
		if err:=checkOperationPermissionTx(ctx,tx,id);err!=nil{return err}
		var current bool;if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM mail_connections WHERE id=? AND org_id=? AND revision=? AND enabled=1)`,c.ID,orgID,c.Revision).Scan(&current);err!=nil{return err};if !current{return provider.Errorf("revision_conflict","连接已经更新")}
		now:=db.Now();credentialIDs:=map[string]int64{}
		for _,credential:=range in.Credentials{result,err:=tx.ExecContext(ctx,`INSERT INTO credentials(org_id,connection_id,mailbox_id,purpose,source,secret_enc,generation,state,hint,created_at,updated_at) VALUES (?,?,?,'mail','entered',?,1,'active',?,?,?)`,orgID,c.ID,mb.ID,sealed[credential.ClientKey],secrets.Hint(credential.Secret),now,now);if err!=nil{return err};credentialIDs[credential.ClientKey],err=result.LastInsertId();if err!=nil{return err}}
		for _,item:=range items{
			var network,username,credentialID,versions,checkedAt any;status:="never";capabilities:="{}"
			if item.resolved!=nil{cid:=credentialIDs[item.input.Credential.ClientKey];credentialID=cid;username=item.input.Username;if item.input.NetworkMode=="override"{network=toJSON(item.input.Network)};versions=toJSON(CheckedVersions{ConnectionRevision:c.Revision,EndpointRevision:2,CredentialID:cid,CredentialGeneration:1});checkedAt=now;status="passed";capabilities=item.capabilities}
			res,err:=tx.ExecContext(ctx,`UPDATE mailbox_endpoints SET network_mode=?,network_override_json=?,username=?,credential_id=?,revision=revision+1,check_status=?,checked_at=?,checked_versions_json=?,capabilities_json=?,updated_at=? WHERE mailbox_id=? AND protocol=? AND revision=1`,item.input.NetworkMode,network,username,credentialID,status,checkedAt,versions,capabilities,now,mb.ID,item.protocol);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","协议配置已经更新")}
		}
		res,err:=tx.ExecContext(ctx,`UPDATE mailboxes SET folder_mapping_json=?,revision=revision+1,access_revision=access_revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=? AND status='suspended'`,toJSON(in.FolderMapping),now,mb.ID,orgID,mb.Revision);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","邮箱已经更新")}
		_,err=tx.ExecContext(ctx,`INSERT INTO operation_steps(operation_id,step_key,sequence,status,result_json,finished_at) SELECT ?,?,COALESCE(MAX(sequence),0)+1,'succeeded',?,? FROM operation_steps WHERE operation_id=?`,id,operationStepKey(ctx,"credential.entered"),toJSON(map[string]any{"mailboxId":mb.ID,"verificationSource":"protocol_authentication"}),now,id);return err
	})
}
