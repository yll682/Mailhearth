package purelymail_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"mailhearth/internal/purelymail"
	"mailhearth/internal/purelymail/fake"
)

func TestClientAgainstFake(t *testing.T) {
	f := fake.New("tok")
	f.AddDomain(fake.Domain{Name: "acme.test", MX: true, SPF: true, DKIM: true, DMARC: false})
	srv := httptest.NewServer(f)
	defer srv.Close()
	ctx := context.Background()

	bad := purelymail.New(srv.URL, "wrong")
	if _, err := bad.CheckAccountCredit(ctx); !purelymail.IsInvalidToken(err) {
		t.Fatalf("expected invalid token error, got %v", err)
	}

	c := purelymail.New(srv.URL, "tok")
	credit, err := c.CheckAccountCredit(ctx)
	if err != nil || credit == "" {
		t.Fatalf("credit: %v %q", err, credit)
	}
	domains, err := c.ListDomains(ctx, false)
	if err != nil || len(domains) != 1 || domains[0].Name != "acme.test" || !domains[0].DNSSummary.PassesMx || domains[0].DNSSummary.PassesDmarc {
		t.Fatalf("domains: %v %+v", err, domains)
	}
	if err := c.CreateUser(ctx, purelymail.CreateUserRequest{UserName: "alice", DomainName: "acme.test", Password: "pw", EnableSearchIndexing: true}); err != nil {
		t.Fatal(err)
	}
	users, err := c.ListUsers(ctx)
	if err != nil || len(users) != 1 || users[0] != "alice@acme.test" {
		t.Fatalf("users: %v %v", err, users)
	}
	pw, err := c.CreateAppPassword(ctx, "alice@acme.test", "Mailhearth")
	if err != nil || pw == "" {
		t.Fatalf("app password: %v", err)
	}
	if !f.CredentialValid("alice@acme.test", pw) {
		t.Fatal("app password should validate")
	}
	if err := c.DeleteAppPassword(ctx, "alice@acme.test", pw); err != nil {
		t.Fatal(err)
	}
	if f.CredentialValid("alice@acme.test", pw) {
		t.Fatal("app password should be revoked")
	}
	np := "newpw"
	if err := c.ModifyUser(ctx, purelymail.ModifyUserRequest{UserName: "alice@acme.test", NewPassword: &np}); err != nil {
		t.Fatal(err)
	}
	if !f.CredentialValid("alice@acme.test", "newpw") {
		t.Fatal("password change not applied")
	}
	if err := c.CreateRoutingRule(ctx, purelymail.CreateRoutingRuleRequest{DomainName: "acme.test", MatchUser: "sales", TargetAddresses: []string{"alice@acme.test"}}); err != nil {
		t.Fatal(err)
	}
	rules, err := c.ListRoutingRules(ctx)
	if err != nil || len(rules) != 1 || rules[0].MatchUser != "sales" {
		t.Fatalf("rules: %v %+v", err, rules)
	}
	if err := c.DeleteRoutingRule(ctx, rules[0].ID); err != nil {
		t.Fatal(err)
	}
	if code, err := c.GetOwnershipCode(ctx); err != nil || code == "" {
		t.Fatalf("ownership: %v", err)
	}
	if err := c.DeleteUser(ctx, "alice@acme.test"); err != nil {
		t.Fatal(err)
	}
}
