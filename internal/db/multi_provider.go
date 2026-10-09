package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"mailhearth/internal/provider"
)

func validateLegacyInputs(ctx context.Context, tx *sql.Tx, in MigrationInputs) error {
	var object string
	var objectID, relatedID int64
	err := tx.QueryRowContext(ctx, `SELECT 'mailbox_owner',b.id,m.id FROM mailboxes b JOIN members m ON m.id=b.owner_member_id WHERE b.org_id!=m.org_id UNION ALL SELECT 'mailbox_domain',b.id,d.id FROM mailboxes b JOIN domains d ON d.id=b.domain_id WHERE b.org_id!=d.org_id UNION ALL SELECT 'address_domain',a.id,d.id FROM addresses a JOIN domains d ON d.id=a.domain_id WHERE a.org_id!=d.org_id UNION ALL SELECT 'mailbox_access',a.id,m.id FROM mailbox_access a JOIN members m ON m.id=a.member_id JOIN mailboxes b ON b.id=a.mailbox_id WHERE m.org_id!=b.org_id UNION ALL SELECT 'member_role',m.id,r.id FROM members m JOIN roles r ON r.id=m.role_id WHERE m.org_id!=r.org_id LIMIT 1`).Scan(&object, &objectID, &relatedID)
	if err == nil {
		return fmt.Errorf("migration_association_conflict: %s %d %d", object, objectID, relatedID)
	}
	if !IsNotFound(err) {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT o.id, (SELECT COUNT(*) FROM purelymail_accounts a WHERE a.org_id=o.id),
		(SELECT COUNT(*) FROM mailboxes b WHERE b.org_id=o.id)+(SELECT COUNT(*) FROM domains d WHERE d.org_id=o.id)+(SELECT COUNT(*) FROM addresses a WHERE a.org_id=o.id)
		FROM organizations o`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var orgID, accounts, resources int64
		if err := rows.Scan(&orgID, &accounts, &resources); err != nil {
			rows.Close()
			return err
		}
		if accounts > 1 {
			rows.Close()
			return fmt.Errorf("migration_ambiguous_connection: organization %d", orgID)
		}
		if resources > 0 && accounts == 0 {
			rows.Close()
			return fmt.Errorf("migration_missing_connection: organization %d", orgID)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, `SELECT 'api',id,api_token_enc FROM purelymail_accounts UNION ALL SELECT 'mail',id,credential_enc FROM mailboxes WHERE credential_enc!=''`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var kind, sealed string
		var id int64
		if err := rows.Scan(&kind, &id, &sealed); err != nil {
			rows.Close()
			return err
		}
		if in.CredentialsBox == nil {
			rows.Close()
			return fmt.Errorf("migration_credentials_box_required: %s %d", kind, id)
		}
		if _, err := in.CredentialsBox.Open(sealed); err != nil {
			rows.Close()
			return fmt.Errorf("migration_credential_decryption_failed: %s %d", kind, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, table := range []string{"mailboxes", "addresses"} {
		column := "address"
		if table == "mailboxes" {
			column = "pm_user"
		}
		var first, second int64
		err := tx.QueryRowContext(ctx, `SELECT a.id,b.id FROM `+table+` a JOIN `+table+` b ON a.org_id=b.org_id AND LOWER(a.`+column+`)=LOWER(b.`+column+`) AND a.id<b.id LIMIT 1`).Scan(&first, &second)
		if err == nil {
			return fmt.Errorf("migration_duplicate_address: %s %d %d", table, first, second)
		}
		if !IsNotFound(err) {
			return err
		}
	}
	return nil
}

func validateAssociationScopes(ctx context.Context, tx *sql.Tx) error {
	var object, objectID string
	var relatedID sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT object_type,object_id,related_id FROM association_scope_errors LIMIT 1`).Scan(&object, &objectID, &relatedID)
	if IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("migration_association_conflict: %s %s %d", object, objectID, relatedID.Int64)
}

func legacyTemplate(addr, mode string, allowPlain bool) (provider.ProtocolTemplate, error) {
	if addr == "" {
		return provider.ProtocolTemplate{}, nil
	}
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return provider.ProtocolTemplate{}, fmt.Errorf("migration_invalid_endpoint: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || host == "" || port < 1 || port > 65535 {
		return provider.ProtocolTemplate{}, fmt.Errorf("migration_invalid_endpoint")
	}
	if mode != "tls" && mode != "starttls" && !(mode == "none" && allowPlain) {
		return provider.ProtocolTemplate{}, fmt.Errorf("migration_unsupported_tls_mode")
	}
	return provider.ProtocolTemplate{Enabled: true, Host: host, Port: port, TLSMode: mode}, nil
}

func convertLegacyProtocols(ctx context.Context, tx *sql.Tx, in MigrationInputs) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM mail_connections`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	if in.IMAPAddr == "" || in.SMTPAddr == "" {
		return fmt.Errorf("migration_protocol_inputs_required: IMAPAddr SMTPAddr")
	}
	imap, err := legacyTemplate(in.IMAPAddr, in.IMAPTLS, in.AllowDevelopmentPlaintext)
	if err != nil {
		return err
	}
	smtp, err := legacyTemplate(in.SMTPAddr, in.SMTPTLS, in.AllowDevelopmentPlaintext)
	if err != nil {
		return err
	}
	sieve, err := legacyTemplate(in.SieveAddr, in.SieveTLS, in.AllowDevelopmentPlaintext)
	if err != nil {
		return err
	}
	templates, err := json.Marshal(provider.ProtocolTemplates{IMAP: imap, SMTP: smtp, ManageSieve: sieve})
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mail_connections SET protocol_defaults_json=?`, string(templates)); err != nil {
		return err
	}
	if !sieve.Enabled {
		if _, err := tx.ExecContext(ctx, `UPDATE mailbox_endpoints SET network_mode='disabled',username=NULL,credential_id=NULL WHERE protocol='managesieve'`); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,address FROM mailboxes`)
	if err != nil {
		return err
	}
	type addressRow struct {
		id      int64
		address string
	}
	var addresses []addressRow
	for rows.Next() {
		var a addressRow
		if err := rows.Scan(&a.id, &a.address); err != nil {
			rows.Close()
			return err
		}
		if _, err := provider.ValidateAddress(a.address); err != nil {
			rows.Close()
			return fmt.Errorf("migration_invalid_address: mailbox %d", a.id)
		}
		addresses = append(addresses, a)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, a := range addresses {
		if _, err := tx.ExecContext(ctx, `UPDATE mailboxes SET address_key=? WHERE id=?`, strings.ToLower(a.address), a.id); err != nil {
			return err
		}
	}
	return nil
}
