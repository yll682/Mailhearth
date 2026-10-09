package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"mailhearth/internal/db"
	"mailhearth/internal/provider"
)

func TestMultiProviderStorageLocalMailboxRetirement(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();connection:=storageConnection(t,s,m,"手动邮箱登记")
	now:=db.Now();result,err:=s.DB.ExecContext(ctx,`INSERT INTO mailboxes(org_id,connection_id,kind,address,address_key,management_mode,remote_state,created_at,updated_at) VALUES (?,?,'personal','registered@example.org','registered@example.org','external','external',?,?)`,m.OrgID,connection.ID,now,now);if err!=nil{t.Fatal(err)};mailboxID,err:=result.LastInsertId();if err!=nil{t.Fatal(err)}
	secret:=uuid.NewString();sealed,err:=s.Box.Seal(secret);if err!=nil{t.Fatal(err)}
	result,err=s.DB.ExecContext(ctx,`INSERT INTO credentials(org_id,connection_id,mailbox_id,purpose,source,secret_enc,state,created_at,updated_at) VALUES (?,?,?,'mail','entered',?,'active',?,?)`,m.OrgID,connection.ID,mailboxID,sealed,now,now);if err!=nil{t.Fatal(err)};credentialID,err:=result.LastInsertId();if err!=nil{t.Fatal(err)}
	if _,err:=s.DB.ExecContext(ctx,`INSERT INTO provider_resources(connection_id,resource_type,remote_key,purpose,credential_id,remote_state,created_at,updated_at) VALUES (?,'entered_credential',?,'login_credential',?,'external',?,?)`,connection.ID,fmtID(credentialID),credentialID,now,now);err!=nil{t.Fatal(err)}
	preview,err:=s.MailboxRetirementCredentials(ctx,m.OrgID,mailboxID);if err!=nil{t.Fatal(err)};if len(preview)!=1 || !preview[0].ExternalRevocationRequired{t.Fatal("解除登记说明没有列出需要外部撤销的凭据")}
	req,cancel:=s.RegisterMailRequest(ctx,m.ID,mailboxID,"");defer cancel()
	if err:=s.DeleteMailbox(ctx,m.OrgID,m.ID,mailboxID,"registered@example.org",2);err==nil{t.Fatal("版本不匹配时仍允许解除登记")}else{requireProviderCode(t,err,"revision_conflict")}
	if err:=s.DeleteMailbox(ctx,m.OrgID,m.ID,mailboxID,"registered@example.org",1);err!=nil{t.Fatal(err)}
	if req.Err()==nil{t.Fatal("解除登记未终止邮箱请求")}
	var count int;if err:=s.DB.QueryRowContext(ctx,`SELECT (SELECT COUNT(*) FROM mailboxes WHERE id=?)+(SELECT COUNT(*) FROM credentials WHERE mailbox_id=?)+(SELECT COUNT(*) FROM provider_resources WHERE credential_id=?)`,mailboxID,mailboxID,credentialID).Scan(&count);err!=nil{t.Fatal(err)};if count!=0{t.Fatal("解除登记没有处理所属资源")}
	var detail string;if err:=s.DB.QueryRowContext(ctx,`SELECT detail_json FROM audit_log WHERE action='mailbox.unregister' AND target_id=?`,fmtID(mailboxID)).Scan(&detail);err!=nil{t.Fatal(err)}
	if strings.Contains(detail,secret) || strings.Contains(detail,sealed){t.Fatal("解除登记审计包含秘密")}
	var decoded struct{Credentials []MailboxRetirementCredential `json:"credentials"`;References []MailboxRetirementReference `json:"references"`};if err:=json.Unmarshal([]byte(detail),&decoded);err!=nil{t.Fatal(err)};if len(decoded.Credentials)!=1 || len(decoded.References)!=1{t.Fatal("解除登记审计没有保留非秘密资源信息")}
}

func TestMultiProviderStorageMailboxHistoryArchive(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();mailboxID,input:=storageSubmission(t,s,m)
	if _,_,err:=s.ReserveSubmission(ctx,m.OrgID,m.ID,mailboxID,input);err!=nil{t.Fatal(err)}
	if err:=s.DeleteMailbox(ctx,m.OrgID,m.ID,mailboxID,"sender@example.org",1);err==nil{t.Fatal("有发送历史的邮箱仍可解除登记")}else{requireProviderCode(t,err,"mailbox_has_history")}
	if err:=s.ArchiveMailbox(ctx,m.OrgID,m.ID,mailboxID,1);err!=nil{t.Fatal(err)}
	mailbox,err:=s.Mailbox(ctx,m.OrgID,mailboxID);if err!=nil{t.Fatal(err)};if mailbox.Status!="archived" || mailbox.Revision!=2 || mailbox.AccessRevision!=2{t.Fatal("归档状态或版本不正确")}
	var count int;if err:=s.DB.QueryRowContext(ctx,`SELECT COUNT(*) FROM submissions WHERE mailbox_id=?`,mailboxID).Scan(&count);err!=nil{t.Fatal(err)};if count!=1{t.Fatal("归档没有保留发送历史")}
}

func TestMultiProviderStorageOperationControls(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();connection:=storageConnection(t,s,m,"操作状态验证")
	queued,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"connection.discover",ConnectionID:connection.ID},[]string{"connection:"+fmtID(connection.ID),"connection:"+fmtID(connection.ID)});if err!=nil{t.Fatal(err)}
	if _,err:=s.ControlOperation(ctx,m.OrgID,m.ID,queued.ID,"cancel");err!=nil{t.Fatal(err)}
	if _,err:=s.ControlOperation(ctx,m.OrgID,m.ID,queued.ID,"retry");err==nil{t.Fatal("已取消操作仍可重试")}else{requireProviderCode(t,err,"operation_not_retryable")}
	operation,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"connection.configure",ConnectionID:connection.ID,Connection:&UpdateConnectionInput{ExpectedRevision:connection.Revision}},nil);if err!=nil{t.Fatal(err)}
	if _,err:=s.DB.ExecContext(ctx,`UPDATE operations SET status='unknown',error_code='process_interrupted' WHERE id=?`,operation.ID);err!=nil{t.Fatal(err)}
	if _,err:=s.DB.ExecContext(ctx,`UPDATE operation_steps SET status='unknown' WHERE operation_id=?`,operation.ID);err!=nil{t.Fatal(err)}
	if _,err:=s.ControlOperation(ctx,m.OrgID,m.ID,operation.ID,"retry");err==nil{t.Fatal("结果未知操作绕过了核查")}else{requireProviderCode(t,err,"operation_not_retryable")}
	verified,err:=s.ControlOperation(ctx,m.OrgID,m.ID,operation.ID,"reconcile");if err!=nil{t.Fatal(err)};if verified.Status!="failed"{t.Fatal("本地事务核查未保存明确失败状态")}
	retry,err:=s.ControlOperation(ctx,m.OrgID,m.ID,operation.ID,"retry");if err!=nil{t.Fatal(err)};if retry.ID!=operation.ID || retry.Status!="queued"{t.Fatal("重试没有保留原操作记录")}
	if _,err:=s.ControlOperation(ctx,m.OrgID,m.ID,operation.ID,"cancel");err!=nil{t.Fatal(err)}
	_,_,err=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"unknown",ConnectionID:connection.ID},nil);requireProviderCode(t,err,"unsupported_operation")
	if err:=s.ReactivateMailbox(ctx,m.OrgID,m.ID,999,1);err==nil{t.Fatal("不存在的邮箱可以恢复")}else if err!=ErrNotFound{t.Fatal(err)}
	if _,err:=s.OperationForMember(ctx,m.OrgID+1,m.ID,operation.ID);err==nil{t.Fatal("操作查询接受其他组织")}
	if !localTransactionalOperation("mailbox.endpoints") || localTransactionalOperation("mailbox.reactivate") || localTransactionalOperation(string(provider.Manual)){t.Fatal("本地事务操作范围不正确")}
}
