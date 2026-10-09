package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"mailhearth/internal/db"
	"mailhearth/internal/model"
)

func TestMultiProviderStorageForwardingExternalLifecycle(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();c:=storageConnection(t,s,m,"外部转发连接");mb:=storageOffboardMailbox(t,s,m,m.ID,c.ID,"original@example.org");domainID:=storageRoutingDomain(t,s,m,"example.org")
	res,err:=s.DB.ExecContext(ctx,`INSERT INTO addresses(org_id,connection_id,domain_id,address_key,local_part,address,kind,mailbox_id,management_mode,sync_state,created_at,updated_at) VALUES (?,?,?,'original@example.org','original','original@example.org','primary',?,'external','unknown',?,?)`,m.OrgID,c.ID,domainID,mb.ID,db.Now(),db.Now());if err!=nil{t.Fatal(err)};addressID,err:=res.LastInsertId();if err!=nil{t.Fatal(err)}
	startStorageOperations(t,s);requestID:=uuid.NewString();in:=ForwardingInput{ExpectedRevision:mb.Revision,Targets:[]string{"b@example.org","a@example.org","a@example.org"},DeliveryMode:"copy"};payload:=OperationPayload{Kind:"mailbox.forwarding.set",MailboxID:mb.ID,Forwarding:&in}
	op,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,requestID,payload,nil);if err!=nil{t.Fatal(err)};finished:=awaitStorageOperation(t,s,m.OrgID,op.ID);if finished.Status!="needs_action"{t.Fatalf("外部转发没有保留管理员处理步骤：%s %s",finished.Status,finished.Result)}
	again,repeated,err:=s.QueueOperation(ctx,m.OrgID,m.ID,requestID,payload,nil);if err!=nil{t.Fatal(err)};if !repeated || again.ID!=op.ID{t.Fatal("转发重复请求创建了其他操作")}
	current,err:=s.MailboxForwarding(ctx,m.OrgID,mb.ID);if err!=nil{t.Fatal(err)};if current==nil || current.DeliveryMode!="copy" || current.Revision!=2 || current.SyncState!="unknown" || len(current.Targets)!=2 || current.Targets[0]!="a@example.org"{t.Fatal("转发登记没有保存独立对象和规范化目标")}
	address,err:=s.Address(ctx,m.OrgID,addressID);if err!=nil{t.Fatal(err)};if address.Kind!=model.AddressPrimary || len(address.Targets)!=0{t.Fatal("转发更改了 primary 地址类型或投递目标")}
	var status struct{Verified bool `json:"systemVerified"`;Source string `json:"verificationSource"`};if err:=json.Unmarshal(current.RemoteStatus,&status);err!=nil{t.Fatal(err)};if status.Verified || status.Source!="local_registration"{t.Fatal("外部登记被当作服务器核验")}
	reported,err:=s.ConfirmExternalOperation(ctx,m.OrgID,m.ID,op.ID,ConfirmExternalInput{StepKeys:[]string{"forwarding.external"},Note:"管理员完成服务商设置"});if err!=nil{t.Fatal(err)};if reported.Status!="succeeded"{t.Fatal("外部报告没有完成转发操作")}
	if err:=json.Unmarshal(reported.Result,&status);err!=nil{t.Fatal(err)};if status.Verified || status.Source!="administrator_report"{t.Fatal("外部完成报告被当作系统核验")}
	stale:=ForwardingInput{ExpectedRevision:1,Targets:[]string{"c@example.org"},DeliveryMode:"redirect"};_,_,err=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"mailbox.forwarding.set",MailboxID:mb.ID,Forwarding:&stale},nil);requireProviderCode(t,err,"revision_conflict")
	remove:=ForwardingInput{ExpectedRevision:current.Revision};deletion,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"mailbox.forwarding.delete",MailboxID:mb.ID,Forwarding:&remove},nil);if err!=nil{t.Fatal(err)};finished=awaitStorageOperation(t,s,m.OrgID,deletion.ID);if finished.Status!="needs_action"{t.Fatal("停止外部转发没有要求管理员报告")}
	if _,err:=s.ConfirmExternalOperation(ctx,m.OrgID,m.ID,deletion.ID,ConfirmExternalInput{StepKeys:[]string{"forwarding.external"},Note:"管理员报告已停止转发"});err!=nil{t.Fatal(err)};current,err=s.MailboxForwarding(ctx,m.OrgID,mb.ID);if err!=nil || current!=nil{t.Fatal("停止转发后仍保留本地转发登记")}
	address,err=s.Address(ctx,m.OrgID,addressID);if err!=nil || address.Kind!=model.AddressPrimary{t.Fatal("停止转发改变了 primary 地址")}
}

func TestMultiProviderStorageForwardingPreflightCancellation(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();c:=storageConnection(t,s,m,"转发预检查连接");mb:=storageOffboardMailbox(t,s,m,m.ID,c.ID,"source@example.org")
	for _,in:=range []ForwardingInput{{ExpectedRevision:mb.Revision,Targets:[]string{"source@example.org"},DeliveryMode:"redirect"},{ExpectedRevision:mb.Revision,Targets:[]string{"target@example.org"}},{ExpectedRevision:mb.Revision,DeliveryMode:"copy"}}{_,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"mailbox.forwarding.set",MailboxID:mb.ID,Forwarding:&in},nil);if err==nil{t.Fatal("无效转发请求通过预检查")}}
	value,err:=s.MailboxForwarding(ctx,m.OrgID,mb.ID);if err!=nil || value!=nil{t.Fatal("失败的预检查创建了转发对象")}
	in:=ForwardingInput{ExpectedRevision:mb.Revision,Targets:[]string{"target@example.org"},DeliveryMode:"redirect"};op,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"mailbox.forwarding.set",MailboxID:mb.ID,Forwarding:&in},nil);if err!=nil{t.Fatal(err)}
	if _,err:=s.ControlOperation(ctx,m.OrgID,m.ID,op.ID,"cancel");err!=nil{t.Fatal(err)};value,err=s.MailboxForwarding(ctx,m.OrgID,mb.ID);if err!=nil || value==nil || value.SyncState!="unknown" || len(value.Targets)!=0{t.Fatal("取消待执行转发没有保留未验证登记")}
}
