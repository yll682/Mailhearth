package core

import (
	"context"
	"errors"
	"testing"

	"mailhearth/internal/model"
)

func TestMultiProviderStorageBillingPermissionAndIsolation(t *testing.T) {
	s,m:=newStorageService(t);ctx:=context.Background();one:=storageConnection(t,s,m,"计费用量连接一");two:=storageConnection(t,s,m,"计费用量连接二");member:=storageOffboardMember(t,s,m,"用量权限成员")
	role,err:=s.CreateRole(ctx,m.OrgID,m.ID,RoleInput{Name:"无用量权限",Permissions:[]string{model.PermMembersManage}});if err!=nil{t.Fatal(err)};member,err=s.UpdateMember(ctx,m.OrgID,m.ID,member.ID,MemberInput{ExpectedRevision:member.Revision,RoleID:role.ID});if err!=nil{t.Fatal(err)}
	if _,err:=s.ConnectionBilling(ctx,m.OrgID,member.ID,one.ID);!errors.Is(err,ErrForbidden){t.Fatal("缺少 billing.read 的成员可以读取用量")}
	role,err=s.CreateRole(ctx,m.OrgID,m.ID,RoleInput{Name:"只有用量权限",Permissions:[]string{model.PermBillingRead}});if err!=nil{t.Fatal(err)};member,err=s.UpdateMember(ctx,m.OrgID,m.ID,member.ID,MemberInput{ExpectedRevision:member.Revision,RoleID:role.ID});if err!=nil{t.Fatal(err)}
	for _,connection:=range []*ConnectionView{one,two}{view,err:=s.ConnectionBilling(ctx,m.OrgID,member.ID,connection.ID);if err!=nil{t.Fatal(err)};if view.ConnectionID!=connection.ID || view.Label!=connection.Label || view.BalanceSupport!="external" || view.UsageSupport!="external" || len(view.Values)!=0{t.Fatal("手动连接用量未保持独立外部状态")}}
	if _,err:=s.ConnectionBilling(ctx,m.OrgID+1,member.ID,one.ID);!errors.Is(err,ErrForbidden){t.Fatal("用量查询接受了其他组织")}
	enabled:=false;if _,err:=s.UpdateConnection(ctx,m.OrgID,m.ID,one.ID,UpdateConnectionInput{ExpectedRevision:one.Revision,Enabled:&enabled});err!=nil{t.Fatal(err)};_,err=s.ConnectionBilling(ctx,m.OrgID,member.ID,one.ID);requireProviderCode(t,err,"endpoint_disabled")
}
