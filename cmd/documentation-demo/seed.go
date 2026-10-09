package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"time"

	"mailhearth/internal/core"
	"mailhearth/internal/db"
	"mailhearth/internal/devstack"
	"mailhearth/internal/provider"
	"mailhearth/internal/purelymail/fake"
	"mailhearth/internal/secrets"
)

type demoPerson struct {
	Local, Name, Title, Department, Status string
}

var people = []demoPerson{
	{"alice", "Alice Chen", "Product Director", "Product", "active"},
	{"bob", "Bob Wilson", "Engineering Lead", "Engineering", "active"},
	{"carol", "Carol Garcia", "Customer Success", "Support", "active"},
	{"david", "David Kim", "Account Manager", "Sales", "active"},
	{"emma", "Emma Martin", "Designer", "Design", "active"},
	{"frank", "Frank Li", "Finance Manager", "Finance", "active"},
	{"grace", "Grace Liu", "People Operations", "People", "active"},
	{"henry", "Henry Brown", "Developer", "Engineering", "active"},
	{"iris", "Iris Sato", "Marketing Specialist", "Marketing", "invited"},
	{"jack", "Jack Lopez", "Support Specialist", "Support", "invited"},
	{"kate", "Kate Davis", "Contractor", "Design", "disabled"},
	{"leo", "Leo Wang", "Former Consultant", "Operations", "departed"},
}

var sharedNames = []string{"support", "sales", "finance"}
var domainNames = []string{"acme.test", "acme-studio.test", "acme-services.test"}

func jsonText(value any) string {
	raw, err := json.Marshal(value)
	require(err)
	return string(raw)
}

func seedInfrastructure(stack *devstack.Stack) {
	for i, name := range domainNames {
		stack.API.AddDomain(fake.Domain{Name: name, MX: true, SPF: true, DKIM: true, DMARC: i != 2})
	}
	for _, person := range people {
		stack.SeedUser(person.Local+"@acme.test", demoPassword)
	}
	for _, local := range sharedNames {
		stack.SeedUser(local+"@acme.test", demoPassword)
	}
	for _, rule := range []fake.Rule{
		{DomainName: "acme.test", MatchUser: "hello", TargetAddresses: []string{"alice@acme.test"}},
		{DomainName: "acme.test", MatchUser: "billing", TargetAddresses: []string{"finance@acme.test"}},
		{DomainName: "acme.test", MatchUser: "team", TargetAddresses: []string{"alice@acme.test", "bob@acme.test", "carol@acme.test"}},
		{DomainName: "acme.test", MatchUser: "support", TargetAddresses: []string{"carol@acme.test"}},
		{DomainName: "acme-studio.test", MatchUser: "projects-", Prefix: true, TargetAddresses: []string{"emma@acme.test"}},
		{DomainName: "acme-services.test", Catchall: true, TargetAddresses: []string{"support@acme.test"}},
	} {
		stack.API.AddRule(rule)
	}
}

func localTemplate(address string) provider.ProtocolTemplate {
	host, port, err := net.SplitHostPort(address)
	require(err)
	number, err := strconv.Atoi(port)
	require(err)
	bundleID := int64(1)
	return provider.ProtocolTemplate{Enabled: true, Host: host, Port: number, TLSMode: "tls", CABundleID: &bundleID}
}

func seedDatabase(ctx context.Context, svc *core.Service, stack *devstack.Stack) {
	org, err := svc.Org(ctx)
	if err == core.ErrNoSetup {
		_, err = svc.Init(ctx, core.InitRequest{
			OrgName: "Mailhearth 演示组织", AdminName: "演示管理员",
			AdminEmail: "admin@acme.test", Password: demoPassword,
		})
		require(err)
		org, err = svc.Org(ctx)
	}
	require(err)
	protocols := jsonText(provider.ProtocolTemplates{
		IMAP: localTemplate(stack.IMAPAddr), SMTP: localTemplate(stack.SMTPAddr),
	})
	version, err := db.GetSetting(ctx, svc.DB, "documentation.demo.version")
	require(err)
	if version != "" {
		if version != "1" {
			panic("不支持当前演示数据版本")
		}
		var connectionID int64
		require(svc.DB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='documentation.demo.connection'").Scan(&connectionID))
		_, err = svc.DB.ExecContext(ctx, "UPDATE mail_connections SET api_base_url=?,protocol_defaults_json=?,updated_at=? WHERE id=? AND org_id=? AND provider_kind='purelymail'", stack.APIURL, protocols, db.Now(), connectionID, org.ID)
		require(err)
		return
	}
	var connections int
	require(svc.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM mail_connections").Scan(&connections))
	if connections != 0 {
		panic("此数据库已有邮件连接，请使用新的独立演示目录")
	}
	var owner, memberRole int64
	require(svc.DB.QueryRowContext(ctx, "SELECT m.id FROM members m JOIN roles r ON r.id=m.role_id WHERE m.org_id=? AND r.key='owner' AND m.status='active'", org.ID).Scan(&owner))
	require(svc.DB.QueryRowContext(ctx, "SELECT id FROM roles WHERE org_id=? AND key='member'", org.ID).Scan(&memberRole))
	passwordHash, err := secrets.HashPassword(demoPassword)
	require(err)
	apiSecret, err := svc.Box.Seal(stack.Token)
	require(err)
	mailSecret, err := svc.Box.Seal(demoPassword)
	require(err)
	now := db.Now()
	seed := func(tx *sql.Tx) error {
		insert := func(query string, args ...any) int64 {
			res, err := tx.ExecContext(ctx, query, args...)
			require(err)
			id, err := res.LastInsertId()
			require(err)
			return id
		}
		connection := insert("INSERT INTO mail_connections(org_id,provider_kind,label,api_base_url,domain_scope_json,protocol_defaults_json,created_at,updated_at) VALUES (?,'purelymail','Purelymail · 本地演示',?,?,?,?,?)", org.ID, stack.APIURL, jsonText(provider.DomainScope{Mode: "all"}), protocols, now, now)
		apiCredential := insert("INSERT INTO credentials(org_id,connection_id,purpose,source,secret_enc,state,hint,created_at,updated_at) VALUES (?,?,'api','entered',?,'active','本地演示',?,?)", org.ID, connection, apiSecret, now, now)
		insert("UPDATE mail_connections SET api_credential_id=? WHERE id=?", apiCredential, connection)
		for _, kind := range []provider.ProviderKind{provider.Manual, provider.Migadu} {
			label, enabled := "IMAP/SMTP · 演示", 1
			if kind == provider.Migadu {
				label, enabled = "Migadu · 待配置演示", 0
			}
			insert("INSERT INTO mail_connections(org_id,provider_kind,label,enabled,domain_scope_json,protocol_defaults_json,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)", org.ID, kind, label, enabled, jsonText(provider.DomainScope{Mode: "all"}), jsonText(provider.DefaultTemplates(kind)), now, now)
		}
		for _, role := range []struct {
			Key, Name   string
			Permissions []string
		}{
			{"demo-support", "客户支持主管 · 演示", []string{"members.manage", "shared.manage", "groups.manage", "audit.read"}},
			{"demo-finance", "财务管理 · 演示", []string{"audit.read"}},
			{"demo-observer", "目录浏览 · 演示", []string{}},
		} {
			insert("INSERT INTO roles(org_id,key,name,description,permissions_json) VALUES (?,?,?,?,?)", org.ID, role.Key, role.Name, "用于文档演示的角色", jsonText(role.Permissions))
		}
		domains, bindings := map[string]int64{}, map[string]int64{}
		for i, name := range domainNames {
			dmarc, result := 1, "pass"
			if i == 2 {
				dmarc, result = 0, "fail"
			}
			domains[name] = insert("INSERT INTO domains(org_id,name,dns_mx,dns_spf,dns_dkim,dns_dmarc,dns_checked_at,created_at,updated_at) VALUES (?,?,1,1,1,?,?,?,?)", org.ID, name, dmarc, now, now, now)
			bindings[name] = insert("INSERT INTO domain_bindings(org_id,domain_id,connection_id,management_mode,remote_state,dns_status_json,dns_checked_at,created_at,updated_at) VALUES (?,?,?,'api','present',?,?,?,?)", org.ID, domains[name], connection, jsonText(map[string]string{"mx": "pass", "spf": "pass", "dkim": "pass", "dmarc": result}), now, now, now)
			insert("INSERT INTO provider_resources(connection_id,resource_type,remote_key,remote_locator_json,purpose,remote_state,domain_binding_id,created_at,updated_at) VALUES (?,'domain',?,?,'domain','present',?,?,?)", connection, name, jsonText(map[string]string{"domain": name}), bindings[name], now, now)
		}
		members, mailboxes := map[string]int64{"alice": owner}, map[string]int64{}
		for i, person := range people {
			if i > 0 {
				members[person.Local] = insert("INSERT INTO members(org_id,display_name,login_email,password_hash,role_id,title,department,status,created_at,updated_at,departed_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)", org.ID, person.Name, person.Local+"@acme.test", passwordHash, memberRole, person.Title, person.Department, person.Status, now, now, departureTime(person.Status))
				if person.Status == "invited" {
					token := secrets.RandomToken(32)
					insert("INSERT INTO invites(org_id,member_id,token_hash,created_by,created_at,expires_at) VALUES (?,?,?,?,?,?)", org.ID, members[person.Local], secrets.HashToken(token), owner, now, time.Now().UTC().Add(7*24*time.Hour).Format(time.RFC3339))
				}
			}
		}
		addMailbox := func(local, name, kind, status string, ownerID any) int64 {
			address := local + "@acme.test"
			id := insert("INSERT INTO mailboxes(org_id,connection_id,kind,address,address_key,domain_id,domain_binding_id,display_name,owner_member_id,status,management_mode,remote_state,folder_mapping_json,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,'api','present',?,?,?)", org.ID, connection, kind, address, address, domains["acme.test"], bindings["acme.test"], name, ownerID, status, jsonText(map[string]string{"sent": "Sent", "drafts": "Drafts", "trash": "Trash", "junk": "Junk", "archive": "Archive"}), now, now)
			credential := insert("INSERT INTO credentials(org_id,connection_id,mailbox_id,purpose,source,secret_enc,state,hint,created_at,updated_at) VALUES (?,?,?,'mail','entered',?,'active','本地演示',?,?)", org.ID, connection, id, mailSecret, now, now)
			for _, protocol := range []string{"imap", "smtp"} {
				insert("INSERT INTO mailbox_endpoints(mailbox_id,protocol,network_mode,username,credential_id,created_at,updated_at) VALUES (?,?,'inherit',?,?,?,?)", id, protocol, address, credential, now, now)
			}
			insert("INSERT INTO mailbox_endpoints(mailbox_id,protocol,network_mode,created_at,updated_at) VALUES (?,'managesieve','disabled',?,?)", id, now, now)
			insert("INSERT INTO addresses(org_id,connection_id,domain_id,domain_binding_id,local_part,address,address_key,kind,mailbox_id,note,created_at,updated_at) VALUES (?,?,?,?,?,?,?,'primary',?,'演示邮箱',?,?)", org.ID, connection, domains["acme.test"], bindings["acme.test"], local, address, address, id, now, now)
			insert("INSERT INTO identities(mailbox_id,address,display_name,signature_html,is_default,created_at) VALUES (?,?,?,?,1,?)", id, address, name, "<p>"+name+"<br>Acme · 本地演示</p>", now)
			insert("INSERT INTO provider_resources(connection_id,resource_type,remote_key,remote_locator_json,purpose,remote_state,mailbox_id,created_at,updated_at) VALUES (?,'mailbox',?,?,'mailbox','present',?,?,?)", connection, address, jsonText(map[string]string{"address": address}), id, now, now)
			mailboxes[local] = id
			return id
		}
		for _, person := range people {
			status := "active"
			if person.Status == "disabled" {
				status = "suspended"
			} else if person.Status == "departed" {
				status = "archived"
			}
			addMailbox(person.Local, person.Name, "personal", status, members[person.Local])
		}
		for _, local := range sharedNames {
			id := addMailbox(local, map[string]string{"support": "Customer Support", "sales": "Sales Team", "finance": "Finance Office"}[local], "shared", "active", nil)
			for i, member := range []string{"alice", "bob", "carol", "david", "frank"} {
				level := []string{"full", "read", "full", "send", "read"}[i]
				insert("INSERT INTO mailbox_access(mailbox_id,member_id,level,granted_by,granted_at) VALUES (?,?,?,?,?)", id, members[member], level, owner, now)
			}
		}
		for _, group := range []struct{ Local, Name string }{
			{"engineering", "Engineering"}, {"success", "Customer Success"},
			{"leaders", "Leadership"}, {"aurora", "Project Aurora"},
		} {
			id := insert("INSERT INTO groups(org_id,name,description,created_at,updated_at) VALUES (?,?,?,?,?)", org.ID, group.Name, "演示工作组", now, now)
			for _, local := range []string{"alice", "bob", "carol"} {
				insert("INSERT INTO group_members(group_id,member_id) VALUES (?,?)", id, members[local])
			}
			address := group.Local + "@acme.test"
			insert("INSERT INTO addresses(org_id,connection_id,domain_id,domain_binding_id,local_part,address,address_key,kind,group_id,targets_json,note,created_at,updated_at) VALUES (?,?,?,?,?,?,?,'group',?,?,'演示工作组地址',?,?)", org.ID, connection, domains["acme.test"], bindings["acme.test"], group.Local, address, address, id, jsonText([]string{"alice@acme.test", "bob@acme.test", "carol@acme.test"}), now, now)
		}
		for _, alias := range []struct{ Local, Target, Kind string }{
			{"hello", "alice", "alias"}, {"billing", "finance", "alias"},
			{"partners", "david", "alias"}, {"press", "emma", "forward"},
		} {
			address := alias.Local + "@acme.test"
			insert("INSERT INTO addresses(org_id,connection_id,domain_id,domain_binding_id,local_part,address,address_key,kind,mailbox_id,targets_json,desired_targets_json,note,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,'演示路由',?,?)", org.ID, connection, domains["acme.test"], bindings["acme.test"], alias.Local, address, address, alias.Kind, mailboxes[alias.Target], jsonText([]string{alias.Target + "@acme.test"}), jsonText([]string{alias.Target + "@acme.test"}), now, now)
			if alias.Kind == "alias" {
				insert("INSERT INTO identities(mailbox_id,address,display_name,signature_html,created_at,updated_at) VALUES (?,?,?,?,?,?)", mailboxes[alias.Target], address, "Acme · 演示别名", "<p>Acme Team · 本地演示</p>", now, now)
			}
		}
		for _, route := range []struct {
			Domain, Local, Kind, Target string
			Prefix, Catchall            int
		}{
			{"acme-studio.test", "projects-", "prefix", "emma", 1, 0},
			{"acme-services.test", "*", "catchall", "support", 0, 1},
		} {
			address := route.Local + "@" + route.Domain
			targets := jsonText([]string{route.Target + "@acme.test"})
			insert("INSERT INTO addresses(org_id,connection_id,domain_id,domain_binding_id,local_part,address,address_key,kind,mailbox_id,targets_json,desired_targets_json,is_prefix,is_catchall,note,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,'演示路由',?,?)", org.ID, connection, domains[route.Domain], bindings[route.Domain], route.Local, address, address, route.Kind, mailboxes[route.Target], targets, targets, route.Prefix, route.Catchall, now, now)
		}
		insert("INSERT INTO mailbox_forwardings(mailbox_id,targets_json,delivery_mode,remote_status_json,created_at,updated_at) VALUES (?,?,'unverified',?,?,?)", mailboxes["support"], jsonText([]string{"carol@acme.test"}), jsonText(map[string]bool{"demo": true}), now, now)
		for i, kind := range []string{"member.create", "mailbox.create", "connection.sync", "mailbox.forwarding", "domain.binding.update", "mailbox.archive"} {
			id := fmt.Sprintf("documentation-demo-%02d", i+1)
			at := time.Now().UTC().Add(-time.Duration(i+1) * 6 * time.Hour).Format(time.RFC3339)
			result := jsonText(map[string]any{"demo": true, "description": "文档演示历史记录"})
			insert("INSERT INTO operations(id,org_id,actor_member_id,kind,request_id,request_digest,status,result_json,created_at,updated_at) VALUES (?,?,?,?,?,?,'succeeded',?,?,?)", id, org.ID, owner, kind, id, secrets.HashToken(id), result, at, at)
			insert("INSERT INTO operation_steps(operation_id,step_key,sequence,status,result_json,started_at,finished_at) VALUES (?,'demonstration',1,'succeeded',?,?,?)", id, result, at, at)
			insert("INSERT INTO audit_log(org_id,actor_member_id,action,target_type,target_id,detail_json,created_at) VALUES (?,?,?,'demo',?,?,?)", org.ID, owner, kind, id, result, at)
		}
		insert("INSERT INTO settings(key,value) VALUES ('documentation.demo.connection',?)", fmt.Sprint(connection))
		insert("INSERT INTO settings(key,value) VALUES ('documentation.demo.version','1')")
		insert("INSERT INTO settings(key,value) VALUES ('setup.completed','1')")
		return nil
	}
	tx, err := svc.DB.BeginTx(ctx, nil)
	require(err)
	defer tx.Rollback()
	require(seed(tx))
	require(tx.Commit())
}

func departureTime(status string) any {
	if status == "departed" {
		return time.Now().UTC().Add(-14 * 24 * time.Hour).Format(time.RFC3339)
	}
	return nil
}
