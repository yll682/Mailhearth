package core

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/purelymail"
)

const domainSelect = `SELECT d.id, d.name, d.is_shared, d.allow_account_reset, d.symbolic_subaddressing, d.dns_mx, d.dns_spf, d.dns_dkim, d.dns_dmarc, d.dns_checked_at, d.status, d.created_at,
	(SELECT COUNT(1) FROM mailboxes b WHERE b.domain_id = d.id AND b.status != 'archived'),
	(SELECT COUNT(1) FROM addresses a WHERE a.domain_id = d.id AND a.kind != 'primary')
	FROM domains d`

func scanDomain(row interface{ Scan(...any) error }) (*model.Domain, error) {
	var d model.Domain
	var shared, reset, sub int
	var mx, spf, dkim, dmarc sql.NullInt64
	var checked sql.NullString
	if err := row.Scan(&d.ID, &d.Name, &shared, &reset, &sub, &mx, &spf, &dkim, &dmarc, &checked, &d.Status, &d.CreatedAt, &d.MailboxCount, &d.AddressCount); err != nil {
		return nil, err
	}
	d.IsShared, d.AllowAccountReset, d.SymbolicSubaddressing = shared == 1, reset == 1, sub == 1
	if mx.Valid {
		d.DNS = &model.DNSSummary{MX: mx.Int64 == 1, SPF: spf.Int64 == 1, DKIM: dkim.Int64 == 1, DMARC: dmarc.Int64 == 1}
	}
	d.DNSCheckedAt = nullStr(checked)
	return &d, nil
}

// Domains lists domains.
func (s *Service) Domains(ctx context.Context, orgID int64) ([]model.Domain, error) {
	rows, err := s.DB.QueryContext(ctx, domainSelect+` WHERE d.org_id = ? ORDER BY d.is_shared, d.name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Domain{}
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// Domain fetches one domain.
func (s *Service) Domain(ctx context.Context, orgID, id int64) (*model.Domain, error) {
	d, err := scanDomain(s.DB.QueryRowContext(ctx, domainSelect+` WHERE d.org_id = ? AND d.id = ?`, orgID, id))
	if db.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return d, err
}

func (s *Service) domainByName(ctx context.Context, q db.Querier, orgID int64, name string) (*model.Domain, error) {
	d, err := scanDomain(q.QueryRowContext(ctx, domainSelect+` WHERE d.org_id = ? AND d.name = ?`, orgID, strings.ToLower(name)))
	if db.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return d, err
}

func (s *Service) upsertDomain(ctx context.Context, q db.Querier, orgID int64, d purelymail.Domain) (int64, error) {
	now := db.Now()
	b := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO domains(org_id, name, is_shared, allow_account_reset, symbolic_subaddressing, dns_mx, dns_spf, dns_dkim, dns_dmarc, dns_checked_at, status, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,'active',?,?)
		ON CONFLICT(org_id, name) DO UPDATE SET is_shared = excluded.is_shared, allow_account_reset = excluded.allow_account_reset, symbolic_subaddressing = excluded.symbolic_subaddressing,
		dns_mx = excluded.dns_mx, dns_spf = excluded.dns_spf, dns_dkim = excluded.dns_dkim, dns_dmarc = excluded.dns_dmarc, dns_checked_at = excluded.dns_checked_at, status = 'active', updated_at = excluded.updated_at`,
		orgID, strings.ToLower(d.Name), b(d.IsShared), b(d.AllowAccountReset), b(d.SymbolicSubaddressing), b(d.DNSSummary.PassesMx), b(d.DNSSummary.PassesSpf), b(d.DNSSummary.PassesDkim), b(d.DNSSummary.PassesDmarc), now, now, now); err != nil {
		return 0, err
	}
	var id int64
	err := q.QueryRowContext(ctx, `SELECT id FROM domains WHERE org_id = ? AND name = ?`, orgID, strings.ToLower(d.Name)).Scan(&id)
	return id, err
}

// OwnershipCode returns the DNS TXT value proving ownership to Purelymail.
func (s *Service) OwnershipCode(ctx context.Context, orgID int64) (string, error) {
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return "", err
	}
	code, err := api.GetOwnershipCode(ctx)
	return code, upstream("purelymail ownership code", err)
}

// DNSGuide describes the records a domain needs.
type DNSGuide struct {
	OwnershipCode string      `json:"ownershipCode"`
	Records       []DNSRecord `json:"records"`
}

// DNSRecord is one suggested DNS record.
type DNSRecord struct {
	Type     string `json:"type"`
	Host     string `json:"host"`
	Value    string `json:"value"`
	Priority int    `json:"priority,omitempty"`
	Purpose  string `json:"purpose"`
}

// DNSGuideFor builds the record list for domain (values per Purelymail docs).
func (s *Service) DNSGuideFor(ctx context.Context, orgID int64, domain string) (*DNSGuide, error) {
	code, err := s.OwnershipCode(ctx, orgID)
	if err != nil {
		return nil, err
	}
	return &DNSGuide{
		OwnershipCode: code,
		Records: []DNSRecord{
			{Type: "TXT", Host: "@", Value: code, Purpose: "ownership"},
			{Type: "MX", Host: "@", Value: "mailserver.purelymail.com.", Priority: 50, Purpose: "mx"},
			{Type: "TXT", Host: "@", Value: "v=spf1 include:_spf.purelymail.com ~all", Purpose: "spf"},
			{Type: "CNAME", Host: "purelymail1._domainkey", Value: "key1.dkimroot.purelymail.com.", Purpose: "dkim"},
			{Type: "CNAME", Host: "purelymail2._domainkey", Value: "key2.dkimroot.purelymail.com.", Purpose: "dkim"},
			{Type: "CNAME", Host: "purelymail3._domainkey", Value: "key3.dkimroot.purelymail.com.", Purpose: "dkim"},
			{Type: "CNAME", Host: "_dmarc", Value: "dmarcroot.purelymail.com.", Purpose: "dmarc"},
		},
	}, nil
}

// AddDomain adds a domain on Purelymail (DNS ownership record must exist).
func (s *Service) AddDomain(ctx context.Context, orgID, actor int64, name string) (*model.Domain, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !validDomain(name) {
		return nil, invalid("%q is not a valid domain name", name)
	}
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if err := api.AddDomain(ctx, name); err != nil {
		return nil, upstream("purelymail add domain", err)
	}
	if err := s.refreshDomains(ctx, orgID, api); err != nil {
		return nil, err
	}
	d, err := s.domainByName(ctx, s.DB, orgID, name)
	if err != nil {
		return nil, err
	}
	s.audit(ctx, orgID, actor, "domain.add", "domain", fmt.Sprint(d.ID), map[string]any{"name": name})
	return d, nil
}

func (s *Service) refreshDomains(ctx context.Context, orgID int64, api purelymail.API) error {
	list, err := api.ListDomains(ctx, true)
	if err != nil {
		return upstream("purelymail list domains", err)
	}
	seen := map[string]bool{}
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		for _, d := range list {
			seen[strings.ToLower(d.Name)] = true
			if _, err := s.upsertDomain(ctx, tx, orgID, d); err != nil {
				return err
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT id, name FROM domains WHERE org_id = ?`, orgID)
		if err != nil {
			return err
		}
		var gone []int64
		for rows.Next() {
			var id int64
			var name string
			rows.Scan(&id, &name)
			if !seen[name] {
				gone = append(gone, id)
			}
		}
		rows.Close()
		for _, id := range gone {
			if _, err := tx.ExecContext(ctx, `UPDATE domains SET status = 'removed', updated_at = ? WHERE id = ?`, db.Now(), id); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// RecheckDomain asks Purelymail to re-verify DNS and refreshes the summary.
func (s *Service) RecheckDomain(ctx context.Context, orgID, actor, id int64) (*model.Domain, error) {
	d, err := s.Domain(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if !d.IsShared {
		if err := api.UpdateDomainSettings(ctx, purelymail.UpdateDomainSettingsRequest{Name: d.Name, RecheckDNS: true}); err != nil {
			return nil, upstream("purelymail recheck dns", err)
		}
	}
	if err := s.refreshDomains(ctx, orgID, api); err != nil {
		return nil, err
	}
	return s.Domain(ctx, orgID, id)
}

// DomainSettingsInput toggles domain options.
type DomainSettingsInput struct {
	AllowAccountReset     *bool `json:"allowAccountReset"`
	SymbolicSubaddressing *bool `json:"symbolicSubaddressing"`
}

// UpdateDomainSettings changes Purelymail-side domain options.
func (s *Service) UpdateDomainSettings(ctx context.Context, orgID, actor, id int64, in DomainSettingsInput) (*model.Domain, error) {
	d, err := s.Domain(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if d.IsShared {
		return nil, invalid("shared Purelymail domains cannot be configured")
	}
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if err := api.UpdateDomainSettings(ctx, purelymail.UpdateDomainSettingsRequest{Name: d.Name, AllowAccountReset: in.AllowAccountReset, SymbolicSubaddressing: in.SymbolicSubaddressing}); err != nil {
		return nil, upstream("purelymail update domain", err)
	}
	if err := s.refreshDomains(ctx, orgID, api); err != nil {
		return nil, err
	}
	s.audit(ctx, orgID, actor, "domain.update", "domain", fmt.Sprint(id), in)
	return s.Domain(ctx, orgID, id)
}

// DeleteDomain removes a domain from Purelymail. This deletes every user and
// rule on it, so callers must have confirmed with the exact domain name.
func (s *Service) DeleteDomain(ctx context.Context, orgID, actor, id int64, confirmName string) error {
	d, err := s.Domain(ctx, orgID, id)
	if err != nil {
		return err
	}
	if d.IsShared {
		return invalid("shared Purelymail domains cannot be removed")
	}
	if !strings.EqualFold(strings.TrimSpace(confirmName), d.Name) {
		return invalid("type the domain name exactly to confirm deletion")
	}
	api, err := s.pm(ctx, orgID)
	if err != nil {
		return err
	}
	if err := api.DeleteDomain(ctx, d.Name); err != nil {
		return upstream("purelymail delete domain", err)
	}
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM addresses WHERE domain_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM mailboxes WHERE domain_id = ?`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM domains WHERE id = ?`, id)
		return err
	})
	if err != nil {
		return err
	}
	s.audit(ctx, orgID, actor, "domain.delete", "domain", fmt.Sprint(id), map[string]any{"name": d.Name})
	return nil
}
