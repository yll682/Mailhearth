package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
)

var capabilityKeys = []string{"domain.discover", "domain.create", "domain.delete", "domain.dnsRecords", "domain.checkDNS", "mailbox.create", "mailbox.delete", "mailbox.resetPassword", "credential.create", "credential.rotate", "credential.revoke", "remoteAccess.revokeAll", "address.alias", "address.forward", "address.catchall", "address.prefix", "mailbox.forwarding", "identity.authorize", "mail.read", "mail.send", "rules.manage", "vacation.manage"}

func (s *Service) ObjectCapabilities(ctx context.Context, orgID, actor, connectionID, mailboxID int64) (map[string]provider.Capability, error) {
	c, err := s.MailConnection(ctx, orgID, connectionID)
	if err != nil {
		return nil, err
	}
	m, err := s.Member(ctx, orgID, actor)
	if err != nil {
		return nil, err
	}
	permissions, _, err := s.Permissions(ctx, actor)
	if err != nil {
		return nil, err
	}
	var mb *model.Mailbox
	mailPermission := model.PermMailboxesManage
	level := ""
	if mailboxID > 0 {
		mb, err = s.Mailbox(ctx, orgID, mailboxID)
		if err != nil {
			return nil, err
		}
		if mb.ConnectionID != connectionID {
			return nil, ErrNotFound
		}
		if mb.Kind == model.MailboxShared {
			mailPermission = model.PermSharedManage
		}
		if mb.OwnerMemberID != nil && *mb.OwnerMemberID == actor {
			level = model.AccessFull
		} else {
			err = s.DB.QueryRowContext(ctx, `SELECT level FROM mailbox_access WHERE mailbox_id=? AND member_id=?`, mailboxID, actor).Scan(&level)
			if err != nil && !db.IsNotFound(err) {
				return nil, err
			}
		}
	}
	result := map[string]provider.Capability{}
	for _, key := range capabilityKeys {
		cap := provider.Capability{Key: key, Support: "automatic", Readiness: "ready"}
		required := mailPermission
		if strings.HasPrefix(key, "domain.") {
			required = model.PermDomainsManage
		}
		if key == "domain.discover" {
			required = model.PermOrgManage
		}
		cap.PermissionAllowed = m.Status == model.MemberActive && HasPermission(permissions, required)
		if strings.HasPrefix(key, "address.") {
			cap.PermissionAllowed = m.Status == model.MemberActive && HasPermission(permissions, model.PermAddressesManage)
		}
		if key == "mailbox.forwarding" {
			cap.PermissionAllowed = cap.PermissionAllowed && HasPermission(permissions, model.PermAddressesManage)
		}
		switch key {
		case "mail.read":
			cap.PermissionAllowed = m.Status == model.MemberActive && level != ""
		case "mail.send":
			cap.PermissionAllowed = m.Status == model.MemberActive && (level == model.AccessSend || level == model.AccessFull)
		case "rules.manage", "vacation.manage":
			cap.PermissionAllowed = m.Status == model.MemberActive && level == model.AccessFull
		case "remoteAccess.revokeAll":
			cap.Support = "external"
			cap.ReasonCode = "external_action_required"
		}
		protocol := ""
		switch key {
		case "mail.read":
			protocol = provider.ProtocolIMAP
		case "mail.send":
			protocol = provider.ProtocolSMTP
		case "rules.manage", "vacation.manage":
			protocol = provider.ProtocolManageSieve
			if key == "vacation.manage" {
				cap.Constraints.RequiredSieveExtensions = []string{"vacation"}
			}
		}
		if protocol != "" {
			cap.Readiness = "unconfigured"
			cap.ReasonCode = "endpoint_unconfigured"
			if mb != nil {
				var ep *model.ProtocolStatus
				switch protocol {
				case provider.ProtocolIMAP:
					ep = mb.Protocols.IMAP
				case provider.ProtocolSMTP:
					ep = mb.Protocols.SMTP
				case provider.ProtocolManageSieve:
					ep = mb.Protocols.ManageSieve
				}
				if ep != nil {
					cap.Readiness = ep.Readiness
					switch ep.Readiness {
					case "ready":
						cap.ReasonCode = ""
					case "disabled":
						cap.ReasonCode = "endpoint_disabled"
					case "unverified":
						cap.ReasonCode = "verification_required"
					}
				}
			}
			if key == "mail.send" && cap.Readiness == "ready" {
				var allowed bool
				if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identities WHERE mailbox_id=? AND authorization_status='allowed')`, mailboxID).Scan(&allowed); err != nil {
					return nil, err
				}
				if !allowed {
					cap.Readiness = "unverified"
					cap.ReasonCode = "verification_required"
				}
			}
			if key == "vacation.manage" && cap.Readiness == "ready" {
				var body string
				if err := s.DB.QueryRowContext(ctx, `SELECT capabilities_json FROM mailbox_endpoints WHERE mailbox_id=? AND protocol='managesieve'`, mailboxID).Scan(&body); err != nil {
					return nil, err
				}
				var capabilities map[string]string
				if err := json.Unmarshal([]byte(body), &capabilities); err != nil {
					return nil, err
				}
				found := false
				for _, extension := range strings.Fields(capabilities["SIEVE"]) {
					if extension == "vacation" {
						found = true
					}
				}
				if !found {
					cap.Support = "unsupported"
					cap.ReasonCode = "sieve_extension_missing"
				}
			}
		} else if key != "identity.authorize" {
			if c.ProviderKind == provider.Manual {
				cap.Support = "external"
				cap.ReasonCode = "external_action_required"
			} else if !c.APIConfigured {
				cap.Readiness = "unconfigured"
				cap.ReasonCode = "endpoint_unconfigured"
			} else if c.LastAPICheckStatus != "passed" {
				cap.Readiness = "unverified"
				cap.ReasonCode = "verification_required"
			}
			if c.ProviderKind == provider.Migadu {
				switch key {
				case "domain.delete":
					cap.Support = "external"
					cap.ReasonCode = "external_action_required"
				case "address.forward":
					cap.Support = "unsupported"
					cap.ReasonCode = "unsupported_operation"
				case "credential.create", "credential.rotate", "credential.revoke", "address.alias", "address.catchall", "address.prefix", "mailbox.forwarding", "domain.checkDNS":
					cap.Readiness = "unverified"
					cap.ReasonCode = "verification_required"
				}
				if strings.HasPrefix(key, "address.") {
					cap.Constraints.SameDomainOnly = true
					cap.Constraints.SameConnectionOnly = true
				}
			}
			if key == "mailbox.forwarding" {
				cap.Constraints.AllowedDeliveryModes = []string{"redirect"}
				if c.ProviderKind == provider.Migadu {
					cap.Constraints.AllowedDeliveryModes = []string{}
				}
			}
			if mb != nil && strings.HasPrefix(key, "credential.") {
				var source sql.NullString
				if err := s.DB.QueryRowContext(ctx, `SELECT source FROM credentials WHERE mailbox_id=? AND purpose='mail' AND state='active' ORDER BY generation DESC LIMIT 1`, mailboxID).Scan(&source); err != nil && !db.IsNotFound(err) {
					return nil, err
				}
				if source.Valid && source.String == "entered" {
					cap.Support = "external"
					cap.ReasonCode = "external_action_required"
				}
			}
			if mb != nil && mb.ManagementMode != "api" {
				cap.Support = "external"
				cap.ReasonCode = "external_action_required"
			}
			if mb != nil && mb.RemoteState != "present" && mb.ManagementMode == "api" {
				cap.Readiness = "unverified"
				cap.ReasonCode = "verification_required"
			}
		}
		if !c.Enabled || (protocol != "" && mb != nil && mb.Status != model.MailboxActive) {
			cap.Readiness = "disabled"
			cap.ReasonCode = "endpoint_disabled"
		}
		result[key] = cap
	}
	return result, nil
}
