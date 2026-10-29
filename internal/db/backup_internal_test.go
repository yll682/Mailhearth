package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// #29: a fresh database is not backed up; an existing one is copied, and
// the copy opens with the same applied migrations.
func TestBackupBeforeMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mailhearth.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if matches, _ := filepath.Glob(path + ".pre-*.bak"); len(matches) != 0 {
		t.Fatalf("新数据库不应生成备份：%v", matches)
	}
	ctx := context.Background()
	if err := d.backupBeforeMigration(ctx, 3, []string{"0099_future.sql"}); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(path + ".pre-0099_future-*.bak")
	if len(matches) != 1 {
		t.Fatalf("没有生成迁移前备份：%v", matches)
	}
	backup, err := sql.Open("sqlite", "file:"+filepath.ToSlash(matches[0]))
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var want, got int
	if err := d.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&want); err != nil {
		t.Fatal(err)
	}
	if err := backup.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want || got == 0 {
		t.Fatalf("备份中的迁移记录 %d 条，要求 %d 条", got, want)
	}
	// Re-opening applies nothing and therefore backs nothing up again.
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
	if matches, _ := filepath.Glob(path + ".pre-*.bak"); len(matches) != 1 || !strings.Contains(matches[0], "0099_future") {
		t.Fatalf("重复打开不应生成新备份：%v", matches)
	}
}
