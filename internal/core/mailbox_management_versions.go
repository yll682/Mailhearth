package core

import (
	"context"
	"database/sql"

	"mailhearth/internal/provider"
)

type mailboxManagementPlan struct {
	ConnectionID int64 `json:"connectionId"`
	ConnectionRevision int64 `json:"connectionRevision"`
	BindingID int64 `json:"bindingId"`
	BindingRevision int64 `json:"bindingRevision"`
}

func (s *Service) prepareMailboxManagementVersions(ctx context.Context,orgID int64,payload *OperationPayload) error {
	c,err:=s.MailConnection(ctx,orgID,payload.ConnectionID);if err!=nil{return err}
	plan:=&mailboxManagementPlan{ConnectionID:c.ID,ConnectionRevision:c.Revision}
	if payload.Create!=nil{binding,err:=s.DomainBinding(ctx,orgID,payload.Create.DomainBindingID);if err!=nil{return err};plan.BindingID=binding.ID;plan.BindingRevision=binding.Revision}
	payload.Management=plan
	return nil
}

func validateMailboxManagementVersions(ctx context.Context,q managementQuery,orgID int64,payload OperationPayload) error {
	plan:=payload.Management;if plan==nil{return nil}
	var revision int64;var enabled bool
	if err:=q.QueryRowContext(ctx,`SELECT revision,enabled FROM mail_connections WHERE id=? AND org_id=?`,plan.ConnectionID,orgID).Scan(&revision,&enabled);err!=nil{return err}
	if !enabled{return provider.Errorf("endpoint_disabled","邮箱所属连接已经停用")};if revision!=plan.ConnectionRevision{return provider.Errorf("revision_conflict","邮箱所属连接已经更新")}
	if plan.BindingID>0{if err:=q.QueryRowContext(ctx,`SELECT revision FROM domain_bindings WHERE id=? AND org_id=? AND connection_id=? AND management_mode='api' AND remote_state='present'`,plan.BindingID,orgID,plan.ConnectionID).Scan(&revision);err!=nil{return err};if revision!=plan.BindingRevision{return provider.Errorf("revision_conflict","邮箱所属域名关联已经更新")}}
	return nil
}

func validateManagedMailboxRevision(ctx context.Context,q managementQuery,orgID int64,id string,mailboxID,expected int64) error {
	var committed sql.NullInt64
	if err:=q.QueryRowContext(ctx,`SELECT (SELECT json_extract(st.result_json,'$.mailboxRevision') FROM operation_steps st JOIN credentials c ON c.id=json_extract(st.result_json,'$.credentialId') WHERE st.operation_id=? AND st.step_key=? AND st.status='succeeded' AND c.mailbox_id=? AND c.state='active')`,id,operationStepKey(ctx,"credential.activate.local.commit"),mailboxID).Scan(&committed);err!=nil{return err}
	if committed.Valid{expected=committed.Int64}
	var current bool;if err:=q.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM mailboxes WHERE id=? AND org_id=? AND revision=?)`,mailboxID,orgID,expected).Scan(&current);err!=nil{return err}
	if !current{return provider.Errorf("revision_conflict","邮箱配置已经更新")}
	return nil
}
