package core

import (
	"context"
	"mailhearth/internal/db"
	"mailhearth/internal/model"
)

const domainSelect = `SELECT d.id,d.name,d.created_at,(SELECT COUNT(*) FROM mailboxes b WHERE b.domain_id=d.id AND b.status!='archived'),(SELECT COUNT(*) FROM addresses a WHERE a.domain_id=d.id AND a.kind!='primary') FROM domains d`

func scanDomain(row interface{ Scan(...any) error }) (*model.Domain, error) {
	var d model.Domain
	if err := row.Scan(&d.ID, &d.Name, &d.CreatedAt, &d.MailboxCount, &d.AddressCount); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Service) Domains(ctx context.Context, orgID int64) ([]model.Domain, error) {
	rows, err := s.DB.QueryContext(ctx, domainSelect+` WHERE d.org_id=? ORDER BY d.name`, orgID)
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

func (s *Service) Domain(ctx context.Context, orgID, id int64) (*model.Domain, error) {
	d, err := scanDomain(s.DB.QueryRowContext(ctx, domainSelect+` WHERE d.org_id=? AND d.id=?`, orgID, id))
	if db.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return d, err
}
