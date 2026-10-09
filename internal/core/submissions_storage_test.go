package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"mailhearth/internal/db"
	"mailhearth/internal/mailproto/mailops"
	"mailhearth/internal/provider"
	"mailhearth/internal/secrets"
)

func storageSubmission(t *testing.T,s *Service,m *storageMember) (int64,ReserveSubmissionInput) {
	t.Helper();ctx:=context.Background();s.Cfg.SessionTTL=time.Hour;c:=storageConnection(t,s,m,"发送记录验证")
	now:=db.Now();result,err:=s.DB.ExecContext(ctx,`INSERT INTO mailboxes(org_id,connection_id,kind,address,address_key,owner_member_id,management_mode,remote_state,created_at,updated_at) VALUES (?,?,'personal','sender@example.org','sender@example.org',?,'external','external',?,?)`,m.OrgID,c.ID,m.ID,now,now);if err!=nil{t.Fatal(err)};boxID,err:=result.LastInsertId();if err!=nil{t.Fatal(err)}
	result,err=s.DB.ExecContext(ctx,`INSERT INTO identities(mailbox_id,address,display_name,is_default,created_at) VALUES (?,'sender@example.org','发件人',1,?)`,boxID,now);if err!=nil{t.Fatal(err)};identityID,err:=result.LastInsertId();if err!=nil{t.Fatal(err)}
	sealed,err:=s.Box.Seal(uuid.NewString());if err!=nil{t.Fatal(err)}
	result,err=s.DB.ExecContext(ctx,`INSERT INTO credentials(org_id,connection_id,mailbox_id,purpose,source,secret_enc,state,created_at,updated_at) VALUES (?,?,?,'mail','entered',?,'active',?,?)`,m.OrgID,c.ID,boxID,sealed,now,now);if err!=nil{t.Fatal(err)};credentialID,err:=result.LastInsertId();if err!=nil{t.Fatal(err)}
	if _,err:=s.DB.ExecContext(ctx,`INSERT INTO mailbox_endpoints(mailbox_id,protocol,network_mode,username,credential_id,created_at,updated_at) VALUES (?,'smtp','inherit','sender@example.org',?,?,?)`,boxID,credentialID,now,now);err!=nil{t.Fatal(err)}
	var endpoint ResolvedEndpoint;endpoint.OrgID=m.OrgID;endpoint.MailboxID=boxID
	if err:=s.DB.QueryRowContext(ctx,`SELECT c.id,c.revision,e.revision FROM mail_connections c JOIN mailboxes b ON b.connection_id=c.id JOIN mailbox_endpoints e ON e.mailbox_id=b.id WHERE b.id=? AND e.protocol='smtp'`,boxID).Scan(&endpoint.ConnectionID,&endpoint.ConnectionRevision,&endpoint.EndpointRevision);err!=nil{t.Fatal(err)}
	token,err:=s.CreateSession(ctx,m.ID,"127.0.0.1","storage-test");if err!=nil{t.Fatal(err)}
	id:=uuid.NewString();messageID:=mailops.NewMessageID("example.org")
	raw,err:=mailops.Build(&mailops.Draft{From:mailops.Recipient{Address:"sender@example.org"},To:[]mailops.Recipient{{Address:"recipient@example.org"}},Bcc:[]mailops.Recipient{{Address:"private@example.org"}},Text:"仅在远程草稿保存的正文",MessageID:messageID,SubmissionID:id});if err!=nil{t.Fatal(err)}
	sent:="Sent"
	return boxID,ReserveSubmissionInput{ID:id,RequestID:uuid.NewString(),RequestDigest:s.Box.RequestDigest([]byte("发送记录内容")),MessageID:messageID,Raw:raw,Envelope:SubmissionEnvelope{IdentityID:identityID,EnvelopeFrom:"sender@example.org",Recipients:[]string{"recipient@example.org","private@example.org"}},SessionHash:secrets.HashToken(token),SMTP:&endpoint,AccessRevision:1,IdentityRevision:1,SentCopyMode:"append",StagingFolder:"Drafts",SentFolder:&sent}
}

func TestMultiProviderStorageSubmissionIdempotency(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();boxID,input:=storageSubmission(t,s,m)
	first,repeated,err:=s.ReserveSubmission(ctx,m.OrgID,m.ID,boxID,input);if err!=nil{t.Fatal(err)};if repeated || first.Status!="preparing"{t.Fatal("发送记录预留状态不正确")}
	second,repeated,err:=s.ReserveSubmission(ctx,m.OrgID,m.ID,boxID,input);if err!=nil{t.Fatal(err)};if !repeated || first.ID!=second.ID{t.Fatal("重复请求创建了新的发送记录")}
	changed:=input;changed.RequestDigest=s.Box.RequestDigest([]byte("另一项发送记录内容"));_,_,err=s.ReserveSubmission(ctx,m.OrgID,m.ID,boxID,changed);requireProviderCode(t,err,"idempotency_conflict")
	var encrypted,sha string;if err:=s.DB.QueryRowContext(ctx,`SELECT envelope_enc,message_sha256 FROM submissions WHERE id=?`,first.ID).Scan(&encrypted,&sha);err!=nil{t.Fatal(err)}
	if strings.Contains(encrypted,"private@example.org") || strings.Contains(encrypted,"正文") || sha!=MessageSHA256(input.Raw){t.Fatal("发送记录秘密或摘要保存不正确")}
	plain,err:=s.Box.Open(encrypted);if err!=nil{t.Fatal(err)};var envelope SubmissionEnvelope;if err:=json.Unmarshal([]byte(plain),&envelope);err!=nil{t.Fatal(err)};if len(envelope.Recipients)!=2 || envelope.Recipients[1]!="private@example.org"{t.Fatal("Bcc 投递目标未完整保存")}
	if err:=s.CompleteSubmissionStaging(ctx,first.ID,nil,nil);err!=nil{t.Fatal(err)}
	current,err:=s.Submission(ctx,m.OrgID,m.ID,boxID,first.ID);if err!=nil{t.Fatal(err)};if current.Status!="unknown" || current.SMTPStatus!="not_started"{t.Fatal("缺少草稿定位信息时未保存未知状态")}
	if err:=s.CompleteSubmissionStaging(ctx,first.ID,nil,nil);err==nil{t.Fatal("重复草稿提交修改了已提交记录")}
	input.ID=uuid.NewString();input.RequestID=uuid.NewString()
	_,_,err=s.ReserveSubmission(ctx,m.OrgID,m.ID,boxID,input);requireProviderCode(t,err,"submission_content_changed")
}

func TestMultiProviderStorageSubmissionRestartAndCopyRetry(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();boxID,input:=storageSubmission(t,s,m)
	v,_,err:=s.ReserveSubmission(ctx,m.OrgID,m.ID,boxID,input);if err!=nil{t.Fatal(err)}
	if _,err:=s.DB.ExecContext(ctx,`UPDATE submissions SET status='running',smtp_status='submitting' WHERE id=?`,v.ID);err!=nil{t.Fatal(err)}
	workerCtx,cancel:=context.WithCancel(ctx);if err:=s.StartSubmissions(workerCtx);err!=nil{t.Fatal(err)};cancel();s.WaitSubmissions()
	restarted,err:=s.Submission(ctx,m.OrgID,m.ID,boxID,v.ID);if err!=nil{t.Fatal(err)}
	if restarted.Status!="unknown" || restarted.SMTPStatus!="unknown"{t.Fatal("中断的 SMTP 提交未保存未知状态")}
	_,err=s.RetrySubmissionSentCopy(ctx,m.OrgID,m.ID,boxID,v.ID,input.SessionHash);requireProviderCode(t,err,"submission_not_retryable")
	if err:=s.persistSubmissionResult(v.ID,"sent_copy_failed","accepted","unknown","upstream_failed",nil);err!=nil{t.Fatal(err)}
	retry,err:=s.RetrySubmissionSentCopy(ctx,m.OrgID,m.ID,boxID,v.ID,input.SessionHash);if err!=nil{t.Fatal(err)}
	if retry.ID!=v.ID || retry.Status!="queued" || retry.SMTPStatus!="accepted" || retry.SentStatus!="unknown"{t.Fatal("副本重试改变了 SMTP 接受状态")}
	if err:=s.RevokeSessions(ctx,m.ID);err!=nil{t.Fatal(err)}
	if err:=s.persistSubmissionResult(v.ID,"sent_copy_failed","accepted","failed","upstream_failed",nil);err!=nil{t.Fatal(err)}
	if _,err:=s.RetrySubmissionSentCopy(ctx,m.OrgID,m.ID,boxID,v.ID,input.SessionHash);err!=ErrForbidden{t.Fatalf("会话撤销后仍允许副本重试：%v",err)}
}

func TestMultiProviderStorageSubmissionPermissionRevision(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();boxID,input:=storageSubmission(t,s,m)
	v,_,err:=s.ReserveSubmission(ctx,m.OrgID,m.ID,boxID,input);if err!=nil{t.Fatal(err)}
	if _,err:=s.DB.ExecContext(ctx,`UPDATE submissions SET status='running' WHERE id=?`,v.ID);err!=nil{t.Fatal(err)}
	w,err:=s.submissionWork(ctx,v.ID);if err!=nil{t.Fatal(err)}
	if _,err:=s.DB.ExecContext(ctx,`UPDATE mailboxes SET access_revision=access_revision+1 WHERE id=?`,boxID);err!=nil{t.Fatal(err)}
	if err:=s.markSMTPSubmitting(ctx,w,input.Envelope);err==nil{t.Fatal("访问版本变化后仍进入 SMTP 提交")}else{var typed *provider.TypedError;if !errors.As(err,&typed) || typed.Code!="revision_conflict"{t.Fatal(err)}}
	current,err:=s.Submission(ctx,m.OrgID,m.ID,boxID,v.ID);if err!=nil{t.Fatal(err)};if current.SMTPStatus!="not_started"{t.Fatal("失败的发送预检查修改了 SMTP 状态")}
}
