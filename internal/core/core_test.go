package core_test

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mailhearth/internal/config"
	"mailhearth/internal/core"
	"mailhearth/internal/db"
	"mailhearth/internal/model"
	"mailhearth/internal/purelymail"
	"mailhearth/internal/purelymail/fake"
	"mailhearth/internal/secrets"
)

type harness struct {
	svc   *core.Service
	fake  *fake.Server
	orgID int64
	owner int64
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	f := fake.New("tok")
	f.AddDomain(fake.Domain{Name: "acme.test", MX: true, SPF: true, DKIM: true, DMARC: true})
	f.AddDomain(fake.Domain{Name: "acme-labs.test", MX: true})
	f.AddUser("alice@acme.test", "pw-alice")
	f.AddUser("bob@acme.test", "pw-bob")
	f.AddUser("support@acme.test", "pw-support")
	f.AddRule(fake.Rule{DomainName: "acme.test", MatchUser: "sales", TargetAddresses: []string{"alice@acme.test"}})
	f.AddRule(fake.Rule{DomainName: "acme.test", MatchUser: "team", TargetAddresses: []string{"alice@acme.test", "bob@acme.test"}})
	f.AddRule(fake.Rule{DomainName: "acme.test", MatchUser: "", Catchall: true, TargetAddresses: []string{"support@acme.test"}})
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	master := make([]byte, 32)
	box, _ := secrets.NewBox(master, "test")
	cfg := &config.Config{PurelymailAPIURL: srv.URL, InviteTTL: 24 * 3600e9, SessionTTL: 24 * 3600e9}
	svc := core.New(database, cfg, box, nil, slog.Default())
	svc.NewPM = func(token string) purelymail.API { return purelymail.New(srv.URL, token) }

	ctx := context.Background()
	owner, err := svc.Init(ctx, core.InitRequest{OrgName: "Acme", AdminName: "Alice", AdminEmail: "alice@acme.test", Password: "correct-horse-battery"})
	if err != nil {
		t.Fatal(err)
	}
	org, _ := svc.Org(ctx)
	return &harness{svc: svc, fake: f, orgID: org.ID, owner: owner.ID}
}

func TestSetupImportAndModel(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	svc := h.svc

	st, _ := svc.Status(ctx)
	if !st.NeedsSetup || st.Step != "connect" {
		t.Fatalf("status: %+v", st)
	}
	if _, err := svc.Connect(ctx, h.orgID, h.owner, "wrong"); err == nil {
		t.Fatal("wrong token accepted")
	}
	disc, err := svc.Connect(ctx, h.orgID, h.owner, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if len(disc.Domains) != 2 || len(disc.Users) != 3 || len(disc.Rules) != 3 {
		t.Fatalf("discovery: %+v", disc)
	}
	res, err := svc.CompleteSetup(ctx, h.orgID, h.owner, "alice@acme.test")
	if err != nil {
		t.Fatal(err)
	}
	if res.Domains != 2 || res.MailboxesNew != 3 || res.AddressesNew != 3 || len(res.Warnings) != 0 {
		t.Fatalf("import: %+v", res)
	}
	st, _ = svc.Status(ctx)
	if st.NeedsSetup {
		t.Fatalf("setup should be done: %+v", st)
	}
	// Nothing on Purelymail was modified except the app password for alice.
	for _, c := range h.fake.Calls {
		if c == "createUser" || c == "createRoutingRule" || c == "deleteRoutingRule" || c == "deleteUser" || c == "modifyUser" {
			t.Fatalf("import must not modify the account, saw %s", c)
		}
	}
	mbs, _ := svc.Mailboxes(ctx, h.orgID)
	var alice, bob, support *model.Mailbox
	for i := range mbs {
		switch mbs[i].Address {
		case "alice@acme.test":
			alice = &mbs[i]
		case "bob@acme.test":
			bob = &mbs[i]
		case "support@acme.test":
			support = &mbs[i]
		}
	}
	if alice == nil || alice.OwnerMemberID == nil || *alice.OwnerMemberID != h.owner || !alice.HasCredential {
		t.Fatalf("alice mailbox should be bound to the owner with a credential: %+v", alice)
	}
	if bob.HasCredential || bob.OwnerMemberID != nil {
		t.Fatalf("bob should be unassigned: %+v", bob)
	}
	addrs, _ := svc.Addresses(ctx, h.orgID)
	kinds := map[string]string{}
	for _, a := range addrs {
		kinds[a.Address] = a.Kind
	}
	if kinds["sales@acme.test"] != model.AddressAlias || kinds["team@acme.test"] != model.AddressForward || kinds["*@acme.test"] != model.AddressCatchall || kinds["alice@acme.test"] != model.AddressPrimary {
		t.Fatalf("address kinds: %v", kinds)
	}
	// Alias becomes a sending identity for alice.
	ids, _ := svc.Identities(ctx, alice.ID)
	if len(ids) != 1 {
		t.Fatalf("imported mailbox should have its primary identity only: %+v", ids)
	}
	if _, err := svc.UpsertIdentity(ctx, h.orgID, h.owner, alice.ID, 0, core.IdentityInput{Address: "sales@acme.test", DisplayName: "Acme Sales", SignatureHTML: "<p>Sales <script>x()</script></p>"}); err != nil {
		t.Fatal(err)
	}
	ids, _ = svc.Identities(ctx, alice.ID)
	if len(ids) != 2 || strings.Contains(ids[1].SignatureHTML, "script") {
		t.Fatalf("identities: %+v", ids)
	}
	if _, err := svc.UpsertIdentity(ctx, h.orgID, h.owner, alice.ID, 0, core.IdentityInput{Address: "bob@acme.test"}); err == nil {
		t.Fatal("must not allow sending as another mailbox")
	}

	// Sync is idempotent.
	res2, err := svc.Sync(ctx, h.orgID, h.owner)
	if err != nil || res2.MailboxesNew != 0 || res2.AddressesNew != 0 || res2.MailboxesKept != 3 {
		t.Fatalf("second sync: %v %+v", err, res2)
	}

	// Onboard a member with a new mailbox.
	domains, _ := svc.Domains(ctx, h.orgID)
	var acme model.Domain
	for _, d := range domains {
		if d.Name == "acme.test" {
			acme = d
		}
	}
	cres, err := svc.CreateMember(ctx, h.orgID, h.owner, core.CreateMemberRequest{
		MemberInput: core.MemberInput{DisplayName: "Carol Chen", Title: "Designer", Department: "Product"},
		NewMailbox:  &core.NewMailboxSpec{DomainID: acme.ID, LocalPart: "carol"},
		SendInvite:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cres.Mailbox == nil || cres.Mailbox.Address != "carol@acme.test" || !cres.Mailbox.HasCredential || cres.InviteLink == "" || cres.Member.Status != model.MemberInvited || cres.Member.LoginEmail != "carol@acme.test" {
		t.Fatalf("create member: %+v warnings=%v", cres, cres.Warnings)
	}
	if !h.fake.UserExists("carol@acme.test") {
		t.Fatal("purelymail user not created")
	}
	token := cres.InviteLink[strings.LastIndex(cres.InviteLink, "/")+1:]
	info, err := svc.Invite(ctx, token)
	if err != nil || info.MemberName != "Carol Chen" {
		t.Fatalf("invite: %v %+v", err, info)
	}
	if _, err := svc.AcceptInvite(ctx, token, "short", ""); err == nil {
		t.Fatal("weak password accepted")
	}
	carol, err := svc.AcceptInvite(ctx, token, "carols-strong-password", "")
	if err != nil || carol.Status != model.MemberActive {
		t.Fatalf("accept: %v %+v", err, carol)
	}
	if _, err := svc.AcceptInvite(ctx, token, "carols-strong-password", ""); err == nil {
		t.Fatal("invite reuse must fail")
	}
	if _, err := svc.VerifyLogin(ctx, "carol@acme.test", "carols-strong-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyLogin(ctx, "carol@acme.test", "nope"); err == nil {
		t.Fatal("bad password accepted")
	}

	// Bind bob's imported mailbox to a new member.
	bres, err := svc.CreateMember(ctx, h.orgID, h.owner, core.CreateMemberRequest{MemberInput: core.MemberInput{DisplayName: "Bob"}, BindMailboxID: bob.ID, Password: "bobs-strong-password"})
	if err != nil {
		t.Fatal(err)
	}
	if bres.Mailbox == nil || !bres.Mailbox.HasCredential || bres.Member.Status != model.MemberActive {
		t.Fatalf("bind: %+v %v", bres, bres.Warnings)
	}

	// Shared mailbox from support@ with access for carol; carol can resolve it, bob cannot.
	shared, err := svc.UpdateMailbox(ctx, h.orgID, h.owner, support.ID, core.UpdateMailboxInput{Kind: strPtr(model.MailboxShared), DisplayName: strPtr("Support")})
	if err != nil || shared.Kind != model.MailboxShared {
		t.Fatalf("to shared: %v %+v", err, shared)
	}
	if err := svc.EnsureCredential(ctx, h.orgID, h.owner, shared.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GrantAccess(ctx, h.orgID, h.owner, shared.ID, carol.ID, model.AccessSend); err != nil {
		t.Fatal(err)
	}
	mc, err := svc.ResolveMailbox(ctx, h.orgID, carol.ID, shared.ID)
	if err != nil || mc.Level != model.AccessSend || mc.Cred.User != "support@acme.test" || mc.Cred.Pass == "" || mc.CanDelete() || !mc.CanSend() {
		t.Fatalf("resolve shared: %v %+v", err, mc)
	}
	if _, err := svc.ResolveMailbox(ctx, h.orgID, bres.Member.ID, shared.ID); err != core.ErrForbidden {
		t.Fatalf("bob must not access support: %v", err)
	}
	if _, err := svc.ResolveMailbox(ctx, h.orgID, h.owner, shared.ID); err != core.ErrForbidden {
		t.Fatalf("admin without grant must not read shared mail: %v", err)
	}
	acc, _ := svc.AccessibleMailboxes(ctx, h.orgID, carol.ID)
	if len(acc) != 2 || acc[0].Address != "carol@acme.test" || acc[1].Level != model.AccessSend {
		t.Fatalf("accessible: %+v", acc)
	}

	// Team activity on the shared mailbox.
	key := core.MessageKey("<abc@x>", "INBOX", 1, 5)
	if err := svc.Assign(ctx, shared.ID, carol.ID, key, &carol.ID); err != nil {
		t.Fatal(err)
	}
	svc.RecordActivity(ctx, shared.ID, carol.ID, key, "replied", "Re: hello")
	svc.SetStatus(ctx, shared.ID, carol.ID, key, "resolved")
	stt, _ := svc.GetMessageState(ctx, shared.ID, key)
	if stt.AssigneeName != "Carol Chen" || stt.Status != "resolved" || len(stt.Activity) != 3 {
		t.Fatalf("state: %+v", stt)
	}
	many, _ := svc.StatesFor(ctx, shared.ID, []string{key, "mid:none"})
	if len(many) != 1 || many[key].Activity[0].Action != "replied" {
		t.Fatalf("states: %+v", many)
	}

	// Addresses: alias, forward, group distribution list.
	alias, err := svc.CreateAddress(ctx, h.orgID, h.owner, core.AddressInput{DomainID: acme.ID, LocalPart: "hello", Kind: model.AddressAlias, MailboxID: alice.ID})
	if err != nil || alias.Kind != model.AddressAlias || alias.PMRuleID == nil || alias.Targets[0] != "alice@acme.test" {
		t.Fatalf("alias: %v %+v", err, alias)
	}
	if _, err := svc.CreateAddress(ctx, h.orgID, h.owner, core.AddressInput{DomainID: acme.ID, LocalPart: "alice", Kind: model.AddressForward, Targets: []string{"x@y.test"}}); err == nil {
		t.Fatal("rule on a mailbox address must be refused")
	}
	grp, err := svc.CreateGroup(ctx, h.orgID, h.owner, core.GroupInput{Name: "Product", MemberIDs: []int64{carol.ID, bres.Member.ID}, AddressDomainID: acme.ID, AddressLocal: "product"})
	if err != nil {
		t.Fatal(err)
	}
	if grp.Address == nil || len(grp.Address.Targets) != 2 || grp.Address.Targets[0] != "bob@acme.test" || grp.Address.Targets[1] != "carol@acme.test" {
		t.Fatalf("group address: %+v", grp.Address)
	}
	rules, _ := purelymail.New(h.fakeURL(t), "tok").ListRoutingRules(ctx)
	var productRule *purelymail.RoutingRule
	for i := range rules {
		if rules[i].MatchUser == "product" {
			productRule = &rules[i]
		}
	}
	if productRule == nil || len(productRule.TargetAddresses) != 2 {
		t.Fatalf("purelymail rule for group: %+v", productRule)
	}
	if err := svc.RemoveGroupMember(ctx, h.orgID, h.owner, grp.ID, bres.Member.ID); err != nil {
		t.Fatal(err)
	}
	grp, _ = svc.Group(ctx, h.orgID, grp.ID)
	if len(grp.Address.Targets) != 1 || grp.Address.Targets[0] != "carol@acme.test" {
		t.Fatalf("group sync: %+v", grp.Address)
	}

	// Offboard carol: hand mailbox to bob, forward new mail, leave groups.
	if _, err := svc.Offboard(ctx, h.orgID, h.owner, h.owner, core.OffboardRequest{}); err == nil {
		t.Fatal("owner offboard must fail")
	}
	oldCred, _ := svc.ResolveMailbox(ctx, h.orgID, carol.ID, cres.Mailbox.ID)
	ores, err := svc.Offboard(ctx, h.orgID, h.owner, carol.ID, core.OffboardRequest{
		Plans:            []core.MailboxPlan{{MailboxID: cres.Mailbox.ID, Action: "handover", NewOwnerID: bres.Member.ID, ForwardTo: []string{"bob@acme.test"}}},
		RemoveFromGroups: true, RevokeShared: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ores.Member.Status != model.MemberDeparted || len(ores.Warnings) != 0 {
		t.Fatalf("offboard: %+v", ores)
	}
	if _, err := svc.VerifyLogin(ctx, "carol@acme.test", "carols-strong-password"); err == nil {
		t.Fatal("departed member must not log in")
	}
	handed, _ := svc.Mailbox(ctx, h.orgID, cres.Mailbox.ID)
	if handed.OwnerMemberID == nil || *handed.OwnerMemberID != bres.Member.ID {
		t.Fatalf("handover owner: %+v", handed)
	}
	newCred, _ := svc.ResolveMailbox(ctx, h.orgID, bres.Member.ID, cres.Mailbox.ID)
	if newCred.Cred.Pass == oldCred.Cred.Pass {
		t.Fatal("credential must be rotated on handover")
	}
	if h.fake.CredentialValid("carol@acme.test", oldCred.Cred.Pass) {
		t.Fatal("old app password must be revoked")
	}
	carolAddr, _ := svc.Addresses(ctx, h.orgID)
	for _, a := range carolAddr {
		if a.Address == "carol@acme.test" && (a.Kind != model.AddressForward || a.Targets[0] != "bob@acme.test") {
			t.Fatalf("forwarding not set: %+v", a)
		}
	}
	grp, _ = svc.Group(ctx, h.orgID, grp.ID)
	if len(grp.MemberIDs) != 0 || grp.Address.PMRuleID != nil {
		t.Fatalf("group after offboard: %+v", grp)
	}
	if _, err := svc.ResolveMailbox(ctx, h.orgID, carol.ID, shared.ID); err != core.ErrForbidden {
		t.Fatal("shared access must be revoked")
	}
	// Clear forwarding restores primary delivery.
	if _, err := svc.SetMailboxForwarding(ctx, h.orgID, h.owner, cres.Mailbox.ID, nil); err != nil {
		t.Fatal(err)
	}
	a, _ := svc.Addresses(ctx, h.orgID)
	for _, x := range a {
		if x.Address == "carol@acme.test" && x.Kind != model.AddressPrimary {
			t.Fatalf("forward not cleared: %+v", x)
		}
	}

	// Roles and permissions.
	perms, key2, _ := svc.Permissions(ctx, h.owner)
	if key2 != model.RoleOwner || !core.HasPermission(perms, model.PermDomainsManage) {
		t.Fatalf("owner perms: %v %s", perms, key2)
	}
	bperms, bkey, _ := svc.Permissions(ctx, bres.Member.ID)
	if bkey != model.RoleMember || core.HasPermission(bperms, model.PermMembersManage) {
		t.Fatalf("member perms: %v", bperms)
	}
	role, err := svc.CreateRole(ctx, h.orgID, h.owner, core.RoleInput{Name: "HR", Permissions: []string{model.PermMembersManage, model.PermAuditRead}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateRole(ctx, h.orgID, h.owner, core.RoleInput{Name: "Bad", Permissions: []string{model.PermOrgOwner}}); err == nil {
		t.Fatal("owner permission must not be grantable")
	}
	if _, err := svc.UpdateMember(ctx, h.orgID, h.owner, bres.Member.ID, core.MemberInput{RoleID: role.ID}); err != nil {
		t.Fatal(err)
	}
	bperms, _, _ = svc.Permissions(ctx, bres.Member.ID)
	if !core.HasPermission(bperms, model.PermMembersManage) || core.HasPermission(bperms, model.PermDomainsManage) {
		t.Fatalf("custom role perms: %v", bperms)
	}
	if err := svc.DeleteRole(ctx, h.orgID, h.owner, role.ID); err == nil {
		t.Fatal("role in use must not be deletable")
	}

	// Audit trail exists.
	audit, _ := svc.Audit(ctx, h.orgID, 0, 100)
	if len(audit) < 15 {
		t.Fatalf("audit entries: %d", len(audit))
	}
	ov, err := svc.Overview(ctx, h.orgID)
	if err != nil || ov.Org.Name != "Acme" || len(ov.Domains) != 2 {
		t.Fatalf("overview: %v %+v", err, ov)
	}
}

func (h *harness) fakeURL(t *testing.T) string {
	return h.svc.Cfg.PurelymailAPIURL
}

func strPtr(s string) *string { return &s }
