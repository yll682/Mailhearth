package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"mailhearth/internal/db"
	"mailhearth/internal/provider"
)

func TestMultiProviderStorageForwardingImportObservation(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();one:=storageConnection(t,s,m,"转发观察一");two:=storageConnection(t,s,m,"转发观察二");startStorageOperations(t,s)
	_,binding:=registerStorageDomain(t,s,m,one,"example.org");registerStorageDomain(t,s,m,two,"example.org")
	mailbox:=storageOffboardMailbox(t,s,m,m.ID,one.ID,"source@example.org");other:=storageOffboardMailbox(t,s,m,m.ID,two.ID,"source@example.org")
	if _,err:=s.DB.ExecContext(ctx,`INSERT INTO addresses(org_id,connection_id,domain_binding_id,domain_id,address_key,local_part,address,kind,mailbox_id,management_mode,sync_state,created_at,updated_at) VALUES (?,?,?,?, 'source@example.org','source','source@example.org','primary',?,'external','unknown',?,?)`,m.OrgID,one.ID,binding.ID,binding.DomainID,mailbox.ID,db.Now(),db.Now());err!=nil{t.Fatal(err)}
	items:=[]provider.DiscoverySnapshotResource{
		{Resource:provider.Resource{ResourceType:"forwarding",RemoteKey:"source@example.org/a@example.net",RemoteLocator:map[string]string{"domain":"example.org","mailboxLocalPart":"source","targetAddress":"a@example.net"},Purpose:provider.PurposeForwarding},Forwarding:&provider.ForwardingInfo{Domain:"example.org",LocalPart:"source",Targets:[]string{"a@example.net"},DeliveryMode:"unverified",StatusByTarget:map[string]string{"a@example.net":"pending_confirmation"}}},
		{Resource:provider.Resource{ResourceType:"forwarding",RemoteKey:"source@example.org/b@example.net",RemoteLocator:map[string]string{"domain":"example.org","mailboxLocalPart":"source","targetAddress":"b@example.net"},Purpose:provider.PurposeForwarding},Forwarding:&provider.ForwardingInfo{Domain:"example.org",LocalPart:"source",Targets:[]string{"b@example.net"},DeliveryMode:"unverified",StatusByTarget:map[string]string{"b@example.net":"blocked"}}},
	}
	apply:=func(){t.Helper();if err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{for _,item:=range items{if _,err:=importDiscoveredForwarding(ctx,tx,m.OrgID,one,item);err!=nil{return err};if err:=saveResourceObservation(ctx,tx,one.ID,item);err!=nil{return err}};return syncForwardingObservations(ctx,tx,m.OrgID,one.ID)});err!=nil{t.Fatal(err)}}
	apply();first,err:=s.MailboxForwarding(ctx,m.OrgID,mailbox.ID);if err!=nil{t.Fatal(err)};if first==nil || first.DeliveryMode!="unverified" || first.SyncState!="unknown" || len(first.Targets)!=2{t.Fatal("转发导入没有保存独立对象和全部选择目标")}
	var status struct{Verified bool `json:"systemVerified"`;Targets []string `json:"observedTargets"`;Statuses map[string]string `json:"statusByTarget"`};if err:=json.Unmarshal(first.RemoteStatus,&status);err!=nil{t.Fatal(err)};if status.Verified || len(status.Targets)!=2 || status.Statuses["a@example.net"]!="pending_confirmation" || status.Statuses["b@example.net"]!="blocked"{t.Fatal("转发导入丢失确认状态或错误声明投递验证")}
	apply();repeated,err:=s.MailboxForwarding(ctx,m.OrgID,mailbox.ID);if err!=nil{t.Fatal(err)};if repeated.ID!=first.ID || repeated.Revision!=first.Revision{t.Fatal("重复导入改变了转发对象或版本")}
	if value,err:=s.MailboxForwarding(ctx,m.OrgID,other.ID);err!=nil || value!=nil{t.Fatal("转发导入改变了其他连接的同名邮箱")}
	var kind string;if err:=s.DB.QueryRowContext(ctx,`SELECT kind FROM addresses WHERE connection_id=? AND address_key='source@example.org'`,one.ID).Scan(&kind);err!=nil{t.Fatal(err)};if kind!="primary"{t.Fatal("转发导入改变了主地址类型")}
	if _,err:=s.DB.ExecContext(ctx,`UPDATE provider_resources SET remote_state='missing' WHERE connection_id=? AND remote_key=?`,one.ID,items[1].Resource.RemoteKey);err!=nil{t.Fatal(err)}
	if err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{return syncForwardingObservations(ctx,tx,m.OrgID,one.ID)});err!=nil{t.Fatal(err)};changed,err:=s.MailboxForwarding(ctx,m.OrgID,mailbox.ID);if err!=nil{t.Fatal(err)};if changed.SyncState!="error" || len(changed.Targets)!=2{t.Fatal("缺失目标没有保留期望目标和差异状态")}
	items[0].Forwarding.StatusByTarget["a@example.net"]="active";apply();changed,err=s.MailboxForwarding(ctx,m.OrgID,mailbox.ID);if err!=nil{t.Fatal(err)};if changed.SyncState!="unknown"{t.Fatal("确认状态更新覆盖了未验证的投递方式")}
}

func TestMultiProviderStorageForwardingImportRequirements(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();c:=storageConnection(t,s,m,"转发依赖检查");startStorageOperations(t,s);registerStorageDomain(t,s,m,c,"example.org")
	item:=provider.DiscoverySnapshotResource{Resource:provider.Resource{ResourceType:"routing_rule",RemoteKey:"41",Purpose:provider.PurposeForwarding},Forwarding:&provider.ForwardingInfo{Domain:"example.org",LocalPart:"source",Targets:[]string{"destination@example.net"},DeliveryMode:"unverified",StatusByTarget:map[string]string{"destination@example.net":"unknown"}}}
	err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{_,err:=importDiscoveredForwarding(ctx,tx,m.OrgID,c,item);return err});requireProviderCode(t,err,"invalid")
	mailbox:=storageOffboardMailbox(t,s,m,m.ID,c.ID,"source@example.org")
	if err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{_,_,err:=importAdditionalResource(ctx,tx,m.OrgID,c,item);if err!=nil{return err};if err:=saveResourceObservation(ctx,tx,c.ID,item);err!=nil{return err};return syncForwardingObservations(ctx,tx,m.OrgID,c.ID)});err!=nil{t.Fatal(err)}
	value,err:=s.MailboxForwarding(ctx,m.OrgID,mailbox.ID);if err!=nil || value==nil || value.DeliveryMode!="unverified"{t.Fatal("主地址规则没有导入独立转发对象")}
	for _,invalid:=range []provider.DiscoverySnapshotResource{{Resource:item.Resource},{Resource:item.Resource,Forwarding:&provider.ForwardingInfo{Domain:"example.org",LocalPart:"source",Targets:[]string{"source@example.org"},DeliveryMode:"unverified",StatusByTarget:map[string]string{"source@example.org":"unknown"}}},{Resource:item.Resource,Forwarding:&provider.ForwardingInfo{Domain:"example.org",LocalPart:"source",Targets:[]string{"destination@example.net"},DeliveryMode:"unverified"}}}{if _,err:=normalizedForwarding(invalid);err==nil{t.Fatal("无效转发观察通过验证")}}
}
