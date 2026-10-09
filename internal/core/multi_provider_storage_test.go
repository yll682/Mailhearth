package core

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"mailhearth/internal/config"
	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
	"mailhearth/internal/secrets"
)

type storageMember struct {
	*model.Member
	OrgID int64
}

func newStorageService(t *testing.T) (*Service,*storageMember) {
	t.Helper()
	dir:=filepath.Join("..","..","data","integration","multi-provider","core-"+t.Name()+"-"+time.Now().UTC().Format("20060102T150405.000000000"))
	if err:=os.MkdirAll(dir,0700);err!=nil{t.Fatal(err)}
	database,err:=db.Open(filepath.Join(dir,"mailhearth.db"));if err!=nil{t.Fatal(err)}
	t.Cleanup(func(){if err:=database.Close();err!=nil{t.Error(err)}})
	master:=make([]byte,32);if _,err:=rand.Read(master);err!=nil{t.Fatal(err)}
	box,err:=secrets.NewBox(master,"credentials");if err!=nil{t.Fatal(err)}
	svc:=New(database,&config.Config{},box,nil,slog.Default())
	member,err:=svc.Init(context.Background(),InitRequest{OrgName:t.Name(),AdminName:"管理员",AdminEmail:"admin@example.org",Password:uuid.NewString()+"Aa1!"});if err!=nil{t.Fatal(err)}
	org,err:=svc.Org(context.Background());if err!=nil{t.Fatal(err)}
	return svc,&storageMember{Member:member,OrgID:org.ID}
}

func storageConnection(t *testing.T,s *Service,m *storageMember,label string) *ConnectionView {
	t.Helper()
	c,err:=s.CreateConnection(context.Background(),m.OrgID,m.ID,CreateConnectionInput{ProviderKind:provider.Manual,Label:label});if err!=nil{t.Fatal(err)}
	return c
}

func requireProviderCode(t *testing.T,err error,code string) {
	t.Helper();var typed *provider.TypedError
	if !errors.As(err,&typed) || typed.Code!=code{t.Fatalf("错误码应为 %s，取得 %v",code,err)}
}

func TestMultiProviderStorageConnectionIsolation(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background()
	one:=storageConnection(t,s,m,"邮件连接一");two:=storageConnection(t,s,m,"邮件连接二")
	label:="更新后的邮件连接"
	updated,err:=s.UpdateConnection(ctx,m.OrgID,m.ID,one.ID,UpdateConnectionInput{ExpectedRevision:one.Revision,Label:&label});if err!=nil{t.Fatal(err)}
	if updated.Revision!=one.Revision+1{t.Fatal("配置版本未增加")}
	other,err:=s.MailConnection(ctx,m.OrgID,two.ID);if err!=nil{t.Fatal(err)}
	if other.Label!=two.Label || other.Revision!=two.Revision{t.Fatal("其他连接被修改")}
	_,err=s.UpdateConnection(ctx,m.OrgID,m.ID,one.ID,UpdateConnectionInput{ExpectedRevision:one.Revision,Label:&label});requireProviderCode(t,err,"revision_conflict")
	scope:=provider.DomainScope{Mode:"all"}
	configured,err:=s.ConfigureConnection(ctx,m.OrgID,m.ID,one.ID,UpdateConnectionInput{ExpectedRevision:updated.Revision,DomainScope:&scope});if err!=nil{t.Fatal(err)}
	if configured.Revision!=updated.Revision+1{t.Fatal("候选配置提交未更新版本")}
	_,err=s.ConfigureConnection(ctx,m.OrgID,m.ID,one.ID,UpdateConnectionInput{ExpectedRevision:configured.Revision,APIAuth:&provider.APIAuth{APIKey:uuid.NewString()}});requireProviderCode(t,err,"invalid")
	unchanged,err:=s.MailConnection(ctx,m.OrgID,one.ID);if err!=nil{t.Fatal(err)};if unchanged.Revision!=configured.Revision || unchanged.APIConfigured{t.Fatal("失败候选修改了已提交配置")}
	if _,err:=s.MailConnection(ctx,m.OrgID+1,one.ID);!errors.Is(err,ErrNotFound){t.Fatalf("跨组织连接查询应当拒绝，取得 %v",err)}
	if err:=s.DeleteConnection(ctx,m.OrgID,m.ID,two.ID,two.Revision,two.Label);err!=nil{t.Fatal(err)}
	if _,err:=s.MailConnection(ctx,m.OrgID,two.ID);!errors.Is(err,ErrNotFound){t.Fatal("空连接未移除")}
}

func TestMultiProviderStorageOperationIdempotency(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();c:=storageConnection(t,s,m,"手动连接")
	requestID:=uuid.NewString();payload:=OperationPayload{Kind:"connection.discover",ConnectionID:c.ID};keys:=[]string{"connection:"+fmtID(c.ID)}
	one,repeated,err:=s.QueueOperation(ctx,m.OrgID,m.ID,requestID,payload,keys);if err!=nil{t.Fatal(err)};if repeated{t.Fatal("新请求被识别为重复请求")}
	two,repeated,err:=s.QueueOperation(ctx,m.OrgID,m.ID,requestID,payload,keys);if err!=nil{t.Fatal(err)};if !repeated || two.ID!=one.ID{t.Fatal("重复请求创建了新操作")}
	payload.ConnectionID++
	_,_,err=s.QueueOperation(ctx,m.OrgID,m.ID,requestID,payload,keys);requireProviderCode(t,err,"idempotency_conflict")
	payload.ConnectionID--
	_,_,err=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),payload,keys);requireProviderCode(t,err,"operation_in_progress")
	var conflict *provider.TypedError;if !errors.As(err,&conflict) || conflict.OperationID==nil || *conflict.OperationID!=one.ID{t.Fatal("资源占用错误未返回原操作标识")}
	list,err:=s.OperationsForMember(ctx,m.OrgID,m.ID);if err!=nil{t.Fatal(err)};if len(list)!=1 || list[0].ID!=one.ID{t.Fatal("操作列表未返回原操作")}
	if _,err:=s.Operation(ctx,m.OrgID+1,one.ID);!errors.Is(err,ErrNotFound){t.Fatal("跨组织操作查询没有被拒绝")}
	var sealed,digest string
	if err:=s.DB.QueryRowContext(ctx,`SELECT payload_enc,request_digest FROM operations WHERE id=?`,one.ID).Scan(&sealed,&digest);err!=nil{t.Fatal(err)}
	if strings.Contains(sealed,"connection.discover") || digest==""{t.Fatal("操作载荷缺少加密或摘要")}
	plain,err:=s.Box.Open(sealed);if err!=nil{t.Fatal(err)};var decoded OperationPayload;if err:=json.Unmarshal([]byte(plain),&decoded);err!=nil{t.Fatal(err)}
	if decoded.Kind!=payload.Kind || decoded.ConnectionID!=c.ID{t.Fatal("加密载荷与请求不一致")}
	workerCtx,cancel:=context.WithCancel(ctx)
	if err:=s.StartOperations(workerCtx);err!=nil{t.Fatal(err)}
	defer func(){cancel();s.WaitOperations()}()
	deadline:=time.Now().Add(5*time.Second)
	for time.Now().Before(deadline){
		operation,err:=s.Operation(ctx,m.OrgID,one.ID);if err!=nil{t.Fatal(err)}
		if operation.Status=="failed"{if operation.ErrorCode==nil || *operation.ErrorCode!="external_action_required"{t.Fatalf("手动连接发现错误码不正确：%v",operation.ErrorCode)};var count int;if err:=s.DB.QueryRowContext(ctx,`SELECT COUNT(*) FROM operation_locks WHERE operation_id=?`,one.ID).Scan(&count);err!=nil{t.Fatal(err)};if count!=0{t.Fatal("已确认失败操作仍占用资源")};return}
		time.Sleep(10*time.Millisecond)
	}
	t.Fatal("操作执行器未完成请求")
}

func TestMultiProviderStorageMailboxIsolationAndSuspend(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();one:=storageConnection(t,s,m,"连接一");two:=storageConnection(t,s,m,"连接二")
	now:=db.Now();var ids []int64
	for _,c:=range []*ConnectionView{one,two}{
		res,err:=s.DB.ExecContext(ctx,`INSERT INTO mailboxes(org_id,connection_id,kind,address,address_key,owner_member_id,management_mode,remote_state,created_at,updated_at) VALUES (?,?,'personal','same@example.org','same@example.org',?,'external','external',?,?)`,m.OrgID,c.ID,m.ID,now,now);if err!=nil{t.Fatal(err)}
		id,err:=res.LastInsertId();if err!=nil{t.Fatal(err)};ids=append(ids,id)
		sealed,err:=s.Box.Seal(" password with surrounding space \t");if err!=nil{t.Fatal(err)}
		if _,err:=s.DB.ExecContext(ctx,`INSERT INTO credentials(org_id,connection_id,mailbox_id,purpose,source,secret_enc,state,created_at,updated_at) VALUES (?,?,?,'mail','entered',?,'active',?,?)`,m.OrgID,c.ID,id,sealed,now,now);err!=nil{t.Fatal(err)}
		for _,protocol:=range []string{"imap","smtp","managesieve"}{if _,err:=s.DB.ExecContext(ctx,`INSERT INTO mailbox_endpoints(mailbox_id,protocol,network_mode,created_at,updated_at) VALUES (?,?,'disabled',?,?)`,id,protocol,now,now);err!=nil{t.Fatal(err)}}
	}
	list,err:=s.Mailboxes(ctx,m.OrgID);if err!=nil{t.Fatal(err)};if len(list)!=2 || list[0].ConnectionID==list[1].ConnectionID{t.Fatal("相同地址的跨连接邮箱未分别保存")}
	request,cancel:=s.RegisterMailRequest(ctx,m.ID,ids[0],"");defer cancel()
	otherRequest,otherCancel:=s.RegisterMailRequest(ctx,m.ID,ids[1],"");defer otherCancel()
	if err:=s.SuspendMailbox(ctx,m.OrgID,m.ID,ids[0]);err!=nil{t.Fatal(err)}
	if request.Err()==nil || otherRequest.Err()!=nil{t.Fatal("暂停邮箱未准确终止对应请求")}
	first,err:=s.Mailbox(ctx,m.OrgID,ids[0]);if err!=nil{t.Fatal(err)};second,err:=s.Mailbox(ctx,m.OrgID,ids[1]);if err!=nil{t.Fatal(err)}
	if first.Status!="suspended" || first.Revision!=2 || first.AccessRevision!=2 || second.Status!="active" || second.Revision!=1{t.Fatal("暂停状态或配置版本不正确")}
	var count int;if err:=s.DB.QueryRowContext(ctx,`SELECT COUNT(*) FROM credentials WHERE state='active' AND secret_enc!=''`).Scan(&count);err!=nil{t.Fatal(err)};if count!=2{t.Fatal("暂停邮箱修改了有效凭据")}
	view,err:=s.MailboxEndpoints(ctx,m.OrgID,ids[0]);if err!=nil{t.Fatal(err)};if len(view.Endpoints)!=3{t.Fatal("协议查询没有返回三项配置")}
	body,err:=json.Marshal(view);if err!=nil{t.Fatal(err)};if strings.Contains(string(body),"secret"){t.Fatal("协议查询包含秘密字段")}
	disabled:=EndpointInputs{IMAP:&EndpointInput{NetworkMode:"disabled"},SMTP:&EndpointInput{NetworkMode:"disabled"},ManageSieve:&EndpointInput{NetworkMode:"disabled"}}
	input:=UpdateEndpointsInput{ExpectedRevision:second.Revision,Endpoints:disabled}
	op,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"mailbox.endpoints",MailboxID:second.ID,Endpoints:&input},[]string{"mailbox:"+fmtID(second.ID)});if err!=nil{t.Fatal(err)}
	workerCtx,stopWorker:=context.WithCancel(ctx);if err:=s.StartOperations(workerCtx);err!=nil{t.Fatal(err)}
	defer func(){stopWorker();s.WaitOperations()}()
	deadline:=time.Now().Add(5*time.Second);finished:=false
	for time.Now().Before(deadline){current,err:=s.Operation(ctx,m.OrgID,op.ID);if err!=nil{t.Fatal(err)};if current.Status=="succeeded"{finished=true;break};if current.Status=="failed"{t.Fatalf("协议更新失败：%v",current.ErrorCode)};time.Sleep(10*time.Millisecond)}
	if !finished{t.Fatal("协议操作未完成")}
	changed,err:=s.MailboxEndpoints(ctx,m.OrgID,second.ID);if err!=nil{t.Fatal(err)}
	if changed.Revision!=second.Revision+1{t.Fatal("协议更新未增加邮箱配置版本")}
	for _,endpoint:=range changed.Endpoints{if endpoint.Revision!=2 || endpoint.NetworkMode!="disabled" || endpoint.Credential!=nil || endpoint.Username!=nil{t.Fatal("停用协议配置未完整提交")}}
	_,err=s.UpdateEndpoints(ctx,m.OrgID,m.ID,second.ID,input);requireProviderCode(t,err,"revision_conflict")
	if err:=s.DeleteConnection(ctx,m.OrgID,m.ID,one.ID,one.Revision,one.Label);err==nil{t.Fatal("移除了仍有关联邮箱的连接")}else{requireProviderCode(t,err,"connection_in_use")}
}
