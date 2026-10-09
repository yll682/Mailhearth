package core

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"mailhearth/internal/model"
)

func TestMultiProviderStorageMemberStatusGroupLifecycle(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();c:=storageConnection(t,s,m,"成员状态连接");member:=storageOffboardMember(t,s,m,"状态成员");mb:=storageOffboardMailbox(t,s,m,member.ID,c.ID,"status@example.org");groupID:=storageMutationGroup(t,s,m,c.ID,member.ID,"状态群组")
	if _,err:=s.DB.Exec(`UPDATE addresses SET targets_json=?,desired_targets_json=?,observed_targets_json=? WHERE group_id=?`,toJSON([]string{mb.Address}),toJSON([]string{mb.Address}),toJSON([]string{mb.Address}),groupID);err!=nil{t.Fatal(err)}
	s.Cfg.SessionTTL=time.Hour;token,err:=s.CreateSession(ctx,member.ID,"127.0.0.1","成员状态检查");if err!=nil{t.Fatal(err)};request,finish:=s.RegisterMailRequest(ctx,member.ID,mb.ID,"");defer finish()
	enabled:=false;in:=MemberStatusInput{ExpectedRevision:member.Revision,Enabled:&enabled};payload:=OperationPayload{Kind:"member.status",MemberID:member.ID,MemberStatus:&in};requestID:=uuid.NewString();op,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,requestID,payload,nil);if err!=nil{t.Fatal(err)}
	current,err:=s.Member(ctx,m.OrgID,member.ID);if err!=nil{t.Fatal(err)};if current.Status!=model.MemberDisabled || current.Revision!=member.Revision+1{t.Fatal("停用没有立即保存成员状态")};if request.Err()==nil{t.Fatal("停用没有取消成员请求")};if session,err:=s.LookupSession(ctx,token);err!=nil || session!=nil{t.Fatal("停用没有撤销已有会话")}
	startStorageOperations(t,s);finished:=awaitStorageOperation(t,s,m.OrgID,op.ID);if finished.Status!="succeeded"{t.Fatalf("停用群组操作没有完成：%s %s",finished.Status,finished.Result)};group,err:=s.Group(ctx,m.OrgID,groupID);if err!=nil{t.Fatal(err)};if group.Address==nil || len(group.Address.Targets)!=0 || group.Revision!=2{t.Fatal("停用成员没有移除其投递目标")}
	again,repeated,err:=s.QueueOperation(ctx,m.OrgID,m.ID,requestID,payload,nil);if err!=nil || !repeated || again.ID!=op.ID{t.Fatal("停用重复请求没有返回原操作")}
	enabled=true;in=MemberStatusInput{ExpectedRevision:current.Revision,Enabled:&enabled};op,_,err=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"member.status",MemberID:member.ID,MemberStatus:&in},nil);if err!=nil{t.Fatal(err)};finished=awaitStorageOperation(t,s,m.OrgID,op.ID);if finished.Status!="succeeded"{t.Fatalf("启用群组操作没有完成：%s %s",finished.Status,finished.Result)}
	current,err=s.Member(ctx,m.OrgID,member.ID);if err!=nil{t.Fatal(err)};if current.Status!=model.MemberActive || current.Revision!=member.Revision+2{t.Fatal("启用成员没有保存版本")};group,err=s.Group(ctx,m.OrgID,groupID);if err!=nil{t.Fatal(err)};if len(group.Address.Targets)!=1 || group.Address.Targets[0]!=mb.Address || group.Revision!=3{t.Fatal("启用成员没有恢复全部群组目标")}
	if session,err:=s.LookupSession(ctx,token);err!=nil || session!=nil{t.Fatal("启用成员恢复了已撤销会话")}
}

func TestMultiProviderStorageMemberStatusStaleGroup(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();c:=storageConnection(t,s,m,"成员状态版本连接");member:=storageOffboardMember(t,s,m,"版本状态成员");storageOffboardMailbox(t,s,m,member.ID,c.ID,"status-revision@example.org");groupID:=storageMutationGroup(t,s,m,c.ID,member.ID,"状态版本群组");enabled:=false
	in:=MemberStatusInput{ExpectedRevision:member.Revision,Enabled:&enabled};op,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"member.status",MemberID:member.ID,MemberStatus:&in},nil);if err!=nil{t.Fatal(err)}
	if _,err:=s.DB.Exec(`UPDATE groups SET revision=revision+1 WHERE id=?`,groupID);err!=nil{t.Fatal(err)};startStorageOperations(t,s);failed:=awaitStorageOperation(t,s,m.OrgID,op.ID);if failed.Status!="needs_action"{t.Fatalf("状态群组版本冲突没有保留操作：%s",failed.Status)}
	current,err:=s.Member(ctx,m.OrgID,member.ID);if err!=nil{t.Fatal(err)};if current.Status!=model.MemberDisabled{t.Fatal("群组处理失败改变了停用成员状态")}
	_,err=s.ControlOperation(ctx,m.OrgID,m.ID,op.ID,"retry");requireProviderCode(t,err,"revision_conflict")
	if _,err:=s.ControlOperation(ctx,m.OrgID,m.ID,op.ID,"cancel");err!=nil{t.Fatal(err)};current,err=s.Member(ctx,m.OrgID,member.ID);if err!=nil{t.Fatal(err)};if current.Status!=model.MemberDisabled{t.Fatal("取消群组处理恢复了成员访问")}
	_,_,err=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"member.status",MemberID:member.ID,MemberStatus:&in},nil);requireProviderCode(t,err,"revision_conflict")
	_,_,err=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"member.status",MemberID:member.ID,MemberStatus:&MemberStatusInput{ExpectedRevision:current.Revision}},nil);requireProviderCode(t,err,"invalid")
}
