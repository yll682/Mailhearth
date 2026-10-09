package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
	"mailhearth/internal/provider/manual"
	"mailhearth/internal/provider/migadu"
	pmprovider "mailhearth/internal/provider/purelymail"
	"mailhearth/internal/secrets"
)

func init() {
	provider.Register(provider.Purelymail, func(base, user, key string) provider.Provider { return pmprovider.New(base, user, key) })
	provider.Register(provider.Migadu, func(base, user, key string) provider.Provider { return migadu.New(base, user, key) })
	provider.Register(provider.Manual, func(base, user, key string) provider.Provider { return manual.New(base, user, key) })
}

type ConnectionView struct {
	ID                 int64                          `json:"id"`
	ProviderKind       provider.ProviderKind          `json:"providerKind"`
	Label              string                         `json:"label"`
	Enabled            bool                           `json:"enabled"`
	Revision           int64                          `json:"revision"`
	APIBaseURL         *string                        `json:"apiBaseUrl"`
	APIUsername        *string                        `json:"apiUsername"`
	APIConfigured      bool                           `json:"apiConfigured"`
	APIHint            string                         `json:"apiHint"`
	DomainScope        provider.DomainScope           `json:"domainScope"`
	ProtocolDefaults   provider.ProtocolTemplates     `json:"protocolDefaults"`
	LastAPICheckAt     *string                        `json:"lastApiCheckAt"`
	LastAPICheckStatus string                         `json:"lastApiCheckStatus"`
	LastAPIErrorCode   *string                        `json:"lastApiErrorCode"`
	LastSyncAt         *string                        `json:"lastSyncAt"`
	CreatedAt          string                         `json:"createdAt"`
	UpdatedAt          string                         `json:"updatedAt"`
	Capabilities       map[string]provider.Capability `json:"capabilities,omitempty"`
}

const connectionSelect = `SELECT mc.id,mc.provider_kind,mc.label,mc.enabled,mc.revision,mc.api_base_url,mc.api_username,
	EXISTS(SELECT 1 FROM credentials c WHERE c.id=mc.api_credential_id AND c.state='active'),COALESCE(c.hint,''),
	mc.domain_scope_json,mc.protocol_defaults_json,mc.last_api_check_at,mc.last_api_check_status,mc.last_api_error_code,mc.last_sync_at,mc.created_at,mc.updated_at
	FROM mail_connections mc LEFT JOIN credentials c ON c.id=mc.api_credential_id`

func scanConnection(row interface{ Scan(...any) error }) (*ConnectionView, error) {
	var c ConnectionView
	var base, user, checked, code, synced sql.NullString
	var scope, protocols string
	if err := row.Scan(&c.ID, &c.ProviderKind, &c.Label, &c.Enabled, &c.Revision, &base, &user, &c.APIConfigured, &c.APIHint, &scope, &protocols, &checked, &c.LastAPICheckStatus, &code, &synced, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	c.APIBaseURL, c.APIUsername, c.LastAPICheckAt, c.LastAPIErrorCode, c.LastSyncAt = nullStr(base), nullStr(user), nullStr(checked), nullStr(code), nullStr(synced)
	if err := json.Unmarshal([]byte(scope), &c.DomainScope); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(protocols), &c.ProtocolDefaults); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Service) Connections(ctx context.Context, orgID int64) ([]ConnectionView, error) {
	rows, err := s.DB.QueryContext(ctx, connectionSelect+` WHERE mc.org_id=? ORDER BY mc.id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConnectionView{}
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *Service) MailConnection(ctx context.Context, orgID, id int64) (*ConnectionView, error) {
	c, err := scanConnection(s.DB.QueryRowContext(ctx, connectionSelect+` WHERE mc.org_id=? AND mc.id=?`, orgID, id))
	if db.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return c, err
}

type CreateConnectionInput struct {
	ProviderKind     provider.ProviderKind       `json:"providerKind"`
	Label            string                      `json:"label"`
	APIAuth          *provider.APIAuth           `json:"apiAuth"`
	DomainScope      *provider.DomainScope       `json:"domainScope"`
	ProtocolDefaults *provider.ProtocolTemplates `json:"protocolDefaults"`
}

func validateConnectionInput(in *CreateConnectionInput) (string, error) {
	in.Label = strings.TrimSpace(in.Label)
	if utf8.RuneCountInString(in.Label) < 1 || utf8.RuneCountInString(in.Label) > 100 {
		return "", provider.Errorf("invalid", "连接名称长度必须为 1–100")
	}
	if in.ProviderKind != provider.Manual && in.ProviderKind != provider.Purelymail && in.ProviderKind != provider.Migadu {
		return "", provider.Errorf("invalid", "服务商类型无效")
	}
	if in.ProtocolDefaults == nil {
		t := provider.DefaultTemplates(in.ProviderKind)
		in.ProtocolDefaults = &t
	}
	if err := provider.ValidateProtocolTemplates(*in.ProtocolDefaults); err != nil {
		return "", err
	}
	if in.DomainScope == nil {
		in.DomainScope = &provider.DomainScope{Mode: "all"}
	}
	scope, err := provider.NormalizeScope(*in.DomainScope)
	if err != nil {
		return "", err
	}
	in.DomainScope = &scope
	if in.ProviderKind == provider.Manual {
		if in.APIAuth != nil || scope.Mode != "all" {
			return "", provider.Errorf("invalid", "手动连接必须使用 all 范围且 API 认证为空")
		}
		return "", nil
	}
	if in.APIAuth == nil || in.APIAuth.APIKey == "" {
		return "", provider.Errorf("invalid", "API key 不能为空")
	}
	if in.ProviderKind == provider.Migadu {
		if _, err := NormalizeEmail(in.APIAuth.Username); err != nil {
			return "", err
		}
		return "https://api.migadu.com/v1", nil
	}
	if in.APIAuth.Username != "" {
		return "", provider.Errorf("invalid", "Purelymail 不接受 API username")
	}
	return "https://purelymail.com/api/v0", nil
}

func (s *Service) CreateConnection(ctx context.Context, orgID, actor int64, in CreateConnectionInput) (*ConnectionView, error) {
	if err := requireManagementPermission(ctx, s.DB, orgID, actor, model.PermOrgManage); err != nil {
		return nil, err
	}
	base, err := validateConnectionInput(&in)
	if err != nil {
		return nil, err
	}
	var key, user string
	if in.APIAuth != nil {
		key, user = in.APIAuth.APIKey, in.APIAuth.Username
	}
	api, err := provider.Create(in.ProviderKind, base, user, key)
	if err != nil {
		return nil, err
	}
	result, err := api.ValidateConnection(ctx, provider.ValidateConnectionRequest{APIBaseURL: base, APIKey: key, Username: user, Protocols: *in.ProtocolDefaults})
	if err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, provider.Errorf("provider_auth_failed", "管理连接验证失败")
	}
	var enc string
	if key != "" {
		enc, err = s.Box.Seal(key)
		if err != nil {
			return nil, err
		}
	}
	scope, err := provider.JSONString(in.DomainScope)
	if err != nil {
		return nil, err
	}
	protocols, err := provider.JSONString(in.ProtocolDefaults)
	if err != nil {
		return nil, err
	}
	var id int64
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := requireManagementPermission(ctx, tx, orgID, actor, model.PermOrgManage); err != nil {
			return err
		}
		now := db.Now()
		var baseValue, userValue any
		if base != "" {
			baseValue = base
		}
		if user != "" {
			userValue = user
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO mail_connections(org_id,provider_kind,label,enabled,revision,api_base_url,api_username,domain_scope_json,protocol_defaults_json,last_api_check_at,last_api_check_status,created_at,updated_at) VALUES (?,?,?,1,1,?,?,?,?,?,'passed',?,?)`, orgID, in.ProviderKind, in.Label, baseValue, userValue, scope, protocols, now, now, now)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		if enc != "" {
			res, err = tx.ExecContext(ctx, `INSERT INTO credentials(org_id,connection_id,purpose,source,secret_enc,generation,state,hint,created_at,updated_at) VALUES (?,?,'api','entered',?,1,'active',?,?,?)`, orgID, id, enc, secrets.Hint(key), now, now)
			if err != nil {
				return err
			}
			cid, err := res.LastInsertId()
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `UPDATE mail_connections SET api_credential_id=? WHERE id=?`, cid, id)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.audit(ctx, orgID, actor, "connection.create", "connection", fmtID(id), nil)
	return s.MailConnection(ctx, orgID, id)
}

func fmtID(id int64) string { return strconv.FormatInt(id, 10) }

type UpdateConnectionInput struct {
	RequestID        string                      `json:"requestId"`
	ExpectedRevision int64                       `json:"expectedRevision"`
	Label            *string                     `json:"label"`
	Enabled          *bool                       `json:"enabled"`
	APIAuth          *provider.APIAuth           `json:"apiAuth,omitempty"`
	DomainScope      *provider.DomainScope       `json:"domainScope,omitempty"`
	ProtocolDefaults *provider.ProtocolTemplates `json:"protocolDefaults,omitempty"`
}

func (s *Service) UpdateConnection(ctx context.Context, orgID, actor, id int64, in UpdateConnectionInput) (*ConnectionView, error) {
	if err := requireManagementPermission(ctx, s.DB, orgID, actor, model.PermOrgManage); err != nil {
		return nil, err
	}
	if in.APIAuth != nil || in.DomainScope != nil || in.ProtocolDefaults != nil {
		return nil, provider.Errorf("invalid", "配置验证需要通过 Operation 执行")
	}
	c, err := s.MailConnection(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if in.ExpectedRevision != c.Revision {
		return nil, provider.Errorf("revision_conflict", "连接已更新，请重新读取")
	}
	if in.Label != nil {
		c.Label = strings.TrimSpace(*in.Label)
		if utf8.RuneCountInString(c.Label) < 1 || utf8.RuneCountInString(c.Label) > 100 {
			return nil, provider.Errorf("invalid", "连接名称长度必须为 1–100")
		}
	}
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := requireManagementPermission(ctx, tx, orgID, actor, model.PermOrgManage); err != nil {
			return err
		}
		if err := requireResourceAvailable(ctx, tx, orgID, "connection:"+fmtID(id)); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE mail_connections SET label=?,enabled=?,revision=revision+1,updated_at=? WHERE id=? AND org_id=? AND revision=?`, c.Label, c.Enabled, db.Now(), id, orgID, in.ExpectedRevision)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return provider.Errorf("revision_conflict", "连接已更新，请重新读取")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if s.Pool != nil {
		s.Pool.InvalidateConnection(id)
	}
	s.audit(ctx, orgID, actor, "connection.update", "connection", fmtID(id), nil)
	if err := s.cancelConnectionMailRequests(id); err != nil {
		return nil, err
	}
	return s.MailConnection(ctx, orgID, id)
}

func (s *Service) TestConnection(ctx context.Context, orgID, actor, id int64) (provider.ValidateConnectionResult, error) {
	if err := requireManagementPermission(ctx, s.DB, orgID, actor, model.PermOrgManage); err != nil {
		return provider.ValidateConnectionResult{}, err
	}
	api, c, err := s.connectionAdapter(ctx, orgID, id)
	if err != nil {
		return provider.ValidateConnectionResult{}, err
	}
	var key, base, user string
	if c.APIBaseURL != nil {
		base = *c.APIBaseURL
	}
	if c.APIUsername != nil {
		user = *c.APIUsername
	}
	if c.ProviderKind != provider.Manual {
		var enc string
		if err := s.DB.QueryRowContext(ctx, `SELECT cr.secret_enc FROM mail_connections mc JOIN credentials cr ON cr.id=mc.api_credential_id WHERE mc.org_id=? AND mc.id=?`, orgID, id).Scan(&enc); err != nil {
			return provider.ValidateConnectionResult{}, err
		}
		key, err = s.Box.Open(enc)
		if err != nil {
			return provider.ValidateConnectionResult{}, provider.Errorf("credential_decryption_failed", "API 凭据解密失败")
		}
	}
	result, checkErr := api.ValidateConnection(ctx, provider.ValidateConnectionRequest{APIBaseURL: base, Username: user, APIKey: key, Protocols: c.ProtocolDefaults})
	status := "passed"
	var code any
	if checkErr != nil || !result.OK {
		status = "failed"
		code = "upstream_failed"
		var typed *provider.TypedError
		if errors.As(checkErr, &typed) {
			code = typed.Code
		}
	}
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := requireManagementPermission(ctx, tx, orgID, actor, model.PermOrgManage); err != nil {
			return err
		}
		if err := requireResourceAvailable(ctx, tx, orgID, "connection:"+fmtID(id)); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE mail_connections SET last_api_check_at=?,last_api_check_status=?,last_api_error_code=? WHERE id=? AND org_id=? AND revision=?`, db.Now(), status, code, id, orgID, c.Revision)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return provider.Errorf("revision_conflict", "连接配置已经更新")
		}
		return nil
	})
	if err != nil {
		return provider.ValidateConnectionResult{}, err
	}
	if !result.OK && checkErr == nil {
		checkErr = provider.Errorf("upstream_failed", "连接验证失败")
	}
	return result, checkErr
}

func (s *Service) DeleteConnection(ctx context.Context, orgID, actor, id, revision int64, label string) error {
	if err := requireManagementPermission(ctx, s.DB, orgID, actor, model.PermOrgManage); err != nil {
		return err
	}
	c, err := s.MailConnection(ctx, orgID, id)
	if err != nil {
		return err
	}
	if c.Revision != revision {
		return provider.Errorf("revision_conflict", "连接已更新，请重新读取")
	}
	if c.Label != label {
		return provider.Errorf("invalid", "请输入完整连接名称")
	}
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := requireManagementPermission(ctx, tx, orgID, actor, model.PermOrgManage); err != nil {
			return err
		}
		var dependencies struct {
			Mailboxes      int `json:"mailboxes"`
			Addresses      int `json:"addresses"`
			DomainBindings int `json:"domainBindings"`
			Resources      int `json:"providerResources"`
			Operations     int `json:"operations"`
		}
		if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM mailboxes WHERE connection_id=?),(SELECT COUNT(*) FROM addresses WHERE connection_id=?),(SELECT COUNT(*) FROM domain_bindings WHERE connection_id=?),(SELECT COUNT(*) FROM provider_resources WHERE connection_id=?),(SELECT COUNT(*) FROM operations o WHERE org_id=? AND status IN ('queued','running','unknown','needs_action') AND (target_connection_id=? OR EXISTS(SELECT 1 FROM operation_locks l WHERE l.operation_id=o.id AND l.org_id=o.org_id AND l.resource_key=?)))`, id, id, id, id, orgID, id, "connection:"+fmtID(id)).Scan(&dependencies.Mailboxes, &dependencies.Addresses, &dependencies.DomainBindings, &dependencies.Resources, &dependencies.Operations); err != nil {
			return err
		}
		if dependencies.Mailboxes+dependencies.Addresses+dependencies.DomainBindings+dependencies.Resources+dependencies.Operations != 0 {
			typed := provider.Errorf("connection_in_use", "连接仍有关联资源或未完成操作")
			typed.Details = dependencies
			return typed
		}
		var retryable bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operations WHERE org_id=? AND target_connection_id=? AND status='failed' AND payload_enc IS NOT NULL)`, orgID, id).Scan(&retryable); err != nil {
			return err
		}
		if retryable {
			return provider.Errorf("connection_in_use", "连接仍有需要处理或取消的失败操作")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE operations SET result_json=json_set(result_json,'$.unregisteredConnectionId',?),target_connection_id=NULL,updated_at=? WHERE org_id=? AND target_connection_id=? AND status IN ('succeeded','cancelled')`, id, db.Now(), orgID, id); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE mail_connections SET api_credential_id=NULL WHERE id=? AND org_id=? AND revision=? AND label=?`, id, orgID, revision, label)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return provider.Errorf("revision_conflict", "连接已更新，请重新读取")
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM credentials WHERE connection_id=? AND purpose='api' AND mailbox_id IS NULL`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(org_id,actor_member_id,action,target_type,target_id,detail_json,created_at) VALUES (?,?,'connection.delete','connection',?,?,?)`, orgID, actor, fmtID(id), toJSON(struct {
			Label                      string                `json:"label"`
			ProviderKind               provider.ProviderKind `json:"providerKind"`
			ExternalRevocationRequired bool                  `json:"externalRevocationRequired"`
		}{c.Label, c.ProviderKind, c.ProviderKind != provider.Manual}), db.Now()); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM mail_connections WHERE id=? AND org_id=? AND revision=?`, id, orgID, revision)
		return err
	})
	if err != nil {
		return err
	}
	if s.Pool != nil {
		s.Pool.InvalidateConnection(id)
	}
	return nil
}

func (s *Service) connectionAdapter(ctx context.Context, orgID, id int64) (provider.Provider, *ConnectionView, error) {
	c, err := s.MailConnection(ctx, orgID, id)
	if err != nil {
		return nil, nil, err
	}
	if !c.Enabled {
		return nil, nil, provider.Errorf("endpoint_disabled", "连接已停用")
	}
	var key, base, user string
	if c.APIBaseURL != nil {
		base = *c.APIBaseURL
	}
	if c.APIUsername != nil {
		user = *c.APIUsername
	}
	if c.ProviderKind != provider.Manual {
		var enc string
		if err := s.DB.QueryRowContext(ctx, `SELECT cr.secret_enc FROM mail_connections mc JOIN credentials cr ON cr.id=mc.api_credential_id AND cr.connection_id=mc.id AND cr.purpose='api' AND cr.state='active' WHERE mc.org_id=? AND mc.id=?`, orgID, id).Scan(&enc); err != nil {
			return nil, nil, err
		}
		key, err = s.Box.Open(enc)
		if err != nil {
			return nil, nil, provider.Errorf("credential_decryption_failed", "API 凭据解密失败")
		}
	}
	if base != "" {
		u, err := url.Parse(base)
		if err != nil || u.Scheme != "https" || u.User != nil {
			return nil, nil, provider.Errorf("invalid", "API URL 无效")
		}
	}
	api, err := provider.Create(c.ProviderKind, base, user, key)
	return api, c, err
}
