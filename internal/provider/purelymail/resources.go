package purelymail

import (
	"context"
	"mailhearth/internal/provider"
	"strconv"
)

func (a *Adapter) ReadResource(ctx context.Context, req provider.ReadResourceRequest) (provider.DiscoverySnapshotResource, error) {
	r := req.Resource
	out := provider.DiscoverySnapshotResource{Resource: r, Summary: map[string]string{}}
	out.Resource.State = provider.ResourcePresent
	switch r.ResourceType {
	case "domain":
		info, err := a.GetDomain(ctx, provider.GetDomainRequest{Domain: r.RemoteLocator["domain"]})
		if err != nil {
			return out, err
		}
		out.Domain = &info
		out.Summary["name"] = info.Name
		return out, nil
	case "mailbox":
		address := r.RemoteLocator["address"]
		if _, err := provider.ValidateAddress(address); err != nil {
			return out, err
		}
		out.Summary["address"] = address
		return out, provider.Errorf("verification_required", "Purelymail 邮箱缺少独立的存在状态查询")
	case "routing_rule":
		id, err := strconv.ParseInt(r.RemoteKey, 10, 64)
		if err != nil || id < 1 {
			return out, provider.Errorf("invalid", "Purelymail rule ID 无效")
		}
		rules, err := a.api.ListRoutingRules(ctx)
		if err != nil {
			return out, err
		}
		for _, rule := range rules {
			if rule.ID == id {
				out.AddressRule = &provider.AddressRuleInfo{RemoteKey: r.RemoteKey, RemoteLocator: r.RemoteLocator, Domain: rule.DomainName, LocalPart: rule.MatchUser, Prefix: rule.Prefix, Catchall: rule.Catchall, Targets: append([]string{}, rule.TargetAddresses...)}
				out.Summary["address"] = ruleAddress(rule)
				if r.Purpose == provider.PurposeForwarding {
					if rule.Prefix || rule.Catchall {
						return out, provider.Errorf("upstream_failed", "邮箱转发规则类型无效")
					}
					out.Forwarding = ruleForwardingInfo(*out.AddressRule)
				}
				return out, nil
			}
		}
		return out, provider.Errorf("verification_required", "Purelymail 规则缺少独立的不存在确认")
	default:
		return out, provider.Errorf("unsupported_operation", "此资源类型没有独立读取方法")
	}
}

func ruleForwardingInfo(rule provider.AddressRuleInfo) *provider.ForwardingInfo {
	statuses := map[string]string{}
	for _, target := range rule.Targets {
		statuses[target] = "unknown"
	}
	return &provider.ForwardingInfo{Domain: rule.Domain, LocalPart: rule.LocalPart, Targets: append([]string{}, rule.Targets...), DeliveryMode: "unverified", StatusByTarget: statuses}
}
