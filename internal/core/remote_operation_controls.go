package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

func remoteOperationKeys(payload OperationPayload) []string {
	var keys []string
	if payload.Offboard!=nil{keys=append(keys,offboardOperationKeys(payload.Offboard)...)}
	if payload.ConnectionID>0{keys=append(keys,"connection:"+fmtID(payload.ConnectionID))}
	if payload.MailboxID>0{keys=append(keys,"mailbox:"+fmtID(payload.MailboxID))}
	if payload.Domain!=nil{keys=append(keys,"domain-name:"+fmtID(payload.ConnectionID)+":"+payload.Domain.DomainName);if payload.DomainBindingID>0{keys=append(keys,"domain-binding:"+fmtID(payload.DomainBindingID))}}
	if payload.Create!=nil{keys=append(keys,"domain-binding:"+fmtID(payload.Create.DomainBindingID))}
	sort.Strings(keys);return keys
}

func (s *Service) controlRemoteOperation(ctx context.Context,orgID,actor int64,view *OperationView,action string) (*OperationView,error) {
	var sealed string;var mailboxID sql.NullInt64
	if err:=s.DB.QueryRowContext(ctx,`SELECT COALESCE(payload_enc,''),target_mailbox_id FROM operations WHERE org_id=? AND id=?`,orgID,view.ID).Scan(&sealed,&mailboxID);err!=nil{return nil,err}
	if err:=s.requireOperationPermission(ctx,orgID,actor,mailboxID.Int64,view.RequiredPermission);err!=nil{return nil,err}
	if sealed==""{return nil,provider.Errorf("operation_not_retryable","操作没有可执行的原始请求")}
	plain,err:=s.Box.Open(sealed);if err!=nil{return nil,provider.Errorf("credential_decryption_failed","操作请求解密失败")}
	var payload OperationPayload;if err:=json.Unmarshal([]byte(plain),&payload);err!=nil{return nil,err};if payload.Kind!=view.Kind{return nil,provider.Errorf("invalid","操作请求类型不一致")};if payload.Domain!=nil{payload.Domain.BindingID=payload.DomainBindingID}
	var routing *routingPlan
	var memberPlan *memberCreationPlan
	var mutationPlan *mailboxMutationPlan
	var statusPlan *memberStatusPlan
	var additionPlan *mailboxAdditionPlan
	if mailboxAdditionOperation(payload.Kind){additionPlan,err=readMailboxAdditionPlan(ctx,s.DB,view.ID);if err!=nil{return nil,err}}
	if payload.Kind=="member.status"{statusPlan,err=readMemberStatusPlan(ctx,s.DB,view.ID);if err!=nil{return nil,err}}
	if mailboxMutationOperation(payload.Kind){mutationPlan,err=readMailboxMutationPlan(ctx,s.DB,view.ID);if err!=nil{return nil,err}}
	if payload.MemberCreate!=nil{memberPlan,err=readMemberCreationPlan(ctx,s.DB,view.ID);if err!=nil{return nil,err}}
	if payload.Routing!=nil{var body string;if err:=s.DB.QueryRowContext(ctx,`SELECT result_json FROM operation_steps WHERE operation_id=? AND step_key='routing.prepare'`,view.ID).Scan(&body);err!=nil{return nil,err};routing=&routingPlan{};if err:=json.Unmarshal([]byte(body),routing);err!=nil{return nil,err}}
	if action=="reconcile"{
		if view.Status!="unknown"{return nil,provider.Errorf("operation_not_reconcilable","仅允许核查结果未知的操作")}
		if err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{return checkOperationControllerPermissionTx(ctx,tx,view.ID,actor)});err!=nil{return nil,err}
		if err:=s.reconcileRemoteSteps(ctx,orgID,actor,view.ID,payload);err!=nil{return nil,err}
	}
	err=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		if err:=checkOperationControllerPermissionTx(ctx,tx,view.ID,actor);err!=nil{return err}
		if action=="retry"{if err:=checkOperationPermissionTx(ctx,tx,view.ID);err!=nil{return err}}
		if view.RequiredPermission==permissionMailFull{if err:=requireFullAccessTx(ctx,tx,orgID,actor,mailboxID.Int64);err!=nil{return err}}else{
			var allowed bool;if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM members m JOIN roles r ON r.id=m.role_id WHERE m.id=? AND m.org_id=? AND m.status='active' AND (EXISTS(SELECT 1 FROM json_each(r.permissions_json) WHERE value=?) OR EXISTS(SELECT 1 FROM json_each(r.permissions_json) WHERE value='org.owner')))`,actor,orgID,view.RequiredPermission).Scan(&allowed);err!=nil{return err};if !allowed{return ErrForbidden}
		}
		var status string;if err:=tx.QueryRowContext(ctx,`SELECT status FROM operations WHERE id=? AND org_id=?`,view.ID,orgID).Scan(&status);err!=nil{return err}
		switch action {
		case "cancel":
			if status!="queued" && status!="failed" && status!="needs_action"{return provider.Errorf("operation_not_cancellable","操作需要处理已有远程影响")}
			var unresolved bool;if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND status IN ('running','unknown'))`,view.ID).Scan(&unresolved);err!=nil{return err};if unresolved{return provider.Errorf("operation_not_cancellable","远程步骤仍需要核查")}
			if payload.Offboard!=nil{var processed bool;if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND step_key='member.processing' AND status='succeeded')`,view.ID).Scan(&processed);err!=nil{return err};if !processed{return provider.Errorf("operation_not_cancellable","离职交接步骤尚未完成")}}
			if routing!=nil{for _,rule:=range routing.Rules{if _,err:=tx.ExecContext(ctx,`UPDATE addresses SET sync_state='unknown',updated_at=? WHERE id=? AND org_id=? AND revision=?`,db.Now(),rule.ID,orgID,rule.Revision);err!=nil{return err}}}
			if payload.Forwarding!=nil{if _,err:=tx.ExecContext(ctx,`UPDATE mailbox_forwardings SET sync_state='unknown',updated_at=? WHERE mailbox_id=?`,db.Now(),payload.MailboxID);err!=nil{return err}}
			if mutationPlan!=nil{for _,group:=range mutationPlan.Groups{for _,rule:=range group.Rules{if _,err:=tx.ExecContext(ctx,`UPDATE addresses SET sync_state='unknown',updated_at=? WHERE id=? AND org_id=? AND sync_state='pending'`,db.Now(),rule.ID,orgID);err!=nil{return err}}}}
			if statusPlan!=nil{for _,group:=range statusPlan.Groups{for _,rule:=range group.Rules{if _,err:=tx.ExecContext(ctx,`UPDATE addresses SET sync_state='unknown',updated_at=? WHERE id=? AND org_id=? AND sync_state='pending'`,db.Now(),rule.ID,orgID);err!=nil{return err}}}}
			if memberPlan!=nil{for _,group:=range memberPlan.Groups{for _,rule:=range group.Rules{if _,err:=tx.ExecContext(ctx,`UPDATE addresses SET sync_state='unknown',updated_at=? WHERE id=? AND org_id=? AND sync_state='pending'`,db.Now(),rule.ID,orgID);err!=nil{return err}}}}
			if additionPlan!=nil{for _,group:=range additionPlan.Groups{for _,rule:=range group.Rules{if _,err:=tx.ExecContext(ctx,`UPDATE addresses SET sync_state='unknown',updated_at=? WHERE id=? AND org_id=? AND sync_state='pending'`,db.Now(),rule.ID,orgID);err!=nil{return err}}}}
			if _,err:=tx.ExecContext(ctx,`UPDATE operations SET status='cancelled',payload_enc=NULL,claim_secret_enc=NULL,updated_at=? WHERE id=?`,db.Now(),view.ID);err!=nil{return err}
			if _,err:=tx.ExecContext(ctx,`DELETE FROM operation_private WHERE operation_id=?`,view.ID);err!=nil{return err}
			_,err:=tx.ExecContext(ctx,`DELETE FROM operation_locks WHERE operation_id=?`,view.ID);return err
		case "reconcile":
			if status!="unknown"{return provider.Errorf("operation_not_reconcilable","操作状态已经变化")}
			var unresolved bool;if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND step_key!='execute' AND status IN ('unknown','running'))`,view.ID).Scan(&unresolved);err!=nil{return err};if unresolved{return provider.Errorf("verification_required","仍有远程步骤需要核查")}
			if _,err:=tx.ExecContext(ctx,`UPDATE operation_steps SET status='failed',error_code='reconciled',finished_at=? WHERE operation_id=? AND step_key='execute'`,db.Now(),view.ID);err!=nil{return err}
			_,err:=tx.ExecContext(ctx,`UPDATE operations SET status='needs_action',error_code='reconciled',updated_at=? WHERE id=?`,db.Now(),view.ID);return err
		case "retry":
			if status!="failed" && status!="needs_action"{return provider.Errorf("operation_not_retryable","需要确认所有远程步骤的结果")}
			var unresolved bool;if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM operation_steps WHERE operation_id=? AND status IN ('unknown','running'))`,view.ID).Scan(&unresolved);err!=nil{return err};if unresolved{return provider.Errorf("operation_not_retryable","远程步骤仍需要核查")}
			if payload.DomainBindingID>0{var revision int64;if err:=tx.QueryRowContext(ctx,`SELECT revision FROM domain_bindings WHERE id=? AND org_id=?`,payload.DomainBindingID,orgID).Scan(&revision);err!=nil{return err};if revision!=payload.Domain.ExpectedRevision{return provider.Errorf("revision_conflict","域名关联已经更新")}}
			if payload.Rules!=nil{var revision int64;if err:=tx.QueryRowContext(ctx,`SELECT revision FROM mailboxes WHERE id=? AND org_id=?`,payload.MailboxID,orgID).Scan(&revision);err!=nil{return err};if revision!=payload.Rules.ExpectedRevision{return provider.Errorf("revision_conflict","邮箱配置已更新")}}
			if err:=validateMailboxManagementVersions(ctx,tx,orgID,payload);err!=nil{return err}
			if payload.RemoteMailbox!=nil && payload.Kind!="mailbox.deleteRemote"{if err:=validateManagedMailboxRevision(ctx,tx,orgID,view.ID,payload.MailboxID,payload.RemoteMailbox.ExpectedRevision);err!=nil{return err}}
			if routing!=nil{if err:=validateRoutingPlan(ctx,tx,orgID,routing);err!=nil{return err}}
			if memberPlan!=nil{if err:=validateMemberCreationProgress(ctx,tx,orgID,view.ID,memberPlan);err!=nil{return err}}
			if mutationPlan!=nil{if err:=validateMailboxMutationProgress(ctx,tx,orgID,view.ID,mutationPlan);err!=nil{return err}}
			if statusPlan!=nil{if err:=validateMemberStatusProgress(ctx,tx,orgID,view.ID,statusPlan);err!=nil{return err}}
			if additionPlan!=nil{if err:=validateMailboxAdditionProgress(ctx,tx,orgID,view.ID,additionPlan);err!=nil{return err}}
			keys:=remoteOperationKeys(payload);if routing!=nil{keys=append(keys,routingKeys(routing)...)};sort.Strings(keys)
			if mailboxID.Valid{keys=append(keys,"mailbox:"+fmtID(mailboxID.Int64));sort.Strings(keys)}
			if memberPlan!=nil{keys=append(keys,memberCreationKeys(memberPlan)...);target,err:=memberCreationMailboxID(ctx,tx,view.ID,memberPlan.MailboxID);if err!=nil{return err};if target>0{keys=append(keys,"mailbox:"+fmtID(target))};sort.Strings(keys)}
			if mutationPlan!=nil{keys=append(keys,mailboxMutationKeys(mutationPlan)...);sort.Strings(keys)}
			if statusPlan!=nil{keys=append(keys,memberStatusKeys(statusPlan)...);sort.Strings(keys)}
			if additionPlan!=nil{keys=append(keys,mailboxAdditionKeys(additionPlan)...);target,_,err:=mailboxAdditionCommitted(ctx,tx,view.ID);if err!=nil{return err};if target>0{keys=append(keys,"mailbox:"+fmtID(target))};sort.Strings(keys)}
			for _,key:=range keys{var other string;err:=tx.QueryRowContext(ctx,`SELECT operation_id FROM operation_locks WHERE org_id=? AND resource_key=? AND operation_id!=?`,orgID,key,view.ID).Scan(&other);if err==nil{typed:=provider.Errorf("operation_in_progress","资源已有进行中的操作");typed.OperationID=&other;return typed};if !db.IsNotFound(err){return err};if _,err:=tx.ExecContext(ctx,`INSERT INTO operation_locks(org_id,resource_key,operation_id) VALUES (?,?,?) ON CONFLICT(org_id,resource_key) DO NOTHING`,orgID,key,view.ID);err!=nil{return err}}
			if _,err:=tx.ExecContext(ctx,`UPDATE operation_steps SET status='pending',error_code=NULL,started_at=NULL,finished_at=NULL WHERE operation_id=? AND status='failed'`,view.ID);err!=nil{return err}
			_,err:=tx.ExecContext(ctx,`UPDATE operations SET status='queued',error_code=NULL,updated_at=? WHERE id=?`,db.Now(),view.ID);return err
		default:return provider.Errorf("invalid","操作动作无效")
		}
	});if err!=nil{return nil,err}
	s.audit(ctx,orgID,actor,"operation."+action,"operation",view.ID,nil)
	select{case s.operationWake<-struct{}{}:default:}
	return s.OperationForMember(ctx,orgID,actor,view.ID)
}

func (s *Service) reconcileRemoteSteps(ctx context.Context,orgID,actor int64,id string,payload OperationPayload) error {
	view,err:=s.Operation(ctx,orgID,id);if err!=nil{return err}
	for _,step:=range view.Steps {
		if step.StepKey=="execute" || (step.Status!="unknown" && step.Status!="running"){continue}
		status:=""
		if strings.HasSuffix(step.StepKey,"credential.create") || strings.Contains(step.StepKey,"credential.revoke."){status,err=s.reconcileCredentialStep(ctx,orgID,id,step.StepKey,payload);if err!=nil{return err}
		}else if step.StepKey=="mailbox.resetPassword"{status,err=s.reconcilePrimaryPassword(ctx,orgID,id,payload);if err!=nil{return err}
		}else if payload.Routing!=nil && strings.HasPrefix(step.StepKey,"address."){
			var body string;if err:=s.DB.QueryRowContext(ctx,`SELECT result_json FROM operation_steps WHERE operation_id=? AND step_key='routing.prepare'`,id).Scan(&body);err!=nil{return err};var plan routingPlan;if err:=json.Unmarshal([]byte(body),&plan);err!=nil{return err}
			found:=false;for _,rule:=range plan.Rules{prefix:="address."+fmtID(rule.ID)+".routing.";if step.StepKey!=prefix+"create" && step.StepKey!=prefix+"delete"{continue};found=true
				api,c,err:=s.connectionAdapter(ctx,orgID,rule.ConnectionID);if err!=nil{return err};if c.Revision!=rule.ConnectionRevision{return provider.Errorf("revision_conflict","规则所属连接已经更新")}
				request:=provider.AddressRuleRequest{Domain:rule.Domain,LocalPart:rule.Local,Prefix:rule.Kind==model.AddressPrefix,Catchall:rule.Kind==model.AddressCatchall,Targets:rule.Targets}
				observed,readErr:=api.GetAddressRule(ctx,request);if readErr!=nil && operationErrorCode(readErr)!="not_found"{return readErr}
				if strings.HasSuffix(step.StepKey,".delete"){if operationErrorCode(readErr)=="not_found"{status="succeeded"}else{status="failed"}}else{if operationErrorCode(readErr)=="not_found"{status="failed"}else if sameRoutingTargets(observed.Targets,rule.Targets){status="succeeded"}else{return provider.Errorf("revision_conflict","远程规则目标已经更新")}}
			};if !found{return provider.Errorf("verification_required","没有独立的远程规则核验信息")}
		}else if payload.Kind=="member.status" && strings.HasPrefix(step.StepKey,"member.status.group."){
			plan,err:=readMemberStatusPlan(ctx,s.DB,id);if err!=nil{return err};status,err=s.reconcileNestedRoutingStep(ctx,orgID,id,step.StepKey,"member.status.group.",plan.Groups);if err!=nil{return err}
		}else if mailboxAdditionOperation(payload.Kind) && strings.HasPrefix(step.StepKey,"mailbox.addition.group."){
			plan,err:=readMailboxAdditionPlan(ctx,s.DB,id);if err!=nil{return err};status,err=s.reconcileNestedRoutingStep(ctx,orgID,id,step.StepKey,"mailbox.addition.group.",plan.Groups);if err!=nil{return err}
		}else if mailboxMutationOperation(payload.Kind) && strings.HasPrefix(step.StepKey,"mailbox.group."){
			plan,err:=readMailboxMutationPlan(ctx,s.DB,id);if err!=nil{return err};found:=false
			for _,group:=range plan.Groups{prefix:="mailbox.group."+fmtID(group.GroupID)+".";if !strings.HasPrefix(step.StepKey,prefix){continue};var body string;if err:=s.DB.QueryRowContext(ctx,`SELECT result_json FROM operation_steps WHERE operation_id=? AND step_key=? AND status='succeeded'`,id,prefix+"routing.prepare").Scan(&body);err!=nil{return err};if err:=json.Unmarshal([]byte(body),&group);err!=nil{return err}
				for _,rule:=range group.Rules{key:=prefix+"address."+fmtID(rule.ID)+".routing.";if step.StepKey!=key+"create" && step.StepKey!=key+"delete"{continue};found=true;api,c,err:=s.connectionAdapter(ctx,orgID,rule.ConnectionID);if err!=nil{return err};if c.Revision!=rule.ConnectionRevision{return provider.Errorf("revision_conflict","群组规则的连接已经更新")};status,err=readRoutingStepOutcome(ctx,api,step.StepKey,provider.AddressRuleRequest{Domain:rule.Domain,LocalPart:rule.Local,Prefix:rule.Kind==model.AddressPrefix,Catchall:rule.Kind==model.AddressCatchall,Targets:rule.Targets});if err!=nil{return err}}
			};if !found{return provider.Errorf("verification_required","邮箱关联群组步骤需要独立核验")}
		}else if payload.MemberCreate!=nil && strings.HasPrefix(step.StepKey,"member."){
			plan,err:=readMemberCreationPlan(ctx,s.DB,id);if err!=nil{return err}
			if step.StepKey=="member.mailbox.mailbox.create" && payload.MemberCreate.Create!=nil{
				api,c,err:=s.connectionAdapter(ctx,orgID,plan.ConnectionID);if err!=nil{return err};if c.Revision!=plan.ConnectionRevision{return provider.Errorf("revision_conflict","成员邮箱的连接已经更新")}
				binding,err:=s.DomainBinding(ctx,orgID,plan.BindingID);if err!=nil{return err};if binding.Revision!=plan.BindingRevision || binding.ConnectionID!=plan.ConnectionID{return provider.Errorf("revision_conflict","成员邮箱的域名关联已经更新")}
				_,readErr:=api.GetMailbox(ctx,provider.GetMailboxRequest{Domain:binding.DomainName,LocalPart:payload.MemberCreate.Create.LocalPart});if operationErrorCode(readErr)=="not_found"{status="failed"}else if readErr!=nil{return readErr}else{status="succeeded"}
			}else{
				found:=false
				for _,group:=range plan.Groups{prefix:="member.group."+fmtID(group.GroupID)+".";if !strings.HasPrefix(step.StepKey,prefix){continue};var body string;if err:=s.DB.QueryRowContext(ctx,`SELECT result_json FROM operation_steps WHERE operation_id=? AND step_key=? AND status='succeeded'`,id,prefix+"routing.prepare").Scan(&body);err!=nil{return err};if err:=json.Unmarshal([]byte(body),&group);err!=nil{return err}
					for _,rule:=range group.Rules{key:=prefix+"address."+fmtID(rule.ID)+".routing.";if step.StepKey!=key+"create" && step.StepKey!=key+"delete"{continue};found=true;api,c,err:=s.connectionAdapter(ctx,orgID,rule.ConnectionID);if err!=nil{return err};if c.Revision!=rule.ConnectionRevision{return provider.Errorf("revision_conflict","群组规则的连接已经更新")};status,err=readRoutingStepOutcome(ctx,api,step.StepKey,provider.AddressRuleRequest{Domain:rule.Domain,LocalPart:rule.Local,Prefix:rule.Kind==model.AddressPrefix,Catchall:rule.Kind==model.AddressCatchall,Targets:rule.Targets});if err!=nil{return err}}
				};if !found{return provider.Errorf("verification_required","成员操作步骤需要独立核验")}
			}
		}else if payload.Forwarding!=nil && strings.HasPrefix(step.StepKey,"forwarding.routing."){
			var body string;if err:=s.DB.QueryRowContext(ctx,`SELECT result_json FROM operation_steps WHERE operation_id=? AND step_key='forwarding.prepare'`,id).Scan(&body);err!=nil{return err};var plan forwardingPlan;if err:=json.Unmarshal([]byte(body),&plan);err!=nil{return err}
			api,c,err:=s.connectionAdapter(ctx,orgID,plan.ConnectionID);if err!=nil{return err};if c.Revision!=plan.ConnectionRevision{return provider.Errorf("revision_conflict","转发所属连接已经更新")};local,domain:=SplitAddress(plan.Address)
			status,err=readRoutingStepOutcome(ctx,api,step.StepKey,provider.AddressRuleRequest{Domain:domain,LocalPart:local,Targets:plan.Targets});if err!=nil{return err}
		}else if payload.Offboard!=nil && strings.HasPrefix(step.StepKey,"group."){
			found:=false;for _,group:=range payload.Offboard.Groups{prefix:="group."+fmtID(group.GroupID)+".routing.";if step.StepKey!=prefix+"create" && step.StepKey!=prefix+"delete"{continue};found=true;api,_,err:=s.connectionAdapter(ctx,orgID,group.ConnectionID);if err!=nil{return err};status,err=readRoutingStepOutcome(ctx,api,step.StepKey,provider.AddressRuleRequest{Domain:group.Domain,LocalPart:group.LocalPart,Targets:group.Targets});if err!=nil{return err}};if !found{return provider.Errorf("verification_required","群组步骤缺少独立核验信息")}
		}else if step.StepKey=="sieve.upload" || step.StepKey=="sieve.activate"{
			if err:=s.requireOperationPermission(ctx,orgID,actor,payload.MailboxID,permissionMailFull);err!=nil{return err}
			if payload.SessionHash!=""{active,err:=s.SessionHashActive(ctx,view.ActorID,payload.SessionHash);if err!=nil{return err};if !active{return ErrForbidden}}
			var body string;if err:=s.DB.QueryRowContext(ctx,`SELECT result_json FROM operation_steps WHERE operation_id=? AND step_key='sieve.prepare'`,id).Scan(&body);err!=nil{return err};var plan sievePlan;if err:=json.Unmarshal([]byte(body),&plan);err!=nil{return err}
			client,endpoint,err:=s.rulesClient(ctx,orgID,payload.MailboxID);if err!=nil{return err}
			if endpoint.ConnectionRevision!=plan.ConnectionRevision || endpoint.EndpointRevision!=plan.EndpointRevision{client.Close();return provider.Errorf("revision_conflict","协议配置已经更新")}
			if step.StepKey=="sieve.upload"{script,readErr:=client.GetScript(plan.CandidateName);client.Close();if readErr!=nil{return provider.Errorf("verification_required","候选脚本读取未确认")};if scriptHash(script)!=plan.CandidateHash{return provider.Errorf("revision_conflict","候选脚本内容已更新")};status="succeeded"}else{
				state,readErr:=readSieveState(client,model.MailboxSettings{});client.Close();if readErr!=nil{return readErr};if state.Active==plan.CandidateName && state.ActiveScriptHash==plan.CandidateHash{status="succeeded"}else if state.Active==plan.OriginalName && state.ActiveScriptHash==plan.OriginalHash{status="failed"}else{return provider.Errorf("revision_conflict","active script 已更新")}
			}
		}else{
			api,_,err:=s.connectionAdapter(ctx,orgID,payload.ConnectionID);if err!=nil{return err}
			switch step.StepKey {
			case "domain.create","domain.delete","domain.configure":
				if payload.Domain==nil{return provider.Errorf("invalid","缺少域名请求")}
				info,readErr:=api.GetDomain(ctx,provider.GetDomainRequest{Domain:payload.Domain.DomainName})
				var typed *provider.TypedError;missing:=errors.As(readErr,&typed) && typed.Code=="not_found"
				if readErr!=nil && !missing{return readErr}
				if step.StepKey=="domain.delete"{if missing{status="succeeded"}else{status="failed"}}else if missing{status="failed"}else if step.StepKey=="domain.create"{status="succeeded"}else{
					settings:=payload.Domain.ProviderSettings;if settings==nil{return provider.Errorf("invalid","缺少域名设置")}
					matches:=(settings.AllowAccountReset==nil || (info.AllowAccountReset!=nil && *settings.AllowAccountReset==*info.AllowAccountReset)) && (settings.SymbolicSubaddressing==nil || (info.SymbolicSubaddressing!=nil && *settings.SymbolicSubaddressing==*info.SymbolicSubaddressing));if matches{status="succeeded"}else{status="failed"}
				}
			case "mailbox.create","mailbox.delete":
				var local,domain string
				if payload.Create!=nil{binding,err:=s.DomainBinding(ctx,orgID,payload.Create.DomainBindingID);if err!=nil{return err};local=payload.Create.LocalPart;domain=binding.DomainName}else{mb,err:=s.Mailbox(ctx,orgID,payload.MailboxID);if err!=nil{return err};local,domain=SplitAddress(mb.Address)}
				_,readErr:=api.GetMailbox(ctx,provider.GetMailboxRequest{Domain:domain,LocalPart:local});var typed *provider.TypedError;missing:=errors.As(readErr,&typed) && typed.Code=="not_found";if readErr!=nil && !missing{return readErr}
				if (step.StepKey=="mailbox.delete" && missing) || (step.StepKey=="mailbox.create" && !missing){status="succeeded"}else{status="failed"}
			default:return provider.Errorf("verification_required","远程步骤 %s 需要独立核验",step.StepKey)
			}
		}
		if _,err:=s.DB.ExecContext(ctx,`UPDATE operation_steps SET status=?,error_code=NULL,finished_at=? WHERE operation_id=? AND step_key=? AND status IN ('unknown','running')`,status,db.Now(),id,step.StepKey);err!=nil{return err}
	}
	return nil
}

func readRoutingStepOutcome(ctx context.Context,api provider.Provider,key string,request provider.AddressRuleRequest) (string,error) {
	observed,err:=api.GetAddressRule(ctx,request);missing:=operationErrorCode(err)=="not_found";if err!=nil && !missing{return "",err}
	if strings.HasSuffix(key,".delete"){if missing{return "succeeded",nil};return "failed",nil}
	if !strings.HasSuffix(key,".create"){return "",provider.Errorf("verification_required","步骤需要独立核验")};if missing{return "failed",nil};if !sameRoutingTargets(observed.Targets,request.Targets){return "",provider.Errorf("revision_conflict","远程目标已经更新")};return "succeeded",nil
}

func (s *Service) reconcileNestedRoutingStep(ctx context.Context,orgID int64,id,key,namespace string,groups []routingPlan) (string,error) {
	for _,group:=range groups{
		prefix:=namespace+fmtID(group.GroupID)+".";if !strings.HasPrefix(key,prefix){continue}
		var body string;if err:=s.DB.QueryRowContext(ctx,`SELECT result_json FROM operation_steps WHERE operation_id=? AND step_key=? AND status='succeeded'`,id,prefix+"routing.prepare").Scan(&body);err!=nil{return "",err};if err:=json.Unmarshal([]byte(body),&group);err!=nil{return "",err}
		for _,rule:=range group.Rules{ruleKey:=prefix+"address."+fmtID(rule.ID)+".routing.";if key!=ruleKey+"create" && key!=ruleKey+"delete"{continue};api,c,err:=s.connectionAdapter(ctx,orgID,rule.ConnectionID);if err!=nil{return "",err};if c.Revision!=rule.ConnectionRevision{return "",provider.Errorf("revision_conflict","群组规则的连接已经更新")};return readRoutingStepOutcome(ctx,api,key,provider.AddressRuleRequest{Domain:rule.Domain,LocalPart:rule.Local,Prefix:rule.Kind==model.AddressPrefix,Catchall:rule.Kind==model.AddressCatchall,Targets:rule.Targets})}
	}
	return "",provider.Errorf("verification_required","群组步骤需要独立核验")
}
