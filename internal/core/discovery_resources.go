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

func discoveryBinding(ctx context.Context,tx *sql.Tx,orgID,connectionID int64,domain string) (int64,int64,error) {
	name,err:=provider.CanonicalDomain(domain);if err!=nil{return 0,0,err}
	var bindingID,domainID int64;err=tx.QueryRowContext(ctx,`SELECT b.id,b.domain_id FROM domain_bindings b JOIN domains d ON d.id=b.domain_id WHERE b.org_id=? AND b.connection_id=? AND d.name=?`,orgID,connectionID,name).Scan(&bindingID,&domainID)
	if db.IsNotFound(err){return 0,0,provider.Errorf("invalid","资源所属域名关联需要一并导入")};return bindingID,domainID,err
}

func importAdditionalResource(ctx context.Context,tx *sql.Tx,orgID int64,c *ConnectionView,item provider.DiscoverySnapshotResource) (int64,int64,error) {
	switch item.Resource.ResourceType{
	case "routing_rule","alias","external_rule":if item.Resource.Purpose==provider.PurposeForwarding{_,err:=importDiscoveredForwarding(ctx,tx,orgID,c,item);return 0,0,err};id,err:=importDiscoveredRule(ctx,tx,orgID,c,item);return id,0,err
	case "identity":id,err:=importDiscoveredIdentity(ctx,tx,orgID,c,item);return 0,id,err
	case "forwarding":_,err:=importDiscoveredForwarding(ctx,tx,orgID,c,item);return 0,0,err
	default:return 0,0,provider.Errorf("unsupported_operation","发现资源类型不受支持")
	}
}

func importDiscoveredRule(ctx context.Context,tx *sql.Tx,orgID int64,c *ConnectionView,item provider.DiscoverySnapshotResource) (int64,error) {
	rule:=item.AddressRule;if rule==nil || item.Resource.Purpose!=provider.PurposeRouting{return 0,provider.Errorf("upstream_failed","发现结果缺少完整规则信息")}
	bindingID,domainID,err:=discoveryBinding(ctx,tx,orgID,c.ID,rule.Domain);if err!=nil{return 0,err}
	domain,err:=provider.CanonicalDomain(rule.Domain);if err!=nil{return 0,err}
	kind:=model.AddressForward;local:=rule.LocalPart;address:=local+"@"+domain;key:=strings.ToLower(address)
	if item.Resource.ResourceType=="external_rule"{if rule.Name=="" || rule.Pattern==""{return 0,provider.Errorf("upstream_failed","复杂规则缺少名称或 pattern")};kind=model.AddressExternalRule;local=rule.Pattern;address=rule.Pattern+"@"+domain;key="external-rule:"+item.Resource.RemoteKey}else if rule.Catchall{kind=model.AddressCatchall;local="";address="*@"+domain;key=address}else if rule.Prefix{kind=model.AddressPrefix;address=local+"*@"+domain;key=strings.ToLower(address)}else if !validLocalPart(strings.ToLower(local)){return 0,provider.Errorf("upstream_failed","规则 localPart 无效")}
	targets:=[]string{};for _,target:=range rule.Targets{value,err:=NormalizeEmail(target);if err!=nil{return 0,err};targets=append(targets,value)};sort.Strings(targets);targets=uniqueStrings(targets)
	var mailboxID sql.NullInt64
	if kind==model.AddressForward && len(targets)==1{err:=tx.QueryRowContext(ctx,`SELECT id FROM mailboxes WHERE org_id=? AND connection_id=? AND address_key=?`,orgID,c.ID,targets[0]).Scan(&mailboxID);if err!=nil && !db.IsNotFound(err){return 0,err};if mailboxID.Valid{kind=model.AddressAlias}}
	if c.ProviderKind==provider.Migadu && kind==model.AddressForward{kind=model.AddressExternalRule}
	var id int64;var existingKind string;var existingBinding sql.NullInt64
	err=tx.QueryRowContext(ctx,`SELECT id,kind,domain_binding_id FROM addresses WHERE connection_id=? AND address_key=?`,c.ID,key).Scan(&id,&existingKind,&existingBinding)
	if err!=nil && !db.IsNotFound(err){return 0,err}
	if err==nil && existingKind==model.AddressPrimary{return 0,provider.Errorf("verification_required","主地址上的规则需要按独立邮箱转发导入并验证 deliveryMode")}
	if err==nil && (!existingBinding.Valid || existingBinding.Int64!=bindingID){return 0,provider.Errorf("revision_conflict","规则地址已经登记到其他域名关联")}
	now:=db.Now()
	if id==0{
		mode:="api";if kind==model.AddressExternalRule{mode="external"}
		res,err:=tx.ExecContext(ctx,`INSERT INTO addresses(org_id,connection_id,domain_binding_id,domain_id,address_key,local_part,address,kind,mailbox_id,targets_json,desired_targets_json,observed_targets_json,is_prefix,is_catchall,management_mode,sync_state,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'synced',?,?)`,orgID,c.ID,bindingID,domainID,key,local,address,kind,mailboxID,toJSON(targets),toJSON(targets),toJSON(targets),boolInt(rule.Prefix),boolInt(rule.Catchall),mode,now,now);if err!=nil{return 0,err};id,err=res.LastInsertId();if err!=nil{return 0,err}
	}else{
		var desiredJSON string;if err:=tx.QueryRowContext(ctx,`SELECT desired_targets_json FROM addresses WHERE id=? AND org_id=?`,id,orgID).Scan(&desiredJSON);err!=nil{return 0,err};var desired []string;if err:=json.Unmarshal([]byte(desiredJSON),&desired);err!=nil{return 0,err};state:="synced";if !sameRoutingTargets(desired,targets){state="error"}
		if _,err:=tx.ExecContext(ctx,`UPDATE addresses SET observed_targets_json=?,sync_state=?,updated_at=? WHERE id=? AND org_id=?`,toJSON(targets),state,now,id,orgID);err!=nil{return 0,err}
	}
	if err:=saveDiscoveredResource(ctx,tx,c.ID,item.Resource,"address_id",id);err!=nil{return 0,err};return id,nil
}

func importDiscoveredIdentity(ctx context.Context,tx *sql.Tx,orgID int64,c *ConnectionView,item provider.DiscoverySnapshotResource) (int64,error) {
	identity:=item.Identity;if identity==nil || identity.MailboxLocalPart==""{return 0,provider.Errorf("upstream_failed","发现结果缺少 identity 信息")}
	if _,_,err:=discoveryBinding(ctx,tx,orgID,c.ID,identity.Domain);err!=nil{return 0,err}
	address,key,err:=canonicalMailboxAddress(c.ProviderKind,identity.Address);if err!=nil{return 0,err};_,domain:=SplitAddress(address);if !strings.EqualFold(domain,identity.Domain){return 0,provider.Errorf("upstream_failed","identity 域名与父邮箱不一致")}
	var mailboxID int64;err=tx.QueryRowContext(ctx,`SELECT id FROM mailboxes WHERE org_id=? AND connection_id=? AND address_key=?`,orgID,c.ID,strings.ToLower(identity.MailboxLocalPart+"@"+domain)).Scan(&mailboxID);if db.IsNotFound(err){return 0,provider.Errorf("invalid","identity 的父邮箱需要一并导入")};if err!=nil{return 0,err}
	if item.Resource.Purpose==provider.PurposeLoginCredential{
		var resourceID,credentialMailboxID int64
		err:=tx.QueryRowContext(ctx,`SELECT p.id,cr.mailbox_id FROM provider_resources p JOIN credentials cr ON cr.id=p.credential_id WHERE p.connection_id=? AND p.resource_type=? AND p.remote_key=? AND p.purpose='login_credential'`,c.ID,item.Resource.ResourceType,item.Resource.RemoteKey).Scan(&resourceID,&credentialMailboxID)
		if err!=nil && !db.IsNotFound(err){return 0,err};if err==nil{if credentialMailboxID!=mailboxID{return 0,provider.Errorf("revision_conflict","专用 identity 已经关联其他邮箱的凭据")};_,err=tx.ExecContext(ctx,`UPDATE provider_resources SET remote_state='present',last_seen_at=?,updated_at=? WHERE id=?`,db.Now(),db.Now(),resourceID);return 0,err}
		return 0,saveDiscoveredResource(ctx,tx,c.ID,item.Resource,"mailbox_id",mailboxID)
	}
	if item.Resource.Purpose!=provider.PurposeSenderIdentity || identity.PasswordUse=="custom"{return 0,provider.Errorf("upstream_failed","identity 用途信息无效")}
	now:=db.Now();if _,err:=tx.ExecContext(ctx,`INSERT INTO identities(mailbox_id,address,display_name,is_default,authorization_source,authorization_status,created_at,updated_at) VALUES (?,?,?,0,'provider','unverified',?,?) ON CONFLICT(mailbox_id,address) DO NOTHING`,mailboxID,key,identity.DisplayName,now,now);err!=nil{return 0,err}
	var identityID int64;if err:=tx.QueryRowContext(ctx,`SELECT id FROM identities WHERE mailbox_id=? AND address=?`,mailboxID,key).Scan(&identityID);err!=nil{return 0,err}
	if err:=saveDiscoveredResource(ctx,tx,c.ID,item.Resource,"identity_id",identityID);err!=nil{return 0,err};return identityID,nil
}
