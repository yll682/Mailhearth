package migadu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"

	"mailhearth/internal/provider"
)

func (a *Adapter) ReadBilling(ctx context.Context,scope provider.DomainScope) (provider.BillingResult,error) {
	out:=provider.BillingResult{BalanceSupport:"unsupported",UsageSupport:"automatic",Values:[]provider.BillingValue{}}
	normalized,err:=provider.NormalizeScope(scope);if err!=nil{return out,err};domains:=append([]string{},normalized.Domains...)
	if normalized.Mode=="all"{var rows []domainDTO;if err:=a.call(ctx,http.MethodGet,"/domains",nil,&rows);err!=nil{return out,err};for _,row:=range rows{domain,err:=provider.CanonicalDomain(row.Name);if err!=nil{return out,provider.Errorf("upstream_failed","用量查询返回无效域名")};domains=append(domains,domain)}}
	sort.Strings(domains);seen:=map[string]bool{}
	for _,domain:=range domains{
		if seen[domain]{return out,provider.Errorf("upstream_failed","用量查询返回重复域名")};seen[domain]=true
		var usage struct{Domain string `json:"domain_name"`;Incoming *json.Number `json:"incoming"`;Outgoing *json.Number `json:"outgoing"`;Storage *json.Number `json:"storage"`}
		if err:=a.call(ctx,http.MethodGet,"/domains/"+url.PathEscape(domain)+"/usage",nil,&usage);err!=nil{return out,err}
		actual,err:=provider.CanonicalDomain(usage.Domain);if err!=nil || actual!=domain || usage.Incoming==nil || usage.Outgoing==nil || usage.Storage==nil{return out,provider.Errorf("upstream_failed","用量结果缺少必要字段或域名不一致")}
		incoming,err:=usage.Incoming.Int64();if err!=nil || incoming<0{return out,provider.Errorf("upstream_failed","收件用量无效")};outgoing,err:=usage.Outgoing.Int64();if err!=nil || outgoing<0{return out,provider.Errorf("upstream_failed","发件用量无效")};storage,err:=usage.Storage.Float64();if err!=nil || storage<0{return out,provider.Errorf("upstream_failed","存储用量无效")}
		out.Values=append(out.Values,provider.BillingValue{Key:"incoming",Value:usage.Incoming.String(),Unit:"messages",Domain:domain,Period:"current_date"},provider.BillingValue{Key:"outgoing",Value:usage.Outgoing.String(),Unit:"messages",Domain:domain,Period:"current_date"},provider.BillingValue{Key:"storage",Value:usage.Storage.String(),Unit:"GB",Domain:domain})
	}
	return out,nil
}
