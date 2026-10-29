package httpapi

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mailhearth/internal/config"
	"mailhearth/internal/core"
	"mailhearth/internal/db"
	"mailhearth/internal/secrets"
)

// #34: the janitor removes expired uploads and orphaned files, keeps fresh
// ones and foreign files, and stops when closed.
func TestUploadSweepAndClose(t *testing.T) {
	root := t.TempDir()
	database, err := db.Open(filepath.Join(root, "mailhearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	box, err := secrets.NewBox(make([]byte, 32), "credentials")
	if err != nil {
		t.Fatal(err)
	}
	svc := core.New(database, &config.Config{DataDir: root, SessionTTL: time.Hour}, box, nil, slog.Default())
	u := newUploadStore(root, svc, slog.Default())
	ctx := context.Background()
	owner, err := svc.Init(ctx, core.InitRequest{OrgName: "o", AdminName: "m", AdminEmail: "m@example.org", Password: "sweep-password-1"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := u.save(ctx, owner.ID, "old.txt", "text/plain", strings.NewReader("old"), 1<<20, 0)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := u.save(ctx, owner.ID, "fresh.txt", "text/plain", strings.NewReader("fresh"), 1<<20, 0)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-48 * time.Hour)
	if _, err := database.Exec(`UPDATE uploads SET created_at=? WHERE id=?`, past.UTC().Format(time.RFC3339), old.ID); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(u.dir, secrets.RandomToken(16))
	foreign := filepath.Join(u.dir, "README")
	for _, p := range []string{orphan, foreign} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(p, past, past)
	}
	if err := u.sweep(ctx, time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for path, wantExists := range map[string]bool{u.path(old.ID): false, orphan: false, u.path(fresh.ID): true, foreign: true} {
		if _, err := os.Stat(path); (err == nil) != wantExists {
			t.Errorf("%s 存在状态错误：%v", filepath.Base(path), err)
		}
	}
	done := make(chan struct{})
	go func() { u.close(); u.close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("close 没有停止清理协程")
	}
}
