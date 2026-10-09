package core

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"mailhearth/internal/model"
)

func TestMultiProviderStoragePublicRoutingOperations(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();connection:=storageConnection(t,s,m,"公共地址方法")
	domainID:=storageRoutingDomain(t,s,m,"example.org")
	in:=AddressInput{RequestID:uuid.NewString(),ConnectionID:connection.ID,DomainID:domainID,LocalPart:"team",Kind:model.AddressForward,Targets:[]string{"recipient@example.org"},Mode:"external"}
	a,err:=s.CreateAddress(ctx,m.OrgID,m.ID,in);if err!=nil{t.Fatal(err)}
	again,err:=s.CreateAddress(ctx,m.OrgID,m.ID,in);if err!=nil{t.Fatal(err)};if again.ID!=a.ID{t.Fatal("重复公共方法请求创建了其他地址")}
	var count int;if err:=s.DB.QueryRow(`SELECT COUNT(*) FROM operations WHERE request_id=?`,in.RequestID).Scan(&count);err!=nil{t.Fatal(err)};if count!=1{t.Fatal("公共地址方法没有保存唯一 Operation")}
	if a.SyncState!="unknown" || len(a.ObservedTargets)!=0{t.Fatal("外部登记没有保留未确认状态")}
	update:=in;update.RequestID=uuid.NewString();update.ExpectedRevision=a.Revision;update.Targets=[]string{"updated@example.org"}
	updated,err:=s.UpdateAddress(ctx,m.OrgID,m.ID,a.ID,update);if err!=nil{t.Fatal(err)};if len(updated.Targets)!=1 || updated.Targets[0]!="updated@example.org"{t.Fatal("公共更新方法没有保存期望目标")}
	if err:=s.DeleteAddress(ctx,m.OrgID,m.ID,a.ID);err==nil{t.Fatal("删除地址没有要求版本")};if err:=s.DeleteAddress(ctx,m.OrgID,m.ID,a.ID,updated.Revision);err!=nil{t.Fatal(err)}
	if _,err:=s.Address(ctx,m.OrgID,a.ID);err!=ErrNotFound{t.Fatal("公共删除方法没有移除地址")}
	group,err:=s.CreateGroup(ctx,m.OrgID,m.ID,GroupInput{RequestID:uuid.NewString(),Name:"公共群组方法",MemberIDs:[]int64{m.ID}});if err!=nil{t.Fatal(err)}
	if err:=s.RemoveGroupMember(ctx,m.OrgID,m.ID,group.ID,m.ID);err!=nil{t.Fatal(err)}
	current,err:=s.Group(ctx,m.OrgID,group.ID);if err!=nil{t.Fatal(err)};if len(current.MemberIDs)!=0 || current.Revision<=group.Revision{t.Fatal("移除成员没有使用群组版本检查")}
	if err:=s.AddGroupMember(ctx,m.OrgID,m.ID,group.ID,m.ID);err!=nil{t.Fatal(err)}
	current,err=s.Group(ctx,m.OrgID,group.ID);if err!=nil{t.Fatal(err)};if len(current.MemberIDs)!=1{t.Fatal("公共群组方法没有添加成员")}
	if err:=s.DeleteGroup(ctx,m.OrgID,m.ID,group.ID);err==nil{t.Fatal("删除群组没有要求版本")};if err:=s.DeleteGroup(ctx,m.OrgID,m.ID,group.ID,current.Revision);err!=nil{t.Fatal(err)}
	if _,err:=s.Sync(ctx,m.OrgID,m.ID);err==nil{t.Fatal("同步没有要求明确连接")}
	if _,err:=s.Discover(ctx,m.OrgID);err==nil{t.Fatal("发现资源没有要求明确连接和操作成员")}
}

func TestMultiProviderStorageGroupReadErrors(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background()
	group,err:=s.CreateGroup(ctx,m.OrgID,m.ID,GroupInput{RequestID:uuid.NewString(),Name:"群组读取检查",MemberIDs:[]int64{m.ID}});if err!=nil{t.Fatal(err)}
	if _,err:=s.DB.Exec(`ALTER TABLE group_members RENAME TO unavailable_group_members`);err!=nil{t.Fatal(err)}
	if _,err:=s.Group(ctx,m.OrgID,group.ID);err==nil{t.Fatal("群组成员读取错误没有返回")}
	if _,err:=s.Groups(ctx,m.OrgID);err==nil{t.Fatal("群组列表读取错误没有返回")}
}

func TestMultiProviderCoreProviderDependencies(t *testing.T) {
	entries,err:=os.ReadDir(".");if err!=nil{t.Fatal(err)}
	for _,entry:=range entries{
		if entry.IsDir() || !strings.HasSuffix(entry.Name(),".go") || strings.HasSuffix(entry.Name(),"_test.go"){continue}
		file,err:=parser.ParseFile(token.NewFileSet(),entry.Name(),nil,parser.ImportsOnly);if err!=nil{t.Fatal(err)}
		for _,item:=range file.Imports{path,err:=strconv.Unquote(item.Path.Value);if err!=nil{t.Fatal(err)};if path=="mailhearth/internal/purelymail"{t.Fatalf("core 业务文件依赖服务商客户端：%s",entry.Name())}}
	}
}
