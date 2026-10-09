package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"mailhearth/internal/provider"
)

func TestMultiProviderStorageDomainObservationIsolation(t *testing.T) {
	s, m := newStorageService(t)
	ctx := context.Background()
	one := storageConnection(t, s, m, "域名观察连接一")
	two := storageConnection(t, s, m, "域名观察连接二")
	startStorageOperations(t, s)
	_, first := registerStorageDomain(t, s, m, one, "example.org")
	_, second := registerStorageDomain(t, s, m, two, "example.org")
	reset := false
	subaddressing := true
	item := provider.DiscoverySnapshotResource{Domain: &provider.DomainInfo{Name: "example.org", AllowAccountReset: &reset, SymbolicSubaddressing: &subaddressing, DNS: provider.DNSStatus{MX: "pass", SPF: "fail", DKIM: "unknown", DMARC: "pass"}}}
	if err := s.DB.Tx(ctx, func(tx *sql.Tx) error { return syncDomainObservation(ctx, tx, m.OrgID, one.ID, first.ID, item) }); err != nil {
		t.Fatal(err)
	}
	updated, err := s.DomainBinding(ctx, m.OrgID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != first.Revision+1 || updated.DNSCheckedAt == nil || updated.DNSStatus["spf"] != "fail" || updated.DNSStatus["dkim"] != "unknown" {
		t.Fatal("域名观察没有保存当前检查状态")
	}
	body, err := json.Marshal(updated.ProviderSettings)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		AllowAccountReset     bool `json:"allowAccountReset"`
		SymbolicSubaddressing bool `json:"symbolicSubaddressing"`
	}
	if err := json.Unmarshal(body, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.AllowAccountReset || !settings.SymbolicSubaddressing {
		t.Fatal("域名观察丢失了服务商设置")
	}
	if err := s.DB.Tx(ctx, func(tx *sql.Tx) error { return syncDomainObservation(ctx, tx, m.OrgID, one.ID, first.ID, item) }); err != nil {
		t.Fatal(err)
	}
	unchanged, err := s.DomainBinding(ctx, m.OrgID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Revision != updated.Revision {
		t.Fatal("相同域名观察重复改变了配置版本")
	}
	other, err := s.DomainBinding(ctx, m.OrgID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if other.Revision != second.Revision || other.DNSCheckedAt != nil {
		t.Fatal("域名观察改变了同名域名的其他连接")
	}
	item.Domain.Name = "another.example.org"
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error { return syncDomainObservation(ctx, tx, m.OrgID, one.ID, first.ID, item) })
	requireProviderCode(t, err, "revision_conflict")
	item.Domain.Name = "example.org"
	item.Domain.DNS.MX = "invalid"
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error { return syncDomainObservation(ctx, tx, m.OrgID, one.ID, first.ID, item) })
	requireProviderCode(t, err, "upstream_failed")
	other, err = s.DomainBinding(ctx, m.OrgID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if other.Revision != updated.Revision || other.DNSStatus["mx"] != "pass" {
		t.Fatal("无效域名观察改变了已有数据")
	}
}
