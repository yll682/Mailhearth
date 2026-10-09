package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

func awaitStorageOperation(t *testing.T,s *Service,orgID int64,id string) *OperationView {
	t.Helper();deadline:=time.Now().Add(10*time.Second)
	for time.Now().Before(deadline){op,err:=s.Operation(context.Background(),orgID,id);if err!=nil{t.Fatal(err)};if op.Status!="queued" && op.Status!="running"{return op};time.Sleep(10*time.Millisecond)}
	t.Fatal("操作执行未在期限内完成");return nil
}

func startStorageOperations(t *testing.T,s *Service) {
	t.Helper();ctx,cancel:=context.WithCancel(context.Background());if err:=s.StartOperations(ctx);err!=nil{cancel();t.Fatal(err)}
	t.Cleanup(func(){cancel();s.WaitOperations();select{case err:=<-s.OperationErrors():t.Error(err);default:}})
}

func registerStorageDomain(t *testing.T,s *Service,m *storageMember,c *ConnectionView,name string) (*OperationView,*model.DomainBinding) {
	t.Helper();in:=DomainBindingInput{ConnectionID:c.ID,DomainName:name,Mode:"register"}
	op,_,err:=s.QueueOperation(context.Background(),m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"domain.register",Domain:&in},nil);if err!=nil{t.Fatal(err)}
	op=awaitStorageOperation(t,s,m.OrgID,op.ID);if op.Status!="succeeded"{t.Fatalf("域名登记失败：%s %v",op.Status,op.ErrorCode)}
	var result struct{ID int64 `json:"domainBindingId"`};if err:=json.Unmarshal(op.Result,&result);err!=nil{t.Fatal(err)}
	binding,err:=s.DomainBinding(context.Background(),m.OrgID,result.ID);if err!=nil{t.Fatal(err)};return op,binding
}

func TestMultiProviderStorageDomainBindingIsolation(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();one:=storageConnection(t,s,m,"连接一");two:=storageConnection(t,s,m,"连接二");startStorageOperations(t,s)
	_,first:=registerStorageDomain(t,s,m,one,"example.org");_,second:=registerStorageDomain(t,s,m,two,"example.org")
	if first.ID==second.ID || first.DomainID!=second.DomainID || first.ManagementMode!="external" || second.RemoteState!="external"{t.Fatal("同名域名没有建立独立的连接关联")}
	list,err:=s.DomainBindings(ctx,m.OrgID,one.ID,0);if err!=nil{t.Fatal(err)};if len(list)!=1 || list[0].ID!=first.ID{t.Fatal("连接筛选返回了其他连接的关联")}
	if _,err:=s.DomainBinding(ctx,m.OrgID+1,first.ID);err!=ErrNotFound{t.Fatal("跨组织域名关联查询没有拒绝")}
	if err:=s.DeleteDomainBinding(ctx,m.OrgID,m.ID,first.ID,first.Revision+1);err==nil{t.Fatal("错误版本仍可解除关联")}else{requireProviderCode(t,err,"revision_conflict")}
	if err:=s.DeleteDomainBinding(ctx,m.OrgID,m.ID,first.ID,first.Revision);err!=nil{t.Fatal(err)}
	if _,err:=s.DomainBinding(ctx,m.OrgID,second.ID);err!=nil{t.Fatal("解除一个关联影响了其他连接")}
}

func TestMultiProviderStorageCompletedRequestReuse(t *testing.T) {
	s,m:=newStorageService(t);c:=storageConnection(t,s,m,"请求幂等性");startStorageOperations(t,s)
	in:=DomainBindingInput{ConnectionID:c.ID,DomainName:"request.example.org",Mode:"register"};payload:=OperationPayload{Kind:"domain.register",Domain:&in};requestID:=uuid.NewString()
	op,_,err:=s.QueueOperation(context.Background(),m.OrgID,m.ID,requestID,payload,nil);if err!=nil{t.Fatal(err)};finished:=awaitStorageOperation(t,s,m.OrgID,op.ID);if finished.Status!="succeeded"{t.Fatal(finished.Status)}
	again,repeated,err:=s.QueueOperation(context.Background(),m.OrgID,m.ID,requestID,payload,nil);if err!=nil{t.Fatal(err)};if !repeated || again.ID!=op.ID{t.Fatal("成功请求重复提交创建了其他操作")}
	var sealed sql.NullString;if err:=s.DB.QueryRowContext(context.Background(),`SELECT payload_enc FROM operations WHERE id=?`,op.ID).Scan(&sealed);err!=nil{t.Fatal(err)};if sealed.Valid{t.Fatal("已完成操作保留了请求密文")}
	in.DomainName="different.example.org";_,_,err=s.QueueOperation(context.Background(),m.OrgID,m.ID,requestID,payload,nil);requireProviderCode(t,err,"idempotency_conflict")
}

func TestMultiProviderStorageClaimSecretOnce(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();c:=storageConnection(t,s,m,"秘密领取存储");startStorageOperations(t,s);op,_:=registerStorageDomain(t,s,m,c,"claim.example.org")
	secret:=uuid.NewString();sealed,err:=s.Box.Seal(secret);if err!=nil{t.Fatal(err)}
	if _,err:=s.DB.ExecContext(ctx,`UPDATE operations SET claim_secret_enc=?,claim_secret_expires_at=? WHERE id=?`,sealed,time.Now().Add(time.Hour).UTC().Format(time.RFC3339),op.ID);err!=nil{t.Fatal(err)}
	if _,err:=s.ClaimOperationSecret(ctx,m.OrgID,m.ID+1,op.ID);err!=ErrForbidden{t.Fatal("其他成员可以领取秘密")}
	claimed,err:=s.ClaimOperationSecret(ctx,m.OrgID,m.ID,op.ID);if err!=nil{t.Fatal(err)};if claimed!=secret{t.Fatal("领取内容与加密秘密不一致")}
	_,err=s.ClaimOperationSecret(ctx,m.OrgID,m.ID,op.ID);requireProviderCode(t,err,"secret_already_claimed")
	var remaining sql.NullString;if err:=s.DB.QueryRowContext(ctx,`SELECT claim_secret_enc FROM operations WHERE id=?`,op.ID).Scan(&remaining);err!=nil{t.Fatal(err)};if remaining.Valid{t.Fatal("领取后仍保留秘密密文")}
	expired,_:=registerStorageDomain(t,s,m,c,"expired.example.org")
	if _,err:=s.DB.ExecContext(ctx,`UPDATE operations SET claim_secret_enc=?,claim_secret_expires_at=? WHERE id=?`,sealed,time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),expired.ID);err!=nil{t.Fatal(err)}
	_,err=s.ClaimOperationSecret(ctx,m.OrgID,m.ID,expired.ID);requireProviderCode(t,err,"secret_expired")
	if err:=s.expireOperationSecrets(ctx);err!=nil{t.Fatal(err)};if err:=s.DB.QueryRowContext(ctx,`SELECT claim_secret_enc FROM operations WHERE id=?`,expired.ID).Scan(&remaining);err!=nil{t.Fatal(err)};if remaining.Valid{t.Fatal("过期秘密密文未清除")}
}

func TestMultiProviderStorageCapabilityMailAccess(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();mailboxID,_:=storageSubmission(t,s,m);mb,err:=s.Mailbox(ctx,m.OrgID,mailboxID);if err!=nil{t.Fatal(err)}
	member,err:=s.CreateMember(ctx,m.OrgID,m.ID,CreateMemberRequest{MemberInput:MemberInput{DisplayName:"读取成员",LoginEmail:"capability@example.org"},Password:uuid.NewString()+"Aa1!"});if err!=nil{t.Fatal(err)}
	caps,err:=s.ObjectCapabilities(ctx,m.OrgID,member.Member.ID,mb.ConnectionID,mailboxID);if err!=nil{t.Fatal(err)};if caps["mail.read"].PermissionAllowed || caps["rules.manage"].PermissionAllowed{t.Fatal("未授权成员获得了邮件权限")}
	if _,err:=s.GrantAccess(ctx,m.OrgID,m.ID,mailboxID,member.Member.ID,model.AccessRead);err!=nil{t.Fatal(err)}
	caps,err=s.ObjectCapabilities(ctx,m.OrgID,member.Member.ID,mb.ConnectionID,mailboxID);if err!=nil{t.Fatal(err)};if !caps["mail.read"].PermissionAllowed || caps["mail.send"].PermissionAllowed || caps["rules.manage"].PermissionAllowed{t.Fatal("读取权限错误地授予了写入能力")}
	if caps["credential.rotate"].Support!="external" || caps["mail.read"].Readiness=="ready"{t.Fatal("手动登记对象能力或未验证协议状态不正确")}
	if _,err:=s.GrantAccess(ctx,m.OrgID,m.ID,mailboxID,member.Member.ID,model.AccessFull);err!=nil{t.Fatal(err)}
	if err:=s.requireOperationPermission(ctx,m.OrgID,member.Member.ID,mailboxID,permissionMailFull);err!=nil{t.Fatal(err)}
	if err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{return requireFullAccessTx(ctx,tx,m.OrgID,member.Member.ID,mailboxID)});err!=nil{t.Fatal(err)}
}

func TestMultiProviderStorageConcurrentConnectionWorkers(t *testing.T) {
	s,m:=newStorageService(t);var operations []*OperationView
	for i:=0;i<4;i++{c:=storageConnection(t,s,m,"独立连接 "+fmtID(int64(i)));in:=DomainBindingInput{ConnectionID:c.ID,DomainName:"worker"+fmtID(int64(i))+".example.org",Mode:"register"};op,_,err:=s.QueueOperation(context.Background(),m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"domain.register",Domain:&in},nil);if err!=nil{t.Fatal(err)};operations=append(operations,op)}
	startStorageOperations(t,s)
	for _,op:=range operations{finished:=awaitStorageOperation(t,s,m.OrgID,op.ID);if finished.Status!="succeeded"{code:="";if finished.ErrorCode!=nil{code=*finished.ErrorCode};t.Fatalf("独立连接操作没有完成：%s %s",finished.Status,code)}}
	var count int;if err:=s.DB.QueryRowContext(context.Background(),`SELECT COUNT(*) FROM domain_bindings`).Scan(&count);err!=nil{t.Fatal(err)};if count!=4{t.Fatal("并发执行重复提交或遗漏了域名关联")}
	if err:=s.DB.QueryRowContext(context.Background(),`SELECT COUNT(*) FROM operation_locks`).Scan(&count);err!=nil{t.Fatal(err)};if count!=0{t.Fatal("成功操作仍占用资源")}
}

func TestMultiProviderStorageManualConnectionScope(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();c:=storageConnection(t,s,m,"限定域名");scope:=provider.DomainScope{Mode:"selected",Domains:[]string{"allowed.example.org"}}
	_,err:=s.ConfigureConnection(ctx,m.OrgID,m.ID,c.ID,UpdateConnectionInput{ExpectedRevision:c.Revision,DomainScope:&scope});requireProviderCode(t,err,"invalid")
	current,err:=s.MailConnection(ctx,m.OrgID,c.ID);if err!=nil{t.Fatal(err)};if current.Revision!=c.Revision || current.DomainScope.Mode!="all"{t.Fatal("无效范围修改了连接配置")}
	var count int;if err:=s.DB.QueryRowContext(ctx,`SELECT COUNT(*) FROM operations`).Scan(&count);err!=nil{t.Fatal(err)};if count!=0{t.Fatal("管理范围校验失败后仍创建操作")}
}

func TestMultiProviderStorageQueuedDomainCancellation(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();c:=storageConnection(t,s,m,"待执行域名登记");in:=DomainBindingInput{ConnectionID:c.ID,DomainName:"cancel.example.org",Mode:"register"}
	op,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"domain.register",Domain:&in},nil);if err!=nil{t.Fatal(err)}
	if err:=s.DeleteConnection(ctx,m.OrgID,m.ID,c.ID,c.Revision,c.Label);err==nil{t.Fatal("待执行操作没有阻止删除连接")}else{requireProviderCode(t,err,"connection_in_use")}
	cancelled,err:=s.ControlOperation(ctx,m.OrgID,m.ID,op.ID,"cancel");if err!=nil{t.Fatal(err)};if cancelled.Status!="cancelled"{t.Fatal("待执行域名登记没有取消")}
	var count int;if err:=s.DB.QueryRowContext(ctx,`SELECT COUNT(*) FROM operation_locks WHERE operation_id=?`,op.ID).Scan(&count);err!=nil{t.Fatal(err)};if count!=0{t.Fatal("取消操作仍占用连接")}
	if err:=s.DeleteConnection(ctx,m.OrgID,m.ID,c.ID,c.Revision,c.Label);err!=nil{t.Fatal(err)}
}

func TestMultiProviderStorageSetupCompletionWithoutMailbox(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();c:=storageConnection(t,s,m,"初始化连接")
	if err:=s.CompleteConnectionSetup(ctx,m.OrgID,m.ID,c.ID,nil);err!=nil{t.Fatal(err)}
	status,err:=s.Status(ctx);if err!=nil{t.Fatal(err)};if status.NeedsSetup || status.Step!="done"{t.Fatal("未绑定邮箱时无法完成初始化")}
	enabled:=false;disabled,err:=s.UpdateConnection(ctx,m.OrgID,m.ID,c.ID,UpdateConnectionInput{ExpectedRevision:c.Revision,Enabled:&enabled});if err!=nil{t.Fatal(err)}
	if err:=s.CompleteConnectionSetup(ctx,m.OrgID,m.ID,disabled.ID,nil);err==nil{t.Fatal("停用连接仍可用于完成初始化")}else{requireProviderCode(t,err,"endpoint_disabled")}
	status,err=s.Status(ctx);if err!=nil{t.Fatal(err)};if status.NeedsSetup || status.Step!="done"{t.Fatal("连接停用导致初始化状态改变")}
}
