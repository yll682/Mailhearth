package integration_test

import (
	"strings"
	"testing"
	"time"

	"mailhearth/internal/core"
	"mailhearth/internal/integration"
	"mailhearth/internal/model"
)

// TestImportExistingAccount proves that connecting Mailhearth to an account
// that already holds users and routing rules reads them faithfully and
// changes nothing upstream.
func TestImportExistingAccount(t *testing.T) {
	h := integration.New(t)
	ctx, cancel := h.Ctx(5 * time.Minute)
	defer cancel()

	// Seed the account directly through the API so the import has something
	// pre-existing to find, exactly like a real migration.
	seeded := h.Addr("seed")
	h.CreateUserDirect(ctx, seeded)
	aliasLocal := h.Name("seedalias")
	h.CreateRuleDirect(ctx, aliasLocal, []string{seeded})

	res := h.CompleteSetup(ctx)
	if res.Domains == 0 {
		t.Fatalf("import found no domains: %+v", res)
	}

	mb := h.MailboxByAddress(ctx, seeded)
	if mb.Kind != model.MailboxPersonal || mb.Status != model.MailboxActive {
		t.Fatalf("imported mailbox should be an active personal mailbox: %+v", mb)
	}
	if mb.HasCredential {
		t.Fatalf("import must not mint an app password for %s before it is bound", seeded)
	}
	if !mb.Imported {
		t.Fatalf("mailbox %s should be marked as imported", seeded)
	}

	alias := h.AddressByFull(ctx, aliasLocal+"@"+h.Env.Domain)
	if alias.Kind != model.AddressAlias {
		t.Fatalf("rule with a single in-account target should import as an alias, got %q", alias.Kind)
	}
	if alias.MailboxID == nil || *alias.MailboxID != mb.ID {
		t.Fatalf("alias %s should point at mailbox %s: %+v", alias.Address, seeded, alias)
	}
	if alias.Note != "" {
		t.Fatalf("import must leave the user-facing note empty, got %q", alias.Note)
	}

	// Re-running the import is a no-op: nothing new, nothing lost.
	again := h.Sync(ctx)
	if again.MailboxesNew != 0 || again.AddressesNew != 0 || again.MailboxesGone != 0 || again.AddressesGone != 0 {
		t.Fatalf("second sync should be idempotent: %+v", again)
	}
}

// TestMailboxLifecycle walks one mailbox from creation to deletion: app
// password, IMAP login, SMTP send, folder discovery, credential rotation and
// removal, checking the live account after each step.
func TestMailboxLifecycle(t *testing.T) {
	h := integration.New(t)
	ctx, cancel := h.Ctx(10 * time.Minute)
	defer cancel()
	h.CompleteSetup(ctx)

	alice := h.Member(ctx, "Alice Integration", model.RoleMember)
	mb := h.Mailbox(ctx, model.MailboxPersonal, "alice", alice.ID)

	if !h.UserExists(ctx, mb.Address) {
		t.Fatalf("Purelymail has no user %s after CreateMailbox", mb.Address)
	}

	cred := h.Cred(ctx, alice.ID, mb.ID)
	inbox := h.SpecialFolder(ctx, cred, "inbox")
	sent := h.SpecialFolder(ctx, cred, "sent")

	// A message the mailbox sends to itself exercises submission and delivery
	// in one step.
	subject := "mh-it roundtrip " + h.Nonce()
	msgID := h.Send(ctx, cred, []string{mb.Address}, subject, "Roundtrip body.")
	got := h.WaitForMessageID(ctx, cred, inbox, msgID)
	if !strings.Contains(got.Subject, subject) {
		t.Fatalf("delivered subject %q does not contain %q", got.Subject, subject)
	}

	full := h.Message(ctx, cred, inbox, got.UID)
	if !strings.Contains(full.Text, "Roundtrip body.") {
		t.Fatalf("rendered body lost its text: %q", full.Text)
	}
	if len(full.From) == 0 || !strings.EqualFold(full.From[0].Address, mb.Address) {
		t.Fatalf("From should be the mailbox itself: %+v", full.From)
	}

	// Sent folders are not populated by the server; Mailhearth appends.
	if uid := h.AppendToSent(ctx, cred, sent, msgID, subject); uid == 0 {
		h.Logf("server did not report a UID for the appended Sent copy")
	}
	h.WaitForMessageID(ctx, cred, sent, msgID)

	// Rotation must invalidate the previous app password at the provider.
	oldCred := cred
	if err := h.Svc.RotateCredential(ctx, h.OrgID, h.Owner, mb.ID); err != nil {
		t.Fatalf("rotate credential: %v", err)
	}
	newCred := h.Cred(ctx, alice.ID, mb.ID)
	if newCred.Pass == oldCred.Pass {
		t.Fatal("rotation returned the same app password")
	}
	h.LoginFails(ctx, oldCred, "app password was rotated")
	h.RequireLogin(ctx, newCred)

	// Deletion removes the Purelymail user and its mail.
	if err := h.Svc.DeleteMailbox(ctx, h.OrgID, h.Owner, mb.ID, mb.Address); err != nil {
		t.Fatalf("delete mailbox: %v", err)
	}
	if h.UserExists(ctx, mb.Address) {
		t.Fatalf("Purelymail still has user %s after DeleteMailbox", mb.Address)
	}
	h.RequireAudit(ctx, "mailbox.create", "mailbox.rotate", "mailbox.delete")
}

// TestRoutingRules checks that every address kind Mailhearth offers turns
// into a routing rule the provider honours, and that mail follows it.
func TestRoutingRules(t *testing.T) {
	h := integration.New(t)
	ctx, cancel := h.Ctx(10 * time.Minute)
	defer cancel()
	h.CompleteSetup(ctx)

	alice := h.Member(ctx, "Alice Routing", model.RoleMember)
	mb := h.Mailbox(ctx, model.MailboxPersonal, "routed", alice.ID)
	cred := h.Cred(ctx, alice.ID, mb.ID)
	inbox := h.SpecialFolder(ctx, cred, "inbox")

	aliasLocal := h.Name("alias")
	alias, err := h.Svc.CreateAddress(ctx, h.OrgID, h.Owner, core.AddressInput{
		DomainID:  h.DomainID(ctx),
		LocalPart: aliasLocal,
		Kind:      model.AddressAlias,
		MailboxID: mb.ID,
	})
	if err != nil {
		t.Fatalf("create alias: %v", err)
	}
	if alias.PMRuleID == nil {
		t.Fatal("alias was stored without a Purelymail rule id")
	}
	h.TrackRule(*alias.PMRuleID, alias.Address)

	live := h.RuleFor(ctx, aliasLocal)
	if live == nil {
		t.Fatalf("Purelymail has no routing rule for %s", alias.Address)
	}
	if len(live.TargetAddresses) != 1 || !strings.EqualFold(live.TargetAddresses[0], mb.Address) {
		t.Fatalf("alias rule targets %v, want %s", live.TargetAddresses, mb.Address)
	}

	subject := "mh-it alias " + h.Nonce()
	msgID := h.Send(ctx, cred, []string{alias.Address}, subject, "Sent to the alias.")
	h.WaitForMessageID(ctx, cred, inbox, msgID)

	// Deleting the address withdraws the rule, and mail to it stops.
	if err := h.Svc.DeleteAddress(ctx, h.OrgID, h.Owner, alias.ID); err != nil {
		t.Fatalf("delete alias: %v", err)
	}
	if h.RuleFor(ctx, aliasLocal) != nil {
		t.Fatalf("routing rule for %s survived DeleteAddress", alias.Address)
	}
	h.RequireAudit(ctx, "address.create", "address.delete")
}

// TestGroupDistribution checks that a group address reaches every member and
// that membership changes rewrite the rule.
func TestGroupDistribution(t *testing.T) {
	h := integration.New(t)
	ctx, cancel := h.Ctx(12 * time.Minute)
	defer cancel()
	h.CompleteSetup(ctx)

	alice := h.Member(ctx, "Alice Group", model.RoleMember)
	bob := h.Member(ctx, "Bob Group", model.RoleMember)
	aliceBox := h.Mailbox(ctx, model.MailboxPersonal, "galice", alice.ID)
	bobBox := h.Mailbox(ctx, model.MailboxPersonal, "gbob", bob.ID)
	aliceCred := h.Cred(ctx, alice.ID, aliceBox.ID)
	bobCred := h.Cred(ctx, bob.ID, bobBox.ID)

	groupLocal := h.Name("team")
	g, err := h.Svc.CreateGroup(ctx, h.OrgID, h.Owner, core.GroupInput{
		Name:            "Integration Team",
		MemberIDs:       []int64{alice.ID, bob.ID},
		AddressDomainID: h.DomainID(ctx),
		AddressLocal:    groupLocal,
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if g.Address == nil || g.Address.PMRuleID == nil {
		t.Fatalf("group has no distribution rule: %+v", g)
	}
	h.TrackRule(*g.Address.PMRuleID, g.Address.Address)

	live := h.RuleFor(ctx, groupLocal)
	if live == nil {
		t.Fatalf("Purelymail has no rule for group address %s", g.Address.Address)
	}
	if len(live.TargetAddresses) != 2 {
		t.Fatalf("group rule should target both members, got %v", live.TargetAddresses)
	}

	subject := "mh-it group " + h.Nonce()
	msgID := h.Send(ctx, aliceCred, []string{g.Address.Address}, subject, "Hello team.")
	h.WaitForMessageID(ctx, aliceCred, h.SpecialFolder(ctx, aliceCred, "inbox"), msgID)
	h.WaitForMessageID(ctx, bobCred, h.SpecialFolder(ctx, bobCred, "inbox"), msgID)

	// Removing Bob rewrites the rule upstream.
	if err := h.Svc.RemoveGroupMember(ctx, h.OrgID, h.Owner, g.ID, bob.ID); err != nil {
		t.Fatalf("remove group member: %v", err)
	}
	live = h.RuleFor(ctx, groupLocal)
	if live == nil || len(live.TargetAddresses) != 1 {
		t.Fatalf("group rule should target one member after removal, got %+v", live)
	}
	if !strings.EqualFold(live.TargetAddresses[0], aliceBox.Address) {
		t.Fatalf("remaining target should be %s, got %v", aliceBox.Address, live.TargetAddresses)
	}
}

// TestSharedMailboxAccess checks that grants decide who can open a shared
// mailbox and that admin rights alone never grant mail access.
func TestSharedMailboxAccess(t *testing.T) {
	h := integration.New(t)
	ctx, cancel := h.Ctx(10 * time.Minute)
	defer cancel()
	h.CompleteSetup(ctx)

	alice := h.Member(ctx, "Alice Shared", model.RoleMember)
	admin := h.Member(ctx, "Admin Shared", model.RoleAdmin)
	shared := h.Mailbox(ctx, model.MailboxShared, "support", 0)

	if _, err := h.Svc.ResolveMailbox(ctx, h.OrgID, alice.ID, shared.ID); err == nil {
		t.Fatal("a member without a grant could open the shared mailbox")
	}
	if _, err := h.Svc.ResolveMailbox(ctx, h.OrgID, admin.ID, shared.ID); err == nil {
		t.Fatal("an administrator could read a shared mailbox without a grant")
	}

	if _, err := h.Svc.GrantAccess(ctx, h.OrgID, h.Owner, shared.ID, alice.ID, model.AccessFull); err != nil {
		t.Fatalf("grant access: %v", err)
	}
	cred := h.Cred(ctx, alice.ID, shared.ID)
	inbox := h.SpecialFolder(ctx, cred, "inbox")

	subject := "mh-it shared " + h.Nonce()
	msgID := h.Send(ctx, cred, []string{shared.Address}, subject, "Shared inbox message.")
	h.WaitForMessageID(ctx, cred, inbox, msgID)

	// Revoking the grant closes the door again, even though the mailbox and
	// its credential still exist.
	if err := h.Svc.RevokeAccess(ctx, h.OrgID, h.Owner, shared.ID, alice.ID); err != nil {
		t.Fatalf("revoke access: %v", err)
	}
	if _, err := h.Svc.ResolveMailbox(ctx, h.OrgID, alice.ID, shared.ID); err == nil {
		t.Fatal("access survived RevokeAccess")
	}
}

// TestSieveRules installs Mailhearth's compiled Sieve script through
// ManageSieve and proves the provider actually files mail with it.
func TestSieveRules(t *testing.T) {
	h := integration.New(t)
	if h.Cfg.SieveAddr == "" {
		t.Skip("no ManageSieve address configured")
	}
	ctx, cancel := h.Ctx(10 * time.Minute)
	defer cancel()
	h.CompleteSetup(ctx)

	alice := h.Member(ctx, "Alice Sieve", model.RoleMember)
	mb := h.Mailbox(ctx, model.MailboxPersonal, "sieve", alice.ID)
	cred := h.Cred(ctx, alice.ID, mb.ID)

	marker := "mh-it-sieve-" + h.Nonce()
	folder := "Integration"
	h.CreateFolder(ctx, cred, folder)

	script := h.InstallSieve(ctx, cred, []model.SieveRule{{
		ID:      "r1",
		Name:    "File integration mail",
		Enabled: true,
		Match:   "all",
		Conditions: []model.SieveCondition{
			{Field: "subject", Op: "contains", Value: marker},
		},
		Actions: []model.SieveAction{
			{Type: "move", Folder: folder},
		},
	}}, nil)
	if !strings.Contains(script, marker) {
		t.Fatalf("compiled script lost the marker: %s", script)
	}

	// The provider must report our script as the active one.
	names := h.SieveScripts(ctx, cred)
	if !names["mailhearth"] {
		t.Fatalf("ManageSieve does not list the mailhearth script: %v", names)
	}

	msgID := h.Send(ctx, cred, []string{mb.Address}, marker+" filed", "Should land in the Integration folder.")
	h.WaitForMessageID(ctx, cred, folder, msgID)
	h.ExpectNoDelivery(ctx, cred, h.SpecialFolder(ctx, cred, "inbox"), msgID, 20*time.Second)
}

// TestOffboarding is the full departure flow: the person loses every way in,
// their mailbox is handed over with its mail, and the new owner can read it.
func TestOffboarding(t *testing.T) {
	h := integration.New(t)
	ctx, cancel := h.Ctx(12 * time.Minute)
	defer cancel()
	h.CompleteSetup(ctx)

	leaver := h.Member(ctx, "Leaver Integration", model.RoleMember)
	successor := h.Member(ctx, "Successor Integration", model.RoleMember)
	mb := h.Mailbox(ctx, model.MailboxPersonal, "leaver", leaver.ID)

	leaverCred := h.Cred(ctx, leaver.ID, mb.ID)
	inbox := h.SpecialFolder(ctx, leaverCred, "inbox")
	subject := "mh-it history " + h.Nonce()
	msgID := h.Send(ctx, leaverCred, []string{mb.Address}, subject, "Message that must survive the handover.")
	h.WaitForMessageID(ctx, leaverCred, inbox, msgID)

	res, err := h.Svc.Offboard(ctx, h.OrgID, h.Owner, leaver.ID, core.OffboardRequest{
		Plans: []core.MailboxPlan{{
			MailboxID:  mb.ID,
			Action:     "handover",
			NewOwnerID: successor.ID,
		}},
		RemoveFromGroups: true,
		RevokeShared:     true,
	})
	if err != nil {
		t.Fatalf("offboard: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("offboard reported warnings: %v", res.Warnings)
	}
	if res.Member.Status != model.MemberDeparted {
		t.Fatalf("member status is %q, want departed", res.Member.Status)
	}

	// The credential the leaver's mail client held must no longer work.
	h.LoginFails(ctx, leaverCred, "owner was offboarded")

	// The successor holds the mailbox and its history.
	if _, err := h.Svc.ResolveMailbox(ctx, h.OrgID, leaver.ID, mb.ID); err == nil {
		t.Fatal("the departed member can still resolve the mailbox")
	}
	successorCred := h.Cred(ctx, successor.ID, mb.ID)
	h.WaitForMessageID(ctx, successorCred, inbox, msgID)
	h.RequireAudit(ctx, "member.offboard", "mailbox.rotate")
}

// TestSuspendAndReactivate checks the suspension path: nobody can log in,
// mail still arrives, and reactivation restores access.
func TestSuspendAndReactivate(t *testing.T) {
	h := integration.New(t)
	ctx, cancel := h.Ctx(10 * time.Minute)
	defer cancel()
	h.CompleteSetup(ctx)

	alice := h.Member(ctx, "Alice Suspend", model.RoleMember)
	mb := h.Mailbox(ctx, model.MailboxPersonal, "suspend", alice.ID)
	sender := h.Mailbox(ctx, model.MailboxPersonal, "sender", h.Owner)
	senderCred := h.Cred(ctx, h.Owner, sender.ID)

	cred := h.Cred(ctx, alice.ID, mb.ID)
	inbox := h.SpecialFolder(ctx, cred, "inbox")

	if err := h.Svc.SuspendMailbox(ctx, h.OrgID, h.Owner, mb.ID); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	h.LoginFails(ctx, cred, "mailbox was suspended")
	if _, err := h.Svc.ResolveMailbox(ctx, h.OrgID, alice.ID, mb.ID); err == nil {
		t.Fatal("a suspended mailbox still resolves")
	}

	// Mail keeps arriving while suspended: that is the point of suspension
	// rather than deletion.
	subject := "mh-it suspended " + h.Nonce()
	msgID := h.Send(ctx, senderCred, []string{mb.Address}, subject, "Arrives while suspended.")

	if err := h.Svc.ReactivateMailbox(ctx, h.OrgID, h.Owner, mb.ID); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	restored := h.Cred(ctx, alice.ID, mb.ID)
	h.WaitForMessageID(ctx, restored, inbox, msgID)
	h.RequireAudit(ctx, "mailbox.suspend", "mailbox.reactivate")
}

// TestPasswordResetForExternalClients checks the path a person uses to set up
// Thunderbird: a fresh Purelymail password that really authenticates.
func TestPasswordResetForExternalClients(t *testing.T) {
	h := integration.New(t)
	ctx, cancel := h.Ctx(8 * time.Minute)
	defer cancel()
	h.CompleteSetup(ctx)

	alice := h.Member(ctx, "Alice Reset", model.RoleMember)
	mb := h.Mailbox(ctx, model.MailboxPersonal, "reset", alice.ID)

	pw, err := h.Svc.ResetMailboxPassword(ctx, h.OrgID, h.Owner, mb.ID)
	if err != nil {
		t.Fatalf("reset password: %v", err)
	}
	if len(pw) < 12 {
		t.Fatalf("generated password is too short: %d characters", len(pw))
	}
	h.LoginSucceeds(ctx, mb.Address, pw)

	// Mailhearth's own credential must still work after the reset.
	cred := h.Cred(ctx, alice.ID, mb.ID)
	h.RequireLogin(ctx, cred)
	if cred.Pass == pw {
		t.Fatal("Mailhearth stored the human-facing password as its app password")
	}
}

// TestExternalForwarding checks a forward to an address outside the account.
func TestExternalForwarding(t *testing.T) {
	h := integration.New(t)
	if h.Env.External == "" {
		t.Skip("set MAILHEARTH_IT_EXTERNAL to a mailbox you control to test external forwarding")
	}
	ctx, cancel := h.Ctx(8 * time.Minute)
	defer cancel()
	h.CompleteSetup(ctx)

	local := h.Name("fwd")
	addr, err := h.Svc.CreateAddress(ctx, h.OrgID, h.Owner, core.AddressInput{
		DomainID:  h.DomainID(ctx),
		LocalPart: local,
		Kind:      model.AddressForward,
		Targets:   []string{h.Env.External},
	})
	if err != nil {
		t.Fatalf("create forward: %v", err)
	}
	if addr.PMRuleID == nil {
		t.Fatal("forward was stored without a Purelymail rule id")
	}
	h.TrackRule(*addr.PMRuleID, addr.Address)

	live := h.RuleFor(ctx, local)
	if live == nil || len(live.TargetAddresses) != 1 || !strings.EqualFold(live.TargetAddresses[0], h.Env.External) {
		t.Fatalf("forward rule should target %s, got %+v", h.Env.External, live)
	}
	h.Logf("forward %s -> %s is live; check that mailbox manually for delivery", addr.Address, h.Env.External)
}

// TestTokenRejection checks that a wrong API token fails clearly rather than
// silently doing nothing.
func TestTokenRejection(t *testing.T) {
	h := integration.New(t)
	ctx, cancel := h.Ctx(2 * time.Minute)
	defer cancel()

	if _, err := h.Svc.Connect(ctx, h.OrgID, h.Owner, "definitely-not-a-valid-token"); err == nil {
		t.Fatal("an invalid API token was accepted")
	}
	// The stored connection must still be the working one.
	if _, err := h.Svc.Discover(ctx, h.OrgID); err != nil {
		t.Fatalf("a rejected token damaged the stored connection: %v", err)
	}
}
