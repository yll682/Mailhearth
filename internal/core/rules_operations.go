package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"mailhearth/internal/db"
	"mailhearth/internal/mailproto/sieve"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

const permissionMailFull = "mail.full"

type RulesOperationInput struct {
	RequestID string `json:"requestId"`
	ExpectedRevision int64 `json:"expectedRevision"`
	Rules []model.SieveRule `json:"rules"`
	Vacation *model.Vacation `json:"vacation"`
	Takeover bool `json:"takeover"`
	ExpectedActiveScriptHash string `json:"expectedActiveScriptHash"`
	SessionHash string `json:"-"`
}

type SieveServerState struct {
	Active string `json:"active"`
	ActiveScriptHash string `json:"activeScriptHash"`
	ManagedByMailhearth bool `json:"managedByMailhearth"`
	Extensions []string `json:"extensions"`
	Scripts []sieve.ScriptInfo `json:"scripts"`
}

type sievePlan struct {
	OriginalName string `json:"originalName"`
	OriginalHash string `json:"originalHash"`
	CandidateName string `json:"candidateName"`
	CandidateHash string `json:"candidateHash"`
	ConnectionRevision int64 `json:"connectionRevision"`
	EndpointRevision int64 `json:"endpointRevision"`
}

func scriptHash(body string) string { sum:=sha256.Sum256([]byte(body));return hex.EncodeToString(sum[:]) }

func readSieveState(client *sieve.Client,settings model.MailboxSettings) (*SieveServerState,error) {
	scripts,err:=client.ListScripts();if err!=nil{return nil,err}
	state:=&SieveServerState{Scripts:scripts,Extensions:strings.Fields(client.Capabilities()["SIEVE"])}
	for _,item:=range scripts {
		if !item.Active{continue}
		if state.Active!=""{return nil,provider.Errorf("upstream_failed","服务器公布了多个 active script")}
		body,err:=client.GetScript(item.Name);if err!=nil{return nil,err}
		state.Active=item.Name;state.ActiveScriptHash=scriptHash(body)
		state.ManagedByMailhearth=item.Name==settings.SieveScriptName && state.ActiveScriptHash==settings.SieveScriptHash
	}
	return state,nil
}

func (s *Service) rulesClient(ctx context.Context,orgID,mailboxID int64) (*sieve.Client,*ResolvedEndpoint,error) {
	endpoint,err:=s.ResolveEndpoint(ctx,orgID,mailboxID,provider.ProtocolManageSieve);if err!=nil{return nil,nil,err}
	client,err:=sieve.DialWithDialer(ctx,endpoint.Address(),endpoint.Network.TLSMode,endpoint.TLSConfig,endpoint.Dialer);if err!=nil{return nil,nil,err}
	if err:=client.Authenticate(endpoint.Username,endpoint.Secret);err!=nil{client.Close();return nil,nil,err}
	return client,endpoint,nil
}

func (s *Service) SieveServer(ctx context.Context,orgID,actor,mailboxID int64) (*SieveServerState,error) {
	mc,err:=s.CheckMailboxAccess(ctx,orgID,actor,mailboxID);if err!=nil{return nil,err}
	client,_,err:=s.rulesClient(ctx,orgID,mailboxID);if err!=nil{return nil,err};defer client.Close()
	return readSieveState(client,mc.Mailbox.Settings)
}

func (s *Service) requireOperationPermission(ctx context.Context,orgID,actor,mailboxID int64,required string) error {
	if required==permissionMailFull{mc,err:=s.CheckMailboxAccess(ctx,orgID,actor,mailboxID);if err!=nil{return err};if mc.Level!=model.AccessFull{return ErrForbidden};return nil}
	permissions,_,err:=s.Permissions(ctx,actor);if err!=nil{return err};if !HasPermission(permissions,required){return ErrForbidden};return nil
}

func requireFullAccessTx(ctx context.Context,tx *sql.Tx,orgID,actor,mailboxID int64) error {
	var allowed bool
	err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM mailboxes b JOIN mail_connections c ON c.id=b.connection_id AND c.org_id=b.org_id JOIN members m ON m.id=? AND m.org_id=b.org_id WHERE b.id=? AND b.org_id=? AND b.status='active' AND c.enabled=1 AND m.status='active' AND (b.owner_member_id=m.id OR EXISTS(SELECT 1 FROM mailbox_access a WHERE a.mailbox_id=b.id AND a.member_id=m.id AND a.level='full')))`,actor,mailboxID,orgID).Scan(&allowed)
	if err!=nil{return err};if !allowed{return ErrForbidden};return nil
}

func validateRulesInput(in *RulesOperationInput) error {
	if in==nil || in.ExpectedRevision<1{return provider.Errorf("invalid","需要完整规则与 expectedRevision")}
	if len(in.Rules)>100{return provider.Errorf("invalid","最多允许 100 条规则")}
	ids:=map[string]bool{}
	for _,rule:=range in.Rules{if rule.ID=="" || ids[rule.ID]{return provider.Errorf("invalid","规则 ID 必须非空且唯一")};ids[rule.ID]=true}
	_,err:=sieve.Compile(in.Rules,in.Vacation);return err
}

func compileServerRules(in *RulesOperationInput,extensions []string) (string,error) {
	script,err:=sieve.Compile(in.Rules,in.Vacation,extensions)
	if err!=nil{var missing *sieve.ExtensionError;if errors.As(err,&missing){typed:=provider.Errorf("sieve_extension_missing","服务器未提供规则所需的 Sieve 扩展");typed.Details=map[string]string{"ruleId":missing.RuleID,"extension":missing.Extension};return "",typed}}
	return script,err
}

func checkSieveTakeover(state *SieveServerState,in *RulesOperationInput) error {
	if state.Active=="" || state.ManagedByMailhearth{return nil}
	if !in.Takeover{typed:=provider.Errorf("sieve_takeover_required","需要确认接管当前 active script");typed.Details=map[string]string{"active":state.Active,"activeScriptHash":state.ActiveScriptHash};return typed}
	if state.ActiveScriptHash!=in.ExpectedActiveScriptHash{return provider.Errorf("revision_conflict","active script 已更新")}
	return nil
}

func (s *Service) preflightRules(ctx context.Context,orgID,mailboxID int64,in *RulesOperationInput) error {
	mb,err:=s.Mailbox(ctx,orgID,mailboxID);if err!=nil{return err}
	client,_,err:=s.rulesClient(ctx,orgID,mailboxID);if err!=nil{return err};defer client.Close()
	if _,err:=compileServerRules(in,strings.Fields(client.Capabilities()["SIEVE"]));err!=nil{return err}
	state,err:=readSieveState(client,mb.Settings);if err!=nil{return err}
	return checkSieveTakeover(state,in)
}

func (s *Service) executeRulesOperation(ctx context.Context,orgID,actor int64,payload OperationPayload) (any,error) {
	in:=payload.Rules;if err:=validateRulesInput(in);err!=nil{return nil,err};in.SessionHash=payload.SessionHash
	ctx,release:=s.RegisterMailRequestSessionHash(ctx,actor,payload.MailboxID,in.SessionHash);defer release()
	checkAccess:=func()error{
		if in.SessionHash!=""{active,err:=s.SessionHashActive(ctx,actor,in.SessionHash);if err!=nil{return err};if !active{return ErrForbidden}}
		return s.requireOperationPermission(ctx,orgID,actor,payload.MailboxID,permissionMailFull)
	}
	if err:=checkAccess();err!=nil{return nil,err}
	mc,err:=s.CheckMailboxAccess(ctx,orgID,actor,payload.MailboxID);if err!=nil{return nil,err};mb:=mc.Mailbox
	if mb.Revision!=in.ExpectedRevision{return nil,provider.Errorf("revision_conflict","邮箱配置已更新")}
	client,endpoint,err:=s.rulesClient(ctx,orgID,mb.ID);if err!=nil{return nil,err};defer client.Close()
	script,err:=compileServerRules(in,strings.Fields(client.Capabilities()["SIEVE"]))
	if err!=nil{return nil,err}
	if err:=client.CheckScript(script);err!=nil{return nil,provider.Errorf("invalid","服务器拒绝了候选规则")}
	state,err:=readSieveState(client,mb.Settings);if err!=nil{return nil,err}
	opID:=ctx.Value(operationContextKey{}).(string)
	plan:=sievePlan{OriginalName:state.Active,OriginalHash:state.ActiveScriptHash,CandidateName:"mailhearth-"+opID,CandidateHash:scriptHash(script),ConnectionRevision:endpoint.ConnectionRevision,EndpointRevision:endpoint.EndpointRevision}
	var saved string
	err=s.DB.QueryRowContext(ctx,`SELECT result_json FROM operation_steps WHERE operation_id=? AND step_key='sieve.prepare'`,opID).Scan(&saved)
	if err==nil{if err:=json.Unmarshal([]byte(saved),&plan);err!=nil{return nil,err}}else if db.IsNotFound(err){
		if err:=checkSieveTakeover(state,in);err!=nil{return nil,err}
		body,err:=json.Marshal(plan);if err!=nil{return nil,err}
		if _,err:=s.DB.ExecContext(ctx,`INSERT INTO operation_steps(operation_id,step_key,sequence,status,result_json,finished_at) SELECT ?,'sieve.prepare',MAX(sequence)+1,'succeeded',?,? FROM operation_steps WHERE operation_id=?`,opID,string(body),db.Now(),opID);err!=nil{return nil,err}
	}else{return nil,err}
	if plan.CandidateHash!=scriptHash(script) || plan.ConnectionRevision!=endpoint.ConnectionRevision || plan.EndpointRevision!=endpoint.EndpointRevision{return nil,provider.Errorf("revision_conflict","协议配置或候选规则已更新")}
	if state.Active!=plan.CandidateName && (state.Active!=plan.OriginalName || state.ActiveScriptHash!=plan.OriginalHash){return nil,provider.Errorf("revision_conflict","active script 已更新")}
	ref:=map[string]string{"scriptName":plan.CandidateName,"scriptHash":plan.CandidateHash}
	if err:=checkAccess();err!=nil{return nil,err}
	if err:=s.remoteOperationStep(ctx,"sieve.upload",ref,func()error{return client.PutScript(plan.CandidateName,script)});err!=nil{return nil,err}
	body,err:=client.GetScript(plan.CandidateName);if err!=nil{return nil,&remoteOutcomeUnknown{err}}
	if scriptHash(body)!=plan.CandidateHash{return nil,&remoteOutcomeUnknown{provider.Errorf("verification_required","候选脚本内容核验失败")}}
	state,err=readSieveState(client,mb.Settings);if err!=nil{return nil,&remoteOutcomeUnknown{err}}
	if state.Active!=plan.CandidateName && (state.Active!=plan.OriginalName || state.ActiveScriptHash!=plan.OriginalHash){return nil,provider.Errorf("revision_conflict","active script 已更新")}
	if err:=checkAccess();err!=nil{return nil,err}
	if err:=s.remoteOperationStep(ctx,"sieve.activate",ref,func()error{return client.SetActive(plan.CandidateName)});err!=nil{return nil,err}
	state,err=readSieveState(client,mb.Settings);if err!=nil{return nil,&remoteOutcomeUnknown{err}}
	if state.Active!=plan.CandidateName || state.ActiveScriptHash!=plan.CandidateHash{return nil,&remoteOutcomeUnknown{provider.Errorf("verification_required","active script 核验失败")}}
	settings:=mb.Settings;settings.SieveRules=in.Rules;settings.Vacation=in.Vacation;settings.SieveSync=db.Now();settings.SieveScriptName=plan.CandidateName;settings.SieveScriptHash=plan.CandidateHash
	settingsJSON,err:=json.Marshal(settings);if err!=nil{return nil,err}
	result:=map[string]any{"mailboxId":mb.ID,"scriptName":plan.CandidateName,"scriptHash":plan.CandidateHash}
	err=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		if err:=requireFullAccessTx(ctx,tx,orgID,actor,mb.ID);err!=nil{return err}
		var current bool
		if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM mailbox_endpoints e JOIN mailboxes b ON b.id=e.mailbox_id JOIN mail_connections c ON c.id=b.connection_id WHERE b.id=? AND b.org_id=? AND e.protocol='managesieve' AND e.revision=? AND c.revision=?)`,mb.ID,orgID,plan.EndpointRevision,plan.ConnectionRevision).Scan(&current);err!=nil{return err};if !current{return provider.Errorf("revision_conflict","协议配置已更新")}
		if in.SessionHash!=""{var active bool;if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM sessions WHERE token_hash=? AND member_id=? AND expires_at>?)`,in.SessionHash,actor,db.Now()).Scan(&active);err!=nil{return err};if !active{return ErrForbidden}}
		res,err:=tx.ExecContext(ctx,`UPDATE mailboxes SET settings_json=?,revision=revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=?`,string(settingsJSON),db.Now(),mb.ID,orgID,in.ExpectedRevision);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("revision_conflict","邮箱配置已更新")}
		return completeLocalOperation(ctx,tx,result)
	});if err!=nil{return nil,&remoteOutcomeUnknown{err}}
	return result,nil
}
