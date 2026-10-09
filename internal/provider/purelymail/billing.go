package purelymail

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"mailhearth/internal/provider"
	purelymailclient "mailhearth/internal/purelymail"
)

func (a *Adapter) ReadBilling(ctx context.Context, scope provider.DomainScope) (provider.BillingResult, error) {
	out := provider.BillingResult{BalanceSupport: "automatic", UsageSupport: "unsupported", Values: []provider.BillingValue{}}
	credit, err := a.api.CheckAccountCredit(ctx)
	if err != nil {
		code := "upstream_failed"
		var remote *purelymailclient.Error
		if purelymailclient.IsInvalidToken(err) || (errors.As(err, &remote) && (remote.Status == 401 || remote.Status == 403)) {
			code = "provider_auth_failed"
		} else if errors.As(err, &remote) && remote.Status == 429 {
			code = "upstream_rate_limited"
		}
		return out, provider.Errorf(code, "Purelymail 余额读取失败")
	}
	var amount json.Number
	if err := json.Unmarshal([]byte(credit), &amount); err != nil || amount.String() == "" || strings.TrimSpace(credit) != amount.String() {
		return out, provider.Errorf("upstream_failed", "服务商余额缺少有效数值")
	}
	out.Values = append(out.Values, provider.BillingValue{Key: "balance", Value: credit, Unit: "provider_credit"})
	return out, nil
}
