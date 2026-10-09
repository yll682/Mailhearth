UPDATE operations SET target_connection_id=NULLIF(target_connection_id,0),target_mailbox_id=NULLIF(target_mailbox_id,0);

CREATE TRIGGER credential_ownership_immutable BEFORE UPDATE OF org_id,connection_id,mailbox_id,purpose ON credentials
WHEN NEW.org_id!=OLD.org_id OR NEW.connection_id!=OLD.connection_id OR NEW.mailbox_id IS NOT OLD.mailbox_id OR NEW.purpose!=OLD.purpose BEGIN
 SELECT RAISE(ABORT,'credential_ownership_immutable');
END;
CREATE TRIGGER endpoint_ownership_immutable BEFORE UPDATE OF mailbox_id,protocol ON mailbox_endpoints
WHEN NEW.mailbox_id!=OLD.mailbox_id OR NEW.protocol!=OLD.protocol BEGIN
 SELECT RAISE(ABORT,'endpoint_ownership_immutable');
END;
CREATE TRIGGER binding_ownership_immutable BEFORE UPDATE OF org_id,connection_id,domain_id ON domain_bindings
WHEN NEW.org_id!=OLD.org_id OR NEW.connection_id!=OLD.connection_id OR NEW.domain_id!=OLD.domain_id BEGIN
 SELECT RAISE(ABORT,'binding_ownership_immutable');
END;
CREATE TRIGGER identity_ownership_immutable BEFORE UPDATE OF mailbox_id ON identities WHEN NEW.mailbox_id!=OLD.mailbox_id BEGIN
 SELECT RAISE(ABORT,'identity_ownership_immutable');
END;
CREATE TRIGGER member_org_immutable BEFORE UPDATE OF org_id ON members WHEN NEW.org_id!=OLD.org_id BEGIN
 SELECT RAISE(ABORT,'member_org_immutable');
END;
CREATE TRIGGER role_org_immutable BEFORE UPDATE OF org_id ON roles WHEN NEW.org_id!=OLD.org_id BEGIN
 SELECT RAISE(ABORT,'role_org_immutable');
END;
CREATE TRIGGER domain_org_immutable BEFORE UPDATE OF org_id ON domains WHEN NEW.org_id!=OLD.org_id BEGIN
 SELECT RAISE(ABORT,'domain_org_immutable');
END;
CREATE TRIGGER group_org_immutable BEFORE UPDATE OF org_id ON groups WHEN NEW.org_id!=OLD.org_id BEGIN
 SELECT RAISE(ABORT,'group_org_immutable');
END;
CREATE TRIGGER forwarding_ownership_immutable BEFORE UPDATE OF mailbox_id ON mailbox_forwardings WHEN NEW.mailbox_id!=OLD.mailbox_id BEGIN
 SELECT RAISE(ABORT,'forwarding_ownership_immutable');
END;
CREATE TRIGGER address_ownership_immutable BEFORE UPDATE OF org_id,connection_id ON addresses
WHEN NEW.org_id!=OLD.org_id OR NEW.connection_id!=OLD.connection_id BEGIN
 SELECT RAISE(ABORT,'address_ownership_immutable');
END;

CREATE VIEW additional_scope_errors AS
SELECT 'group_member' AS object_type,g.group_id AS object_id,g.member_id AS related_id FROM group_members g
WHERE NOT EXISTS(SELECT 1 FROM groups team JOIN members m ON m.org_id=team.org_id WHERE team.id=g.group_id AND m.id=g.member_id)
UNION ALL
SELECT 'credential_purpose',c.id,c.connection_id FROM credentials c
WHERE (c.purpose='api' AND c.mailbox_id IS NOT NULL) OR (c.purpose='mail' AND c.mailbox_id IS NULL)
UNION ALL
SELECT 'mailbox_owner',b.id,b.owner_member_id FROM mailboxes b WHERE b.kind='shared' AND b.owner_member_id IS NOT NULL
UNION ALL
SELECT 'operation_actor',o.id,o.actor_member_id FROM operations o WHERE o.actor_member_id IS NOT NULL
AND NOT EXISTS(SELECT 1 FROM members m WHERE m.id=o.actor_member_id AND m.org_id=o.org_id)
UNION ALL
SELECT 'operation_connection',o.id,o.target_connection_id FROM operations o JOIN mail_connections c ON c.id=o.target_connection_id WHERE c.org_id!=o.org_id
UNION ALL
SELECT 'operation_mailbox',o.id,o.target_mailbox_id FROM operations o JOIN mailboxes b ON b.id=o.target_mailbox_id
WHERE b.org_id!=o.org_id OR (o.target_connection_id IS NOT NULL AND b.connection_id!=o.target_connection_id);

CREATE TRIGGER group_member_scope_insert AFTER INSERT ON group_members BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM additional_scope_errors WHERE object_type='group_member' AND object_id=NEW.group_id AND related_id=NEW.member_id);
END;
CREATE TRIGGER group_member_scope_update AFTER UPDATE ON group_members BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM additional_scope_errors WHERE object_type='group_member' AND object_id=NEW.group_id AND related_id=NEW.member_id);
END;
CREATE TRIGGER credential_purpose_insert AFTER INSERT ON credentials BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM additional_scope_errors WHERE object_type='credential_purpose' AND object_id=NEW.id);
END;
CREATE TRIGGER mailbox_owner_insert AFTER INSERT ON mailboxes BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE NEW.kind='shared' AND NEW.owner_member_id IS NOT NULL;
END;
CREATE TRIGGER mailbox_owner_update AFTER UPDATE ON mailboxes BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE NEW.kind='shared' AND NEW.owner_member_id IS NOT NULL;
END;
CREATE TRIGGER connection_credential_scope_insert AFTER INSERT ON mail_connections BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM association_scope_errors WHERE object_type='connection_credential' AND object_id=NEW.id);
END;
CREATE TRIGGER operation_scope_insert AFTER INSERT ON operations BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM additional_scope_errors WHERE object_type IN ('operation_actor','operation_connection','operation_mailbox') AND object_id=NEW.id);
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE NEW.target_connection_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM mail_connections WHERE id=NEW.target_connection_id);
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE NEW.target_mailbox_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM mailboxes WHERE id=NEW.target_mailbox_id);
END;
CREATE TRIGGER operation_scope_update AFTER UPDATE OF org_id,actor_member_id,target_connection_id,target_mailbox_id ON operations BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM additional_scope_errors WHERE object_type IN ('operation_actor','operation_connection','operation_mailbox') AND object_id=NEW.id);
END;
CREATE TRIGGER mailbox_history_restrict BEFORE DELETE ON mailboxes BEGIN
 SELECT RAISE(ABORT,'mailbox_has_history') WHERE EXISTS(SELECT 1 FROM submissions WHERE mailbox_id=OLD.id) OR EXISTS(SELECT 1 FROM message_state WHERE mailbox_id=OLD.id) OR EXISTS(SELECT 1 FROM mail_activity WHERE mailbox_id=OLD.id);
END;
