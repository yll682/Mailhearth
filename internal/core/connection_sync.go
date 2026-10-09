package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"mailhearth/internal/db"
	"mailhearth/internal/provider"
)

type syncResource struct {
	id                                  int64
	resource                            provider.Resource
	bindingID, mailboxID                sql.NullInt64
	addressID, identityID, forwardingID sql.NullInt64
	observation                         *provider.DiscoverySnapshotResource
	state                               string
}

func resourceDomain(r provider.Resource) string {
	if name := r.RemoteLocator["domain"]; name != "" {
		return strings.ToLower(name)
	}
	_, name := SplitAddress(r.RemoteLocator["address"])
	return strings.ToLower(name)
}

func (s *Service) SyncConnection(ctx context.Context, orgID, connectionID int64) (any, error) {
	api, c, err := s.connectionAdapter(ctx, orgID, connectionID)
	if err != nil {
		return nil, err
	}
	snapshot, err := api.Discover(ctx, provider.DiscoverRequest{Scope: c.DomainScope})
	if err != nil {
		return nil, err
	}
	if !snapshot.Snapshot.Complete {
		return nil, provider.Errorf("upstream_failed", "同步范围读取未完成")
	}
	seen := map[string]provider.DiscoverySnapshotResource{}
	for i := range snapshot.Snapshot.Resources {
		entry := &snapshot.Snapshot.Resources[i]
		entry.Resource.ConnectionID = connectionID
		r := entry.Resource
		key := r.ResourceType + "\x00" + r.RemoteKey + "\x00" + string(r.Purpose)
		if _, exists := seen[key]; exists {
			return nil, provider.Errorf("upstream_failed", "同步结果出现重复资源")
		}
		seen[key] = *entry
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,resource_type,remote_key,purpose,remote_locator_json,domain_binding_id,mailbox_id,address_id,identity_id,mailbox_forwarding_id,remote_state FROM provider_resources WHERE connection_id=? ORDER BY id`, connectionID)
	if err != nil {
		return nil, err
	}
	var resources []syncResource
	for rows.Next() {
		var item syncResource
		var locator string
		if err := rows.Scan(&item.id, &item.resource.ResourceType, &item.resource.RemoteKey, &item.resource.Purpose, &locator, &item.bindingID, &item.mailboxID, &item.addressID, &item.identityID, &item.forwardingID, &item.state); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(locator), &item.resource.RemoteLocator); err != nil {
			rows.Close()
			return nil, err
		}
		resources = append(resources, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	registered := map[string]bool{}
	for _, item := range resources {
		registered[item.resource.ResourceType+"\x00"+item.resource.RemoteKey+"\x00"+string(item.resource.Purpose)] = true
	}
	pending := []provider.DiscoverySnapshotResource{}
	for _, entry := range snapshot.Snapshot.Resources {
		r := entry.Resource
		if !registered[r.ResourceType+"\x00"+r.RemoteKey+"\x00"+string(r.Purpose)] {
			pending = append(pending, entry)
		}
	}
	for i := range resources {
		item := &resources[i]
		if !scopeContainsDomain(c.DomainScope, resourceDomain(item.resource)) {
			item.state = "external"
			continue
		}
		if observation, ok := seen[item.resource.ResourceType+"\x00"+item.resource.RemoteKey+"\x00"+string(item.resource.Purpose)]; ok {
			item.state = "present"
			item.observation = &observation
			continue
		}
		var readErr error
		switch item.resource.ResourceType {
		case "app_password":
			continue
		case "mailbox":
			local, domain := SplitAddress(item.resource.RemoteLocator["address"])
			if domain == "" {
				domain = item.resource.RemoteLocator["domain"]
				local = item.resource.RemoteLocator["localPart"]
			}
			_, readErr = api.GetMailbox(ctx, provider.GetMailboxRequest{Domain: domain, LocalPart: local})
		case "domain":
			info, err := api.GetDomain(ctx, provider.GetDomainRequest{Domain: resourceDomain(item.resource)})
			readErr = err
			if err == nil {
				item.observation = &provider.DiscoverySnapshotResource{Resource: item.resource, Domain: &info}
			}
		default:
			reader, ok := api.(provider.ResourceReader)
			if !ok {
				continue
			}
			observation, err := reader.ReadResource(ctx, provider.ReadResourceRequest{Resource: item.resource})
			readErr = err
			if err == nil {
				item.observation = &observation
			}
		}
		if readErr == nil {
			item.state = "present"
			continue
		}
		var typed *provider.TypedError
		if errors.As(readErr, &typed) {
			switch typed.Code {
			case "not_found":
				item.state = "missing"
				continue
			case "provider_auth_failed", "forbidden":
				item.state = "inaccessible"
				continue
			case "verification_required":
				continue
			}
		}
		return nil, readErr
	}
	counts := map[string]int{"present": 0, "missing": 0, "inaccessible": 0, "external": 0}
	result := map[string]any{"resources": pending}
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		var current bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail_connections WHERE id=? AND org_id=? AND revision=? AND enabled=1)`, connectionID, orgID, c.Revision).Scan(&current); err != nil {
			return err
		}
		if !current {
			return provider.Errorf("revision_conflict", "连接配置已经更新")
		}
		for _, item := range resources {
			counts[item.state]++
			if _, err := tx.ExecContext(ctx, `UPDATE provider_resources SET remote_state=?,last_seen_at=CASE WHEN ?='present' THEN ? ELSE last_seen_at END,updated_at=? WHERE id=? AND connection_id=?`, item.state, item.state, db.Now(), db.Now(), item.id, connectionID); err != nil {
				return err
			}
			if item.observation != nil {
				if err := saveResourceObservation(ctx, tx, connectionID, *item.observation); err != nil {
					return err
				}
				if item.addressID.Valid {
					if err := syncRuleObservation(ctx, tx, orgID, connectionID, item.addressID.Int64, *item.observation); err != nil {
						return err
					}
				}
				if item.bindingID.Valid {
					if err := syncDomainObservation(ctx, tx, orgID, connectionID, item.bindingID.Int64, *item.observation); err != nil {
						return err
					}
				}
			}
			if item.mailboxID.Valid && item.resource.Purpose == provider.PurposeMailbox {
				if _, err := tx.ExecContext(ctx, `UPDATE mailboxes SET remote_state=?,management_mode=CASE WHEN ?='external' THEN 'external' ELSE management_mode END,revision=revision+1,updated_at=? WHERE id=? AND connection_id=? AND remote_state!=?`, item.state, item.state, db.Now(), item.mailboxID.Int64, connectionID, item.state); err != nil {
					return err
				}
			}
			if item.addressID.Valid && item.state != "present" {
				if _, err := tx.ExecContext(ctx, `UPDATE addresses SET sync_state='unknown',management_mode=CASE WHEN ?='external' THEN 'external' ELSE management_mode END,revision=revision+1,updated_at=? WHERE id=? AND connection_id=?`, item.state, db.Now(), item.addressID.Int64, connectionID); err != nil {
					return err
				}
			}
			if item.identityID.Valid && item.state != "present" {
				if _, err := tx.ExecContext(ctx, `UPDATE identities SET authorization_status=CASE WHEN authorization_source='provider' THEN 'unverified' ELSE authorization_status END,revision=revision+1,updated_at=? WHERE id=? AND mailbox_id IN (SELECT id FROM mailboxes WHERE connection_id=?)`, db.Now(), item.identityID.Int64, connectionID); err != nil {
					return err
				}
			}
			if item.bindingID.Valid {
				if _, err := tx.ExecContext(ctx, `UPDATE domain_bindings SET remote_state=?,management_mode=CASE WHEN ?='external' THEN 'external' ELSE management_mode END,revision=revision+1,updated_at=? WHERE id=? AND connection_id=? AND remote_state!=?`, item.state, item.state, db.Now(), item.bindingID.Int64, connectionID, item.state); err != nil {
					return err
				}
			}
		}
		if err := syncForwardingObservations(ctx, tx, orgID, connectionID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE mail_connections SET last_sync_at=?,updated_at=? WHERE id=? AND org_id=?`, db.Now(), db.Now(), connectionID, orgID); err != nil {
			return err
		}
		if len(pending) > 0 {
			body, err := json.Marshal(snapshot.Snapshot.Resources)
			if err != nil {
				return err
			}
			scope, err := json.Marshal(c.DomainScope)
			if err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx, `INSERT INTO discovery_snapshots(connection_id,connection_revision,scope_json,resources_json,complete,created_at,expires_at) VALUES (?,?,?,?,1,?,?)`, connectionID, c.Revision, string(scope), string(body), db.Now(), time.Now().UTC().Add(15*time.Minute).Format(time.RFC3339))
			if err != nil {
				return err
			}
			snapshotID, err := res.LastInsertId()
			if err != nil {
				return err
			}
			result["snapshotId"] = snapshotID
		}
		for state, count := range counts {
			result[state] = count
		}
		return completeLocalOperation(ctx, tx, result)
	})
	return result, err
}
