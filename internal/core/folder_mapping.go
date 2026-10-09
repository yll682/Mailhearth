package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"mailhearth/internal/db"
	"mailhearth/internal/mailproto/mailops"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

type FolderMappingView struct {
	MailboxID    int64               `json:"mailboxId"`
	Revision     int64               `json:"revision"`
	Mapping      model.FolderMapping `json:"mapping"`
	SentCopyMode string              `json:"sentCopyMode"`
}

type UpdateFolderMappingInput struct {
	ExpectedRevision int64                `json:"expectedRevision"`
	Mapping          *model.FolderMapping `json:"mapping"`
	SentCopyMode     string               `json:"sentCopyMode"`
}

func validateFolderMappingInput(mapping model.FolderMapping) error {
	for _, name := range []*string{mapping.Sent, mapping.Drafts, mapping.Trash, mapping.Junk, mapping.Archive} {
		if name != nil && (*name == "" || len(*name) > 1024 || strings.ContainsAny(*name, "\x00\r\n")) {
			return provider.Errorf("invalid", "文件夹名称无效")
		}
	}
	return nil
}

func (s *Service) validateCandidateFolderMapping(ctx context.Context, items []candidateEndpoint, mapping model.FolderMapping) error {
	if err := validateFolderMappingInput(mapping); err != nil {
		return err
	}
	if mapping == (model.FolderMapping{}) {
		return nil
	}
	for _, item := range items {
		if item.protocol != provider.ProtocolIMAP || item.resolved == nil {
			continue
		}
		if s.Pool == nil {
			return provider.Errorf("internal", "IMAP 连接管理器未启动")
		}
		conn, err := s.Pool.GetFresh(ctx, item.resolved.IMAPCredential())
		if err != nil {
			return err
		}
		defer s.Pool.Put(conn)
		folders, err := mailops.ListFolders(ctx, conn)
		if err != nil {
			return err
		}
		for _, entry := range []struct {
			role string
			name *string
		}{{mailops.RoleSent, mapping.Sent}, {mailops.RoleDrafts, mapping.Drafts}, {mailops.RoleTrash, mapping.Trash}, {mailops.RoleJunk, mapping.Junk}, {mailops.RoleArchive, mapping.Archive}} {
			if entry.name != nil {
				if _, err := mailops.ResolveSpecialFolder(folders, entry.role, entry.name); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return provider.Errorf("endpoint_unconfigured", "文件夹映射需要启用的 IMAP 配置")
}

func (s *Service) FolderMapping(ctx context.Context, orgID, memberID, mailboxID int64) (*FolderMappingView, error) {
	mc, err := s.CheckMailboxAccess(ctx, orgID, memberID, mailboxID)
	if err != nil {
		return nil, err
	}
	mb := mc.Mailbox
	return &FolderMappingView{MailboxID: mb.ID, Revision: mb.Revision, Mapping: mb.FolderMapping, SentCopyMode: mb.SentCopyMode}, nil
}

func (s *Service) UpdateFolderMapping(ctx context.Context, orgID, memberID, mailboxID int64, in UpdateFolderMappingInput) (*FolderMappingView, error) {
	mc, err := s.CheckMailboxAccess(ctx, orgID, memberID, mailboxID)
	if err != nil {
		return nil, err
	}
	mb := mc.Mailbox
	if mc.Level != model.AccessFull {
		return nil, ErrForbidden
	}
	if in.ExpectedRevision < 1 || in.Mapping == nil {
		return nil, provider.Errorf("invalid", "需要 expectedRevision 和完整 mapping")
	}
	if in.ExpectedRevision != mb.Revision {
		return nil, provider.Errorf("revision_conflict", "邮箱配置已更新")
	}
	if in.SentCopyMode != "append" && in.SentCopyMode != "server" {
		return nil, provider.Errorf("invalid", "sentCopyMode 必须为 append 或 server")
	}
	if err := validateFolderMappingInput(*in.Mapping); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	endpoint, err := s.ResolveEndpoint(ctx, orgID, mailboxID, provider.ProtocolIMAP)
	if err != nil {
		return nil, err
	}
	conn, err := s.Pool.Get(ctx, endpoint.IMAPCredential())
	if err != nil {
		return nil, err
	}
	defer s.Pool.Put(conn)
	folders, err := mailops.ListFolders(ctx, conn)
	if err != nil {
		conn.MarkBroken()
		return nil, err
	}
	for _, entry := range []struct {
		role string
		name *string
	}{{mailops.RoleSent, in.Mapping.Sent}, {mailops.RoleDrafts, in.Mapping.Drafts}, {mailops.RoleTrash, in.Mapping.Trash}, {mailops.RoleJunk, in.Mapping.Junk}, {mailops.RoleArchive, in.Mapping.Archive}} {
		if entry.name != nil {
			if _, err := mailops.ResolveSpecialFolder(folders, entry.role, entry.name); err != nil {
				return nil, err
			}
		}
	}
	mapping, err := json.Marshal(in.Mapping)
	if err != nil {
		return nil, err
	}
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		var current bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM members m JOIN mailboxes b ON b.org_id=m.org_id JOIN mail_connections c ON c.id=b.connection_id JOIN mailbox_endpoints ep ON ep.mailbox_id=b.id AND ep.protocol='imap' JOIN credentials cr ON cr.id=ep.credential_id WHERE m.id=? AND m.org_id=? AND m.status='active' AND b.id=? AND b.status='active' AND b.revision=? AND b.access_revision=? AND c.enabled=1 AND c.revision=? AND ep.revision=? AND cr.id=? AND cr.generation=? AND cr.state='active' AND (b.owner_member_id=m.id OR EXISTS(SELECT 1 FROM mailbox_access a WHERE a.mailbox_id=b.id AND a.member_id=m.id AND a.level='full')))`, memberID, orgID, mailboxID, in.ExpectedRevision, mb.AccessRevision, endpoint.ConnectionRevision, endpoint.EndpointRevision, endpoint.CredentialID, endpoint.CredentialGeneration).Scan(&current); err != nil {
			return err
		}
		if !current {
			return provider.Errorf("revision_conflict", "邮箱权限或协议配置已更新")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE mailboxes SET folder_mapping_json=?,sent_copy_mode=?,revision=revision+1,updated_at=? WHERE id=?`, string(mapping), in.SentCopyMode, db.Now(), mailboxID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log(org_id,actor_member_id,action,target_type,target_id,detail_json,created_at) VALUES (?,?,'mailbox.folder_mapping','mailbox',?,?,?)`, orgID, memberID, fmtID(mailboxID), toJSON(in), db.Now())
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.FolderMapping(ctx, orgID, memberID, mailboxID)
}
