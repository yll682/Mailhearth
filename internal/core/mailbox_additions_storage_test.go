package core

import (
	"context"
	"database/sql"
	"testing"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

func TestMultiProviderStorageMailboxAdditionCandidates(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();connection:=storageConnection(t,s,m,"新增邮箱连接");owner:=storageOffboardMember(t,s,m,"新增邮箱所有者");existing:=storageOffboardMailbox(t,s,m,owner.ID,connection.ID,"existing@example.org");groupID:=storageMutationGroup(t,s,m,connection.ID,owner.ID,"新增邮箱群组")
	startStorageOperations(t,s);_,binding:=registerStorageDomain(t,s,m,connection,"example.org")
	payload:=OperationPayload{Kind:"mailbox.create",Create:&ManagedMailboxCreateInput{ConnectionID:connection.ID,DomainBindingID:binding.ID,LocalPart:"additional",Kind:model.MailboxPersonal,OwnerMemberID:&owner.ID}}
	plan,err:=s.prepareMailboxAddition(ctx,m.OrgID,m.ID,&payload);if err!=nil{t.Fatal(err)}
	if plan.OwnerID!=owner.ID || plan.OwnerRevision!=owner.Revision || plan.ConnectionID!=connection.ID || plan.ConnectionRevision!=connection.Revision || len(plan.Groups)!=1 || len(plan.Before)!=1{t.Fatal("新增邮箱没有保存全部前提与关联群组")}
	before:=plan.Before[0].Rules[0].Targets;after:=plan.Groups[0].Rules[0].Targets
	if len(before)!=1 || before[0]!=existing.Address || len(after)!=2 || after[0]!="additional@example.org" || after[1]!=existing.Address{t.Fatal("新增邮箱候选没有保留全部原有邮箱")}
	current,err:=s.Group(ctx,m.OrgID,groupID);if err!=nil{t.Fatal(err)};if current.Revision!=1 || current.Address.Revision!=1 || len(current.Address.DesiredTargets)!=0{t.Fatal("新增邮箱预检查改变了群组")}
	keys:=mailboxAdditionKeys(plan);required:=map[string]bool{"connection:"+fmtID(connection.ID):false,"member:"+fmtID(owner.ID):false,"group:"+fmtID(groupID):false,"mailbox:"+fmtID(existing.ID):false,"mailbox-address:"+fmtID(connection.ID)+":additional@example.org":false};for _,key:=range keys{if _,ok:=required[key];ok{required[key]=true}};for key,present:=range required{if !present{t.Fatalf("新增邮箱缺少资源占用：%s",key)}}
	if _,err:=s.DB.Exec(`UPDATE members SET revision=revision+1 WHERE id=?`,owner.ID);err!=nil{t.Fatal(err)}
	err=s.DB.Tx(ctx,func(tx *sql.Tx)error{return reserveMailboxAddition(ctx,tx,m.OrgID,"unused",plan)});requireProviderCode(t,err,"revision_conflict")
	current,err=s.Group(ctx,m.OrgID,groupID);if err!=nil{t.Fatal(err)};if current.Address.Revision!=1{t.Fatal("所有者版本冲突仍修改了群组地址")}
	var count int;if err:=s.DB.QueryRow(`SELECT COUNT(*) FROM mailboxes WHERE address='additional@example.org'`).Scan(&count);err!=nil{t.Fatal(err)};if count!=0{t.Fatal("新增邮箱预检查提前创建了邮箱")}
}

func TestMultiProviderStorageMailboxAdditionMigaduConstraint(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();mailboxConnection:=storageConnection(t,s,m,"候选邮箱连接");owner:=storageOffboardMember(t,s,m,"候选邮箱所有者")
	templates,err:=provider.JSONString(provider.DefaultTemplates(provider.Migadu));if err!=nil{t.Fatal(err)};now:=db.Now();result,err:=s.DB.Exec(`INSERT INTO mail_connections(org_id,provider_kind,label,api_username,domain_scope_json,protocol_defaults_json,created_at,updated_at) VALUES (?,'migadu','群组限制连接','admin','{"mode":"explicit","domains":["example.org"]}',?,?,?)`,m.OrgID,templates,now,now);if err!=nil{t.Fatal(err)};groupConnection,err:=result.LastInsertId();if err!=nil{t.Fatal(err)}
	storageOffboardMailbox(t,s,m,owner.ID,groupConnection,"existing@example.org");groupID:=storageMutationGroup(t,s,m,groupConnection,owner.ID,"同连接群组")
	startStorageOperations(t,s);_,binding:=registerStorageDomain(t,s,m,mailboxConnection,"example.org")
	payload:=OperationPayload{Kind:"mailbox.create",Create:&ManagedMailboxCreateInput{ConnectionID:mailboxConnection.ID,DomainBindingID:binding.ID,LocalPart:"additional",Kind:model.MailboxPersonal,OwnerMemberID:&owner.ID}}
	_,err=s.prepareMailboxAddition(ctx,m.OrgID,m.ID,&payload);requireProviderCode(t,err,"target_constraint_failed")
	current,err:=s.Group(ctx,m.OrgID,groupID);if err!=nil{t.Fatal(err)};if current.Revision!=1 || current.Address.Revision!=1{t.Fatal("目标限制失败仍改变了群组")}
	var count int;if err:=s.DB.QueryRow(`SELECT COUNT(*) FROM operation_steps WHERE step_key='mailbox.addition.prepare'`).Scan(&count);err!=nil{t.Fatal(err)};if count!=0{t.Fatal("目标限制失败仍然预留了操作")}
}
