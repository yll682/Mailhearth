package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

type ForwardingInput struct {
	RequestID string `json:"requestId"`
	ExpectedRevision int64 `json:"expectedRevision"`
	Targets []string `json:"targets,omitempty"`
	DeliveryMode string `json:"deliveryMode,omitempty"`
}

type ForwardingView struct {
	ID int64 `json:"id"`
	MailboxID int64 `json:"mailboxId"`
	Revision int64 `json:"revision"`
	Targets []string `json:"targets"`
	DeliveryMode string `json:"deliveryMode"`
	SyncState string `json:"syncState"`
	RemoteStatus json.RawMessage `json:"remoteStatus"`
}

type forwardingPlan struct {
	ID int64 `json:"forwardingId"`
	Revision int64 `json:"revision"`
	MailboxID int64 `json:"mailboxId"`
	MailboxRevision int64 `json:"mailboxRevision"`
	ConnectionID int64 `json:"connectionId"`
	ConnectionRevision int64 `json:"connectionRevision"`
	Address string `json:"address"`
	Mode string `json:"mode"`
	DeliveryMode string `json:"deliveryMode"`
	Targets []string `json:"targets"`
	Delete bool `json:"delete"`
}

func (s *Service) MailboxForwarding(ctx context.Context,orgID,mailboxID int64) (*ForwardingView,error) {
	var value ForwardingView;var targets,status string
	err:=s.DB.QueryRowContext(ctx,`SELECT f.id,f.mailbox_id,f.revision,f.targets_json,f.delivery_mode,f.sync_state,f.remote_status_json FROM mailbox_forwardings f JOIN mailboxes b ON b.id=f.mailbox_id WHERE b.org_id=? AND b.id=?`,orgID,mailboxID).Scan(&value.ID,&value.MailboxID,&value.Revision,&targets,&value.DeliveryMode,&value.SyncState,&status)
	if db.IsNotFound(err){return nil,nil};if err!=nil{return nil,err};if err:=json.Unmarshal([]byte(targets),&value.Targets);err!=nil{return nil,err};value.RemoteStatus=json.RawMessage(status);return &value,nil
}

func (s *Service) prepareForwarding(ctx context.Context,orgID,actor int64,payload *OperationPayload) (*forwardingPlan,string,error) {
	if payload.Forwarding==nil{return nil,"",provider.Errorf("invalid","需要转发请求")}
	mb,err:=s.Mailbox(ctx,orgID,payload.MailboxID);if err!=nil{return nil,"",err}
	permission:=model.PermMailboxesManage;if mb.Kind==model.MailboxShared{permission=model.PermSharedManage}
	if err:=s.requireOperationPermission(ctx,orgID,actor,mb.ID,permission);err!=nil{return nil,"",err};if err:=s.requireOperationPermission(ctx,orgID,actor,mb.ID,model.PermAddressesManage);err!=nil{return nil,"",err}
	c,err:=s.MailConnection(ctx,orgID,mb.ConnectionID);if err!=nil{return nil,"",err};if !c.Enabled{return nil,"",provider.Errorf("endpoint_disabled","连接已经停用")}
	plan:=&forwardingPlan{MailboxID:mb.ID,MailboxRevision:mb.Revision,ConnectionID:c.ID,ConnectionRevision:c.Revision,Address:mb.Address,Mode:mb.ManagementMode,Delete:payload.Kind=="mailbox.forwarding.delete",Targets:[]string{},DeliveryMode:payload.Forwarding.DeliveryMode}
	if c.ProviderKind==provider.Manual{plan.Mode="external"}
	if plan.Delete{if len(payload.Forwarding.Targets)>0 || plan.DeliveryMode!=""{return nil,"",provider.Errorf("invalid","停止转发不能指定新的目标或投递方式")}}else{
		if plan.DeliveryMode!="redirect" && plan.DeliveryMode!="copy"{return nil,"",provider.Errorf("invalid","需要明确的 redirect 或 copy")}
		set:=map[string]bool{};for _,target:=range payload.Forwarding.Targets{address,err:=NormalizeEmail(target);if err!=nil{return nil,"",err};if address==strings.ToLower(mb.Address){return nil,"",provider.Errorf("invalid","转发不能指向邮箱自身")};set[address]=true};for address:=range set{plan.Targets=append(plan.Targets,address)};sort.Strings(plan.Targets);if len(plan.Targets)==0{return nil,"",provider.Errorf("invalid","需要至少一个转发目标")}
	}
	current,err:=s.MailboxForwarding(ctx,orgID,mb.ID);if err!=nil{return nil,"",err};expected:=mb.Revision
	if current!=nil{plan.ID=current.ID;plan.Revision=current.Revision;expected=current.Revision;if plan.Delete{plan.DeliveryMode=current.DeliveryMode}}else if plan.Delete{return nil,"",ErrNotFound}
	if payload.Forwarding.ExpectedRevision!=expected{return nil,"",provider.Errorf("revision_conflict","邮箱转发配置已经更新")}
	if plan.Mode=="api"{
		_,domain:=SplitAddress(mb.Address);if !scopeContainsDomain(c.DomainScope,domain){return nil,"",provider.Errorf("target_constraint_failed","邮箱域名不在连接范围内")}
		if mb.RemoteState!="present" || !c.APIConfigured || c.LastAPICheckStatus!="passed"{return nil,"",provider.Errorf("verification_required","管理认证和远程邮箱需要通过核验")}
		if c.ProviderKind==provider.Migadu{return nil,"",provider.Errorf("verification_required","Migadu 转发方式需要真实投递验证")}
		if c.ProviderKind!=provider.Purelymail || plan.DeliveryMode!="redirect"{return nil,"",provider.Errorf("unsupported_operation","该连接没有声明所选投递方式")}
	}
	payload.ConnectionID=c.ID;return plan,permission,nil
}

func reserveForwarding(ctx context.Context,tx *sql.Tx,orgID int64,id string,plan *forwardingPlan) error {
	if err:=validateForwardingPlan(ctx,tx,orgID,plan);err!=nil{return err}
	now:=db.Now()
	if plan.ID==0{result,err:=tx.ExecContext(ctx,`INSERT INTO mailbox_forwardings(mailbox_id,delivery_mode,sync_state,created_at,updated_at) VALUES (?,?,'pending',?,?)`,plan.MailboxID,plan.DeliveryMode,now,now);if err!=nil{return err};plan.ID,err=result.LastInsertId();if err!=nil{return err};plan.Revision=1}
	res,err:=tx.ExecContext(ctx,`UPDATE mailbox_forwardings SET sync_state='pending',remote_status_json=?,updated_at=? WHERE id=? AND revision=?`,toJSON(map[string]any{"desiredTargets":plan.Targets,"desiredDeliveryMode":plan.DeliveryMode,"delete":plan.Delete}),now,plan.ID,plan.Revision);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","转发配置已经更新")}
	_,err=tx.ExecContext(ctx,`INSERT INTO operation_steps(operation_id,step_key,sequence,status,result_json,finished_at) SELECT ?,'forwarding.prepare',COALESCE(MAX(sequence),0)+2,'succeeded',?,? FROM operation_steps WHERE operation_id=?`,id,toJSON(plan),now,id);return err
}

func validateForwardingPlan(ctx context.Context,q managementQuery,orgID int64,plan *forwardingPlan) error {
	var mailboxRevision,connectionRevision int64;var enabled bool
	if err:=q.QueryRowContext(ctx,`SELECT b.revision,c.revision,c.enabled FROM mailboxes b JOIN mail_connections c ON c.id=b.connection_id WHERE b.id=? AND b.org_id=? AND c.id=?`,plan.MailboxID,orgID,plan.ConnectionID).Scan(&mailboxRevision,&connectionRevision,&enabled);err!=nil{return err}
	if mailboxRevision!=plan.MailboxRevision || connectionRevision!=plan.ConnectionRevision{return provider.Errorf("revision_conflict","邮箱或连接已经更新")};if !enabled{return provider.Errorf("endpoint_disabled","连接已经停用")}
	if plan.ID>0{var revision int64;if err:=q.QueryRowContext(ctx,`SELECT revision FROM mailbox_forwardings WHERE id=? AND mailbox_id=?`,plan.ID,plan.MailboxID).Scan(&revision);err!=nil{return err};if revision!=plan.Revision{return provider.Errorf("revision_conflict","转发配置已经更新")}};return nil
}

func (s *Service) executeForwarding(ctx context.Context,orgID,actor int64,payload OperationPayload) (any,error) {
	id:=ctx.Value(operationContextKey{}).(string);var body string
	if err:=s.DB.QueryRowContext(ctx,`SELECT result_json FROM operation_steps WHERE operation_id=? AND step_key='forwarding.prepare'`,id).Scan(&body);err!=nil{return nil,err};var plan forwardingPlan;if err:=json.Unmarshal([]byte(body),&plan);err!=nil{return nil,err}
	var committed bool;if err:=s.DB.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND step_key='forwarding.commit' AND status='succeeded')`,id).Scan(&committed);err!=nil{return nil,err};if committed{return nil,provider.Errorf("external_action_required","转发配置已经登记，等待外部处理报告")}
	if err:=s.requireOperationPermission(ctx,orgID,actor,plan.MailboxID,model.PermAddressesManage);err!=nil{return nil,err}
	if err:=validateForwardingPlan(ctx,s.DB,orgID,&plan);err!=nil{return nil,err}
	var observed provider.AddressRuleInfo
	if plan.Mode=="api"{
		api,_,err:=s.connectionAdapter(ctx,orgID,plan.ConnectionID);if err!=nil{return nil,err};local,domain:=SplitAddress(plan.Address);request:=provider.AddressRuleRequest{Domain:domain,LocalPart:local,Targets:plan.Targets}
		child:=context.WithValue(ctx,operationStepPrefixKey{},"forwarding.");if err:=s.replaceRoutingRule(child,api,request);err!=nil{return nil,err}
		if !plan.Delete{observed,err=api.GetAddressRule(ctx,request);if err!=nil{return nil,&remoteOutcomeUnknown{err}};if observed.RemoteKey=="" || !sameRoutingTargets(observed.Targets,plan.Targets){return nil,&remoteOutcomeUnknown{provider.Errorf("verification_required","转发规则需要独立核验")}}}
	}else{if err:=s.externalOperationStep(ctx,"forwarding.external",map[string]any{"mailboxId":plan.MailboxID,"forwardingId":plan.ID,"targets":plan.Targets,"deliveryMode":plan.DeliveryMode,"delete":plan.Delete,"systemVerified":false});err!=nil{return nil,err}}
	result:=map[string]any{"mailboxId":plan.MailboxID,"forwardingId":plan.ID,"verificationSource":"provider_read","systemVerified":true};if plan.Mode!="api"{result["verificationSource"]="local_registration";result["systemVerified"]=false}
	err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		if err:=checkOperationPermissionTx(ctx,tx,id);err!=nil{return err};if err:=validateForwardingPlan(ctx,tx,orgID,&plan);err!=nil{return err}
		var permissions string;if err:=tx.QueryRowContext(ctx,`SELECT r.permissions_json FROM members m JOIN roles r ON r.id=m.role_id WHERE m.id=? AND m.org_id=? AND m.status='active'`,actor,orgID).Scan(&permissions);err!=nil{return err};var values []string;if err:=json.Unmarshal([]byte(permissions),&values);err!=nil{return err};if !HasPermission(values,model.PermAddressesManage){return ErrForbidden}
		state:="synced";if plan.Mode!="api"{state="unknown"}
		if plan.Delete && plan.Mode=="api"{if _,err:=tx.ExecContext(ctx,`DELETE FROM provider_resources WHERE mailbox_forwarding_id=?`,plan.ID);err!=nil{return err};if _,err:=tx.ExecContext(ctx,`DELETE FROM mailbox_forwardings WHERE id=? AND revision=?`,plan.ID,plan.Revision);err!=nil{return err}}else{
			status:=map[string]any{"verificationSource":result["verificationSource"],"systemVerified":result["systemVerified"],"desiredTargets":plan.Targets,"delete":plan.Delete};if plan.Mode=="api"{status["observedTargets"]=observed.Targets}
			if _,err:=tx.ExecContext(ctx,`UPDATE mailbox_forwardings SET targets_json=?,delivery_mode=?,remote_status_json=?,sync_state=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?`,toJSON(plan.Targets),plan.DeliveryMode,toJSON(status),state,db.Now(),plan.ID,plan.Revision);err!=nil{return err}
			if plan.Mode=="api"{if err:=saveForwardingObservation(ctx,tx,plan.ConnectionID,plan.ID,observed);err!=nil{return err}}
		}
		if plan.Mode=="api"{return completeLocalOperation(ctx,tx,result)}
		_,err:=tx.ExecContext(ctx,`INSERT INTO operation_steps(operation_id,step_key,sequence,status,result_json,finished_at) SELECT ?,'forwarding.commit',COALESCE(MAX(sequence),0)+1,'succeeded',?,? FROM operation_steps WHERE operation_id=? ON CONFLICT(operation_id,step_key) DO NOTHING`,id,toJSON(result),db.Now(),id);return err
	});if err!=nil{return nil,err}
	if plan.Mode!="api"{failure:=provider.Errorf("external_action_required","转发配置需要管理员在服务商侧处理并报告完成");failure.Details=result;return result,failure};s.audit(ctx,orgID,actor,payload.Kind,"mailbox",fmtID(plan.MailboxID),result);return result,nil
}

func saveForwardingObservation(ctx context.Context,tx *sql.Tx,connectionID,forwardingID int64,observed provider.AddressRuleInfo) error {
	if observed.RemoteKey==""{return provider.Errorf("upstream_failed","转发规则读取结果缺少 remoteKey")}
	var existing sql.NullInt64
	err:=tx.QueryRowContext(ctx,`SELECT mailbox_forwarding_id FROM provider_resources WHERE connection_id=? AND resource_type='routing_rule' AND remote_key=? AND purpose='forwarding'`,connectionID,observed.RemoteKey).Scan(&existing)
	if err!=nil && !db.IsNotFound(err){return err};if err==nil && (!existing.Valid || existing.Int64!=forwardingID){return provider.Errorf("revision_conflict","远程规则已经关联其他转发对象")}
	now:=db.Now();if _,err:=tx.ExecContext(ctx,`UPDATE provider_resources SET remote_state='missing',updated_at=? WHERE mailbox_forwarding_id=? AND connection_id=?`,now,forwardingID,connectionID);err!=nil{return err}
	_,err=tx.ExecContext(ctx,`INSERT INTO provider_resources(connection_id,resource_type,remote_key,remote_locator_json,purpose,owned_by_mailhearth,last_seen_at,remote_state,mailbox_forwarding_id,created_at,updated_at) VALUES (?,'routing_rule',?,?,'forwarding',1,?,'present',?,?,?) ON CONFLICT(connection_id,resource_type,remote_key,purpose) DO UPDATE SET remote_locator_json=excluded.remote_locator_json,last_seen_at=excluded.last_seen_at,remote_state='present',updated_at=excluded.updated_at`,connectionID,observed.RemoteKey,toJSON(observed.RemoteLocator),now,forwardingID,now,now);return err
}
