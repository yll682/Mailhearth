package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

type DomainSettings struct {
	AllowAccountReset *bool `json:"allowAccountReset,omitempty"`
	SymbolicSubaddressing *bool `json:"symbolicSubaddressing,omitempty"`
}

type DomainBindingInput struct {
	RequestID string `json:"requestId"`
	ConnectionID int64 `json:"connectionId"`
	DomainName string `json:"domainName"`
	Mode string `json:"mode"`
	BindingID int64 `json:"-"`
	ExpectedRevision int64 `json:"expectedRevision"`
	ProviderSettings *DomainSettings `json:"providerSettings,omitempty"`
	ConfirmDomain string `json:"confirmDomain,omitempty"`
}

func scanDomainBinding(row interface{ Scan(...any) error }) (*model.DomainBinding,error) {
	var b model.DomainBinding
	var settings,dns string
	var checked sql.NullString
	if err:=row.Scan(&b.ID,&b.DomainID,&b.DomainName,&b.ConnectionID,&b.ConnectionLabel,&b.ManagementMode,&b.RemoteState,&settings,&dns,&checked,&b.Revision,&b.CreatedAt,&b.UpdatedAt);err!=nil{return nil,err}
	if err:=json.Unmarshal([]byte(settings),&b.ProviderSettings);err!=nil{return nil,err}
	if err:=json.Unmarshal([]byte(dns),&b.DNSStatus);err!=nil{return nil,err}
	b.DNSCheckedAt=nullStr(checked)
	return &b,nil
}

const bindingSelect=`SELECT b.id,b.domain_id,d.name,b.connection_id,c.label,b.management_mode,b.remote_state,b.provider_settings_json,b.dns_status_json,b.dns_checked_at,b.revision,b.created_at,b.updated_at FROM domain_bindings b JOIN domains d ON d.id=b.domain_id JOIN mail_connections c ON c.id=b.connection_id`

func (s *Service) DomainBinding(ctx context.Context,orgID,id int64) (*model.DomainBinding,error) {
	b,err:=scanDomainBinding(s.DB.QueryRowContext(ctx,bindingSelect+` WHERE b.org_id=? AND b.id=?`,orgID,id))
	if db.IsNotFound(err){return nil,ErrNotFound};return b,err
}

func (s *Service) DomainBindings(ctx context.Context,orgID,connectionID,domainID int64) ([]model.DomainBinding,error) {
	rows,err:=s.DB.QueryContext(ctx,bindingSelect+` WHERE b.org_id=? AND (?=0 OR b.connection_id=?) AND (?=0 OR b.domain_id=?) ORDER BY d.name,b.connection_id`,orgID,connectionID,connectionID,domainID,domainID)
	if err!=nil{return nil,err};defer rows.Close()
	out:=[]model.DomainBinding{}
	for rows.Next(){b,err:=scanDomainBinding(rows);if err!=nil{return nil,err};out=append(out,*b)}
	return out,rows.Err()
}

func scopeContainsDomain(scope provider.DomainScope,name string) bool {
	if scope.Mode=="all"{return true}
	for _,domain:=range scope.Domains{if strings.EqualFold(domain,name){return true}}
	return false
}

func (s *Service) prepareDomainOperation(ctx context.Context,orgID int64,kind string,in *DomainBindingInput) error {
	if in==nil{return provider.Errorf("invalid","需要域名请求")}
	if in.BindingID>0{
		b,err:=s.DomainBinding(ctx,orgID,in.BindingID);if err!=nil{return err}
		if in.ExpectedRevision!=b.Revision{return provider.Errorf("revision_conflict","域名关联已经更新")}
		in.ConnectionID=b.ConnectionID;in.DomainName=b.DomainName
		if b.ManagementMode!="api" && kind!="domain.register"{return provider.Errorf("external_action_required","此域名通过外部管理页面配置")}
	}
	name,err:=provider.CanonicalDomain(in.DomainName);if err!=nil{return err};in.DomainName=name
	c,err:=s.MailConnection(ctx,orgID,in.ConnectionID);if err!=nil{return err};if !c.Enabled{return provider.Errorf("endpoint_disabled","连接已停用")}
	if !scopeContainsDomain(c.DomainScope,name){return provider.Errorf("target_constraint_failed","域名不在连接的管理范围内")}
	if in.BindingID==0{var exists bool;if err:=s.DB.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM domain_bindings b JOIN domains d ON d.id=b.domain_id WHERE b.org_id=? AND b.connection_id=? AND d.name=?)`,orgID,c.ID,name).Scan(&exists);err!=nil{return err};if exists{return provider.Errorf("idempotency_conflict","该连接已经关联此域名")}}
	if kind=="domain.register"{if in.Mode!="register"{return provider.Errorf("invalid","登记域名需要 mode=register")};return nil}
	if kind=="domain.create" && in.Mode!="create"{return provider.Errorf("invalid","创建域名需要 mode=create")}
	if c.ProviderKind==provider.Manual{return provider.Errorf("external_action_required","请在服务商管理页面配置域名")}
	if kind=="domain.delete" && c.ProviderKind==provider.Migadu{return provider.Errorf("external_action_required","请在 Migadu 管理页面删除域名")}
	if kind=="domain.delete" && in.ConfirmDomain!=name{return provider.Errorf("invalid","请输入完整域名")}
	if in.ProviderSettings!=nil && c.ProviderKind!=provider.Purelymail{return provider.Errorf("unsupported_operation","该连接不提供这些域名设置")}
	if kind=="domain.delete"{return s.checkBindingDependencies(ctx,s.DB,in.BindingID)}
	return nil
}

func (s *Service) checkBindingDependencies(ctx context.Context,q db.Querier,id int64) error {
	var count int
	if err:=q.QueryRowContext(ctx,`SELECT (SELECT COUNT(*) FROM mailboxes WHERE domain_binding_id=?)+(SELECT COUNT(*) FROM addresses WHERE domain_binding_id=?)`,id,id).Scan(&count);err!=nil{return err}
	if count>0{return provider.Errorf("domain_in_use","域名关联仍有邮箱或地址规则")}
	return nil
}

type remoteOutcomeUnknown struct { err error }
func (e *remoteOutcomeUnknown) Error() string{return "远程操作结果需要核查"}
func (e *remoteOutcomeUnknown) Unwrap() error{return e.err}

// 每个远程写入保存开始状态，结果未确认时保留步骤与资源占用。
func (s *Service) remoteOperationStep(ctx context.Context,key string,ref any,run func() error) error {
	key=operationStepKey(ctx,key)
	id,ok:=ctx.Value(operationContextKey{}).(string);if !ok{return provider.Errorf("invalid","远程写入需要 Operation")}
	body,err:=json.Marshal(ref);if err!=nil{return err}
	var status string
	err=s.DB.QueryRowContext(ctx,`SELECT status FROM operation_steps WHERE operation_id=? AND step_key=?`,id,key).Scan(&status)
	if db.IsNotFound(err){if _,err=s.DB.ExecContext(ctx,`INSERT INTO operation_steps(operation_id,step_key,sequence,status,remote_ref_json) SELECT ?,?,COALESCE(MAX(sequence),0)+1,'pending',? FROM operation_steps WHERE operation_id=?`,id,key,string(body),id);err!=nil{return err};status="pending"}else if err!=nil{return err}
	if status=="succeeded"{return nil}
	if status=="unknown" || status=="running"{return &remoteOutcomeUnknown{provider.Errorf("verification_required","远程步骤需要独立读取核查")}}
	var orgID,actor int64;var mailboxID sql.NullInt64;var required string
	if err:=s.DB.QueryRowContext(ctx,`SELECT org_id,actor_member_id,target_mailbox_id,required_permission FROM operations WHERE id=? AND status='running'`,id).Scan(&orgID,&actor,&mailboxID,&required);err!=nil{return err}
	member,err:=s.Member(ctx,orgID,actor);if err!=nil{return err};if member.Status!=model.MemberActive{return ErrForbidden}
	if err:=s.requireOperationPermission(ctx,orgID,actor,mailboxID.Int64,required);err!=nil{return err}
	if err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{return checkOperationPermissionTx(ctx,tx,id)});err!=nil{return err}
	if _,err:=s.DB.ExecContext(ctx,`UPDATE operation_steps SET status='running',remote_ref_json=?,started_at=?,error_code=NULL WHERE operation_id=? AND step_key=?`,string(body),db.Now(),id,key);err!=nil{return err}
	runErr:=run()
	status="succeeded";var code any
	if runErr!=nil{
		status="unknown";code=operationErrorCode(runErr)
		var typed *provider.TypedError
		var unknown *remoteOutcomeUnknown
		if !errors.As(runErr,&unknown) && errors.As(runErr,&typed){switch typed.Code{case "invalid","not_found","forbidden","provider_auth_failed","external_action_required","unsupported_operation","verification_required","target_constraint_failed":status="failed"}}
	}
	if _,err:=s.DB.ExecContext(context.Background(),`UPDATE operation_steps SET status=?,error_code=?,finished_at=? WHERE operation_id=? AND step_key=?`,status,code,db.Now(),id,key);err!=nil{return &remoteOutcomeUnknown{err}}
	if status=="unknown"{return &remoteOutcomeUnknown{runErr}}
	return runErr
}

func (s *Service) executeDomainOperation(ctx context.Context,orgID,actor int64,kind string,in *DomainBindingInput) (any,error) {
	if err:=s.prepareDomainOperation(ctx,orgID,kind,in);err!=nil{return nil,err}
	c,err:=s.MailConnection(ctx,orgID,in.ConnectionID);if err!=nil{return nil,err}
	info:=provider.DomainInfo{Name:in.DomainName,DNS:provider.DNSStatus{MX:"unknown",SPF:"unknown",DKIM:"unknown",DMARC:"unknown"}};state,mode:="external","external"
	if c.ProviderKind!=provider.Manual{
		api,_,err:=s.connectionAdapter(ctx,orgID,c.ID);if err!=nil{return nil,err}
		ref:=map[string]string{"domain":in.DomainName}
		switch kind{
		case "domain.create":err=s.remoteOperationStep(ctx,"domain.create",ref,func()error{_,err:=api.CreateDomain(ctx,provider.CreateDomainRequest{Domain:in.DomainName});return err})
		case "domain.configure":err=s.remoteOperationStep(ctx,"domain.configure",ref,func()error{_,err:=api.UpdateDomain(ctx,provider.UpdateDomainRequest{Domain:in.DomainName,AllowAccountReset:in.ProviderSettings.AllowAccountReset,SymbolicSubaddressing:in.ProviderSettings.SymbolicSubaddressing});return err})
		case "domain.recheck":err=s.remoteOperationStep(ctx,"domain.recheck",ref,func()error{_,err:=api.CheckDNS(ctx,provider.CheckDNSRequest{Domain:in.DomainName});return err})
		case "domain.activate":err=s.remoteOperationStep(ctx,"domain.activate",ref,func()error{_,err:=api.ActivateDomain(ctx,provider.ActivateDomainRequest{Domain:in.DomainName});return err})
		case "domain.delete":err=s.remoteOperationStep(ctx,"domain.delete",ref,func()error{_,err:=api.DeleteDomain(ctx,provider.DeleteDomainRequest{Domain:in.DomainName});return err})
		}
		if err!=nil{return nil,err}
		info,err=api.GetDomain(ctx,provider.GetDomainRequest{Domain:in.DomainName})
		if kind=="domain.delete"{var typed *provider.TypedError;if !errors.As(err,&typed) || typed.Code!="not_found"{if err!=nil{return nil,err};return nil,&remoteOutcomeUnknown{provider.Errorf("upstream_failed","远程域名仍存在")}};info.Name=in.DomainName;state="missing"}else{if err!=nil{return nil,err};state="present"}
		mode="api"
	}
	var bindingID int64
	err=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		var current bool;if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM mail_connections WHERE id=? AND org_id=? AND revision=? AND enabled=1)`,c.ID,orgID,c.Revision).Scan(&current);err!=nil{return err};if !current{return provider.Errorf("revision_conflict","连接配置已经更新")}
		now:=db.Now()
		var checkedAt any;if domainDNSObserved(info.DNS){checkedAt=now}
		if _,err:=tx.ExecContext(ctx,`INSERT INTO domains(org_id,name,created_at,updated_at) VALUES (?,?,?,?) ON CONFLICT(org_id,name) DO NOTHING`,orgID,in.DomainName,now,now);err!=nil{return err}
		var domainID int64;if err:=tx.QueryRowContext(ctx,`SELECT id FROM domains WHERE org_id=? AND name=?`,orgID,in.DomainName).Scan(&domainID);err!=nil{return err}
		settings,err:=json.Marshal(DomainSettings{AllowAccountReset:info.AllowAccountReset,SymbolicSubaddressing:info.SymbolicSubaddressing});if err!=nil{return err};dns,err:=json.Marshal(info.DNS);if err!=nil{return err}
		if in.BindingID==0{
			res,err:=tx.ExecContext(ctx,`INSERT INTO domain_bindings(org_id,domain_id,connection_id,management_mode,remote_state,provider_settings_json,dns_status_json,dns_checked_at,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,orgID,domainID,c.ID,mode,state,string(settings),string(dns),checkedAt,now,now);if err!=nil{return err};bindingID,err=res.LastInsertId();if err!=nil{return err}
		}else{
			bindingID=in.BindingID
			res,err:=tx.ExecContext(ctx,`UPDATE domain_bindings SET remote_state=?,provider_settings_json=?,dns_status_json=?,dns_checked_at=?,revision=revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=?`,state,string(settings),string(dns),checkedAt,now,bindingID,orgID,in.ExpectedRevision);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","域名关联已经更新")}
		}
		if mode=="api"{if err:=saveDiscoveredResource(ctx,tx,c.ID,provider.Resource{ResourceType:"domain",RemoteKey:in.DomainName,RemoteLocator:map[string]string{"domain":in.DomainName},Purpose:provider.PurposeDomain},"domain_binding_id",bindingID);err!=nil{return err};if _,err:=tx.ExecContext(ctx,`UPDATE provider_resources SET remote_state=? WHERE domain_binding_id=?`,state,bindingID);err!=nil{return err}}
		return completeLocalOperation(ctx,tx,map[string]int64{"domainBindingId":bindingID})
	});if err!=nil{return nil,err}
	s.audit(ctx,orgID,actor,kind,"domain_binding",fmtID(bindingID),nil)
	return s.DomainBinding(ctx,orgID,bindingID)
}

func (s *Service) DeleteDomainBinding(ctx context.Context,orgID,actor,id,revision int64) error {
	if revision<1{return provider.Errorf("invalid","需要 expectedRevision")}
	err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		if err:=s.checkBindingDependencies(ctx,tx,id);err!=nil{return err}
		var current int64;if err:=tx.QueryRowContext(ctx,`SELECT revision FROM domain_bindings WHERE id=? AND org_id=?`,id,orgID).Scan(&current);err!=nil{if db.IsNotFound(err){return ErrNotFound};return err};if current!=revision{return provider.Errorf("revision_conflict","域名关联已经更新")}
		var pending bool;if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM operation_locks WHERE org_id=? AND resource_key=?)`,orgID,"domain-binding:"+fmtID(id)).Scan(&pending);err!=nil{return err};if pending{return provider.Errorf("operation_in_progress","域名关联仍有进行中的操作")}
		if _,err:=tx.ExecContext(ctx,`DELETE FROM provider_resources WHERE domain_binding_id=?`,id);err!=nil{return err}
		_,err:=tx.ExecContext(ctx,`DELETE FROM domain_bindings WHERE id=? AND org_id=? AND revision=?`,id,orgID,revision);return err
	});if err!=nil{return err};s.audit(ctx,orgID,actor,"domain.unregister","domain_binding",fmtID(id),nil);return nil
}

func (s *Service) DomainDNSRecords(ctx context.Context,orgID,id int64) (provider.GetDNSRecordsResult,error) {
	b,err:=s.DomainBinding(ctx,orgID,id);if err!=nil{return provider.GetDNSRecordsResult{},err}
	api,_,err:=s.connectionAdapter(ctx,orgID,b.ConnectionID);if err!=nil{return provider.GetDNSRecordsResult{},err}
	return api.GetDNSRecords(ctx,provider.GetDNSRecordsRequest{Domain:b.DomainName})
}
