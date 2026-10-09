package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"

	"mailhearth/internal/db"
	"mailhearth/internal/provider"
)

func normalizedForwarding(item provider.DiscoverySnapshotResource) (provider.ForwardingInfo,error) {
	if item.Resource.Purpose!=provider.PurposeForwarding || item.Forwarding==nil{return provider.ForwardingInfo{},provider.Errorf("upstream_failed","发现结果缺少完整转发信息")}
	value:=*item.Forwarding;domain,err:=provider.CanonicalDomain(value.Domain);if err!=nil{return value,err};value.Domain=domain
	if !validLocalPart(strings.ToLower(value.LocalPart)) || (value.DeliveryMode!="redirect" && value.DeliveryMode!="copy" && value.DeliveryMode!="unverified"){return value,provider.Errorf("upstream_failed","转发来源或 deliveryMode 无效")}
	if item.Resource.RemoteKey==""{return value,provider.Errorf("upstream_failed","转发资源缺少远程引用")}
	locator:=item.Resource.RemoteLocator;if name:=locator["domain"];name!="" && !strings.EqualFold(name,domain){return value,provider.Errorf("upstream_failed","转发域名与远程引用不一致")};for _,key:=range []string{"mailboxLocalPart","localPart"}{if local:=locator[key];local!="" && !strings.EqualFold(local,value.LocalPart){return value,provider.Errorf("upstream_failed","转发来源邮箱与远程引用不一致")}}
	targets:=[]string{};statuses:=map[string]string{}
	for _,target:=range value.Targets{address,err:=NormalizeEmail(target);if err!=nil{return value,err};if address==strings.ToLower(value.LocalPart+"@"+domain){return value,provider.Errorf("upstream_failed","转发目标指向来源邮箱")};status:=value.StatusByTarget[target];switch status{case "active","pending_confirmation","blocked","unknown":default:return value,provider.Errorf("upstream_failed","转发目标缺少有效确认状态")};if old,exists:=statuses[address];exists && old!=status{return value,provider.Errorf("upstream_failed","转发目标存在冲突确认状态")};targets=append(targets,address);statuses[address]=status}
	if target:=locator["targetAddress"];target!=""{address,err:=NormalizeEmail(target);if err!=nil{return value,err};if len(targets)!=1 || targets[0]!=address{return value,provider.Errorf("upstream_failed","转发目标与远程引用不一致")}}
	if len(targets)==0{return value,provider.Errorf("upstream_failed","转发观察结果缺少目标")};sort.Strings(targets);value.Targets=uniqueStrings(targets);value.StatusByTarget=statuses;return value,nil
}

func importDiscoveredForwarding(ctx context.Context,tx *sql.Tx,orgID int64,c *ConnectionView,item provider.DiscoverySnapshotResource) (int64,error) {
	value,err:=normalizedForwarding(item);if err!=nil{return 0,err}
	if _,_,err:=discoveryBinding(ctx,tx,orgID,c.ID,value.Domain);err!=nil{return 0,err}
	var mailboxID int64;err=tx.QueryRowContext(ctx,`SELECT id FROM mailboxes WHERE org_id=? AND connection_id=? AND address_key=?`,orgID,c.ID,strings.ToLower(value.LocalPart+"@"+value.Domain)).Scan(&mailboxID);if db.IsNotFound(err){return 0,provider.Errorf("invalid","转发的来源邮箱需要一并导入")};if err!=nil{return 0,err}
	var id int64;var desiredJSON string
	err=tx.QueryRowContext(ctx,`SELECT id,targets_json FROM mailbox_forwardings WHERE mailbox_id=?`,mailboxID).Scan(&id,&desiredJSON);if err!=nil && !db.IsNotFound(err){return 0,err}
	if id==0{now:=db.Now();result,err:=tx.ExecContext(ctx,`INSERT INTO mailbox_forwardings(mailbox_id,targets_json,delivery_mode,sync_state,remote_status_json,created_at,updated_at) VALUES (?,?,'unverified','unknown',?, ?,?)`,mailboxID,toJSON(value.Targets),toJSON(map[string]any{"imported":true}),now,now);if err!=nil{return 0,err};id,err=result.LastInsertId();if err!=nil{return 0,err}}else{
		var exists bool;if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM provider_resources WHERE connection_id=? AND resource_type=? AND remote_key=? AND purpose='forwarding')`,c.ID,item.Resource.ResourceType,item.Resource.RemoteKey).Scan(&exists);err!=nil{return 0,err}
		// 每个新选择的远程目标加入登记；重复导入保留管理员的目标设置。
		if !exists{var desired []string;if err:=json.Unmarshal([]byte(desiredJSON),&desired);err!=nil{return 0,err};desired=append(desired,value.Targets...);sort.Strings(desired);desired=uniqueStrings(desired);if _,err:=tx.ExecContext(ctx,`UPDATE mailbox_forwardings SET targets_json=?,revision=revision+1,updated_at=? WHERE id=?`,toJSON(desired),db.Now(),id);err!=nil{return 0,err}}
	}
	if err:=saveDiscoveredResource(ctx,tx,c.ID,item.Resource,"mailbox_forwarding_id",id);err!=nil{return 0,err};return id,nil
}

func syncForwardingObservations(ctx context.Context,tx *sql.Tx,orgID,connectionID int64) error {
	rows,err:=tx.QueryContext(ctx,`SELECT f.id,f.targets_json,f.delivery_mode,f.remote_status_json,f.sync_state FROM mailbox_forwardings f JOIN mailboxes b ON b.id=f.mailbox_id WHERE b.org_id=? AND b.connection_id=? ORDER BY f.id`,orgID,connectionID);if err!=nil{return err}
	type registration struct{id int64;targets,mode,status,state string};var registrations []registration
	for rows.Next(){var value registration;if err:=rows.Scan(&value.id,&value.targets,&value.mode,&value.status,&value.state);err!=nil{rows.Close();return err};registrations=append(registrations,value)};err=rows.Err();rows.Close();if err!=nil{return err}
	for _,registration:=range registrations{
		rows,err:=tx.QueryContext(ctx,`SELECT remote_key,remote_state,observation_json FROM provider_resources WHERE connection_id=? AND mailbox_forwarding_id=? ORDER BY resource_type,remote_key`,connectionID,registration.id);if err!=nil{return err}
		targets:=[]string{};statuses:=map[string]string{};states:=map[string]string{};modes:=map[string]string{};verified:=true;count:=0
		for rows.Next(){var key,state,body string;if err:=rows.Scan(&key,&state,&body);err!=nil{rows.Close();return err};count++;states[key]=state;if state!="present"{verified=false;continue};var item provider.DiscoverySnapshotResource;if err:=json.Unmarshal([]byte(body),&item);err!=nil{rows.Close();return err};if item.Forwarding==nil{verified=false;continue};value,err:=normalizedForwarding(item);if err!=nil{rows.Close();return err};modes[key]=value.DeliveryMode;if value.DeliveryMode=="unverified" || value.DeliveryMode!=registration.mode{verified=false};for _,target:=range value.Targets{targets=append(targets,target);status:=value.StatusByTarget[target];if previous,exists:=statuses[target];exists && previous!=status{status="unknown"};statuses[target]=status;if status!="active"{verified=false}}}
		err=rows.Err();rows.Close();if err!=nil{return err};if count==0{continue};sort.Strings(targets);targets=uniqueStrings(targets)
		var desired []string;if err:=json.Unmarshal([]byte(registration.targets),&desired);err!=nil{return err};var status map[string]any;if err:=json.Unmarshal([]byte(registration.status),&status);err!=nil{return err};if status==nil{status=map[string]any{}}
		status["desiredTargets"]=desired;status["desiredDeliveryMode"]=registration.mode;status["observedTargets"]=targets;status["observedDeliveryModes"]=modes;status["statusByTarget"]=statuses;status["remoteStates"]=states;status["verificationSource"]="provider_read";status["systemVerified"]=verified
		next:="unknown";if !sameRoutingTargets(desired,targets){next="error"}else if verified{next="synced"};if registration.state=="pending"{next="pending"};body:=toJSON(status)
		if body!=registration.status || next!=registration.state{if _,err:=tx.ExecContext(ctx,`UPDATE mailbox_forwardings SET remote_status_json=?,sync_state=?,revision=revision+1,updated_at=? WHERE id=?`,body,next,db.Now(),registration.id);err!=nil{return err}}
	}
	return nil
}
