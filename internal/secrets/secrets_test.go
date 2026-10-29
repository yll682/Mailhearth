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

// #37: generated passwords use the whole alphabet uniformly enough, and
// hints reveal at most two characters.
func TestRandomPasswordAndHint(t *testing.T) {
	counts := map[rune]int{}
	for i := 0; i < 20000; i++ {
		p := RandomPassword()
		if len(p) != 24 {
			t.Fatalf("密码长度为 %d", len(p))
		}
		for _, r := range p {
			counts[r]++
		}
	}
	if len(counts) != 61 {
		t.Fatalf("只出现了 %d 种字符，要求 61 种", len(counts))
	}
	min, max := 1<<30, 0
	for _, n := range counts {
		if n < min {
			min = n
		}
		if n > max {
			max = n
		}
	}
	// 480000 draws over 61 symbols: about 7870 each (σ ≈ 88), so a uniform
	// generator stays well under 1.15. Plain modulo (256 % 61 = 12) made 12
	// symbols 25% more likely than the rest.
	if float64(max)/float64(min) > 1.15 {
		t.Fatalf("字符分布不均匀：最少 %d，最多 %d", min, max)
	}
	if h := Hint("sk-live-abcdefgh12"); h != "…12" {
		t.Fatalf("Hint = %q", h)
	}
	if h := Hint("short-key"); h != "****" {
		t.Fatalf("短密钥 Hint = %q", h)
	}
}
