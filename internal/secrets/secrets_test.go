package secrets

import "testing"

func TestBoxRoundTrip(t *testing.T) {
	master := make([]byte, 32)
	for i := range master {
		master[i] = byte(i)
	}
	b, err := NewBox(master, "test")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := b.Seal("app-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if sealed == "app-password-123" {
		t.Fatal("not encrypted")
	}
	got, err := b.Open(sealed)
	if err != nil || got != "app-password-123" {
		t.Fatalf("open: %v %q", err, got)
	}
	other, _ := NewBox(master, "other-purpose")
	if _, err := other.Open(sealed); err == nil {
		t.Fatal("expected failure with different purpose key")
	}
	if v, err := b.Open(""); err != nil || v != "" {
		t.Fatal("empty should round-trip to empty")
	}
}

func TestPassword(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "correct horse") {
		t.Fatal("should verify")
	}
	if VerifyPassword(h, "wrong") {
		t.Fatal("should not verify")
	}
	if VerifyPassword("garbage", "x") {
		t.Fatal("garbage hash should fail")
	}
}

func TestTokens(t *testing.T) {
	a, b := RandomToken(32), RandomToken(32)
	if a == b || len(a) < 40 {
		t.Fatal("tokens should be random and long")
	}
	if HashToken(a) == HashToken(b) {
		t.Fatal("hash collision")
	}
	if len(RandomPassword()) != 24 {
		t.Fatal("password length")
	}
}
