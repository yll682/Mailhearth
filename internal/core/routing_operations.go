package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

type RoutingOperationInput struct {
	AddressID int64 `json:"addressId,omitempty"`
	GroupID int64 `json:"groupId,omitempty"`
	Address *AddressInput `json:"address,omitempty"`
	Group *GroupInput `json:"group,omitempty"`
	ExpectedRevision int64 `json:"expectedRevision,omitempty"`
}

type routingVersion struct {
	ID int64 `json:"id"`
	Revision int64 `json:"revision"`
}

type routingRulePlan struct {
	ID int64 `json:"addressId"`
	Revision int64 `json:"revision"`
	ConnectionID int64 `json:"connectionId"`
	ConnectionRevision int64 `json:"connectionRevision"`
	DomainID int64 `json:"domainId"`
	BindingID *int64 `json:"domainBindingId"`
	Domain string `json:"domain"`
	Local string `json:"localPart"`
	Address string `json:"address"`
	Kind string `json:"kind"`
	MailboxID *int64 `json:"mailboxId"`
	Mode string `json:"managementMode"`
	Targets []string `json:"desiredTargets"`
	Delete bool `json:"delete"`
	Note string `json:"note"`
}

type routingPlan struct {
	Kind string `json:"kind"`
	GroupID int64 `json:"groupId"`
	GroupRevision int64 `json:"groupRevision"`
	GroupName string `json:"groupName"`
	GroupDescription string `json:"groupDescription"`
	GroupMembers []int64 `json:"groupMembers"`
	Members []routingVersion `json:"members"`
	Mailboxes []routingVersion `json:"mailboxes"`
	Rules []routingRulePlan `json:"rules"`
}

func routingKeys(plan *routingPlan) []string {
	keys:=[]string{}
	if plan.GroupID>0{keys=append(keys,"group:"+fmtID(plan.GroupID))}else if strings.HasPrefix(plan.Kind,"group."){keys=append(keys,"group-name:"+plan.GroupName)}
	for _,rule:=range plan.Rules{keys=append(keys,"connection:"+fmtID(rule.ConnectionID),"address-name:"+fmtID(rule.ConnectionID)+":"+rule.Address);if rule.ID>0{keys=append(keys,"address:"+fmtID(rule.ID))};if rule.BindingID!=nil{keys=append(keys,"domain-binding:"+fmtID(*rule.BindingID))}}
	for _,member:=range plan.Members{keys=append(keys,"member:"+fmtID(member.ID))};for _,mailbox:=range plan.Mailboxes{keys=append(keys,"mailbox:"+fmtID(mailbox.ID))}
	return keys
}

func (s *Service) prepareRouting(ctx context.Context,orgID int64,kind string,in *RoutingOperationInput) (*routingPlan,error) {
	if in==nil{return nil,provider.Errorf("invalid","需要地址或群组请求")}
	var plan *routingPlan
	err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{var err error;plan,err=prepareRoutingQuery(ctx,tx,orgID,kind,in);return err})
	return plan,err
}

func routingAddress(ctx context.Context,q managementQuery,orgID,id int64) (*model.Address,error) {
	address,err:=scanAddress(q.QueryRowContext(ctx,addressSelect+` WHERE a.org_id=? AND a.id=?`,orgID,id));if db.IsNotFound(err){return nil,ErrNotFound};return address,err
}

func existingRulePlan(ctx context.Context,q managementQuery,orgID int64,address *model.Address) (routingRulePlan,error) {
	var revision int64;if err:=q.QueryRowContext(ctx,`SELECT revision FROM mail_connections WHERE id=? AND org_id=?`,address.ConnectionID,orgID).Scan(&revision);err!=nil{return routingRulePlan{},err}
	plan:=routingRulePlan{ID:address.ID,Revision:address.Revision,ConnectionID:address.ConnectionID,ConnectionRevision:revision,DomainID:address.DomainID,BindingID:address.DomainBindingID,Domain:address.Domain,Local:address.LocalPart,Address:address.Address,Kind:address.Kind,MailboxID:address.MailboxID,Mode:address.ManagementMode,Targets:append([]string{},address.Targets...),Note:address.Note}
	if err:=checkRoutingManagement(ctx,q,orgID,plan);err!=nil{return routingRulePlan{},err};return plan,nil
}

func checkRoutingManagement(ctx context.Context,q managementQuery,orgID int64,rule routingRulePlan) error {
	if rule.Mode=="external"{return nil}
	if rule.Mode!="api" || rule.BindingID==nil{return provider.Errorf("verification_required","远程规则需要可管理的域名关联")}
	var kind,status,scopeJSON,bindingMode,state string;var credential sql.NullInt64;var enabled bool
	if err:=q.QueryRowContext(ctx,`SELECT c.provider_kind,c.enabled,c.last_api_check_status,c.api_credential_id,c.domain_scope_json,b.management_mode,b.remote_state FROM mail_connections c JOIN domain_bindings b ON b.connection_id=c.id WHERE c.id=? AND c.org_id=? AND b.id=? AND b.domain_id=?`,rule.ConnectionID,orgID,*rule.BindingID,rule.DomainID).Scan(&kind,&enabled,&status,&credential,&scopeJSON,&bindingMode,&state);err!=nil{return err}
	if !enabled{return provider.Errorf("endpoint_disabled","所属连接已经停用")}
	if kind==string(provider.Manual){return provider.Errorf("external_action_required","手动连接需要登记外部规则")}
	if kind==string(provider.Migadu){return provider.Errorf("verification_required","Migadu 地址管理需要完成真实投递验证")}
	if !credential.Valid || status!="passed" || bindingMode!="api" || state!="present"{return provider.Errorf("verification_required","管理认证和域名关联需要通过核验")}
	var scope provider.DomainScope;if err:=json.Unmarshal([]byte(scopeJSON),&scope);err!=nil{return err};if !scopeContainsDomain(scope,rule.Domain){return provider.Errorf("target_constraint_failed","规则域名不在连接范围内")};return nil
}

func newRulePlan(ctx context.Context,q managementQuery,orgID,connectionID,bindingID,domainID int64,local,kind,mode string) (routingRulePlan,error) {
	var plan routingRulePlan;plan.Kind=kind;plan.ConnectionID=connectionID;plan.Mode=mode;plan.Targets=[]string{}
	if connectionID<1 || (mode!="api" && mode!="external"){return plan,provider.Errorf("invalid","需要 connectionId 和 api/external 管理方式")}
	if bindingID>0{plan.BindingID=&bindingID;var bindingMode,state string;if err:=q.QueryRowContext(ctx,`SELECT b.domain_id,d.name,b.management_mode,b.remote_state FROM domain_bindings b JOIN domains d ON d.id=b.domain_id WHERE b.id=? AND b.org_id=? AND b.connection_id=?`,bindingID,orgID,connectionID).Scan(&plan.DomainID,&plan.Domain,&bindingMode,&state);err!=nil{if db.IsNotFound(err){return plan,ErrNotFound};return plan,err};if mode=="api" && (bindingMode!="api" || state!="present"){return plan,provider.Errorf("verification_required","域名关联需要确认可管理")}}else{
		if mode=="api" || domainID<1{return plan,provider.Errorf("invalid","远程规则需要 domainBindingId")}
		plan.DomainID=domainID;if err:=q.QueryRowContext(ctx,`SELECT name FROM domains WHERE id=? AND org_id=?`,domainID,orgID).Scan(&plan.Domain);err!=nil{return plan,err}
	}
	var providerKind,scopeJSON,apiStatus string;var enabled bool;var credential sql.NullInt64
	if err:=q.QueryRowContext(ctx,`SELECT provider_kind,revision,enabled,domain_scope_json,api_credential_id,last_api_check_status FROM mail_connections WHERE id=? AND org_id=?`,connectionID,orgID).Scan(&providerKind,&plan.ConnectionRevision,&enabled,&scopeJSON,&credential,&apiStatus);err!=nil{return plan,err}
	if !enabled{return plan,provider.Errorf("endpoint_disabled","所属连接已经停用")}
	if mode=="api"{
		if providerKind==string(provider.Manual){return plan,provider.Errorf("external_action_required","手动连接需要明确登记外部规则")}
		if providerKind==string(provider.Migadu){return plan,provider.Errorf("verification_required","Migadu 地址管理需要完成真实投递验证")}
		if !credential.Valid || apiStatus!="passed"{return plan,provider.Errorf("verification_required","管理认证尚未通过")}
		var scope provider.DomainScope;if err:=json.Unmarshal([]byte(scopeJSON),&scope);err!=nil{return plan,err};if !scopeContainsDomain(scope,plan.Domain){return plan,provider.Errorf("target_constraint_failed","规则域名不在连接范围内")}
	}
	local=strings.TrimSpace(local);if providerKind!=string(provider.Manual){local=strings.ToLower(local)}
	if kind==model.AddressCatchall{if local!=""{return plan,provider.Errorf("invalid","catch-all 的 localPart 必须为空")};plan.Local="";plan.Address="*@"+plan.Domain}else{
		if !validLocalPart(local){return plan,provider.Errorf("invalid","localPart 无效")};plan.Local=local;plan.Address=local+"@"+plan.Domain;if kind==model.AddressPrefix{plan.Address=local+"*@"+plan.Domain}
	}
	var exists bool;if err:=q.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM addresses WHERE connection_id=? AND address_key=?)`,connectionID,plan.Address).Scan(&exists);err!=nil{return plan,err};if exists{return plan,provider.Errorf("address_exists","该连接已经登记此地址")}
	return plan,nil
}

func prepareRoutingQuery(ctx context.Context,q managementQuery,orgID int64,kind string,in *RoutingOperationInput) (*routingPlan,error) {
	plan:=&routingPlan{Kind:kind,Rules:[]routingRulePlan{},Members:[]routingVersion{},Mailboxes:[]routingVersion{},GroupMembers:[]int64{}}
	if strings.HasPrefix(kind,"address."){
		var rule routingRulePlan
		if kind=="address.create"{
			if in.Address==nil{return nil,provider.Errorf("invalid","需要地址请求")};input:=in.Address
			var err error;rule,err=newRulePlan(ctx,q,orgID,input.ConnectionID,input.DomainBindingID,input.DomainID,input.LocalPart,input.Kind,input.Mode);if err!=nil{return nil,err}
		}else{
			address,err:=routingAddress(ctx,q,orgID,in.AddressID);if err!=nil{return nil,err};if address.Kind==model.AddressPrimary || address.Kind==model.AddressGroup || address.Kind==model.AddressExternalRule{return nil,provider.Errorf("invalid","此地址需要通过所属对象的专用操作管理")}
			expected:=in.ExpectedRevision;if in.Address!=nil{expected=in.Address.ExpectedRevision};if expected<1 || address.Revision!=expected{return nil,provider.Errorf("revision_conflict","地址已经更新")}
			rule,err=existingRulePlan(ctx,q,orgID,address);if err!=nil{return nil,err}
		}
		if kind=="address.delete"{rule.Delete=true;rule.Targets=[]string{}}else{
			input:=in.Address;if input==nil{return nil,provider.Errorf("invalid","需要地址请求")}
			if kind=="address.update" && ((input.Kind!="" && input.Kind!=rule.Kind) || (input.ConnectionID>0 && input.ConnectionID!=rule.ConnectionID) || (input.DomainBindingID>0 && (rule.BindingID==nil || input.DomainBindingID!=*rule.BindingID)) || (input.LocalPart!="" && input.LocalPart!=rule.Local)){return nil,provider.Errorf("invalid","地址更新不能改变类型、连接或名称")}
			switch rule.Kind{case model.AddressAlias,model.AddressForward,model.AddressCatchall,model.AddressPrefix:default:return nil,provider.Errorf("invalid","地址类型无效")}
			if rule.Kind==model.AddressAlias{
				if input.MailboxID<1 || len(input.Targets)>0{return nil,provider.Errorf("invalid","alias 需要唯一 mailboxId")}
				var address,status string;var connectionID,revision int64;if err:=q.QueryRowContext(ctx,`SELECT address,connection_id,revision,status FROM mailboxes WHERE id=? AND org_id=?`,input.MailboxID,orgID).Scan(&address,&connectionID,&revision,&status);err!=nil{return nil,err};if connectionID!=rule.ConnectionID || status!=model.MailboxActive{return nil,provider.Errorf("target_constraint_failed","alias 目标必须为相同连接的 active 邮箱")};id:=input.MailboxID;rule.MailboxID=&id;rule.Targets=[]string{address};plan.Mailboxes=append(plan.Mailboxes,routingVersion{id,revision})
			}else{
				if input.MailboxID!=0{return nil,provider.Errorf("invalid","此规则只接受 targets")};set:=map[string]bool{};for _,target:=range input.Targets{value,err:=NormalizeEmail(target);if err!=nil{return nil,err};if value==strings.ToLower(rule.Address){return nil,provider.Errorf("invalid","规则不能转发到自身")};set[value]=true};if len(set)==0{return nil,provider.Errorf("invalid","需要至少一个投递目标")};rule.MailboxID=nil;rule.Targets=make([]string,0,len(set));for value:=range set{rule.Targets=append(rule.Targets,value)};sort.Strings(rule.Targets)
			}
			rule.Note=strings.TrimSpace(input.Note)
		}
		plan.Rules=append(plan.Rules,rule);return plan,nil
	}
	input:=in.Group
	if kind!="group.create"{
		if err:=q.QueryRowContext(ctx,`SELECT id,revision,name,description FROM groups WHERE id=? AND org_id=?`,in.GroupID,orgID).Scan(&plan.GroupID,&plan.GroupRevision,&plan.GroupName,&plan.GroupDescription);err!=nil{if db.IsNotFound(err){return nil,ErrNotFound};return nil,err}
		expected:=in.ExpectedRevision;if input!=nil{expected=input.ExpectedRevision};if expected<1 || expected!=plan.GroupRevision{return nil,provider.Errorf("revision_conflict","群组已经更新")}
		rows,err:=q.QueryContext(ctx,`SELECT member_id FROM group_members WHERE group_id=? ORDER BY member_id`,plan.GroupID);if err!=nil{return nil,err};for rows.Next(){var id int64;if err:=rows.Scan(&id);err!=nil{rows.Close();return nil,err};plan.GroupMembers=append(plan.GroupMembers,id)};err=rows.Err();rows.Close();if err!=nil{return nil,err}
	}
	var old *model.Address
	if plan.GroupID>0{var addressID int64;err:=q.QueryRowContext(ctx,`SELECT id FROM addresses WHERE org_id=? AND group_id=?`,orgID,plan.GroupID).Scan(&addressID);if err==nil{old,err=routingAddress(ctx,q,orgID,addressID);if err!=nil{return nil,err}}else if !db.IsNotFound(err){return nil,err}}
	if kind=="group.delete"{if old!=nil{rule,err:=existingRulePlan(ctx,q,orgID,old);if err!=nil{return nil,err};rule.Delete=true;rule.Targets=[]string{};plan.Rules=append(plan.Rules,rule)};return plan,nil}
	if input==nil{return nil,provider.Errorf("invalid","需要群组请求")}
	if strings.TrimSpace(input.Name)!=""{plan.GroupName=strings.TrimSpace(input.Name)};if plan.GroupName==""{return nil,provider.Errorf("invalid","群组名称不能为空")};plan.GroupDescription=strings.TrimSpace(input.Description)
	if input.MemberIDs!=nil{plan.GroupMembers=append([]int64{},input.MemberIDs...)};sort.Slice(plan.GroupMembers,func(i,j int)bool{return plan.GroupMembers[i]<plan.GroupMembers[j]})
	for i,id:=range plan.GroupMembers{if id<1 || (i>0 && plan.GroupMembers[i-1]==id){return nil,provider.Errorf("invalid","群组成员必须唯一")};var revision int64;if err:=q.QueryRowContext(ctx,`SELECT revision FROM members WHERE id=? AND org_id=?`,id,orgID).Scan(&revision);err!=nil{return nil,provider.Errorf("invalid","群组成员不属于当前组织")};plan.Members=append(plan.Members,routingVersion{id,revision})}
	if input.RemoveAddress{
		if input.DomainBindingID>0 || input.AddressLocal!=""{return nil,provider.Errorf("invalid","移除群组地址不能同时指定新地址")}
		if old!=nil{rule,err:=existingRulePlan(ctx,q,orgID,old);if err!=nil{return nil,err};rule.Delete=true;rule.Targets=[]string{};plan.Rules=append(plan.Rules,rule)}
		return plan,nil
	}
	var rule *routingRulePlan
	if input.DomainBindingID>0 || input.AddressDomainID>0{
		if old!=nil && input.ConnectionID==old.ConnectionID && input.AddressLocal==old.LocalPart && ((old.DomainBindingID!=nil && input.DomainBindingID==*old.DomainBindingID) || input.AddressDomainID==old.DomainID){value,err:=existingRulePlan(ctx,q,orgID,old);if err!=nil{return nil,err};rule=&value}else{
			value,err:=newRulePlan(ctx,q,orgID,input.ConnectionID,input.DomainBindingID,input.AddressDomainID,input.AddressLocal,model.AddressGroup,input.Mode);if err!=nil{return nil,err};rule=&value
			if old!=nil{previous,err:=existingRulePlan(ctx,q,orgID,old);if err!=nil{return nil,err};previous.Delete=true;previous.Targets=[]string{};plan.Rules=append(plan.Rules,previous)}
		}
	}else if old!=nil{value,err:=existingRulePlan(ctx,q,orgID,old);if err!=nil{return nil,err};rule=&value}
	if rule!=nil{
		targets,versions,err:=groupCandidateTargets(ctx,q,orgID,plan.GroupID,plan.GroupMembers,*rule);if err!=nil{return nil,err};rule.Targets=targets;plan.Mailboxes=versions;plan.Rules=append(plan.Rules,*rule)
	}
	return plan,nil
}

func groupCandidateTargets(ctx context.Context,q managementQuery,orgID,groupID int64,members []int64,rule routingRulePlan,proposed ...*model.Mailbox) ([]string,[]routingVersion,error) {
	change:=&model.Mailbox{};if len(proposed)>0{change=proposed[0]};return computeGroupCandidateTargets(ctx,q,orgID,groupID,members,rule,change,&model.Member{})
}

func computeGroupCandidateTargets(ctx context.Context,q managementQuery,orgID,groupID int64,members []int64,rule routingRulePlan,change *model.Mailbox,memberChange *model.Member) ([]string,[]routingVersion,error) {
	var kind string;if err:=q.QueryRowContext(ctx,`SELECT provider_kind FROM mail_connections WHERE id=? AND org_id=?`,rule.ConnectionID,orgID).Scan(&kind);err!=nil{return nil,nil,err}
	rows,err:=q.QueryContext(ctx,`WITH candidates AS (SELECT id,org_id,connection_id,address,CASE WHEN id=? THEN ? ELSE revision END AS revision,CASE WHEN id=? THEN ? ELSE owner_member_id END AS owner_member_id,CASE WHEN id=? THEN ? ELSE kind END AS kind,CASE WHEN id=? THEN ? ELSE status END AS status FROM mailboxes WHERE org_id=?) SELECT b.id,b.revision,b.connection_id,b.address,m.id FROM candidates b JOIN members m ON m.id=b.owner_member_id WHERE m.org_id=? AND b.kind='personal' AND b.status='active' AND CASE WHEN m.id=? THEN ? ELSE m.status END IN ('active','invited') AND m.id IN (SELECT value FROM json_each(?)) ORDER BY b.id`,change.ID,change.Revision,change.ID,sqlNullInt(change.OwnerMemberID),change.ID,change.Kind,change.ID,change.Status,orgID,orgID,memberChange.ID,memberChange.Status,toJSON(members));if err!=nil{return nil,nil,err};defer rows.Close()
	set:=map[string]bool{};versions:=[]routingVersion{}
	for rows.Next(){var id,revision,connectionID,memberID int64;var address string;if err:=rows.Scan(&id,&revision,&connectionID,&address,&memberID);err!=nil{return nil,nil,err};_,domain:=SplitAddress(address)
		if kind==string(provider.Migadu) && (connectionID!=rule.ConnectionID || domain!=rule.Domain){failure:=provider.Errorf("target_constraint_failed","群组目标必须属于相同连接和域名");failure.Details=map[string]any{"memberId":memberID,"mailboxId":id,"groupId":groupID,"reasonCode":"same_connection_and_domain_required"};return nil,nil,failure}
		set[strings.ToLower(address)]=true;versions=append(versions,routingVersion{id,revision})
	};if err:=rows.Err();err!=nil{return nil,nil,err}
	targets:=make([]string,0,len(set));for address:=range set{targets=append(targets,address)};sort.Strings(targets);return targets,versions,nil
}

func (s *Service) reserveRouting(ctx context.Context,tx *sql.Tx,orgID int64,id string,in *RoutingOperationInput,prepared *routingPlan) error {
	if err:=checkOperationPermissionTx(ctx,tx,id);err!=nil{return err}
	current,err:=prepareRoutingQuery(ctx,tx,orgID,prepared.Kind,in);if err!=nil{return err};if !reflect.DeepEqual(current,prepared){return provider.Errorf("revision_conflict","群组或地址关联的数据已经更新")}
	plan:=*prepared;plan.Rules=append([]routingRulePlan{},prepared.Rules...);now:=db.Now()
	if plan.Kind=="group.create"{result,err:=tx.ExecContext(ctx,`INSERT INTO groups(org_id,name,description,created_at,updated_at) VALUES (?,?,?,?,?)`,orgID,plan.GroupName,plan.GroupDescription,now,now);if err!=nil{return err};plan.GroupID,err=result.LastInsertId();if err!=nil{return err};plan.GroupRevision=1;if _,err:=tx.ExecContext(ctx,`INSERT INTO operation_locks(org_id,resource_key,operation_id) VALUES (?,?,?)`,orgID,"group:"+fmtID(plan.GroupID),id);err!=nil{return err}}
	for i:=range plan.Rules{
		rule:=&plan.Rules[i]
		if rule.ID==0{
			var groupID any;if rule.Kind==model.AddressGroup{groupID=plan.GroupID}
			result,err:=tx.ExecContext(ctx,`INSERT INTO addresses(org_id,connection_id,domain_id,domain_binding_id,local_part,address,address_key,kind,mailbox_id,group_id,management_mode,desired_targets_json,sync_state,is_prefix,is_catchall,note,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,'pending',?,?,?,?,?)`,orgID,rule.ConnectionID,rule.DomainID,sqlNullInt(rule.BindingID),rule.Local,rule.Address,rule.Address,rule.Kind,sqlNullInt(rule.MailboxID),groupID,rule.Mode,toJSON(rule.Targets),boolInt(rule.Kind==model.AddressPrefix),boolInt(rule.Kind==model.AddressCatchall),rule.Note,now,now);if err!=nil{return err};rule.ID,err=result.LastInsertId();if err!=nil{return err};rule.Revision=1
			if _,err:=tx.ExecContext(ctx,`INSERT INTO operation_locks(org_id,resource_key,operation_id) VALUES (?,?,?)`,orgID,"address:"+fmtID(rule.ID),id);err!=nil{return err}
		}else{res,err:=tx.ExecContext(ctx,`UPDATE addresses SET desired_targets_json=?,sync_state='pending',revision=revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=?`,toJSON(rule.Targets),now,rule.ID,orgID,rule.Revision);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","地址已经更新")};rule.Revision++}
	}
	_,err=tx.ExecContext(ctx,`INSERT INTO operation_steps(operation_id,step_key,sequence,status,result_json,finished_at) SELECT ?,'routing.prepare',COALESCE(MAX(sequence),0)+2,'succeeded',?,? FROM operation_steps WHERE operation_id=?`,id,toJSON(plan),now,id);return err
}

func (s *Service) executeRouting(ctx context.Context,orgID,actor int64,payload OperationPayload) (any,error) {
	id:=ctx.Value(operationContextKey{}).(string);var body string
	if err:=s.DB.QueryRowContext(ctx,`SELECT result_json FROM operation_steps WHERE operation_id=? AND step_key=?`,id,operationStepKey(ctx,"routing.prepare")).Scan(&body);err!=nil{return nil,err};var plan routingPlan;if err:=json.Unmarshal([]byte(body),&plan);err!=nil{return nil,err}
	if err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{if err:=validateRoutingPlan(ctx,tx,orgID,&plan);err!=nil{return err};return validateGroupCandidates(ctx,tx,orgID,&plan)});err!=nil{return nil,err}
	observations:=map[int64]provider.AddressRuleInfo{}
	for _,rule:=range plan.Rules{
		if rule.Mode=="external"{continue}
		api,c,err:=s.connectionAdapter(ctx,orgID,rule.ConnectionID);if err!=nil{return nil,err};if c.Revision!=rule.ConnectionRevision{return nil,provider.Errorf("revision_conflict","地址所属连接已经更新")}
		child:=context.WithValue(ctx,operationStepPrefixKey{},operationStepKey(ctx,"address."+fmtID(rule.ID)+"."))
		request:=provider.AddressRuleRequest{Domain:rule.Domain,LocalPart:rule.Local,Prefix:rule.Kind==model.AddressPrefix,Catchall:rule.Kind==model.AddressCatchall,Targets:rule.Targets};if rule.Delete{request.Targets=[]string{}}
		if err:=s.replaceRoutingRule(child,api,request);err!=nil{return nil,err}
		if len(request.Targets)>0{observed,err:=api.GetAddressRule(ctx,request);if err!=nil{return nil,&remoteOutcomeUnknown{err}};if observed.RemoteKey=="" || !sameRoutingTargets(observed.Targets,request.Targets){return nil,&remoteOutcomeUnknown{provider.Errorf("verification_required","远程规则引用和目标需要独立核验")}};observations[rule.ID]=observed}
	}
	result:=map[string]any{"groupId":plan.GroupID,"addressIds":[]int64{},"verificationSource":"provider_read"};ids:=[]int64{};external:=false
	for _,rule:=range plan.Rules{ids=append(ids,rule.ID);if rule.Mode=="external"{external=true}}
	result["addressIds"]=ids;if len(ids)==1{result["addressId"]=ids[0]};if external{result["verificationSource"]="local_registration";result["systemVerified"]=false}
	err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		if err:=checkOperationPermissionTx(ctx,tx,id);err!=nil{return err}
		if err:=validateRoutingPlan(ctx,tx,orgID,&plan);err!=nil{return err}
		if err:=validateGroupCandidates(ctx,tx,orgID,&plan);err!=nil{return err}
		for _,member:=range plan.Members{var revision int64;if err:=tx.QueryRowContext(ctx,`SELECT revision FROM members WHERE id=? AND org_id=?`,member.ID,orgID).Scan(&revision);err!=nil{return err};if revision!=member.Revision{return provider.Errorf("revision_conflict","群组成员已经更新")}}
		for _,mailbox:=range plan.Mailboxes{var revision int64;if err:=tx.QueryRowContext(ctx,`SELECT revision FROM mailboxes WHERE id=? AND org_id=?`,mailbox.ID,orgID).Scan(&revision);err!=nil{return err};if revision!=mailbox.Revision{return provider.Errorf("revision_conflict","投递目标邮箱已经更新")}}
		for _,rule:=range plan.Rules{
			if rule.Mode=="api"{if err:=saveRoutingObservation(ctx,tx,rule.ConnectionID,rule.ID,observations[rule.ID]);err!=nil{return err}}
			if rule.Delete{if _,err:=tx.ExecContext(ctx,`DELETE FROM provider_resources WHERE address_id=?`,rule.ID);err!=nil{return err};res,err:=tx.ExecContext(ctx,`DELETE FROM addresses WHERE id=? AND org_id=? AND revision=?`,rule.ID,orgID,rule.Revision);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","地址已经更新")};continue}
			state:="synced";if rule.Mode=="external"{state="unknown"}
			res,err:=tx.ExecContext(ctx,`UPDATE addresses SET mailbox_id=?,targets_json=?,observed_targets_json=CASE WHEN management_mode='api' THEN ? ELSE observed_targets_json END,note=?,sync_state=?,updated_at=? WHERE id=? AND org_id=? AND revision=?`,sqlNullInt(rule.MailboxID),toJSON(rule.Targets),toJSON(rule.Targets),rule.Note,state,db.Now(),rule.ID,orgID,rule.Revision);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","地址已经更新")}
		}
		if strings.HasPrefix(plan.Kind,"group."){
			if plan.Kind=="group.delete"{if _,err:=tx.ExecContext(ctx,`DELETE FROM group_members WHERE group_id=?`,plan.GroupID);err!=nil{return err};res,err:=tx.ExecContext(ctx,`DELETE FROM groups WHERE id=? AND org_id=? AND revision=?`,plan.GroupID,orgID,plan.GroupRevision);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","群组已经更新")}}else{
				res,err:=tx.ExecContext(ctx,`UPDATE groups SET name=?,description=?,revision=revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=?`,plan.GroupName,plan.GroupDescription,db.Now(),plan.GroupID,orgID,plan.GroupRevision);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","群组已经更新")}
				if _,err:=tx.ExecContext(ctx,`DELETE FROM group_members WHERE group_id=?`,plan.GroupID);err!=nil{return err};for _,memberID:=range plan.GroupMembers{if _,err:=tx.ExecContext(ctx,`INSERT INTO group_members(group_id,member_id) VALUES (?,?)`,plan.GroupID,memberID);err!=nil{return err}}
			}
		}
		return completeLocalOperation(ctx,tx,result)
	});if err!=nil{return nil,err}
	s.audit(ctx,orgID,actor,plan.Kind,"operation",id,result);return result,nil
}

func validateRoutingPlan(ctx context.Context,q managementQuery,orgID int64,plan *routingPlan) error {
	for _,rule:=range plan.Rules{if err:=checkRoutingManagement(ctx,q,orgID,rule);err!=nil{return err}}
	if plan.GroupID>0{var revision int64;if err:=q.QueryRowContext(ctx,`SELECT revision FROM groups WHERE id=? AND org_id=?`,plan.GroupID,orgID).Scan(&revision);err!=nil{return err};if revision!=plan.GroupRevision{return provider.Errorf("revision_conflict","群组已经更新")}}
	for _,rule:=range plan.Rules{var revision,connectionRevision int64;var enabled bool;if err:=q.QueryRowContext(ctx,`SELECT a.revision,c.revision,c.enabled FROM addresses a JOIN mail_connections c ON c.id=a.connection_id WHERE a.id=? AND a.org_id=?`,rule.ID,orgID).Scan(&revision,&connectionRevision,&enabled);err!=nil{return err};if revision!=rule.Revision || connectionRevision!=rule.ConnectionRevision{return provider.Errorf("revision_conflict","规则或所属连接已经更新")};if !enabled{return provider.Errorf("endpoint_disabled","规则所属连接已经停用")}}
	for _,member:=range plan.Members{var revision int64;if err:=q.QueryRowContext(ctx,`SELECT revision FROM members WHERE id=? AND org_id=?`,member.ID,orgID).Scan(&revision);err!=nil{return err};if revision!=member.Revision{return provider.Errorf("revision_conflict","群组成员已经更新")}}
	for _,mailbox:=range plan.Mailboxes{var revision int64;if err:=q.QueryRowContext(ctx,`SELECT revision FROM mailboxes WHERE id=? AND org_id=?`,mailbox.ID,orgID).Scan(&revision);err!=nil{return err};if revision!=mailbox.Revision{return provider.Errorf("revision_conflict","投递邮箱已经更新")}}
	return nil
}

func sameRoutingTargets(first,second []string) bool {
	a:=append([]string{},first...);b:=append([]string{},second...);sort.Strings(a);sort.Strings(b);return reflect.DeepEqual(a,b)
}

func validateGroupCandidates(ctx context.Context,q managementQuery,orgID int64,plan *routingPlan) error {
	if !strings.HasPrefix(plan.Kind,"group.") || plan.Kind=="group.delete"{return nil}
	for _,rule:=range plan.Rules{
		if rule.Delete{continue}
		targets,versions,err:=groupCandidateTargets(ctx,q,orgID,plan.GroupID,plan.GroupMembers,rule);if err!=nil{return err}
		if !sameRoutingTargets(targets,rule.Targets){return provider.Errorf("revision_conflict","群组的完整候选目标已经更新")}
		if len(versions)!=len(plan.Mailboxes){return provider.Errorf("revision_conflict","群组候选邮箱集合已经更新")}
		expected:=map[int64]int64{};for _,item:=range plan.Mailboxes{expected[item.ID]=item.Revision};for _,item:=range versions{if expected[item.ID]!=item.Revision{return provider.Errorf("revision_conflict","群组候选邮箱版本已经更新")}}
	}
	return nil
}

func saveRoutingObservation(ctx context.Context,tx *sql.Tx,connectionID,addressID int64,observed provider.AddressRuleInfo) error {
	now:=db.Now()
	if _,err:=tx.ExecContext(ctx,`UPDATE provider_resources SET remote_state='missing',updated_at=? WHERE address_id=? AND connection_id=? AND purpose='routing'`,now,addressID,connectionID);err!=nil{return err}
	if observed.RemoteKey==""{return nil}
	var existing sql.NullInt64
	err:=tx.QueryRowContext(ctx,`SELECT address_id FROM provider_resources WHERE connection_id=? AND resource_type='routing_rule' AND remote_key=? AND purpose='routing'`,connectionID,observed.RemoteKey).Scan(&existing)
	if err!=nil && !db.IsNotFound(err){return err};if err==nil && (!existing.Valid || existing.Int64!=addressID){return provider.Errorf("revision_conflict","远程规则已经关联其他地址")}
	_,err=tx.ExecContext(ctx,`INSERT INTO provider_resources(connection_id,resource_type,remote_key,remote_locator_json,purpose,owned_by_mailhearth,last_seen_at,remote_state,address_id,created_at,updated_at) VALUES (?,'routing_rule',?,?,'routing',1,?,'present',?,?,?) ON CONFLICT(connection_id,resource_type,remote_key,purpose) DO UPDATE SET remote_locator_json=excluded.remote_locator_json,last_seen_at=excluded.last_seen_at,remote_state='present',updated_at=excluded.updated_at`,connectionID,observed.RemoteKey,toJSON(observed.RemoteLocator),now,addressID,now,now);return err
}
