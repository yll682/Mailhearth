package core

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"mailhearth/internal/db"
	"mailhearth/internal/model"
)

func TestMultiProviderStorageEnteredCredentialManagement(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();mailboxID,_:=storageSubmission(t,s,m);mb,err:=s.Mailbox(ctx,m.OrgID,mailboxID);if err!=nil{t.Fatal(err)}
	for _,rotate:=range []bool{false,true}{err:=s.checkManagedCredentialTargets(ctx,mb,rotate);requireProviderCode(t,err,"external_action_required")}
	var count int;if err:=s.DB.QueryRow(`SELECT COUNT(*) FROM credentials WHERE mailbox_id=?`,mailboxID).Scan(&count);err!=nil{t.Fatal(err)};if count!=1{t.Fatal("entered 管理前提检查创建了 managed 凭据")}
	current,err:=s.Mailbox(ctx,m.OrgID,mailboxID);if err!=nil{t.Fatal(err)};if current.Revision!=mb.Revision{t.Fatal("entered 管理前提检查改变了邮箱版本")}
	_,err=s.verifySavedManagedCredential(ctx,m.OrgID,mb,1,true);requireProviderCode(t,err,"verification_required")
}

func TestMultiProviderStorageCandidateFolderMappingPrerequisites(t *testing.T) {
	s,_:=newStorageService(t);ctx:=context.Background();invalid:="Sent\r\n";err:=s.validateCandidateFolderMapping(ctx,nil,model.FolderMapping{Sent:&invalid});requireProviderCode(t,err,"invalid")
	valid:="已发送";err=s.validateCandidateFolderMapping(ctx,nil,model.FolderMapping{Sent:&valid});requireProviderCode(t,err,"endpoint_unconfigured")
	if err:=s.validateCandidateFolderMapping(ctx,nil,model.FolderMapping{});err!=nil{t.Fatal(err)}
}

func TestMultiProviderStorageManagedCredentialNetworkPrerequisites(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();c:=storageConnection(t,s,m,"managed 网络前提");mb:=storageOffboardMailbox(t,s,m,m.ID,c.ID,"managed@example.org");now:=db.Now()
	if _,err:=s.DB.Exec(`INSERT INTO mailbox_endpoints(mailbox_id,protocol,network_mode,created_at,updated_at) VALUES (?,'imap','inherit',?,?)`,mb.ID,now,now);err!=nil{t.Fatal(err)}
	requireProviderCode(t,s.checkManagedCredentialTargets(ctx,mb,false),"external_action_required")
	if _,err:=s.DB.Exec(`UPDATE mail_connections SET protocol_defaults_json=? WHERE id=?`,`{"imap":{"enabled":true,"host":"imap.example.org","port":993,"tlsMode":"tls"},"smtp":{"enabled":false},"managesieve":{"enabled":false}}`,c.ID);err!=nil{t.Fatal(err)}
	if err:=s.checkManagedCredentialTargets(ctx,mb,false);err!=nil{t.Fatal(err)}
	if _,err:=s.DB.Exec(`UPDATE mailbox_endpoints SET network_mode='override',network_override_json='{"enabled":false}' WHERE mailbox_id=?`,mb.ID);err!=nil{t.Fatal(err)}
	requireProviderCode(t,s.checkManagedCredentialTargets(ctx,mb,false),"external_action_required")
}

func TestMultiProviderStorageManagedEndpointReadiness(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();c:=storageConnection(t,s,m,"managed 就绪检查");mb:=storageOffboardMailbox(t,s,m,m.ID,c.ID,"ready@example.org");now:=db.Now()
	if _,err:=s.DB.Exec(`UPDATE mail_connections SET protocol_defaults_json=? WHERE id=?`,`{"imap":{"enabled":true,"host":"imap.example.org","port":993,"tlsMode":"tls"},"smtp":{"enabled":false},"managesieve":{"enabled":false}}`,c.ID);err!=nil{t.Fatal(err)}
	sealed,err:=s.Box.Seal(uuid.NewString());if err!=nil{t.Fatal(err)};res,err:=s.DB.Exec(`INSERT INTO credentials(org_id,connection_id,mailbox_id,purpose,source,secret_enc,generation,state,hint,created_at,updated_at) VALUES (?,?,?,'mail','purelymail_app_password',?,1,'active','readiness',?,?)`,m.OrgID,c.ID,mb.ID,sealed,now,now);if err!=nil{t.Fatal(err)};credentialID,err:=res.LastInsertId();if err!=nil{t.Fatal(err)}
	versions:=toJSON(CheckedVersions{ConnectionRevision:c.Revision,EndpointRevision:1,CredentialID:credentialID,CredentialGeneration:1})
	if _,err:=s.DB.Exec(`INSERT INTO mailbox_endpoints(mailbox_id,protocol,network_mode,username,credential_id,check_status,checked_versions_json,created_at,updated_at) VALUES (?,'imap','inherit',?,?,'passed',?,?,?)`,mb.ID,mb.Address,credentialID,versions,now,now);err!=nil{t.Fatal(err)}
	if _,err:=s.DB.Exec(`INSERT INTO mailbox_endpoints(mailbox_id,protocol,network_mode,created_at,updated_at) VALUES (?,'smtp','inherit',?,?)`,mb.ID,now,now);err!=nil{t.Fatal(err)}
	ready,err:=s.managedEndpointsReady(ctx,mb.ID);if err!=nil{t.Fatal(err)};if !ready{t.Fatal("disabled SMTP 模板阻止了已检查的 IMAP 状态")}
	if _,err:=s.DB.Exec(`UPDATE mailbox_endpoints SET revision=revision+1 WHERE mailbox_id=? AND protocol='imap'`,mb.ID);err!=nil{t.Fatal(err)}
	ready,err=s.managedEndpointsReady(ctx,mb.ID);if err!=nil{t.Fatal(err)};if ready{t.Fatal("旧协议版本仍被标记为 ready")}
	if _,err:=s.DB.Exec(`UPDATE mailbox_endpoints SET checked_versions_json=json_set(checked_versions_json,'$.endpointRevision',revision) WHERE mailbox_id=? AND protocol='imap'`,mb.ID);err!=nil{t.Fatal(err)}
	if _,err:=s.DB.Exec(`UPDATE credentials SET generation=generation+1 WHERE id=?`,credentialID);err!=nil{t.Fatal(err)}
	ready,err=s.managedEndpointsReady(ctx,mb.ID);if err!=nil{t.Fatal(err)};if ready{t.Fatal("旧凭据 generation 仍被标记为 ready")}
}

func TestMultiProviderStorageMailboxManagementVersionSnapshot(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();c:=storageConnection(t,s,m,"管理版本检查");mb:=storageOffboardMailbox(t,s,m,m.ID,c.ID,"version@example.org")
	payload:=OperationPayload{Kind:"mailbox.revokeRemoteAccess",MailboxID:mb.ID,RemoteMailbox:&RemoteMailboxInput{ExpectedRevision:mb.Revision}}
	op,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),payload,nil);if err!=nil{t.Fatal(err)}
	if _,err:=s.DB.Exec(`UPDATE mail_connections SET revision=revision+1 WHERE id=?`,c.ID);err!=nil{t.Fatal(err)}
	startStorageOperations(t,s);failed:=awaitStorageOperation(t,s,m.OrgID,op.ID);if failed.ErrorCode==nil || *failed.ErrorCode!="revision_conflict"{t.Fatalf("管理操作没有使用排队时的连接版本：%s %s",failed.Status,failed.Result)}
	_,err=s.ControlOperation(ctx,m.OrgID,m.ID,op.ID,"retry");requireProviderCode(t,err,"revision_conflict")
	var count int;if err:=s.DB.QueryRow(`SELECT COUNT(*) FROM operation_steps WHERE operation_id=? AND step_key LIKE 'remote_access.%'`,op.ID).Scan(&count);err!=nil{t.Fatal(err)};if count!=0{t.Fatal("版本冲突后执行了远程访问撤销步骤")}
}

func TestMultiProviderStorageManagedCredentialActivationVersion(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();mailboxID,_:=storageSubmission(t,s,m);mb,err:=s.Mailbox(ctx,m.OrgID,mailboxID);if err!=nil{t.Fatal(err)}
	op,_,err:=s.QueueOperation(ctx,m.OrgID,m.ID,uuid.NewString(),OperationPayload{Kind:"mailbox.revokeRemoteAccess",MailboxID:mb.ID,RemoteMailbox:&RemoteMailboxInput{ExpectedRevision:mb.Revision}},nil);if err!=nil{t.Fatal(err)}
	var credentialID int64;if err:=s.DB.QueryRow(`SELECT id FROM credentials WHERE mailbox_id=?`,mb.ID).Scan(&credentialID);err!=nil{t.Fatal(err)}
	child:=context.WithValue(ctx,operationContextKey{},op.ID);child=context.WithValue(child,intermediateOperationKey{},true);child=context.WithValue(child,operationStepPrefixKey{},"credential.activate.")
	err=s.DB.Tx(ctx,func(tx *sql.Tx)error{if _,err:=tx.ExecContext(ctx,`UPDATE mailboxes SET revision=revision+1 WHERE id=?`,mb.ID);err!=nil{return err};return completeLocalOperation(child,tx,map[string]int64{"mailboxId":mb.ID,"credentialId":credentialID})});if err!=nil{t.Fatal(err)}
	if err:=validateManagedMailboxRevision(ctx,s.DB,m.OrgID,op.ID,mb.ID,mb.Revision);err!=nil{t.Fatal(err)}
	if _,err:=s.DB.Exec(`UPDATE mailboxes SET revision=revision+1 WHERE id=?`,mb.ID);err!=nil{t.Fatal(err)}
	requireProviderCode(t,validateManagedMailboxRevision(ctx,s.DB,m.OrgID,op.ID,mb.ID,mb.Revision),"revision_conflict")
}
