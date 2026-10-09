package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/mail"
	"time"

	"github.com/google/uuid"
	"mailhearth/internal/db"
	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/mailproto/mailops"
	"mailhearth/internal/provider"
)

type SubmissionReplyContext struct {
	mailops.MessageLocator
	MessageID string `json:"messageId"`
	Forward bool `json:"forward"`
}

type SubmissionEnvelope struct {
	IdentityID int64 `json:"identityId"`
	EnvelopeFrom string `json:"envelopeFrom"`
	Recipients []string `json:"recipients"`
	OriginalDraft *mailops.MessageLocator `json:"originalDraft"`
	ReplyContext *SubmissionReplyContext `json:"replyContext"`
}

type SubmissionView struct {
	ID string `json:"submissionId"`
	MailboxID int64 `json:"mailboxId"`
	MemberID int64 `json:"memberId"`
	MessageID string `json:"messageId"`
	Status string `json:"status"`
	SMTPStatus string `json:"smtpStatus"`
	SentStatus string `json:"sentStatus"`
	ErrorCode *string `json:"errorCode"`
	CleanupErrorCode *string `json:"cleanupErrorCode"`
	SentLocator *mailops.MessageLocator `json:"sentLocator"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type ReserveSubmissionInput struct {
	ID string
	RequestID string
	RequestDigest string
	MessageID string
	Raw []byte
	Envelope SubmissionEnvelope
	SessionHash string
	SMTP *ResolvedEndpoint
	AccessRevision int64
	IdentityRevision int64
	SentCopyMode string
	StagingFolder string
	SentFolder *string
}

func MessageSHA256(raw []byte) string {digest:=sha256.Sum256(raw);return hex.EncodeToString(digest[:])}

func (s *Service) submission(ctx context.Context,orgID,mailboxID int64,id string) (*SubmissionView,error) {
	return scanSubmission(s.DB.QueryRowContext(ctx,submissionSelect+` WHERE org_id=? AND mailbox_id=? AND id=?`,orgID,mailboxID,id))
}

const submissionSelect=`SELECT id,mailbox_id,member_id,message_id,status,smtp_status,sent_status,error_code,cleanup_error_code,sent_locator_json,created_at,updated_at FROM submissions`

func scanSubmission(row interface{Scan(...any) error}) (*SubmissionView,error) {
	var v SubmissionView;var code,cleanup,sent sql.NullString
	err:=row.Scan(&v.ID,&v.MailboxID,&v.MemberID,&v.MessageID,&v.Status,&v.SMTPStatus,&v.SentStatus,&code,&cleanup,&sent,&v.CreatedAt,&v.UpdatedAt)
	if db.IsNotFound(err){return nil,ErrNotFound};if err!=nil{return nil,err}
	v.ErrorCode=nullStr(code);v.CleanupErrorCode=nullStr(cleanup)
	if sent.Valid{if err:=json.Unmarshal([]byte(sent.String),&v.SentLocator);err!=nil{return nil,err}}
	return &v,nil
}

func (s *Service) Submissions(ctx context.Context,orgID,memberID,mailboxID int64) ([]SubmissionView,error) {
	mc,err:=s.CheckMailboxAccess(ctx,orgID,memberID,mailboxID);if err!=nil{return nil,err}
	rows,err:=s.DB.QueryContext(ctx,submissionSelect+` WHERE org_id=? AND mailbox_id=? AND (member_id=? OR ?) ORDER BY created_at DESC,id DESC LIMIT 100`,orgID,mailboxID,memberID,mc.CanDelete());if err!=nil{return nil,err};defer rows.Close()
	result:=[]SubmissionView{}
	for rows.Next(){v,err:=scanSubmission(rows);if err!=nil{return nil,err};result=append(result,*v)}
	return result,rows.Err()
}

func (s *Service) Submission(ctx context.Context,orgID,memberID,mailboxID int64,id string) (*SubmissionView,error) {
	mc,err:=s.CheckMailboxAccess(ctx,orgID,memberID,mailboxID);if err!=nil{return nil,err}
	v,err:=s.submission(ctx,orgID,mailboxID,id);if err!=nil{return nil,err}
	if memberID!=v.MemberID && !mc.CanDelete(){return nil,ErrForbidden}
	return v,nil
}

func (s *Service) SubmissionByRequest(ctx context.Context,orgID,memberID,mailboxID int64,requestID,digest string) (*SubmissionView,error) {
	if id,err:=uuid.Parse(requestID);err!=nil || id.String()!=requestID{return nil,provider.Errorf("invalid","requestId 必须为 UUID")}
	if _,err:=s.CheckMailboxAccess(ctx,orgID,memberID,mailboxID);err!=nil{return nil,err}
	var id,prior string
	err:=s.DB.QueryRowContext(ctx,`SELECT id,request_digest FROM submissions WHERE org_id=? AND mailbox_id=? AND member_id=? AND request_id=?`,orgID,mailboxID,memberID,requestID).Scan(&id,&prior)
	if db.IsNotFound(err){return nil,nil};if err!=nil{return nil,err}
	if subtle.ConstantTimeCompare([]byte(prior),[]byte(digest))!=1{return nil,provider.Errorf("idempotency_conflict","requestId 已用于其他内容")}
	return s.submission(ctx,orgID,mailboxID,id)
}

func (s *Service) ReserveSubmission(ctx context.Context,orgID,memberID,mailboxID int64,in ReserveSubmissionInput) (*SubmissionView,bool,error) {
	if in.SMTP==nil || in.SMTP.OrgID!=orgID || in.SMTP.MailboxID!=mailboxID || in.StagingFolder=="" || in.RequestDigest=="" || len(in.Raw)==0{return nil,false,provider.Errorf("invalid","发送配置不完整")}
	if in.SentCopyMode!="append" && in.SentCopyMode!="server"{return nil,false,provider.Errorf("invalid","Sent 保存模式无效")}
	if in.SentCopyMode=="append" && (in.SentFolder==nil || *in.SentFolder==""){return nil,false,provider.Errorf("folder_mapping_required","请选择 Sent 文件夹")}
	if id,err:=uuid.Parse(in.ID);err!=nil || id.String()!=in.ID{return nil,false,provider.Errorf("invalid","submissionId 必须为 UUID")}
	message,err:=mail.ReadMessage(bytes.NewReader(in.Raw));if err!=nil{return nil,false,err}
	if message.Header.Get("X-Mailhearth-Submission-ID")!=in.ID || message.Header.Get("Message-ID")!=in.MessageID{return nil,false,provider.Errorf("submission_content_changed","邮件提交标识不一致")}
	prior,err:=s.SubmissionByRequest(ctx,orgID,memberID,mailboxID,in.RequestID,in.RequestDigest);if err!=nil{return nil,false,err};if prior!=nil{return prior,true,nil}
	mc,err:=s.CheckMailboxAccess(ctx,orgID,memberID,mailboxID);if err!=nil{return nil,false,err};if !mc.CanSend(){return nil,false,ErrForbidden}
	body,err:=json.Marshal(in.Envelope);if err!=nil{return nil,false,err};sealed,err:=s.Box.Seal(string(body));if err!=nil{return nil,false,err}
	id:=in.ID;repeated:=false
	err=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		var priorID,priorDigest string
		err:=tx.QueryRowContext(ctx,`SELECT id,request_digest FROM submissions WHERE org_id=? AND mailbox_id=? AND member_id=? AND request_id=?`,orgID,mailboxID,memberID,in.RequestID).Scan(&priorID,&priorDigest)
		if err==nil{if subtle.ConstantTimeCompare([]byte(priorDigest),[]byte(in.RequestDigest))!=1{return provider.Errorf("idempotency_conflict","requestId 已用于其他内容")};id=priorID;repeated=true;return nil};if !db.IsNotFound(err){return err}
		var valid bool
		if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM mailboxes b JOIN mail_connections c ON c.id=b.connection_id AND c.org_id=b.org_id JOIN mailbox_endpoints e ON e.mailbox_id=b.id AND e.protocol='smtp' JOIN identities i ON i.mailbox_id=b.id JOIN members m ON m.org_id=b.org_id AND m.id=? JOIN sessions ss ON ss.member_id=m.id WHERE b.id=? AND b.org_id=? AND b.status='active' AND b.access_revision=? AND c.enabled=1 AND c.revision=? AND e.revision=? AND i.id=? AND i.revision=? AND i.address=? AND i.authorization_status='allowed' AND m.status='active' AND ss.token_hash=? AND ss.expires_at>? AND (b.owner_member_id=m.id OR EXISTS(SELECT 1 FROM mailbox_access a WHERE a.mailbox_id=b.id AND a.member_id=m.id AND a.level IN ('send','full'))))`,memberID,mailboxID,orgID,in.AccessRevision,in.SMTP.ConnectionRevision,in.SMTP.EndpointRevision,in.Envelope.IdentityID,in.IdentityRevision,in.Envelope.EnvelopeFrom,in.SessionHash,db.Now()).Scan(&valid);err!=nil{return err}
		if !valid{return provider.Errorf("revision_conflict","发送权限或配置已变化")}
		now:=db.Now()
		_,err=tx.ExecContext(ctx,`INSERT INTO submissions(id,org_id,mailbox_id,member_id,execution_member_id,request_id,request_digest,message_id,message_sha256,envelope_enc,status,smtp_status,sent_status,smtp_endpoint_revision,connection_revision,access_revision,identity_revision,session_hash,sent_copy_mode,staging_folder,sent_folder,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,'preparing','not_started','not_started',?,?,?,?,?,?,?,?,?,?)`,id,orgID,mailboxID,memberID,memberID,in.RequestID,in.RequestDigest,in.MessageID,MessageSHA256(in.Raw),sealed,in.SMTP.EndpointRevision,in.SMTP.ConnectionRevision,in.AccessRevision,in.IdentityRevision,in.SessionHash,in.SentCopyMode,in.StagingFolder,in.SentFolder,now,now)
		return err
	});if err!=nil{return nil,false,err}
	v,err:=s.submission(ctx,orgID,mailboxID,id);return v,repeated,err
}

func (s *Service) CompleteSubmissionStaging(ctx context.Context,id string,locator *mailops.MessageLocator,stageErr error) error {
	status:="queued";var code,body any
	if stageErr!=nil || locator==nil{status="unknown";code="staging_unknown"}else{encoded,err:=json.Marshal(locator);if err!=nil{return err};body=string(encoded)}
	res,err:=s.DB.ExecContext(ctx,`UPDATE submissions SET status=?,error_code=?,draft_locator_json=?,updated_at=? WHERE id=? AND status='preparing'`,status,code,body,db.Now(),id);if err!=nil{return err}
	n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","发送状态已经变化")}
	if status=="queued"{select{case s.submissionWake<-struct{}{}:default:}}
	return nil
}

type submissionWork struct {
	ID string
	OrgID,MailboxID,MemberID,ActorID int64
	SessionHash,Status,SMTPStatus,SentStatus,EnvelopeEnc,MessageSHA,StagingFolder,SentCopyMode string
	ConnectionRevision,AccessRevision,IdentityRevision,SMTPRevision int64
	Draft,SentFolder sql.NullString
}

func (s *Service) submissionWork(ctx context.Context,id string) (*submissionWork,error) {
	var w submissionWork
	err:=s.DB.QueryRowContext(ctx,`SELECT id,org_id,mailbox_id,member_id,execution_member_id,session_hash,status,smtp_status,sent_status,envelope_enc,message_sha256,staging_folder,sent_copy_mode,connection_revision,access_revision,identity_revision,smtp_endpoint_revision,draft_locator_json,sent_folder FROM submissions WHERE id=?`,id).Scan(&w.ID,&w.OrgID,&w.MailboxID,&w.MemberID,&w.ActorID,&w.SessionHash,&w.Status,&w.SMTPStatus,&w.SentStatus,&w.EnvelopeEnc,&w.MessageSHA,&w.StagingFolder,&w.SentCopyMode,&w.ConnectionRevision,&w.AccessRevision,&w.IdentityRevision,&w.SMTPRevision,&w.Draft,&w.SentFolder)
	return &w,err
}

func (s *Service) StartSubmissions(ctx context.Context) error {
	if !s.submissionsStarted.CompareAndSwap(false,true){return provider.Errorf("operation_in_progress","发送执行器已经启动")}
	rows,err:=s.DB.QueryContext(ctx,`SELECT id FROM submissions WHERE status='preparing'`);if err!=nil{return err}
	var preparing []string;for rows.Next(){var id string;if err:=rows.Scan(&id);err!=nil{rows.Close();return err};preparing=append(preparing,id)};err=rows.Err();rows.Close();if err!=nil{return err}
	if err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		if _,err:=tx.ExecContext(ctx,`UPDATE submissions SET status='unknown',smtp_status='unknown',error_code='process_interrupted',updated_at=? WHERE smtp_status='submitting'`,db.Now());err!=nil{return err}
		if _,err:=tx.ExecContext(ctx,`UPDATE submissions SET status='sent_copy_failed',sent_status=CASE WHEN sent_status='saving' THEN 'unknown' ELSE sent_status END,error_code='process_interrupted',updated_at=? WHERE smtp_status='accepted' AND status='running'`,db.Now());err!=nil{return err}
		_,err:=tx.ExecContext(ctx,`UPDATE submissions SET status='unknown',error_code='process_interrupted',updated_at=? WHERE status IN ('preparing','running')`,db.Now());return err
	});err!=nil{return err}
	s.submissionWG.Add(1)
	go func(){
		defer s.submissionWG.Done()
		for _,id:=range preparing{if ctx.Err()!=nil{return};if err:=s.reconcileStaging(ctx,id);err!=nil{s.workerFailure("草稿核查进度保存失败");return}}
		ticker:=time.NewTicker(time.Second);defer ticker.Stop()
		for{
			if ctx.Err()!=nil{return}
			rows,err:=s.DB.QueryContext(ctx,`SELECT id,mailbox_id FROM submissions WHERE status='queued' ORDER BY created_at,id`);if err!=nil{if ctx.Err()==nil{s.workerFailure("发送执行器读取失败")};return}
			type pending struct{id string;mailbox int64};var pendingWork []pending
			for rows.Next(){var p pending;if err=rows.Scan(&p.id,&p.mailbox);err!=nil{break};pendingWork=append(pendingWork,p)}
			if err==nil{err=rows.Err()};rows.Close();if err!=nil{s.workerFailure("发送执行器读取失败");return}
			for _,p:=range pendingWork{
				s.submissionMu.Lock();if len(s.submissionActive)>=4 || s.submissionActive[p.mailbox]{s.submissionMu.Unlock();continue};s.submissionActive[p.mailbox]=true;s.submissionMu.Unlock()
				s.submissionWG.Add(1)
				go func(p pending){defer s.submissionWG.Done();defer func(){s.submissionMu.Lock();delete(s.submissionActive,p.mailbox);s.submissionMu.Unlock();select{case s.submissionWake<-struct{}{}:default:}}();if err:=s.executeSubmission(ctx,p.id);err!=nil && ctx.Err()==nil{s.workerFailure("发送进度保存失败")}}(p)
			}
			select{case <-ctx.Done():return;case <-ticker.C:case <-s.submissionWake:}
		}
	}()
	return nil
}

func (s *Service) workerFailure(message string) {select{case s.operationErrors<-errors.New(message):default:}}
func (s *Service) WaitSubmissions() {s.submissionWG.Wait()}

func (s *Service) persistSubmissionResult(id,status,smtpStatus,sentStatus string,code any,locator *mailops.MessageLocator) error {
	ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second);defer cancel()
	var body any;if locator!=nil{encoded,err:=json.Marshal(locator);if err!=nil{return err};body=string(encoded)}
	_,err:=s.DB.ExecContext(ctx,`UPDATE submissions SET status=?,smtp_status=?,sent_status=?,error_code=?,sent_locator_json=COALESCE(?,sent_locator_json),updated_at=? WHERE id=?`,status,smtpStatus,sentStatus,code,body,db.Now(),id)
	return err
}

func (s *Service) executeSubmission(parent context.Context,id string) error {
	res,err:=s.DB.ExecContext(parent,`UPDATE submissions SET status='running',updated_at=? WHERE id=? AND status='queued'`,db.Now(),id);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil || n!=1{return err}
	w,err:=s.submissionWork(parent,id);if err!=nil{return err}
	ctx,cancel:=context.WithTimeout(parent,3*time.Minute);defer cancel()
	ctx,release:=s.RegisterMailRequestSessionHash(ctx,w.ActorID,w.MailboxID,w.SessionHash);defer release()
	fail:=func(runErr error)error{status,smtpStatus,sentStatus:="failed",w.SMTPStatus,w.SentStatus;if smtpStatus=="accepted"{status="sent_copy_failed";if sentStatus=="saving" || sentStatus=="unknown"{sentStatus="unknown"}else{sentStatus="failed"}};return s.persistSubmissionResult(id,status,smtpStatus,sentStatus,operationErrorCode(runErr),nil)}
	active,err:=s.SessionHashActive(ctx,w.ActorID,w.SessionHash);if err!=nil{return fail(err)};if !active{return fail(ErrForbidden)}
	mc,err:=s.ResolveMailbox(ctx,w.OrgID,w.ActorID,w.MailboxID);if err!=nil{return fail(err)}
	if !mc.CanSend() || (w.ActorID!=w.MemberID && !mc.CanDelete()){return fail(ErrForbidden)}
	if mc.Mailbox.AccessRevision!=w.AccessRevision{return fail(provider.Errorf("revision_conflict","邮箱访问权限已变化"))}
	plain,err:=s.Box.Open(w.EnvelopeEnc);if err!=nil{return fail(provider.Errorf("credential_decryption_failed","发送记录解密失败"))}
	var envelope SubmissionEnvelope;if err:=json.Unmarshal([]byte(plain),&envelope);err!=nil{return fail(err)}
	var draft mailops.MessageLocator;if !w.Draft.Valid{return fail(provider.Errorf("staging_missing","待发送草稿不存在"))};if err:=json.Unmarshal([]byte(w.Draft.String),&draft);err!=nil{return fail(err)}
	conn,err:=s.Pool.Get(ctx,mc.Cred);if err!=nil{return fail(err)};defer s.Pool.Put(conn)
	raw,err:=mailops.ReadSubmissionMessage(ctx,conn,draft,id,s.Cfg.MaxMessageBytes);if err!=nil{return fail(err)}
	if MessageSHA256(raw)!=w.MessageSHA{return fail(provider.Errorf("submission_content_changed","待发送邮件内容摘要不一致"))}
	if w.SMTPStatus!="accepted"{
		if w.SMTPStatus!="not_started"{return fail(provider.Errorf("submission_unknown","SMTP 提交结果需要核查"))}
		endpoint,err:=s.ResolveEndpoint(ctx,w.OrgID,w.MailboxID,provider.ProtocolSMTP);if err!=nil{return fail(err)}
		if endpoint.ConnectionRevision!=w.ConnectionRevision || endpoint.EndpointRevision!=w.SMTPRevision{return fail(provider.Errorf("revision_conflict","SMTP 配置已变化"))}
		var valid bool
		if err:=s.DB.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM identities WHERE id=? AND mailbox_id=? AND revision=? AND address=? AND authorization_status='allowed')`,envelope.IdentityID,w.MailboxID,w.IdentityRevision,envelope.EnvelopeFrom).Scan(&valid);err!=nil{return fail(err)};if !valid{return fail(ErrForbidden)}
		if ctx.Err()!=nil{return fail(ctx.Err())}
		if err:=s.markSMTPSubmitting(ctx,w,envelope);err!=nil{return fail(err)}
		cfg:=mailops.SMTPConfig{Addr:endpoint.Address(),TLSMode:endpoint.Network.TLSMode,TLSConfig:endpoint.TLSConfig,Dialer:endpoint.Dialer}
		smtpStatus,sendErr:=mailops.SubmitSMTP(ctx,cfg,endpoint.Username,endpoint.Secret,envelope.EnvelopeFrom,envelope.Recipients,raw)
		w.SMTPStatus=smtpStatus
		if sendErr!=nil{status:="failed";if smtpStatus=="unknown"{status="unknown"};return s.persistSubmissionResult(id,status,smtpStatus,"not_started",operationErrorCode(sendErr),nil)}
		if err:=s.persistSubmissionResult(id,"running","accepted","not_started",nil,nil);err!=nil{return err}
	}
	if w.SentCopyMode=="server"{if err:=s.persistSubmissionResult(id,"sent","accepted","server_managed",nil,nil);err!=nil{return err};return s.cleanupSubmission(ctx,w,conn,draft,envelope)}
	if !w.SentFolder.Valid || w.SentFolder.String==""{return fail(provider.Errorf("folder_mapping_required","Sent 文件夹未配置"))}
	// 重试副本保存前查询唯一提交标识，保留已确认的 SMTP 接受状态。
	locator,err:=mailops.FindSubmissionMessage(ctx,conn,w.SentFolder.String,id,s.Cfg.MaxMessageBytes);if err!=nil{return fail(err)}
	if locator!=nil{stored,err:=mailops.ReadSubmissionMessage(ctx,conn,*locator,id,s.Cfg.MaxMessageBytes);if err!=nil{return fail(err)};if MessageSHA256(stored)!=w.MessageSHA{return fail(provider.Errorf("submission_content_changed","Sent 副本内容摘要不一致"))}}
	if locator==nil{
		if err:=s.persistSubmissionResult(id,"running","accepted","saving",nil,nil);err!=nil{return err};w.SentStatus="saving"
		locator,err=mailops.AppendSubmissionMessage(ctx,conn,w.SentFolder.String,id,[]string{`\Seen`},raw,s.Cfg.MaxMessageBytes)
		if err!=nil{return fail(err)}
	}
	if err:=s.persistSubmissionResult(id,"sent","accepted","saved",nil,locator);err!=nil{return err}
	return s.cleanupSubmission(ctx,w,conn,draft,envelope)
}

func (s *Service) markSMTPSubmitting(ctx context.Context,w *submissionWork,envelope SubmissionEnvelope) error {
	return s.DB.Tx(ctx,func(tx *sql.Tx)error{
		var valid bool
		if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM mailboxes b JOIN mail_connections c ON c.id=b.connection_id AND c.org_id=b.org_id JOIN mailbox_endpoints e ON e.mailbox_id=b.id AND e.protocol='smtp' JOIN credentials cr ON cr.id=e.credential_id AND cr.mailbox_id=b.id AND cr.connection_id=c.id AND cr.purpose='mail' AND cr.state='active' JOIN identities i ON i.mailbox_id=b.id JOIN members m ON m.org_id=b.org_id AND m.id=? JOIN sessions ss ON ss.member_id=m.id WHERE b.id=? AND b.org_id=? AND b.status='active' AND b.access_revision=? AND c.enabled=1 AND c.revision=? AND e.revision=? AND e.network_mode!='disabled' AND i.id=? AND i.revision=? AND i.address=? AND i.authorization_status='allowed' AND m.status='active' AND ss.token_hash=? AND ss.expires_at>? AND (b.owner_member_id=m.id OR EXISTS(SELECT 1 FROM mailbox_access a WHERE a.mailbox_id=b.id AND a.member_id=m.id AND a.level IN ('send','full'))))`,w.ActorID,w.MailboxID,w.OrgID,w.AccessRevision,w.ConnectionRevision,w.SMTPRevision,envelope.IdentityID,w.IdentityRevision,envelope.EnvelopeFrom,w.SessionHash,db.Now()).Scan(&valid);err!=nil{return err}
		if !valid{return provider.Errorf("revision_conflict","发送权限或配置已变化")}
		res,err:=tx.ExecContext(ctx,`UPDATE submissions SET smtp_status='submitting',updated_at=? WHERE id=? AND status='running' AND smtp_status='not_started'`,db.Now(),w.ID);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("submission_not_retryable","SMTP 提交状态已变化")};return nil
	})
}

func (s *Service) cleanupSubmission(ctx context.Context,w *submissionWork,conn *imappool.Conn,draft mailops.MessageLocator,envelope SubmissionEnvelope) error {
	var cleanupErr error
	if _,err:=mailops.ReadSubmissionMessage(ctx,conn,draft,w.ID,s.Cfg.MaxMessageBytes);err!=nil{cleanupErr=err}else{cleanupErr=mailops.DeleteLocatedMessage(ctx,conn,draft)}
	if cleanupErr==nil && envelope.OriginalDraft!=nil{
		original:=envelope.OriginalDraft;conn.Invalidate();selected,err:=conn.Select(ctx,original.Folder,false)
		if err!=nil{cleanupErr=err}else if selected.UIDValidity!=original.UIDValidity{cleanupErr=provider.Errorf("draft_locator_changed","原草稿定位信息已变化")}else{cleanupErr=mailops.DeleteLocatedMessage(ctx,conn,*original)}
	}
	if cleanupErr==nil && envelope.ReplyContext!=nil{
		reply:=envelope.ReplyContext;conn.Invalidate();selected,err:=conn.Select(ctx,reply.Folder,false)
		if err!=nil{cleanupErr=err}else if selected.UIDValidity!=reply.UIDValidity{cleanupErr=provider.Errorf("draft_locator_changed","原邮件定位信息已变化")}else{
			flag,action:=`\Answered`,"replied";if reply.Forward{flag,action=`$Forwarded`,"forwarded"}
			if err:=mailops.StoreFlags(ctx,conn,reply.Folder,[]uint32{reply.UID},[]string{flag},nil);err!=nil{cleanupErr=err}else{cleanupErr=s.RecordActivity(ctx,w.MailboxID,w.ActorID,MessageKey(reply.MessageID,reply.Folder,reply.UIDValidity,reply.UID),action,"submission:"+w.ID)}
		}
	}
	persistCtx,cancel:=context.WithTimeout(context.Background(),10*time.Second);defer cancel()
	var code any;if cleanupErr!=nil{code=operationErrorCode(cleanupErr)}
	_,err:=s.DB.ExecContext(persistCtx,`UPDATE submissions SET cleanup_error_code=?,updated_at=? WHERE id=?`,code,db.Now(),w.ID);return err
}

func (s *Service) reconcileStaging(parent context.Context,id string) error {
	w,err:=s.submissionWork(parent,id);if err!=nil{return err}
	ctx,cancel:=context.WithTimeout(parent,90*time.Second);defer cancel();ctx,release:=s.RegisterMailRequestSessionHash(ctx,w.ActorID,w.MailboxID,w.SessionHash);defer release()
	fail:=func(runErr error)error{return s.persistSubmissionResult(id,"unknown","not_started","not_started",operationErrorCode(runErr),nil)}
	active,err:=s.SessionHashActive(ctx,w.ActorID,w.SessionHash);if err!=nil{return fail(err)};if !active{return fail(ErrForbidden)}
	mc,err:=s.ResolveMailbox(ctx,w.OrgID,w.ActorID,w.MailboxID);if err!=nil{return fail(err)};if !mc.CanSend(){return fail(ErrForbidden)}
	conn,err:=s.Pool.Get(ctx,mc.Cred);if err!=nil{return fail(err)};defer s.Pool.Put(conn)
	locator,err:=mailops.FindSubmissionMessage(ctx,conn,w.StagingFolder,id,s.Cfg.MaxMessageBytes);if err!=nil{return fail(err)};if locator==nil{return fail(provider.Errorf("staging_missing","待发送草稿不存在"))}
	raw,err:=mailops.ReadSubmissionMessage(ctx,conn,*locator,id,s.Cfg.MaxMessageBytes);if err!=nil{return fail(err)};if MessageSHA256(raw)!=w.MessageSHA{return fail(provider.Errorf("submission_content_changed","草稿内容摘要不一致"))}
	body,err:=json.Marshal(locator);if err!=nil{return err}
	_,err=s.DB.ExecContext(ctx,`UPDATE submissions SET status='queued',draft_locator_json=?,error_code=NULL,updated_at=? WHERE id=? AND smtp_status='not_started' AND status='unknown'`,string(body),db.Now(),id);return err
}

func (s *Service) RetrySubmissionSentCopy(ctx context.Context,orgID,actor,mailboxID int64,id,sessionHash string) (*SubmissionView,error) {
	v,err:=s.Submission(ctx,orgID,actor,mailboxID,id);if err!=nil{return nil,err}
	mc,err:=s.CheckMailboxAccess(ctx,orgID,actor,mailboxID);if err!=nil{return nil,err};if !mc.CanSend(){return nil,ErrForbidden}
	active,err:=s.SessionHashActive(ctx,actor,sessionHash);if err!=nil{return nil,err};if !active{return nil,ErrForbidden}
	if v.SMTPStatus!="accepted" || v.Status!="sent_copy_failed"{return nil,provider.Errorf("submission_not_retryable","仅允许重试已发送邮件的副本保存")}
	res,err:=s.DB.ExecContext(ctx,`UPDATE submissions SET status='queued',execution_member_id=?,session_hash=?,access_revision=?,error_code=NULL,updated_at=? WHERE id=? AND status='sent_copy_failed' AND smtp_status='accepted'`,actor,sessionHash,mc.Mailbox.AccessRevision,db.Now(),id);if err!=nil{return nil,err};n,err:=res.RowsAffected();if err!=nil{return nil,err};if n!=1{return nil,provider.Errorf("revision_conflict","发送状态已经变化")}
	if actor!=v.MemberID{s.audit(ctx,orgID,actor,"submission.takeover","submission",id,struct{MemberID int64 `json:"originalMemberId"`}{v.MemberID})}
	select{case s.submissionWake<-struct{}{}:default:}
	return s.Submission(ctx,orgID,actor,mailboxID,id)
}
