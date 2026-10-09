package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"mailhearth/internal/db"
	"mailhearth/internal/model"
)

func storageMemberGroup(t *testing.T,s *Service,m *storageMember,cID int64,withAddress bool) int64 {
	t.Helper();now:=db.Now();result,err:=s.DB.Exec(`INSERT INTO groups(org_id,name,description,created_at,updated_at) VALUES (?,'成员创建群组','',?,?)`,m.OrgID,now,now);if err!=nil{t.Fatal(err)};id,err:=result.LastInsertId();if err!=nil{t.Fatal(err)}
	if withAddress{var domainID int64;err:=s.DB.QueryRow(`SELECT id FROM domains WHERE org_id=? AND name='example.org'`,m.OrgID).Scan(&domainID);if db.IsNotFound(err){domainID=storageRoutingDomain(t,s,m,"example.org")}else if err!=nil{t.Fatal(err)};if _,err:=s.DB.Exec(`INSERT INTO addresses(org_id,connection_id,domain_id,local_part,address,address_key,kind,group_id,management_mode,created_at,updated_at) VALUES (?,?,?,'team','team@example.org','team@example.org','group',?,'external',?,?)`,m.OrgID,cID,domainID,id,now,now);err!=nil{t.Fatal(err)}};return id
}

func TestMultiProviderStorageMemberCreationNone(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();mailboxID,_:=storageSubmission(t,s,m);if _,err:=s.DB.Exec(`UPDATE mailboxes SET owner_member_id=NULL,kind='shared' WHERE id=?`,mailboxID);err!=nil{t.Fatal(err)};groupID:=storageMemberGroup(t,s,m,0,false)
	in:=CreateMemberRequest{MemberInput:MemberInput{DisplayName:"统一创建成员",LoginEmail:"new@example.org"},MailboxAction:"none",Password:uuid.NewString()+"Aa1!",GroupIDs:[]int64{groupID},GroupExpectedRevisions:map[int64]int64{groupID:1},SharedMailboxes:[]int64{mailboxID},SharedExpectedRevisions:map[int64]int64{mailboxID:1}}
	requestID:=uuid.NewString();payload:=OperationPayload{Kind:"member.create",MemberCreate:&in};op,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,requestID,payload,nil);if err!=nil{t.Fatal(err)};var result struct{MemberID int64 `json:"memberId"`};if err:=json.Unmarshal(op.Result,&result);err!=nil{t.Fatal(err)};if result.MemberID<1{t.Fatal("成员创建没有返回预留的实际 ID")};member,err:=s.Member(ctx,m.OrgID,result.MemberID);if err!=nil{t.Fatal(err)};if member.Status!=model.MemberInvited{t.Fatal("未完成成员已经可以登录")}
	startStorageOperations(t,s);finished:=awaitStorageOperation(t,s,m.OrgID,op.ID);if finished.Status!="succeeded"{t.Fatalf("成员创建没有完成：%s %s",finished.Status,finished.Result)}
	member,err=s.Member(ctx,m.OrgID,result.MemberID);if err!=nil{t.Fatal(err)};if member.Status!=model.MemberActive || member.Revision!=2{t.Fatal("成员完成状态或版本无效")}
	var level string;if err:=s.DB.QueryRow(`SELECT level FROM mailbox_access WHERE mailbox_id=? AND member_id=?`,mailboxID,member.ID).Scan(&level);err!=nil{t.Fatal(err)};if level!=model.AccessFull{t.Fatal("统一创建没有授予共享邮箱访问")};group,err:=s.Group(ctx,m.OrgID,groupID);if err!=nil{t.Fatal(err)};if len(group.MemberIDs)!=1 || group.MemberIDs[0]!=member.ID || group.Revision!=2{t.Fatal("统一创建没有保存群组成员")}
	var count int;if err:=s.DB.QueryRow(`SELECT COUNT(*) FROM invites WHERE member_id=?`,member.ID).Scan(&count);err!=nil{t.Fatal(err)};if count!=0{t.Fatal("没有请求邀请仍生成了链接")}
	again,repeated,err:=s.QueueOperation(ctx,m.OrgID,m.ID,requestID,payload,nil);if err!=nil{t.Fatal(err)};if !repeated || again.ID!=op.ID{t.Fatal("重复创建没有返回原操作")};if err:=s.DB.QueryRow(`SELECT COUNT(*) FROM members WHERE login_email='new@example.org'`).Scan(&count);err!=nil{t.Fatal(err)};if count!=1{t.Fatal("重复请求创建了多名成员")}
}

func TestMultiProviderStorageMemberCreationBindDistribution(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();c:=storageConnection(t,s,m,"成员绑定连接");mb:=storageOffboardMailbox(t,s,m,m.ID,c.ID,"bound@example.org");if _,err:=s.DB.Exec(`UPDATE mailboxes SET owner_member_id=NULL WHERE id=?`,mb.ID);err!=nil{t.Fatal(err)};groupID:=storageMemberGroup(t,s,m,c.ID,true)
	in:=CreateMemberRequest{MemberInput:MemberInput{DisplayName:"绑定成员"},MailboxAction:"bind",MailboxID:mb.ID,MailboxExpectedRevision:mb.Revision,GroupIDs:[]int64{groupID},GroupExpectedRevisions:map[int64]int64{groupID:1},SendInvite:true}
	op,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"member.create",MemberCreate:&in},nil);if err!=nil{t.Fatal(err)};startStorageOperations(t,s);finished:=awaitStorageOperation(t,s,m.OrgID,op.ID);if finished.Status!="succeeded"{t.Fatalf("成员绑定分发操作没有完成：%s %s",finished.Status,finished.Result)}
	var result struct{MemberID int64 `json:"memberId"`;MailboxID int64 `json:"mailboxId"`;InviteLink string `json:"inviteLink"`};if err:=json.Unmarshal(finished.Result,&result);err!=nil{t.Fatal(err)};if result.MailboxID!=mb.ID || result.InviteLink==""{t.Fatal("绑定和邀请结果没有保存")}
	bound,err:=s.Mailbox(ctx,m.OrgID,mb.ID);if err!=nil{t.Fatal(err)};if bound.OwnerMemberID==nil || *bound.OwnerMemberID!=result.MemberID || bound.HasCredential{t.Fatal("bind 没有正确修改 owner 或自动创建了凭据")}
	group,err:=s.Group(ctx,m.OrgID,groupID);if err!=nil{t.Fatal(err)};if group.Address==nil || len(group.Address.Targets)!=1 || group.Address.Targets[0]!=mb.Address || group.Address.SyncState!="unknown"{t.Fatal("绑定成员的候选邮箱没有参与分发登记")}
	var count int;if err:=s.DB.QueryRow(`SELECT COUNT(*) FROM invites WHERE member_id=?`,result.MemberID).Scan(&count);err!=nil{t.Fatal(err)};if count!=1{t.Fatal("全部步骤完成后没有生成唯一邀请")}
}

func TestMultiProviderStorageMemberCreationPreflightAndFailure(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();c:=storageConnection(t,s,m,"成员预检查连接");mb:=storageOffboardMailbox(t,s,m,m.ID,c.ID,"candidate@example.org");if _,err:=s.DB.Exec(`UPDATE mailboxes SET owner_member_id=NULL WHERE id=?`,mb.ID);err!=nil{t.Fatal(err)}
	in:=CreateMemberRequest{MemberInput:MemberInput{DisplayName:"预检查成员",LoginEmail:"candidate-login@example.org"},MailboxAction:"none",MailboxID:mb.ID};_,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"member.create",MemberCreate:&in},nil);requireProviderCode(t,err,"invalid")
	var count int;if err:=s.DB.QueryRow(`SELECT COUNT(*) FROM members WHERE login_email=?`,in.LoginEmail).Scan(&count);err!=nil{t.Fatal(err)};if count!=0{t.Fatal("无效邮箱组合创建了成员")}
	in.MailboxAction="bind";in.MailboxExpectedRevision=mb.Revision;in.SendInvite=true;op,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"member.create",MemberCreate:&in},nil);if err!=nil{t.Fatal(err)};var created struct{ID int64 `json:"memberId"`};if err:=json.Unmarshal(op.Result,&created);err!=nil{t.Fatal(err)}
	if _,err:=s.DB.Exec(`UPDATE mailboxes SET revision=revision+1 WHERE id=?`,mb.ID);err!=nil{t.Fatal(err)};startStorageOperations(t,s);failed:=awaitStorageOperation(t,s,m.OrgID,op.ID);if failed.Status!="needs_action"{t.Fatalf("成员创建失败没有保留步骤：%s %s",failed.Status,failed.Result)}
	var detail struct{Details struct{ID int64 `json:"memberId"`} `json:"errorDetails"`};if err:=json.Unmarshal(failed.Result,&detail);err!=nil{t.Fatal(err)};if detail.Details.ID!=created.ID{t.Fatal("创建失败没有返回实际成员 ID")};if err:=s.DB.QueryRow(`SELECT COUNT(*) FROM invites WHERE member_id=?`,created.ID).Scan(&count);err!=nil{t.Fatal(err)};if count!=0{t.Fatal("存在失败步骤时生成了邀请链接")}
	_,err=s.ControlOperation(ctx,m.OrgID,m.ID,op.ID,"retry");requireProviderCode(t,err,"revision_conflict")
}

func TestMultiProviderStorageMemberCreationPendingAccess(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background()
	in:=CreateMemberRequest{MemberInput:MemberInput{DisplayName:"待完成成员",LoginEmail:"pending@example.org"},MailboxAction:"none",Password:uuid.NewString()+"Aa1!"}
	op,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"member.create",MemberCreate:&in},nil);if err!=nil{t.Fatal(err)}
	plan,err:=readMemberCreationPlan(ctx,s.DB,op.ID);if err!=nil{t.Fatal(err)}
	_,err=s.SetMemberStatus(ctx,m.OrgID,m.ID,plan.MemberID,true);requireProviderCode(t,err,"operation_in_progress")
	_,err=s.UpdateMember(ctx,m.OrgID,m.ID,plan.MemberID,MemberInput{DisplayName:"提前修改",ExpectedRevision:1});requireProviderCode(t,err,"operation_in_progress")
	_,err=s.CreateInvite(ctx,m.OrgID,m.ID,plan.MemberID);requireProviderCode(t,err,"operation_in_progress")
	err=s.SetPassword(ctx,m.OrgID,m.ID,plan.MemberID,uuid.NewString()+"Aa1!",true);requireProviderCode(t,err,"operation_in_progress")
	if _,err:=s.VerifyLogin(ctx,in.LoginEmail,in.Password);err==nil{t.Fatal("待完成成员获得了登录权限")}
	member,err:=s.Member(ctx,m.OrgID,plan.MemberID);if err!=nil{t.Fatal(err)};if member.Revision!=1 || member.Status!=model.MemberInvited || member.DisplayName!=in.DisplayName{t.Fatal("被拒绝的管理动作改变了成员")}
	if _,err:=s.ControlOperation(ctx,m.OrgID,m.ID,op.ID,"cancel");err!=nil{t.Fatal(err)}
	member,err=s.Member(ctx,m.OrgID,plan.MemberID);if err!=nil{t.Fatal(err)};if member.Revision!=1 || member.Status!=model.MemberInvited{t.Fatal("取消创建改变了预留成员状态")}
}

func TestMultiProviderStorageMemberCreationCancelGroupState(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();c:=storageConnection(t,s,m,"成员取消群组状态");groupID:=storageMemberGroup(t,s,m,c.ID,true)
	in:=CreateMemberRequest{MemberInput:MemberInput{DisplayName:"取消成员",LoginEmail:"cancel-group@example.org"},MailboxAction:"none",GroupIDs:[]int64{groupID},GroupExpectedRevisions:map[int64]int64{groupID:1},SendInvite:true}
	op,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"member.create",MemberCreate:&in},nil);if err!=nil{t.Fatal(err)}
	group,err:=s.Group(ctx,m.OrgID,groupID);if err!=nil{t.Fatal(err)};if group.Address==nil || group.Address.SyncState!="pending"{t.Fatal("群组地址没有保存待处理状态")}
	if _,err:=s.ControlOperation(ctx,m.OrgID,m.ID,op.ID,"cancel");err!=nil{t.Fatal(err)}
	group,err=s.Group(ctx,m.OrgID,groupID);if err!=nil{t.Fatal(err)};if group.Address.SyncState!="unknown"{t.Fatal("取消成员创建后仍将群组地址标记为待执行")}
	var count int;if err:=s.DB.QueryRow(`SELECT COUNT(*) FROM invites`).Scan(&count);err!=nil{t.Fatal(err)};if count!=0{t.Fatal("取消成员创建产生了邀请")}
}

func TestMultiProviderStorageMemberCreationGroupPreflight(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();c:=storageConnection(t,s,m,"成员群组版本连接");mb:=storageOffboardMailbox(t,s,m,m.ID,c.ID,"pending-bound@example.org");if _,err:=s.DB.Exec(`UPDATE mailboxes SET owner_member_id=NULL WHERE id=?`,mb.ID);err!=nil{t.Fatal(err)};groupID:=storageMemberGroup(t,s,m,c.ID,true)
	in:=CreateMemberRequest{MemberInput:MemberInput{DisplayName:"等待群组版本的成员"},MailboxAction:"bind",MailboxID:mb.ID,MailboxExpectedRevision:mb.Revision,GroupIDs:[]int64{groupID},GroupExpectedRevisions:map[int64]int64{groupID:1},SendInvite:true}
	op,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"member.create",MemberCreate:&in},nil);if err!=nil{t.Fatal(err)}
	if _,err:=s.DB.Exec(`UPDATE groups SET revision=revision+1 WHERE id=?`,groupID);err!=nil{t.Fatal(err)}
	startStorageOperations(t,s);failed:=awaitStorageOperation(t,s,m.OrgID,op.ID);if failed.Status!="needs_action"{t.Fatalf("群组版本变化没有终止创建：%s",failed.Status)}
	mb,err=s.Mailbox(ctx,m.OrgID,mb.ID);if err!=nil{t.Fatal(err)};if mb.OwnerMemberID!=nil || mb.Revision!=in.MailboxExpectedRevision{t.Fatal("群组预检查失败仍然绑定了邮箱")}
	_,err=s.ControlOperation(ctx,m.OrgID,m.ID,op.ID,"retry");requireProviderCode(t,err,"revision_conflict")
}

func TestMultiProviderStorageGroupCandidateSetChange(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();one:=storageConnection(t,s,m,"候选连接一");two:=storageConnection(t,s,m,"候选连接二");member:=storageOffboardMember(t,s,m,"候选成员");storageOffboardMailbox(t,s,m,member.ID,one.ID,"same-candidate@example.org");groupID:=storageMemberGroup(t,s,m,one.ID,true)
	in:=GroupInput{ExpectedRevision:1,Name:"候选集合群组",MemberIDs:[]int64{member.ID}}
	op,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"group.update",Routing:&RoutingOperationInput{GroupID:groupID,Group:&in}},nil);if err!=nil{t.Fatal(err)}
	storageOffboardMailbox(t,s,m,member.ID,two.ID,"same-candidate@example.org")
	startStorageOperations(t,s);failed:=awaitStorageOperation(t,s,m.OrgID,op.ID);if failed.Status!="needs_action"{t.Fatalf("候选邮箱集合变化没有终止操作：%s",failed.Status)}
	group,err:=s.Group(ctx,m.OrgID,groupID);if err!=nil{t.Fatal(err)};if len(group.MemberIDs)!=0 || len(group.Address.Targets)!=0 || group.Revision!=1{t.Fatal("候选集合变化仍然提交了群组配置")}
}
