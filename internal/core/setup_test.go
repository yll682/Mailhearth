package core

import (
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

	"mailhearth/internal/config"
	"mailhearth/internal/db"
	"mailhearth/internal/secrets"
)

// An instance without any setup token refuses to create the organisation
// rather than letting the first visitor claim it.
func TestInitFailsClosedWithoutSetupToken(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "mailhearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	box, err := secrets.NewBox(make([]byte, 32), "credentials")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	svc := New(database, cfg, box, nil, slog.Default())
	req := InitRequest{OrgName: "Acme", AdminName: "Alice", AdminEmail: "alice@example.org", Password: "correct horse battery"}
	for _, token := range []string{"", "anything"} {
		req.SetupToken = token
		if _, err := svc.Init(t.Context(), req); !errors.Is(err, ErrForbidden) {
			t.Fatalf("token %q: want ErrForbidden, got %v", token, err)
		}
	}
	cfg.SetupToken = "configured"
	req.SetupToken = "configured"
	if _, err := svc.Init(t.Context(), req); err != nil {
		t.Fatal(err)
	}
}
