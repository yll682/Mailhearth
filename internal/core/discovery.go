package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"mailhearth/internal/db"
	"mailhearth/internal/provider"
)

type ImportSelection struct {
	SnapshotID int64 `json:"snapshotId"`
	SelectedResources []SelectedResource `json:"selectedResources"`
}

type SelectedResource struct {
	ResourceType string `json:"resourceType"`
	RemoteKey string `json:"remoteKey"`
}

func (s *Service) DiscoverConnection(ctx context.Context,orgID,id int64) (any,error) {
	api,c,err:=s.connectionAdapter(ctx,orgID,id);if err!=nil{return nil,err}
	result,err:=api.Discover(ctx,provider.DiscoverRequest{Scope:c.DomainScope});if err!=nil{return nil,err}
	if !result.Snapshot.Complete{return nil,provider.Errorf("upstream_failed","发现范围读取未完成")}
	snapshot:=result.Snapshot;snapshot.ConnectionID=id;snapshot.ConnectionRevision=c.Revision
	for i:=range snapshot.Resources{snapshot.Resources[i].Resource.ConnectionID=id}
	resources,err:=json.Marshal(snapshot.Resources);if err!=nil{return nil,err};scope,err:=json.Marshal(c.DomainScope);if err!=nil{return nil,err}
	var snapshotID int64
	err=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		var current bool
		if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM mail_connections WHERE id=? AND org_id=? AND revision=? AND enabled=1)`,id,orgID,c.Revision).Scan(&current);err!=nil{return err};if !current{return provider.Errorf("revision_conflict","连接配置已经更新")}
		res,err:=tx.ExecContext(ctx,`INSERT INTO discovery_snapshots(connection_id,connection_revision,scope_json,resources_json,complete,created_at,expires_at) VALUES (?,?,?,?,1,?,?)`,id,c.Revision,string(scope),string(resources),db.Now(),time.Now().UTC().Add(15*time.Minute).Format(time.RFC3339));if err!=nil{return err}
		snapshotID,err=res.LastInsertId();if err!=nil{return err}
		return completeLocalOperation(ctx,tx,struct{SnapshotID int64 `json:"snapshotId"`;Resources []provider.DiscoverySnapshotResource `json:"resources"`}{snapshotID,snapshot.Resources})
	});if err!=nil{return nil,err}
	return struct{SnapshotID int64 `json:"snapshotId"`;Resources []provider.DiscoverySnapshotResource `json:"resources"`}{snapshotID,snapshot.Resources},nil
}

func (s *Service) ImportConnection(ctx context.Context,orgID,connectionID int64,in ImportSelection) (any,error) {
	c,err:=s.MailConnection(ctx,orgID,connectionID);if err!=nil{return nil,err};if !c.Enabled{return nil,provider.Errorf("endpoint_disabled","连接已停用")}
	var snapshotConnection,revision int64;var raw,expires string;var complete bool
	err=s.DB.QueryRowContext(ctx,`SELECT connection_id,connection_revision,resources_json,complete,expires_at FROM discovery_snapshots WHERE id=?`,in.SnapshotID).Scan(&snapshotConnection,&revision,&raw,&complete,&expires)
	if db.IsNotFound(err){return nil,ErrNotFound};if err!=nil{return nil,err}
	if snapshotConnection!=connectionID{return nil,ErrNotFound}
	if revision!=c.Revision{return nil,provider.Errorf("revision_conflict","连接配置已经更新")}
	deadline,err:=time.Parse(time.RFC3339,expires);if err!=nil{return nil,err};if !complete || !time.Now().Before(deadline){return nil,provider.Errorf("invalid","发现结果未完成或已经过期")}
	var resources []provider.DiscoverySnapshotResource;if err:=json.Unmarshal([]byte(raw),&resources);err!=nil{return nil,err}
	available:=map[SelectedResource]provider.DiscoverySnapshotResource{}
	for _,resource:=range resources{key:=SelectedResource{resource.Resource.ResourceType,resource.Resource.RemoteKey};if _,ok:=available[key];ok{return nil,provider.Errorf("upstream_failed","发现结果出现重复资源")};available[key]=resource}
	selected:=map[SelectedResource]bool{}
	for _,key:=range in.SelectedResources{if _,ok:=available[key];!ok{return nil,provider.Errorf("invalid","选择的资源不属于发现结果")};selected[key]=true}
	if len(selected)==0{return nil,provider.Errorf("invalid","请选择需要导入的资源")}
	mailboxIDs:=[]int64{};bindingIDs:=[]int64{};addressIDs:=[]int64{};identityIDs:=[]int64{};forwardingIDs:=[]int64{}
	err=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		res,err:=tx.ExecContext(ctx,`UPDATE mail_connections SET revision=revision WHERE id=? AND org_id=? AND revision=? AND enabled=1`,connectionID,orgID,revision);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","连接配置已经更新")}
		now:=db.Now();bindings:=map[string]int64{}
		for _,resource:=range resources{
			key:=SelectedResource{resource.Resource.ResourceType,resource.Resource.RemoteKey};if !selected[key] || key.ResourceType!="domain"{continue}
			domain,err:=provider.CanonicalDomain(resource.Summary["name"]);if err!=nil{return err}
			if _,err:=tx.ExecContext(ctx,`INSERT INTO domains(org_id,name,created_at,updated_at) VALUES (?,?,?,?) ON CONFLICT(org_id,name) DO NOTHING`,orgID,domain,now,now);err!=nil{return err}
			var domainID int64;if err:=tx.QueryRowContext(ctx,`SELECT id FROM domains WHERE org_id=? AND name=?`,orgID,domain).Scan(&domainID);err!=nil{return err}
			if _,err:=tx.ExecContext(ctx,`INSERT INTO domain_bindings(org_id,domain_id,connection_id,management_mode,remote_state,created_at,updated_at) VALUES (?,?,?,'api','present',?,?) ON CONFLICT(domain_id,connection_id) DO UPDATE SET remote_state='present',updated_at=excluded.updated_at`,orgID,domainID,connectionID,now,now);err!=nil{return err}
			var bindingID int64;if err:=tx.QueryRowContext(ctx,`SELECT id FROM domain_bindings WHERE domain_id=? AND connection_id=?`,domainID,connectionID).Scan(&bindingID);err!=nil{return err}
			if resource.Domain!=nil{mode:="api";if resource.Domain.IsShared{mode="external"};if _,err:=tx.ExecContext(ctx,`UPDATE domain_bindings SET management_mode=?,provider_settings_json=?,dns_status_json=? WHERE id=? AND connection_id=?`,mode,toJSON(struct{IsShared bool `json:"isShared"`;AllowAccountReset *bool `json:"allowAccountReset"`;SymbolicSubaddressing *bool `json:"symbolicSubaddressing"`}{resource.Domain.IsShared,resource.Domain.AllowAccountReset,resource.Domain.SymbolicSubaddressing}),toJSON(resource.Domain.DNS),bindingID,connectionID);err!=nil{return err}}
			bindings[domain]=bindingID;bindingIDs=append(bindingIDs,bindingID)
			if err:=saveDiscoveredResource(ctx,tx,connectionID,resource.Resource,"domain_binding_id",bindingID);err!=nil{return err}
		}
		for _,resource:=range resources{
			key:=SelectedResource{resource.Resource.ResourceType,resource.Resource.RemoteKey};if !selected[key] || key.ResourceType=="domain"{continue}
			if key.ResourceType!="mailbox"{continue}
			address:=resource.Summary["address"];if address==""{address=resource.Resource.RemoteLocator["address"]}
			address,addressKey,err:=canonicalMailboxAddress(c.ProviderKind,address);if err!=nil{return err};local,domain:=SplitAddress(address)
			bindingID:=bindings[domain]
			if bindingID==0{if err:=tx.QueryRowContext(ctx,`SELECT db.id FROM domain_bindings db JOIN domains d ON d.id=db.domain_id WHERE db.connection_id=? AND d.name=?`,connectionID,domain).Scan(&bindingID);err!=nil{return provider.Errorf("invalid","邮箱所属域名必须一并导入")}}
			var domainID int64;if err:=tx.QueryRowContext(ctx,`SELECT domain_id FROM domain_bindings WHERE id=?`,bindingID).Scan(&domainID);err!=nil{return err}
			if _,err:=tx.ExecContext(ctx,`INSERT INTO mailboxes(org_id,connection_id,domain_binding_id,domain_id,kind,address,address_key,display_name,status,imported,management_mode,remote_state,created_at,updated_at) VALUES (?,?,?,?,'personal',?,?,?,'active',1,'api','present',?,?) ON CONFLICT(connection_id,address_key) DO UPDATE SET remote_state='present',updated_at=excluded.updated_at`,orgID,connectionID,bindingID,domainID,address,addressKey,local,now,now);err!=nil{return err}
			var mailboxID int64;if err:=tx.QueryRowContext(ctx,`SELECT id FROM mailboxes WHERE connection_id=? AND address_key=?`,connectionID,addressKey).Scan(&mailboxID);err!=nil{return err};mailboxIDs=append(mailboxIDs,mailboxID)
			for _,protocol:=range []string{provider.ProtocolIMAP,provider.ProtocolSMTP,provider.ProtocolManageSieve}{template,err:=templateFor(c.ProtocolDefaults,protocol);if err!=nil{return err};mode:="inherit";var username any=address;if !template.Enabled{mode="disabled";username=nil};if _,err:=tx.ExecContext(ctx,`INSERT INTO mailbox_endpoints(mailbox_id,protocol,network_mode,username,created_at,updated_at) VALUES (?,?,?,?,?,?) ON CONFLICT(mailbox_id,protocol) DO NOTHING`,mailboxID,protocol,mode,username,now,now);err!=nil{return err}}
			if _,err:=tx.ExecContext(ctx,`INSERT INTO addresses(org_id,connection_id,domain_binding_id,address_key,domain_id,local_part,address,kind,mailbox_id,management_mode,sync_state,created_at,updated_at) VALUES (?,?,?,?,?,?,?,'primary',?,'api','synced',?,?) ON CONFLICT(connection_id,address_key) WHERE kind!='catchall' DO NOTHING`,orgID,connectionID,bindingID,addressKey,domainID,local,address,mailboxID,now,now);err!=nil{return err}
			if _,err:=tx.ExecContext(ctx,`INSERT INTO identities(mailbox_id,address,display_name,is_default,authorization_source,authorization_status,created_at) VALUES (?,?,?,1,'admin','unverified',?) ON CONFLICT(mailbox_id,address) DO NOTHING`,mailboxID,address,local,now);err!=nil{return err}
			if err:=saveDiscoveredResource(ctx,tx,connectionID,resource.Resource,"mailbox_id",mailboxID);err!=nil{return err}
		}
		for _,resource:=range resources{
			key:=SelectedResource{resource.Resource.ResourceType,resource.Resource.RemoteKey};if !selected[key] || key.ResourceType=="domain" || key.ResourceType=="mailbox"{continue}
			addressID,identityID,err:=importAdditionalResource(ctx,tx,orgID,c,resource);if err!=nil{return err};if addressID>0{addressIDs=append(addressIDs,addressID)};if identityID>0{identityIDs=append(identityIDs,identityID)}
		}
		forwardings:=map[int64]bool{}
		for _,resource:=range resources{key:=SelectedResource{resource.Resource.ResourceType,resource.Resource.RemoteKey};if selected[key]{if err:=saveResourceObservation(ctx,tx,connectionID,resource);err!=nil{return err};if resource.Resource.Purpose==provider.PurposeForwarding{var id int64;if err:=tx.QueryRowContext(ctx,`SELECT mailbox_forwarding_id FROM provider_resources WHERE connection_id=? AND resource_type=? AND remote_key=? AND purpose='forwarding'`,connectionID,key.ResourceType,key.RemoteKey).Scan(&id);err!=nil{return err};if !forwardings[id]{forwardingIDs=append(forwardingIDs,id);forwardings[id]=true}}}}
		if err:=syncForwardingObservations(ctx,tx,orgID,connectionID);err!=nil{return err}
		return completeLocalOperation(ctx,tx,struct{MailboxIDs []int64 `json:"mailboxIds"`;DomainBindingIDs []int64 `json:"domainBindingIds"`;AddressIDs []int64 `json:"addressIds"`;IdentityIDs []int64 `json:"identityIds"`;ForwardingIDs []int64 `json:"forwardingIds"`}{mailboxIDs,bindingIDs,addressIDs,identityIDs,forwardingIDs})
	});if err!=nil{return nil,err}
	return struct{MailboxIDs []int64 `json:"mailboxIds"`;DomainBindingIDs []int64 `json:"domainBindingIds"`;AddressIDs []int64 `json:"addressIds"`;IdentityIDs []int64 `json:"identityIds"`;ForwardingIDs []int64 `json:"forwardingIds"`}{mailboxIDs,bindingIDs,addressIDs,identityIDs,forwardingIDs},nil
}

func saveDiscoveredResource(ctx context.Context,tx *sql.Tx,connectionID int64,resource provider.Resource,column string,localID int64) error {
	if column!="mailbox_id" && column!="domain_binding_id" && column!="address_id" && column!="identity_id" && column!="mailbox_forwarding_id"{return provider.Errorf("invalid","资源关联字段无效")}
	var existing sql.NullInt64
	err:=tx.QueryRowContext(ctx,`SELECT `+column+` FROM provider_resources WHERE connection_id=? AND resource_type=? AND remote_key=? AND purpose=?`,connectionID,resource.ResourceType,resource.RemoteKey,resource.Purpose).Scan(&existing)
	if err!=nil && !db.IsNotFound(err){return err};if err==nil && (!existing.Valid || existing.Int64!=localID){return provider.Errorf("revision_conflict","远程资源已经关联其他对象")}
	locator,err:=json.Marshal(resource.RemoteLocator);if err!=nil{return err};now:=db.Now()
	_,err=tx.ExecContext(ctx,`INSERT INTO provider_resources(connection_id,resource_type,remote_key,remote_locator_json,purpose,owned_by_mailhearth,last_seen_at,remote_state,`+column+`,created_at,updated_at) VALUES (?,?,?,?,?,0,?,'present',?,?,?) ON CONFLICT(connection_id,resource_type,remote_key,purpose) DO UPDATE SET remote_locator_json=excluded.remote_locator_json,last_seen_at=excluded.last_seen_at,remote_state='present',updated_at=excluded.updated_at`,connectionID,resource.ResourceType,resource.RemoteKey,string(locator),resource.Purpose,now,localID,now,now)
	return err
}

func canonicalMailboxAddress(kind provider.ProviderKind,value string) (string,string,error) {
	if _,err:=provider.ValidateAddress(value);err!=nil{return "","",err}
	local,domain:=SplitAddress(value);domain,err:=provider.CanonicalDomain(domain);if err!=nil{return "","",err}
	address:=local+"@"+domain;key:=address
	if kind!=provider.Manual{address=strings.ToLower(address);key=address}
	return address,key,nil
}
