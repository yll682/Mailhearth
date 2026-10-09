package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

func storageOffboardMember(t *testing.T,s *Service,m *storageMember,name string) *model.Member {
	t.Helper()
	result,err:=s.CreateMember(context.Background(),m.OrgID,m.ID,CreateMemberRequest{MemberInput:MemberInput{DisplayName:name,LoginEmail:uuid.NewString()+"@example.org"},Password:uuid.NewString()+"Aa1!"})
	if err!=nil{t.Fatal(err)}
	return result.Member
}

func storageOffboardMailbox(t *testing.T,s *Service,m *storageMember,owner int64,connectionID int64,address string) *model.Mailbox {
	t.Helper();ctx:=context.Background();now:=db.Now()
	result,err:=s.DB.ExecContext(ctx,`INSERT INTO mailboxes(org_id,connection_id,kind,address,address_key,owner_member_id,management_mode,remote_state,created_at,updated_at) VALUES (?,?,'personal',?,?,?,'external','external',?,?)`,m.OrgID,connectionID,address,address,owner,now,now);if err!=nil{t.Fatal(err)};id,err:=result.LastInsertId();if err!=nil{t.Fatal(err)}
	mailbox,err:=s.Mailbox(ctx,m.OrgID,id);if err!=nil{t.Fatal(err)};return mailbox
}

func TestMultiProviderStorageOffboardAtomicAccess(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();leaver:=storageOffboardMember(t,s,m,"离职成员");successor:=storageOffboardMember(t,s,m,"接收成员")
	first:=storageConnection(t,s,m,"离职连接一");second:=storageConnection(t,s,m,"离职连接二")
	one:=storageOffboardMailbox(t,s,m,leaver.ID,first.ID,"same@example.org");two:=storageOffboardMailbox(t,s,m,leaver.ID,second.ID,"same@example.org")
	shared,_:=storageSubmission(t,s,m);if _,err:=s.GrantAccess(ctx,m.OrgID,m.ID,shared,leaver.ID,model.AccessFull);err!=nil{t.Fatal(err)}
	s.Cfg.SessionTTL=time.Hour;token,err:=s.CreateSession(ctx,leaver.ID,"127.0.0.1","离职测试");if err!=nil{t.Fatal(err)}
	request,cancel:=s.RegisterMailRequest(ctx,leaver.ID,one.ID,token);defer cancel()
	in:=OffboardRequest{RequestID:uuid.NewString(),ExpectedRevision:leaver.Revision,Plans:[]MailboxPlan{{MailboxID:one.ID,ExpectedRevision:one.Revision,Action:"handover",NewOwnerID:successor.ID},{MailboxID:two.ID,ExpectedRevision:two.Revision,Action:"suspend"}}}
	result,err:=s.Offboard(ctx,m.OrgID,m.ID,leaver.ID,in);if err!=nil{t.Fatal(err)}
	if result.Member.Status!=model.MemberDeparted || result.Operation.Status!="queued"{t.Fatal("离职状态与 Operation 没有同时保存")}
	if _,err:=s.SetMemberStatus(ctx,m.OrgID,m.ID,leaver.ID,true);err==nil{t.Fatal("离职成员通过启用操作恢复了访问")}
	if _,err:=s.SetMemberStatus(ctx,m.OrgID,m.ID,leaver.ID,false);err==nil{t.Fatal("离职成员通过停用操作改变了状态")}
	if request.Err()==nil{t.Fatal("离职提交没有取消成员请求")}
	if member,err:=s.LookupSession(ctx,token);err!=nil || member!=nil{t.Fatalf("离职会话仍可使用：%v",err)}
	var count int;if err:=s.DB.QueryRowContext(ctx,`SELECT COUNT(*) FROM mailbox_access WHERE member_id=?`,leaver.ID).Scan(&count);err!=nil{t.Fatal(err)};if count!=0{t.Fatal("离职没有撤销全部共享邮箱授权")}
	again,err:=s.Offboard(ctx,m.OrgID,m.ID,leaver.ID,in);if err!=nil{t.Fatal(err)};if again.Operation.ID!=result.Operation.ID{t.Fatal("重复请求生成了其他离职 Operation")}
	startStorageOperations(t,s);finished:=awaitStorageOperation(t,s,m.OrgID,result.Operation.ID)
	if finished.Status!="needs_action"{t.Fatalf("外部撤销事项状态不正确：%s %v",finished.Status,finished.ErrorCode)}
	transferred,err:=s.Mailbox(ctx,m.OrgID,one.ID);if err!=nil{t.Fatal(err)};if transferred.OwnerMemberID==nil || *transferred.OwnerMemberID!=successor.ID{t.Fatal("指定邮箱没有移交")}
	suspended,err:=s.Mailbox(ctx,m.OrgID,two.ID);if err!=nil{t.Fatal(err)};if suspended.Status!=model.MailboxSuspended || suspended.OwnerMemberID!=nil{t.Fatal("其他连接的同名邮箱没有独立暂停")}
	var keys []string;for _,step:=range finished.Steps{if step.StepKey!="execute" && step.ErrorCode!=nil && *step.ErrorCode=="external_action_required"{keys=append(keys,step.StepKey)}}
	if len(keys)!=3{t.Fatalf("外部访问撤销没有包含全部接触过的邮箱：%d",len(keys))}
	confirmed,err:=s.ConfirmExternalOperation(ctx,m.OrgID,m.ID,finished.ID,ConfirmExternalInput{StepKeys:keys,Note:"管理员已在对应服务商完成访问处理"});if err!=nil{t.Fatal(err)}
	if confirmed.Status!="succeeded"{t.Fatal("全部外部报告没有完成操作")}
	for _,step:=range confirmed.Steps{for _,key:=range keys{if step.StepKey==key && step.Status!="external_reported"{t.Fatal("管理员报告被标记为系统验证")}}}
	var verification struct{Verified bool `json:"externalAccessSystemVerified"`;Source string `json:"verificationSource"`};if err:=json.Unmarshal(confirmed.Result,&verification);err!=nil{t.Fatal(err)};if verification.Verified || verification.Source!="administrator_report"{t.Fatal("离职完成结果没有保留验证来源")}
	if err:=s.DB.QueryRowContext(ctx,`SELECT COUNT(*) FROM operation_locks WHERE operation_id=?`,finished.ID).Scan(&count);err!=nil{t.Fatal(err)};if count!=0{t.Fatal("操作完成没有释放资源占用")}
}

func TestMultiProviderStorageOffboardPreflight(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();leaver:=storageOffboardMember(t,s,m,"预检查成员");c:=storageConnection(t,s,m,"预检查连接");mailbox:=storageOffboardMailbox(t,s,m,leaver.ID,c.ID,"preflight@example.org")
	s.Cfg.SessionTTL=time.Hour;token,err:=s.CreateSession(ctx,leaver.ID,"127.0.0.1","预检查测试");if err!=nil{t.Fatal(err)}
	in:=OffboardRequest{RequestID:uuid.NewString(),ExpectedRevision:leaver.Revision,Plans:[]MailboxPlan{{MailboxID:mailbox.ID,ExpectedRevision:mailbox.Revision,Action:"handover",NewOwnerID:m.ID+999}}}
	_,err=s.Offboard(ctx,m.OrgID,m.ID,leaver.ID,in);requireProviderCode(t,err,"invalid")
	current,err:=s.Member(ctx,m.OrgID,leaver.ID);if err!=nil{t.Fatal(err)};if current.Status!=model.MemberActive || current.Revision!=leaver.Revision{t.Fatal("失败的预检查改变了成员")}
	if member,err:=s.LookupSession(ctx,token);err!=nil || member==nil{t.Fatal("失败的预检查撤销了成员会话")}
	var count int;if err:=s.DB.QueryRowContext(ctx,`SELECT COUNT(*) FROM operations WHERE kind='member.offboard'`).Scan(&count);err!=nil{t.Fatal(err)};if count!=0{t.Fatal("失败计划生成了离职操作")}
	in.Plans[0].Action="keep";in.Plans[0].NewOwnerID=0
	blocking,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"connection.discover",ConnectionID:c.ID},nil);if err!=nil{t.Fatal(err)}
	_,err=s.Offboard(ctx,m.OrgID,m.ID,leaver.ID,in);requireProviderCode(t,err,"operation_in_progress")
	current,err=s.Member(ctx,m.OrgID,leaver.ID);if err!=nil{t.Fatal(err)};if current.Status!=model.MemberActive{t.Fatal("资源占用时仍提交了离职状态")}
	if _,err:=s.ControlOperation(ctx,m.OrgID,m.ID,blocking.ID,"cancel");err!=nil{t.Fatal(err)}
	in.ExpectedRevision++;_,err=s.Offboard(ctx,m.OrgID,m.ID,leaver.ID,in);requireProviderCode(t,err,"revision_conflict")
}

func TestMultiProviderStorageOffboardExternalCannotResolveUnknown(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();leaver:=storageOffboardMember(t,s,m,"独立核验成员");c:=storageConnection(t,s,m,"独立核验连接");mailbox:=storageOffboardMailbox(t,s,m,leaver.ID,c.ID,"unknown@example.org")
	result,err:=s.Offboard(ctx,m.OrgID,m.ID,leaver.ID,OffboardRequest{RequestID:uuid.NewString(),ExpectedRevision:leaver.Revision,Plans:[]MailboxPlan{{MailboxID:mailbox.ID,ExpectedRevision:mailbox.Revision,Action:"keep"}}});if err!=nil{t.Fatal(err)}
	startStorageOperations(t,s);finished:=awaitStorageOperation(t,s,m.OrgID,result.Operation.ID)
	key:="mailbox."+fmtID(mailbox.ID)+".revokeAll.external"
	if _,err:=s.DB.ExecContext(ctx,`UPDATE operation_steps SET status='unknown' WHERE operation_id=? AND step_key=?`,finished.ID,key);err!=nil{t.Fatal(err)}
	_,err=s.ConfirmExternalOperation(ctx,m.OrgID,m.ID,finished.ID,ConfirmExternalInput{StepKeys:[]string{key},Note:"管理员报告"});requireProviderCode(t,err,"verification_required")
	current,err:=s.Member(ctx,m.OrgID,leaver.ID);if err!=nil{t.Fatal(err)};if current.Status!=model.MemberDeparted{t.Fatal("未知远程状态恢复了离职成员")}
}

func TestMultiProviderStorageOffboardSharedAndGroups(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();leaver:=storageOffboardMember(t,s,m,"共享交接成员");successor:=storageOffboardMember(t,s,m,"共享授权成员");c:=storageConnection(t,s,m,"共享交接连接")
	mailbox:=storageOffboardMailbox(t,s,m,leaver.ID,c.ID,"shared@example.org")
	now:=db.Now();res,err:=s.DB.ExecContext(ctx,`INSERT INTO domains(org_id,name,created_at,updated_at) VALUES (?,'example.org',?,?)`,m.OrgID,now,now);if err!=nil{t.Fatal(err)};domainID,err:=res.LastInsertId();if err!=nil{t.Fatal(err)}
	res,err=s.DB.ExecContext(ctx,`INSERT INTO groups(org_id,name,created_at,updated_at) VALUES (?,'交接群组',?,?)`,m.OrgID,now,now);if err!=nil{t.Fatal(err)};groupID,err:=res.LastInsertId();if err!=nil{t.Fatal(err)}
	if _,err:=s.DB.ExecContext(ctx,`INSERT INTO group_members(group_id,member_id) VALUES (?,?)`,groupID,leaver.ID);err!=nil{t.Fatal(err)}
	res,err=s.DB.ExecContext(ctx,`INSERT INTO addresses(org_id,connection_id,domain_id,local_part,address,address_key,kind,group_id,targets_json,desired_targets_json,observed_targets_json,management_mode,sync_state,created_at,updated_at) VALUES (?,?,?,'team','team@example.org','team@example.org','group',?,'["shared@example.org"]','["shared@example.org"]','["shared@example.org"]','external','synced',?,?)`,m.OrgID,c.ID,domainID,groupID,now,now);if err!=nil{t.Fatal(err)};addressID,err:=res.LastInsertId();if err!=nil{t.Fatal(err)}
	result,err:=s.Offboard(ctx,m.OrgID,m.ID,leaver.ID,OffboardRequest{RequestID:uuid.NewString(),ExpectedRevision:leaver.Revision,RemoveFromGroups:true,Plans:[]MailboxPlan{{MailboxID:mailbox.ID,ExpectedRevision:mailbox.Revision,Action:"shared",GrantMemberIDs:[]int64{successor.ID}}}});if err!=nil{t.Fatal(err)}
	startStorageOperations(t,s);finished:=awaitStorageOperation(t,s,m.OrgID,result.Operation.ID);if finished.Status!="needs_action"{t.Fatalf("共享交接状态不正确：%s",finished.Status)}
	updated,err:=s.Mailbox(ctx,m.OrgID,mailbox.ID);if err!=nil{t.Fatal(err)};if updated.Kind!=model.MailboxShared || updated.OwnerMemberID!=nil{t.Fatal("邮箱没有转换为 shared")}
	access,err:=s.AccessList(ctx,m.OrgID,mailbox.ID);if err!=nil{t.Fatal(err)};if len(access)!=1 || access[0].MemberID!=successor.ID || access[0].Level!=model.AccessFull{t.Fatal("共享交接没有保存指定授权")}
	var desired,observed,state string;if err:=s.DB.QueryRowContext(ctx,`SELECT desired_targets_json,observed_targets_json,sync_state FROM addresses WHERE id=?`,addressID).Scan(&desired,&observed,&state);err!=nil{t.Fatal(err)};if desired!="[]" || observed!=`["shared@example.org"]` || state!="pending"{t.Fatal("群组外部处理没有分别保留候选目标和已观察目标")}
	var count int;if err:=s.DB.QueryRowContext(ctx,`SELECT COUNT(*) FROM group_members WHERE member_id=?`,leaver.ID).Scan(&count);err!=nil{t.Fatal(err)};if count!=0{t.Fatal("离职成员没有从群组中移除")}
}

func TestMultiProviderStorageOffboardGroupChecksBeforeDeduplication(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();leaver:=storageOffboardMember(t,s,m,"群组预检查成员");successor:=storageOffboardMember(t,s,m,"群组目标成员");other:=storageConnection(t,s,m,"其他连接")
	defaults,err:=provider.JSONString(provider.DefaultTemplates(provider.Migadu));if err!=nil{t.Fatal(err)};now:=db.Now()
	res,err:=s.DB.ExecContext(ctx,`INSERT INTO mail_connections(org_id,provider_kind,label,api_username,domain_scope_json,protocol_defaults_json,created_at,updated_at) VALUES (?,'migadu','群组关联连接','admin','{"mode":"all"}',?,?,?)`,m.OrgID,defaults,now,now);if err!=nil{t.Fatal(err)};connectionID,err:=res.LastInsertId();if err!=nil{t.Fatal(err)}
	one:=storageOffboardMailbox(t,s,m,leaver.ID,connectionID,"duplicate@example.org");storageOffboardMailbox(t,s,m,successor.ID,other.ID,"duplicate@example.org")
	res,err=s.DB.ExecContext(ctx,`INSERT INTO domains(org_id,name,created_at,updated_at) VALUES (?,'example.org',?,?)`,m.OrgID,now,now);if err!=nil{t.Fatal(err)};domainID,err:=res.LastInsertId();if err!=nil{t.Fatal(err)}
	res,err=s.DB.ExecContext(ctx,`INSERT INTO groups(org_id,name,created_at,updated_at) VALUES (?,'连接限制群组',?,?)`,m.OrgID,now,now);if err!=nil{t.Fatal(err)};groupID,err:=res.LastInsertId();if err!=nil{t.Fatal(err)}
	if _,err:=s.DB.ExecContext(ctx,`INSERT INTO group_members(group_id,member_id) VALUES (?,?),(?,?)`,groupID,leaver.ID,groupID,successor.ID);err!=nil{t.Fatal(err)}
	if _,err:=s.DB.ExecContext(ctx,`INSERT INTO addresses(org_id,connection_id,domain_id,local_part,address,address_key,kind,group_id,management_mode,created_at,updated_at) VALUES (?,?,?,'team','team@example.org','team@example.org','group',?,'external',?,?)`,m.OrgID,connectionID,domainID,groupID,now,now);err!=nil{t.Fatal(err)}
	_,err=s.Offboard(ctx,m.OrgID,m.ID,leaver.ID,OffboardRequest{RequestID:uuid.NewString(),ExpectedRevision:leaver.Revision,Plans:[]MailboxPlan{{MailboxID:one.ID,ExpectedRevision:one.Revision,Action:"handover",NewOwnerID:successor.ID}}});requireProviderCode(t,err,"target_constraint_failed")
	current,err:=s.Member(ctx,m.OrgID,leaver.ID);if err!=nil{t.Fatal(err)};if current.Status!=model.MemberActive{t.Fatal("未通过群组预检查的请求提交了离职状态")}
}
