package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"

	"mailhearth/internal/db"
	"mailhearth/internal/provider"
)

func saveResourceObservation(ctx context.Context, tx *sql.Tx, connectionID int64, item provider.DiscoverySnapshotResource) error {
	item.Resource.ConnectionID = connectionID
	var owned bool
	if err := tx.QueryRowContext(ctx, `SELECT owned_by_mailhearth FROM provider_resources WHERE connection_id=? AND resource_type=? AND remote_key=? AND purpose=?`, connectionID, item.Resource.ResourceType, item.Resource.RemoteKey, item.Resource.Purpose).Scan(&owned); err != nil {
		return err
	}
	item.Resource.OwnedByMailhearth = owned
	body, err := json.Marshal(item)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE provider_resources SET observation_json=?,observed_at=? WHERE connection_id=? AND resource_type=? AND remote_key=? AND purpose=?`, string(body), db.Now(), connectionID, item.Resource.ResourceType, item.Resource.RemoteKey, item.Resource.Purpose)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return provider.Errorf("revision_conflict", "观察结果没有唯一的本地资源关联")
	}
	return nil
}

func syncRuleObservation(ctx context.Context, tx *sql.Tx, orgID, connectionID, addressID int64, item provider.DiscoverySnapshotResource) error {
	if item.AddressRule == nil {
		return provider.Errorf("upstream_failed", "远程规则缺少目标信息")
	}
	var desiredJSON, observedJSON, state, local, domain, kind string
	if err := tx.QueryRowContext(ctx, `SELECT a.desired_targets_json,a.observed_targets_json,a.sync_state,a.local_part,d.name,a.kind FROM addresses a JOIN domains d ON d.id=a.domain_id WHERE a.id=? AND a.org_id=? AND a.connection_id=?`, addressID, orgID, connectionID).Scan(&desiredJSON, &observedJSON, &state, &local, &domain, &kind); err != nil {
		return err
	}
	var desired, observed []string
	if err := json.Unmarshal([]byte(desiredJSON), &desired); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(observedJSON), &observed); err != nil {
		return err
	}
	targets := []string{}
	for _, target := range item.AddressRule.Targets {
		value, err := NormalizeEmail(target)
		if err != nil {
			return err
		}
		targets = append(targets, value)
	}
	sort.Strings(targets)
	targets = uniqueStrings(targets)
	rule := item.AddressRule
	remoteDomain, err := provider.CanonicalDomain(rule.Domain)
	if err != nil {
		return err
	}
	shapeMatches := domain == remoteDomain && strings.EqualFold(local, rule.LocalPart) && (kind == "prefix") == rule.Prefix && (kind == "catchall") == rule.Catchall
	if kind == "external_rule" && rule.Pattern != "" {
		shapeMatches = domain == remoteDomain && local == rule.Pattern
	}
	next := "synced"
	if !shapeMatches || !sameRoutingTargets(desired, targets) {
		next = "error"
	}
	if sameRoutingTargets(observed, targets) && state == next {
		return nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE addresses SET observed_targets_json=?,sync_state=?,revision=revision+1,updated_at=? WHERE id=? AND org_id=? AND connection_id=?`, toJSON(targets), next, db.Now(), addressID, orgID, connectionID)
	return err
}

func syncDomainObservation(ctx context.Context, tx *sql.Tx, orgID, connectionID, bindingID int64, item provider.DiscoverySnapshotResource) error {
	if item.Domain == nil {
		return provider.Errorf("upstream_failed", "远程域名缺少设置和 DNS 状态")
	}
	info := item.Domain
	name, err := provider.CanonicalDomain(info.Name)
	if err != nil {
		return err
	}
	var domain, settingsJSON, dnsJSON, mode string
	if err := tx.QueryRowContext(ctx, `SELECT d.name,b.provider_settings_json,b.dns_status_json,b.management_mode FROM domain_bindings b JOIN domains d ON d.id=b.domain_id WHERE b.id=? AND b.org_id=? AND b.connection_id=?`, bindingID, orgID, connectionID).Scan(&domain, &settingsJSON, &dnsJSON, &mode); err != nil {
		return err
	}
	if domain != name {
		return provider.Errorf("revision_conflict", "远程域名与本地关联不一致")
	}
	for _, status := range []string{info.DNS.MX, info.DNS.SPF, info.DNS.DKIM, info.DNS.DMARC} {
		if status != "pass" && status != "fail" && status != "unknown" {
			return provider.Errorf("upstream_failed", "DNS 状态无效")
		}
	}
	settings := toJSON(struct {
		IsShared              bool  `json:"isShared"`
		AllowAccountReset     *bool `json:"allowAccountReset"`
		SymbolicSubaddressing *bool `json:"symbolicSubaddressing"`
	}{info.IsShared, info.AllowAccountReset, info.SymbolicSubaddressing})
	dns := toJSON(info.DNS)
	nextMode := mode
	if info.IsShared {
		nextMode = "external"
	}
	changed := settingsJSON != settings || dnsJSON != dns || mode != nextMode
	var checkedAt any
	if domainDNSObserved(info.DNS) {
		checkedAt = db.Now()
	}
	_, err = tx.ExecContext(ctx, `UPDATE domain_bindings SET provider_settings_json=?,dns_status_json=?,dns_checked_at=?,management_mode=?,revision=revision+?,updated_at=? WHERE id=? AND org_id=? AND connection_id=?`, settings, dns, checkedAt, nextMode, boolInt(changed), db.Now(), bindingID, orgID, connectionID)
	return err
}

func domainDNSObserved(status provider.DNSStatus) bool {
	for _, value := range []string{status.MX, status.SPF, status.DKIM, status.DMARC} {
		if value == "pass" || value == "fail" {
			return true
		}
	}
	return false
}
