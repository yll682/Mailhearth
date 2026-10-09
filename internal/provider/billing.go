package provider

import "context"

type BillingValue struct {
	Key string `json:"key"`
	Value string `json:"value"`
	Unit string `json:"unit"`
	Domain string `json:"domain,omitempty"`
	Period string `json:"period,omitempty"`
}

type BillingResult struct {
	BalanceSupport string `json:"balanceSupport"`
	UsageSupport string `json:"usageSupport"`
	Values []BillingValue `json:"values"`
}

type BillingReader interface {
	ReadBilling(context.Context, DomainScope) (BillingResult,error)
}
