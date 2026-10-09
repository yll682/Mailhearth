package core

import (
	"context"

	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

type ConnectionBillingView struct {
	ConnectionID int64                 `json:"connectionId"`
	Label        string                `json:"label"`
	ProviderKind provider.ProviderKind `json:"providerKind"`
	provider.BillingResult
}

func (s *Service) ConnectionBilling(ctx context.Context, orgID, actor, connectionID int64) (*ConnectionBillingView, error) {
	if err := requireManagementPermission(ctx, s.DB, orgID, actor, model.PermBillingRead); err != nil {
		return nil, err
	}
	api, c, err := s.connectionAdapter(ctx, orgID, connectionID)
	if err != nil {
		return nil, err
	}
	out := &ConnectionBillingView{ConnectionID: c.ID, Label: c.Label, ProviderKind: c.ProviderKind, BillingResult: provider.BillingResult{BalanceSupport: "external", UsageSupport: "external", Values: []provider.BillingValue{}}}
	if reader, ok := api.(provider.BillingReader); ok {
		out.BillingResult, err = reader.ReadBilling(ctx, c.DomainScope)
		if err != nil {
			return nil, err
		}
	}
	if err := requireManagementPermission(ctx, s.DB, orgID, actor, model.PermBillingRead); err != nil {
		return nil, err
	}
	current, err := s.MailConnection(ctx, orgID, c.ID)
	if err != nil {
		return nil, err
	}
	if current.Revision != c.Revision || !current.Enabled {
		return nil, provider.Errorf("revision_conflict", "查询期间连接配置已经更新")
	}
	return out, nil
}
