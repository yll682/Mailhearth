package core

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"mailhearth/internal/model"
)

func TestMultiProviderStorageGroupAddressPermissions(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();actor:=storageOffboardMember(t,s,m,"群组管理成员");c:=storageConnection(t,s,m,"群组权限连接");groupID:=storageMemberGroup(t,s,m,c.ID,true)
	limited,err:=s.CreateRole(ctx,m.OrgID,m.ID,RoleInput{Name:"群组权限",Permissions:[]string{model.PermGroupsManage}});if err!=nil{t.Fatal(err)}
	actor,err=s.UpdateMember(ctx,m.OrgID,m.ID,actor.ID,MemberInput{ExpectedRevision:actor.Revision,RoleID:limited.ID});if err!=nil{t.Fatal(err)}
	input:=GroupInput{ExpectedRevision:1,Name:"群组管理更新"};payload:=OperationPayload{Kind:"group.update",Routing:&RoutingOperationInput{GroupID:groupID,Group:&input}}
	var before int;if err:=s.DB.QueryRow(`SELECT COUNT(*) FROM operations`).Scan(&before);err!=nil{t.Fatal(err)}
	requestID:=uuid.NewString();_,_,err=s.QueueOperation(ctx,m.OrgID,actor.ID,requestID,payload,nil);if !errors.Is(err,ErrForbidden){t.Fatalf("缺少地址管理权限的群组更新没有被拒绝：%v",err)}
	var count int;if err:=s.DB.QueryRow(`SELECT COUNT(*) FROM operations`).Scan(&count);err!=nil{t.Fatal(err)};if count!=before{t.Fatal("权限不足的群组更新创建了 Operation")}
	full,err:=s.CreateRole(ctx,m.OrgID,m.ID,RoleInput{Name:"群组与地址权限",Permissions:[]string{model.PermGroupsManage,model.PermAddressesManage}});if err!=nil{t.Fatal(err)}
	actor,err=s.UpdateMember(ctx,m.OrgID,m.ID,actor.ID,MemberInput{ExpectedRevision:actor.Revision,RoleID:full.ID});if err!=nil{t.Fatal(err)}
	queued,_,err:=s.QueueOperation(ctx,m.OrgID,actor.ID,requestID,payload,nil);if err!=nil{t.Fatal(err)}
	actor,err=s.UpdateMember(ctx,m.OrgID,m.ID,actor.ID,MemberInput{ExpectedRevision:actor.Revision,RoleID:limited.ID});if err!=nil{t.Fatal(err)}
	_,_,err=s.QueueOperation(ctx,m.OrgID,actor.ID,requestID,payload,nil);if !errors.Is(err,ErrForbidden){t.Fatal("重复请求没有检查当前地址管理权限")}
	startStorageOperations(t,s);failed:=awaitStorageOperation(t,s,m.OrgID,queued.ID);if failed.ErrorCode==nil || *failed.ErrorCode!="forbidden"{t.Fatalf("权限撤销后继续执行群组更新：%s %s",failed.Status,failed.Result)}
	group,err:=s.Group(ctx,m.OrgID,groupID);if err!=nil{t.Fatal(err)};if group.Name==input.Name{t.Fatal("权限撤销后提交了群组名称")}
}

func TestMultiProviderStorageOperationControllerAfterActorDisabled(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();successor:=storageOffboardMember(t,s,m,"接收管理权限的成员")
	in:=CreateMemberRequest{MemberInput:MemberInput{DisplayName:"取消的成员创建",LoginEmail:"cancelled-create@example.org"},MailboxAction:"none",Password:uuid.NewString()+"Aa1!",SendInvite:true}
	op,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"member.create",MemberCreate:&in},nil);if err!=nil{t.Fatal(err)};plan,err:=readMemberCreationPlan(ctx,s.DB,op.ID);if err!=nil{t.Fatal(err)}
	if err:=s.TransferOwnership(ctx,m.OrgID,m.ID,successor.ID);err!=nil{t.Fatal(err)};if _,err:=s.SetMemberStatus(ctx,m.OrgID,successor.ID,m.ID,false);err!=nil{t.Fatal(err)}
	cancelled,err:=s.ControlOperation(ctx,m.OrgID,successor.ID,op.ID,"cancel");if err!=nil{t.Fatal(err)};if cancelled.Status!="cancelled"{t.Fatal("当前管理员不能取消已停用发起人的待执行请求")}
	member,err:=s.Member(ctx,m.OrgID,plan.MemberID);if err!=nil{t.Fatal(err)};if member.Status!=model.MemberInvited || member.Revision!=1{t.Fatal("取消请求激活了待完成成员")};var count int;if err:=s.DB.QueryRow(`SELECT COUNT(*) FROM invites WHERE member_id=?`,member.ID).Scan(&count);err!=nil{t.Fatal(err)};if count!=0{t.Fatal("取消请求生成了邀请链接")}
}

func TestMultiProviderStorageExternalReportAfterActorDisabled(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();successor:=storageOffboardMember(t,s,m,"处理外部事项的管理员");c:=storageConnection(t,s,m,"外部事项连接");mb:=storageOffboardMailbox(t,s,m,m.ID,c.ID,"external-review@example.org")
	op,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"mailbox.revokeRemoteAccess",MailboxID:mb.ID,RemoteMailbox:&RemoteMailboxInput{ExpectedRevision:mb.Revision}},nil);if err!=nil{t.Fatal(err)};startStorageOperations(t,s);op=awaitStorageOperation(t,s,m.OrgID,op.ID);if op.Status!="needs_action"{t.Fatalf("外部事项没有保存：%s",op.Status)}
	if err:=s.TransferOwnership(ctx,m.OrgID,m.ID,successor.ID);err!=nil{t.Fatal(err)};if _,err:=s.SetMemberStatus(ctx,m.OrgID,successor.ID,m.ID,false);err!=nil{t.Fatal(err)}
	if _,err:=s.ConfirmExternalOperation(ctx,m.OrgID,m.ID,op.ID,ConfirmExternalInput{ItemID:"remoteAccess.external",Note:"完成外部检查"});!errors.Is(err,ErrForbidden){t.Fatal("已停用发起人仍然能够报告外部事项")}
	completed,err:=s.ConfirmExternalOperation(ctx,m.OrgID,successor.ID,op.ID,ConfirmExternalInput{ItemID:"remoteAccess.external",Note:"管理员已完成逐项外部检查"});if err!=nil{t.Fatal(err)};if completed.Status!="succeeded"{t.Fatal("当前管理员不能处理已停用发起人的外部事项")}
}
