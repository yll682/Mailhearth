package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"mailhearth/internal/config"
	"mailhearth/internal/core"
	"mailhearth/internal/db"
	"mailhearth/internal/secrets"
	"mailhearth/internal/web"
)

func coreID(id int64) string {return strconv.FormatInt(id,10)}

func TestMultiProviderStorageHTTP(t *testing.T) {
	root:=filepath.Join("..","..","data","integration","multi-provider","http-"+uuid.NewString())
	if err:=os.MkdirAll(root,0700);err!=nil{t.Fatal(err)}
	database,err:=db.Open(filepath.Join(root,"mailhearth.db"));if err!=nil{t.Fatal(err)};defer database.Close()
	master:=make([]byte,32);if _,err:=rand.Read(master);err!=nil{t.Fatal(err)};box,err:=secrets.NewBox(master,"credentials");if err!=nil{t.Fatal(err)}
	cfg:=&config.Config{DataDir:root,SessionTTL:time.Hour,MaxUploadBytes:1<<20}
	svc:=core.New(database,cfg,box,nil,slog.Default())
	member,err:=svc.Init(context.Background(),core.InitRequest{OrgName:"HTTP 验收",AdminName:"管理员",AdminEmail:"admin@example.org",Password:uuid.NewString()+"Aa1!"});if err!=nil{t.Fatal(err)}
	token,err:=svc.CreateSession(context.Background(),member.ID,"127.0.0.1","integration");if err!=nil{t.Fatal(err)}
	workerCtx,stopWorker:=context.WithCancel(context.Background());if err:=svc.StartOperations(workerCtx);err!=nil{t.Fatal(err)}
	defer func(){stopWorker();svc.WaitOperations()}()
	api:=New(cfg,svc,nil,web.Handler(),master,slog.Default())
	listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
	server:=&http.Server{Handler:api.Handler(),ReadHeaderTimeout:5*time.Second};done:=make(chan error,1)
	go func(){done<-server.Serve(listener)}()
	defer func(){if err:=server.Close();err!=nil{t.Error(err)};if err:=<-done;err!=http.ErrServerClosed{t.Error(err)}}()
	client:=&http.Client{Timeout:5*time.Second};base:="http://"+listener.Addr().String()
	request:=func(method,path string,body []byte,status int) []byte {
		t.Helper();r,err:=http.NewRequest(method,base+path,bytes.NewReader(body));if err!=nil{t.Fatal(err)}
		r.AddCookie(&http.Cookie{Name:sessionCookie,Value:token});r.Header.Set("Content-Type","application/json");r.Header.Set("X-Requested-With","Mailhearth")
		response,err:=client.Do(r);if err!=nil{t.Fatal(err)};defer response.Body.Close()
		data,err:=io.ReadAll(response.Body);if err!=nil{t.Fatal(err)};if response.StatusCode!=status{t.Fatalf("%s %s HTTP 状态为 %d，要求 %d",method,path,response.StatusCode,status)}
		return data
	}
	for _,route:=range []struct{method,path string}{{"GET","/api/admin/connection"},{"PUT","/api/admin/connection"},{"POST","/api/admin/connection/sync"},{"GET","/api/admin/connection/discover"},{"POST","/api/setup/connect"},{"GET","/api/setup/discover"}}{
		data:=request(route.method,route.path,[]byte(`{}`),410);var result apiError;if err:=json.Unmarshal(data,&result);err!=nil{t.Fatal(err)};if result.Code!="api_replaced"{t.Fatal("旧接口错误码不正确")}
	}
	for _,body:=range []string{`{"providerKind":"manual","label":"连接","protocolDefaults":{"imap":{"enabled":false}}}`,`{"providerKind":"manual","label":"连接","internal":"value"}`,`{"providerKind":"manual","label":"连接"} {}`}{request("POST","/api/admin/connections",[]byte(body),400)}
	data:=request("POST","/api/admin/connections",[]byte(`{"providerKind":"manual","label":"手动连接","apiAuth":null}`),201)
	var connection core.ConnectionView;if err:=json.Unmarshal(data,&connection);err!=nil{t.Fatal(err)};if connection.ID<=0 || connection.Revision!=1 || connection.APIConfigured{t.Fatal("连接查询字段不正确")}
	data=request("GET","/api/admin/connections/"+coreID(connection.ID)+"/billing",nil,200);var billing core.ConnectionBillingView;if err:=json.Unmarshal(data,&billing);err!=nil{t.Fatal(err)};if billing.ConnectionID!=connection.ID || billing.BalanceSupport!="external" || billing.UsageSupport!="external" || len(billing.Values)!=0{t.Fatal("HTTP 手动用量状态不正确")}
	secret:=uuid.NewString()+"SensitivePassword"
	data=request("POST","/api/admin/connections",[]byte(`{"providerKind":"manual","label":"连接","apiAuth":{"apiKey":"`+secret+`"}}`),400)
	if strings.Contains(string(data),secret){t.Fatal("错误响应包含认证秘密")}
	patchBody,err:=json.Marshal(core.UpdateConnectionInput{RequestID:uuid.NewString(),ExpectedRevision:connection.Revision,DomainScope:&connection.DomainScope});if err!=nil{t.Fatal(err)}
	data=request("PATCH","/api/admin/connections/"+coreID(connection.ID),patchBody,202)
	var operation core.OperationView;if err:=json.Unmarshal(data,&operation);err!=nil{t.Fatal(err)}
	deadline:=time.Now().Add(5*time.Second);completed:=false
	for time.Now().Before(deadline){
		data=request("GET","/api/admin/operations/"+operation.ID,nil,200);if err:=json.Unmarshal(data,&operation);err!=nil{t.Fatal(err)}
		if operation.Status=="succeeded"{completed=true;break};if operation.Status=="failed"{t.Fatal("连接配置 Operation 失败")};time.Sleep(10*time.Millisecond)
	}
	if !completed{t.Fatal("连接配置 Operation 未完成")}
	data=request("PATCH","/api/admin/connections/"+coreID(connection.ID),patchBody,200)
	var repeated core.OperationView;if err:=json.Unmarshal(data,&repeated);err!=nil{t.Fatal(err)};if repeated.ID!=operation.ID{t.Fatal("重复 HTTP 请求创建了其他 Operation")}
	data=request("GET","/api/admin/operations",nil,200);var operations []core.OperationView;if err:=json.Unmarshal(data,&operations);err!=nil{t.Fatal(err)};if len(operations)!=1 || operations[0].ID!=operation.ID{t.Fatal("HTTP 操作列表未返回已完成操作")}
	if strings.Contains(string(data),"payload_enc") || strings.Contains(string(data),"secret_enc") || strings.Contains(string(data),secret){t.Fatal("HTTP 操作列表包含秘密")}
	data=request("GET","/api/admin/connections/"+coreID(connection.ID),nil,200);if err:=json.Unmarshal(data,&connection);err!=nil{t.Fatal(err)};if connection.Revision!=2{t.Fatal("操作未提交连接配置版本")}
	data=request("GET","/api/admin/overview",nil,200)
	var overview map[string]json.RawMessage;if err:=json.Unmarshal(data,&overview);err!=nil{t.Fatal(err)};if _,exists:=overview["connection"];exists{t.Fatal("Overview 返回了单连接字段")};var summaries []core.OverviewConnection;if err:=json.Unmarshal(overview["connections"],&summaries);err!=nil{t.Fatal(err)};if len(summaries)!=1 || summaries[0].ID!=connection.ID{t.Fatal("Overview 没有按连接 ID 返回连接")}
	for _,route:=range []struct{method,path string}{{"POST","/api/admin/domains"},{"PATCH","/api/admin/domains/1"},{"POST","/api/admin/domains/1/recheck"},{"DELETE","/api/admin/domains/1"}}{data:=request(route.method,route.path,[]byte(`{}`),410);var replaced apiError;if err:=json.Unmarshal(data,&replaced);err!=nil{t.Fatal(err)};if replaced.Code!="api_replaced"{t.Fatal("旧域名写入接口没有拒绝")}}
	org,err:=svc.Org(context.Background());if err!=nil{t.Fatal(err)}
	result,err:=database.Exec(`INSERT INTO mailboxes(org_id,connection_id,kind,address,address_key,owner_member_id,management_mode,remote_state,created_at,updated_at) VALUES (?,?,'personal','registered@example.org','registered@example.org',?,'external','external',?,?)`,org.ID,connection.ID,member.ID,db.Now(),db.Now());if err!=nil{t.Fatal(err)};mailboxID,err:=result.LastInsertId();if err!=nil{t.Fatal(err)}
	folderPath:="/api/mail/mailboxes/"+coreID(mailboxID)+"/folder-mapping"
	data=request("GET",folderPath,nil,200);var mapping core.FolderMappingView;if err:=json.Unmarshal(data,&mapping);err!=nil{t.Fatal(err)};if mapping.MailboxID!=mailboxID || mapping.Revision!=1{t.Fatal("文件夹映射响应不正确")}
	request("PUT",folderPath,[]byte(`{"expectedRevision":1,"mapping":{"sent":null,"unknown":null},"sentCopyMode":"append"}`),400)
	data=request("PUT",folderPath,[]byte(`{"expectedRevision":1,"mapping":{"sent":null,"drafts":null,"trash":null,"junk":null,"archive":null},"sentCopyMode":"append"}`),409)
	var folderError apiError;if err:=json.Unmarshal(data,&folderError);err!=nil{t.Fatal(err)};if folderError.Code!="endpoint_unconfigured"{t.Fatal("文件夹映射没有报告协议未配置")}
	data=request("GET","/api/mail/mailboxes/"+coreID(mailboxID)+"/submissions",nil,200);if strings.TrimSpace(string(data))!="[]"{t.Fatal("空发送记录列表响应不正确")}
	data=request("GET","/api/admin/resource-options",nil,200);var options struct{Connections []struct{ID int64 `json:"id"`} `json:"connections"`;Domains []struct{ID int64 `json:"id"`} `json:"domains"`};if err:=json.Unmarshal(data,&options);err!=nil{t.Fatal(err)};if len(options.Connections)!=1 || options.Connections[0].ID!=connection.ID || strings.Contains(string(data),"apiUsername") || strings.Contains(string(data),"apiHint"){t.Fatal("资源选项没有保持连接归属和安全字段")}
	forwardingPath:="/api/admin/mailboxes/"+coreID(mailboxID)+"/forwarding";request("POST",forwardingPath,[]byte(`{}`),410)
	request("PUT",forwardingPath,[]byte(`{"requestId":"`+uuid.NewString()+`","expectedRevision":1,"targets":["target@example.org"],"deliveryMode":"redirect","password":"sensitive"}`),400)
	forwardRequest:=uuid.NewString();forwardBody:=[]byte(`{"requestId":"`+forwardRequest+`","expectedRevision":1,"targets":["target@example.org"],"deliveryMode":"redirect"}`)
	data=request("PUT",forwardingPath,forwardBody,202);var forwardOperation core.OperationView;if err:=json.Unmarshal(data,&forwardOperation);err!=nil{t.Fatal(err)}
	deadline=time.Now().Add(5*time.Second);for time.Now().Before(deadline){data=request("GET","/api/admin/operations/"+forwardOperation.ID,nil,200);if err:=json.Unmarshal(data,&forwardOperation);err!=nil{t.Fatal(err)};if forwardOperation.Status=="needs_action"{break};if forwardOperation.Status!="queued" && forwardOperation.Status!="running"{t.Fatalf("外部转发状态无效：%s",forwardOperation.Status)};time.Sleep(10*time.Millisecond)};if forwardOperation.Status!="needs_action"{t.Fatal("外部转发没有生成管理员处理事项")}
	data=request("PUT",forwardingPath,forwardBody,200);var repeatedForward core.OperationView;if err:=json.Unmarshal(data,&repeatedForward);err!=nil{t.Fatal(err)};if repeatedForward.ID!=forwardOperation.ID{t.Fatal("HTTP 重复转发请求创建了其他操作")}
	data=request("POST","/api/admin/operations/"+forwardOperation.ID+"/confirm-external",[]byte(`{"itemId":"forwarding.external","note":"管理员已完成外部设置"}`),200);if err:=json.Unmarshal(data,&forwardOperation);err!=nil{t.Fatal(err)};if forwardOperation.Status!="succeeded"{t.Fatal("HTTP 外部报告没有完成操作")}
	data=request("GET","/api/admin/mailboxes/"+coreID(mailboxID),nil,200);var details struct{Forwarding *core.ForwardingView `json:"forwarding"`};if err:=json.Unmarshal(data,&details);err!=nil{t.Fatal(err)};if details.Forwarding==nil || details.Forwarding.SyncState!="unknown" || details.Forwarding.Revision!=2{t.Fatal("HTTP 邮箱详情没有返回独立转发对象")}
	awaitHTTP:=func(id string) core.OperationView{t.Helper();deadline:=time.Now().Add(5*time.Second);for time.Now().Before(deadline){body:=request("GET","/api/admin/operations/"+id,nil,200);var op core.OperationView;if err:=json.Unmarshal(body,&op);err!=nil{t.Fatal(err)};if op.Status=="succeeded"{return op};if op.Status!="queued" && op.Status!="running"{t.Fatalf("HTTP 操作没有完成：%s %s",op.Status,op.Result)};time.Sleep(10*time.Millisecond)};t.Fatal("HTTP 操作超出完成期限");return core.OperationView{}}
	memberRequest:=uuid.NewString();memberSecret:=uuid.NewString()+"Aa1!";memberBody:=[]byte(`{"requestId":"`+memberRequest+`","displayName":"HTTP 成员","loginEmail":"created-http@example.org","mailboxAction":"none","password":"`+memberSecret+`","sendInvite":false}`)
	data=request("POST","/api/admin/members",memberBody,202);var created core.OperationView;if err:=json.Unmarshal(data,&created);err!=nil{t.Fatal(err)};created=awaitHTTP(created.ID);var memberResult struct{ID int64 `json:"memberId"`};if err:=json.Unmarshal(created.Result,&memberResult);err!=nil{t.Fatal(err)};if memberResult.ID<1{t.Fatal("HTTP 成员创建没有返回实际 ID")};if strings.Contains(string(data),memberSecret) || strings.Contains(string(created.Result),memberSecret){t.Fatal("HTTP 成员创建返回了秘密")}
	data=request("POST","/api/admin/members",memberBody,200);var memberAgain core.OperationView;if err:=json.Unmarshal(data,&memberAgain);err!=nil{t.Fatal(err)};if memberAgain.ID!=created.ID{t.Fatal("HTTP 重复成员创建生成了其他操作")}
	editBody:=[]byte(`{"requestId":"`+uuid.NewString()+`","expectedRevision":1,"ownerMemberId":`+coreID(memberResult.ID)+`}`);data=request("PATCH","/api/admin/mailboxes/"+coreID(mailboxID),editBody,202);var edit core.OperationView;if err:=json.Unmarshal(data,&edit);err!=nil{t.Fatal(err)};edit=awaitHTTP(edit.ID)
	request("PATCH","/api/admin/mailboxes/"+coreID(mailboxID),[]byte(`{"requestId":"`+uuid.NewString()+`","expectedRevision":1,"displayName":"过期版本"}`),409)
	statusPath:="/api/admin/members/"+coreID(memberResult.ID)+"/status";statusBody:=[]byte(`{"requestId":"`+uuid.NewString()+`","expectedRevision":2,"enabled":false}`);data=request("POST",statusPath,statusBody,202);var status core.OperationView;if err:=json.Unmarshal(data,&status);err!=nil{t.Fatal(err)};status=awaitHTTP(status.ID)
	request("POST",statusPath,[]byte(`{"requestId":"`+uuid.NewString()+`","expectedRevision":3,"enabled":true,"password":"sensitive"}`),400)
	archiveBody:=[]byte(`{"requestId":"`+uuid.NewString()+`","expectedRevision":2}`);data=request("POST","/api/admin/mailboxes/"+coreID(mailboxID)+"/archive",archiveBody,202);var archive core.OperationView;if err:=json.Unmarshal(data,&archive);err!=nil{t.Fatal(err)};awaitHTTP(archive.ID)
	sessionHash:=secrets.HashToken(token)
	viewToken:=api.viewToken(member.ID,1,"INBOX",1,time.Minute,sessionHash)
	id,hash,valid:=api.checkViewToken(viewToken,1,"INBOX",1);if !valid || id!=member.ID || hash!=sessionHash{t.Fatal("临时链接未保持会话引用")}
	_,_,valid=api.checkViewToken(viewToken,2,"INBOX",1);if valid{t.Fatal("临时链接接受了其他邮箱 ID")}
	_,_,valid=api.checkViewToken(viewToken,1,"INBOX",2);if valid{t.Fatal("临时链接接受了其他邮件 UID")}
	active,err:=svc.SessionHashActive(context.Background(),member.ID,sessionHash);if err!=nil{t.Fatal(err)};if !active{t.Fatal("有效会话被拒绝")}
	inflight,finish:=svc.RegisterMailRequestSessionHash(context.Background(),member.ID,1,sessionHash);defer finish()
	if err:=svc.DeleteSession(context.Background(),token);err!=nil{t.Fatal(err)}
	if inflight.Err()==nil{t.Fatal("会话撤销没有终止临时链接请求")}
	active,err=svc.SessionHashActive(context.Background(),member.ID,sessionHash);if err!=nil{t.Fatal(err)};if active{t.Fatal("已撤销会话仍被接受")}
	response,err:=client.Get(base+"/api/mail/mailboxes/1/messages/1/view?t="+viewToken);if err!=nil{t.Fatal(err)};defer response.Body.Close()
	if response.StatusCode!=http.StatusForbidden{t.Fatalf("已撤销会话的临时链接 HTTP 状态为 %d",response.StatusCode)}
}
