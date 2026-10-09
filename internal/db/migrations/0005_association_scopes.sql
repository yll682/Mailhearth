CREATE VIEW association_scope_errors AS
SELECT 'mailbox' AS object_type,b.id AS object_id,b.connection_id AS related_id
FROM mailboxes b WHERE
 NOT EXISTS(SELECT 1 FROM mail_connections c WHERE c.id=b.connection_id AND c.org_id=b.org_id)
 OR (b.owner_member_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM members m WHERE m.id=b.owner_member_id AND m.org_id=b.org_id))
 OR (b.domain_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM domains d WHERE d.id=b.domain_id AND d.org_id=b.org_id))
 OR (b.domain_binding_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM domain_bindings d WHERE d.id=b.domain_binding_id AND d.org_id=b.org_id AND d.connection_id=b.connection_id AND d.domain_id=b.domain_id))
UNION ALL
SELECT 'credential',cr.id,cr.connection_id FROM credentials cr WHERE
 NOT EXISTS(SELECT 1 FROM mail_connections c WHERE c.id=cr.connection_id AND c.org_id=cr.org_id)
 OR (cr.mailbox_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM mailboxes b WHERE b.id=cr.mailbox_id AND b.connection_id=cr.connection_id AND b.org_id=cr.org_id))
UNION ALL
SELECT 'endpoint',e.id,e.credential_id FROM mailbox_endpoints e WHERE
 e.credential_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM credentials cr JOIN mailboxes b ON b.id=e.mailbox_id WHERE cr.id=e.credential_id AND cr.mailbox_id=b.id AND cr.connection_id=b.connection_id AND cr.org_id=b.org_id AND cr.purpose='mail')
UNION ALL
SELECT 'domain_binding',b.id,b.connection_id FROM domain_bindings b WHERE
 NOT EXISTS(SELECT 1 FROM mail_connections c WHERE c.id=b.connection_id AND c.org_id=b.org_id)
 OR NOT EXISTS(SELECT 1 FROM domains d WHERE d.id=b.domain_id AND d.org_id=b.org_id)
UNION ALL
SELECT 'address',a.id,a.connection_id FROM addresses a WHERE
 NOT EXISTS(SELECT 1 FROM mail_connections c WHERE c.id=a.connection_id AND c.org_id=a.org_id)
 OR NOT EXISTS(SELECT 1 FROM domains d WHERE d.id=a.domain_id AND d.org_id=a.org_id)
 OR (a.mailbox_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM mailboxes b WHERE b.id=a.mailbox_id AND b.org_id=a.org_id AND b.connection_id=a.connection_id))
 OR (a.group_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM groups g WHERE g.id=a.group_id AND g.org_id=a.org_id))
 OR (a.domain_binding_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM domain_bindings b WHERE b.id=a.domain_binding_id AND b.connection_id=a.connection_id AND b.org_id=a.org_id AND b.domain_id=a.domain_id))
UNION ALL
SELECT 'provider_resource',r.id,r.connection_id FROM provider_resources r WHERE
 (r.domain_binding_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM domain_bindings b WHERE b.id=r.domain_binding_id AND b.connection_id=r.connection_id))
 OR (r.mailbox_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM mailboxes b WHERE b.id=r.mailbox_id AND b.connection_id=r.connection_id))
 OR (r.address_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM addresses a WHERE a.id=r.address_id AND a.connection_id=r.connection_id))
 OR (r.identity_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM identities i JOIN mailboxes b ON b.id=i.mailbox_id WHERE i.id=r.identity_id AND b.connection_id=r.connection_id))
 OR (r.credential_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM credentials cr WHERE cr.id=r.credential_id AND cr.connection_id=r.connection_id))
 OR (r.mailbox_forwarding_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM mailbox_forwardings f JOIN mailboxes b ON b.id=f.mailbox_id WHERE f.id=r.mailbox_forwarding_id AND b.connection_id=r.connection_id))
UNION ALL
SELECT 'mailbox_access',a.id,a.member_id FROM mailbox_access a WHERE
 NOT EXISTS(SELECT 1 FROM members m JOIN mailboxes b ON b.id=a.mailbox_id WHERE m.id=a.member_id AND m.org_id=b.org_id)
UNION ALL
SELECT 'member_role',m.id,m.role_id FROM members m WHERE
 NOT EXISTS(SELECT 1 FROM roles r WHERE r.id=m.role_id AND r.org_id=m.org_id)
UNION ALL
SELECT 'connection_credential',c.id,c.api_credential_id FROM mail_connections c WHERE
 c.api_credential_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM credentials cr WHERE cr.id=c.api_credential_id AND cr.connection_id=c.id AND cr.org_id=c.org_id AND cr.purpose='api' AND cr.mailbox_id IS NULL)
UNION ALL
SELECT 'submission',s.id,s.mailbox_id FROM submissions s WHERE
 NOT EXISTS(SELECT 1 FROM mailboxes b JOIN members m ON m.org_id=b.org_id AND m.id=s.member_id JOIN members actor ON actor.org_id=b.org_id AND actor.id=s.execution_member_id WHERE b.id=s.mailbox_id AND b.org_id=s.org_id);

CREATE TRIGGER mailbox_scope_insert AFTER INSERT ON mailboxes BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM association_scope_errors WHERE object_type='mailbox' AND object_id=NEW.id);
END;
CREATE TRIGGER mailbox_scope_update AFTER UPDATE ON mailboxes BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM association_scope_errors WHERE object_type='mailbox' AND object_id=NEW.id);
END;
CREATE TRIGGER mailbox_connection_immutable BEFORE UPDATE OF connection_id,org_id ON mailboxes WHEN NEW.connection_id!=OLD.connection_id OR NEW.org_id!=OLD.org_id BEGIN
 SELECT RAISE(ABORT,'mailbox_connection_immutable');
END;
CREATE TRIGGER connection_kind_immutable BEFORE UPDATE OF provider_kind,org_id ON mail_connections WHEN NEW.provider_kind!=OLD.provider_kind OR NEW.org_id!=OLD.org_id BEGIN
 SELECT RAISE(ABORT,'connection_kind_immutable');
END;
CREATE TRIGGER credential_scope_insert AFTER INSERT ON credentials BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM association_scope_errors WHERE object_type='credential' AND object_id=NEW.id);
END;
CREATE TRIGGER credential_scope_update AFTER UPDATE ON credentials BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM association_scope_errors WHERE object_type='credential' AND object_id=NEW.id);
END;
CREATE TRIGGER endpoint_scope_insert AFTER INSERT ON mailbox_endpoints BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM association_scope_errors WHERE object_type='endpoint' AND object_id=NEW.id);
END;
CREATE TRIGGER endpoint_scope_update AFTER UPDATE ON mailbox_endpoints BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM association_scope_errors WHERE object_type='endpoint' AND object_id=NEW.id);
END;
CREATE TRIGGER domain_binding_scope_insert AFTER INSERT ON domain_bindings BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM association_scope_errors WHERE object_type='domain_binding' AND object_id=NEW.id);
END;
CREATE TRIGGER domain_binding_scope_update AFTER UPDATE ON domain_bindings BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM association_scope_errors WHERE object_type='domain_binding' AND object_id=NEW.id);
END;
CREATE TRIGGER address_scope_insert AFTER INSERT ON addresses BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM association_scope_errors WHERE object_type='address' AND object_id=NEW.id);
END;
CREATE TRIGGER address_scope_update AFTER UPDATE ON addresses BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM association_scope_errors WHERE object_type='address' AND object_id=NEW.id);
END;
CREATE TRIGGER provider_resource_scope_insert AFTER INSERT ON provider_resources BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM association_scope_errors WHERE object_type='provider_resource' AND object_id=NEW.id);
END;
CREATE TRIGGER provider_resource_scope_update AFTER UPDATE ON provider_resources BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM association_scope_errors WHERE object_type='provider_resource' AND object_id=NEW.id);
END;
CREATE TRIGGER mailbox_access_scope_insert AFTER INSERT ON mailbox_access BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM association_scope_errors WHERE object_type='mailbox_access' AND object_id=NEW.id);
END;
CREATE TRIGGER mailbox_access_scope_update AFTER UPDATE ON mailbox_access BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM association_scope_errors WHERE object_type='mailbox_access' AND object_id=NEW.id);
END;
CREATE TRIGGER member_role_scope_insert AFTER INSERT ON members BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM association_scope_errors WHERE object_type='member_role' AND object_id=NEW.id);
END;
CREATE TRIGGER member_role_scope_update AFTER UPDATE ON members BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM association_scope_errors WHERE object_type='member_role' AND object_id=NEW.id);
END;
CREATE TRIGGER connection_credential_scope_update AFTER UPDATE OF api_credential_id ON mail_connections BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM association_scope_errors WHERE object_type='connection_credential' AND object_id=NEW.id);
END;
CREATE TRIGGER submission_scope_insert AFTER INSERT ON submissions BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM association_scope_errors WHERE object_type='submission' AND object_id=NEW.id);
END;
CREATE TRIGGER submission_scope_update AFTER UPDATE ON submissions BEGIN
 SELECT RAISE(ABORT,'association_scope_conflict') WHERE EXISTS(SELECT 1 FROM association_scope_errors WHERE object_type='submission' AND object_id=NEW.id);
END;
