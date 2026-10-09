package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestMultiProviderStorageRemoteAccessExternalReport(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();mailboxID,input:=storageSubmission(t,s,m);mb,err:=s.Mailbox(ctx,m.OrgID,mailboxID);if err!=nil{t.Fatal(err)}
	request,finish:=s.RegisterMailRequestSessionHash(ctx,m.ID,mailboxID,input.SessionHash);defer finish();startStorageOperations(t,s)
	requestID:=uuid.NewString();remote:=RemoteMailboxInput{ExpectedRevision:mb.Revision};payload:=OperationPayload{Kind:"mailbox.revokeRemoteAccess",MailboxID:mailboxID,RemoteMailbox:&remote}
	op,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,requestID,payload,nil);if err!=nil{t.Fatal(err)};pending:=awaitStorageOperation(t,s,m.OrgID,op.ID);if pending.Status!="needs_action"{t.Fatalf("远程撤销没有保留外部处理事项：%s %s",pending.Status,pending.Result)}
	var state string;if err:=s.DB.QueryRowContext(ctx,`SELECT state FROM credentials WHERE mailbox_id=? AND purpose='mail'`,mailboxID).Scan(&state);err!=nil{t.Fatal(err)};if state!="active" || request.Err()!=nil{t.Fatal("外部处理报告前更改了已提交凭据")}
	completed,err:=s.ConfirmExternalOperation(ctx,m.OrgID,m.ID,op.ID,ConfirmExternalInput{ItemID:"remoteAccess.external",Note:"管理员已逐项检查并处理远程访问"});if err!=nil{t.Fatal(err)};if completed.Status!="succeeded"{t.Fatal("远程撤销报告没有完成操作")}
	var result struct{Source string `json:"verificationSource"`;Verified bool `json:"systemVerified"`;Retired bool `json:"platformCredentialsRetired"`};if err:=json.Unmarshal(completed.Result,&result);err!=nil{t.Fatal(err)};if result.Source!="administrator_report" || result.Verified || !result.Retired{t.Fatal("远程撤销报告没有区分外部来源与本地凭据状态")}
	if request.Err()==nil{t.Fatal("远程撤销报告提交后没有取消邮箱请求")};if err:=s.DB.QueryRowContext(ctx,`SELECT state FROM credentials WHERE mailbox_id=? AND purpose='mail'`,mailboxID).Scan(&state);err!=nil{t.Fatal(err)};if state!="retired"{t.Fatal("已经报告撤销的凭据仍用于本地访问")}
	again,repeated,err:=s.QueueOperation(ctx,m.OrgID,m.ID,requestID,payload,nil);if err!=nil{t.Fatal(err)};if !repeated || again.ID!=op.ID{t.Fatal("重复远程撤销请求创建了其他操作")}
	updated,err:=s.Mailbox(ctx,m.OrgID,mailboxID);if err!=nil{t.Fatal(err)};if updated.Revision!=mb.Revision+1 || updated.AccessRevision!=mb.AccessRevision+1 || updated.HasCredential{t.Fatal("远程撤销报告没有更新邮箱访问版本")}
}

func TestMultiProviderStorageOperationInputIsolation(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();c:=storageConnection(t,s,m,"请求隔离连接")
	domain:=DomainBindingInput{ConnectionID:c.ID,DomainName:" EXAMPLE.ORG ",Mode:"register"};payload:=OperationPayload{Kind:"domain.register",Domain:&domain};requestID:=uuid.NewString()
	op,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,requestID,payload,nil);if err!=nil{t.Fatal(err)};if domain.DomainName!=" EXAMPLE.ORG "{t.Fatal("操作预检查修改了调用者的输入")}
	again,repeated,err:=s.QueueOperation(ctx,m.OrgID,m.ID,requestID,payload,nil);if err!=nil{t.Fatal(err)};if !repeated || again.ID!=op.ID{t.Fatal("规范化字段使原请求无法重复提交")}
	domain.DomainName="other.example.org";_,_,err=s.QueueOperation(ctx,m.OrgID,m.ID,requestID,payload,nil);requireProviderCode(t,err,"idempotency_conflict")
}
