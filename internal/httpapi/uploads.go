package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mailhearth/internal/core"
	"mailhearth/internal/db"
	"mailhearth/internal/secrets"
)

// uploadStore keeps composer attachments on disk until they are sent.
type uploadStore struct {
	dir string
	svc *core.Service
	log *slog.Logger
}

func newUploadStore(dataDir string, svc *core.Service, log *slog.Logger) *uploadStore {
	dir := filepath.Join(dataDir, "uploads")
	os.MkdirAll(dir, 0o700)
	u := &uploadStore{dir: dir, svc: svc, log: log}
	go u.janitor()
	return u
}

type uploadInfo struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	MIME     string `json:"mime"`
	Size     int64  `json:"size"`
}

func (u *uploadStore) path(id string) string { return filepath.Join(u.dir, id) }

func (u *uploadStore) save(ctx context.Context, memberID int64, filename, mimeType string, r io.Reader, limit int64) (*uploadInfo, error) {
	id := secrets.RandomToken(16)
	f, err := os.OpenFile(u.path(id), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	n, err := io.Copy(f, io.LimitReader(r, limit+1))
	f.Close()
	if err != nil || n > limit {
		os.Remove(u.path(id))
		if n > limit {
			return nil, &core.ValidationError{Msg: fmt.Sprintf("attachment exceeds the %d MB limit", limit>>20)}
		}
		return nil, err
	}
	if mimeType == "" || mimeType == "application/octet-stream" {
		if byExt := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))); byExt != "" {
			mimeType = byExt
		}
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	if _, err := u.svc.DB.ExecContext(ctx, `INSERT INTO uploads(id, member_id, filename, mime, size, created_at) VALUES (?,?,?,?,?,?)`, id, memberID, filename, mimeType, n, db.Now()); err != nil {
		os.Remove(u.path(id))
		return nil, err
	}
	return &uploadInfo{ID: id, Filename: filename, MIME: mimeType, Size: n}, nil
}

func (u *uploadStore) get(ctx context.Context, memberID int64, id string) (*uploadInfo, error) {
	var info uploadInfo
	err := u.svc.DB.QueryRowContext(ctx, `SELECT id, filename, mime, size FROM uploads WHERE id = ? AND member_id = ?`, id, memberID).Scan(&info.ID, &info.Filename, &info.MIME, &info.Size)
	if err != nil {
		return nil, core.ErrNotFound
	}
	return &info, nil
}

func (u *uploadStore) open(id string) (io.ReadCloser, error) { return os.Open(u.path(id)) }

func (u *uploadStore) delete(ctx context.Context, memberID int64, id string) {
	u.svc.DB.ExecContext(ctx, `DELETE FROM uploads WHERE id = ? AND member_id = ?`, id, memberID)
	os.Remove(u.path(id))
}

func (u *uploadStore) janitor() {
	for {
		time.Sleep(time.Hour)
		ctx := context.Background()
		cutoff := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
		rows, err := u.svc.DB.QueryContext(ctx, `SELECT id FROM uploads WHERE created_at < ?`, cutoff)
		if err != nil {
			continue
		}
		var ids []string
		for rows.Next() {
			var id string
			rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			os.Remove(u.path(id))
			u.svc.DB.ExecContext(ctx, `DELETE FROM uploads WHERE id = ?`, id)
		}
		// Orphaned files (crash between write and insert).
		entries, _ := os.ReadDir(u.dir)
		for _, e := range entries {
			info, err := e.Info()
			if err != nil || info.ModTime().After(time.Now().Add(-24*time.Hour)) {
				continue
			}
			var n int
			u.svc.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM uploads WHERE id = ?`, e.Name()).Scan(&n)
			if n == 0 {
				os.Remove(u.path(e.Name()))
			}
		}
	}
}

var errUploadTooLarge = errors.New("upload too large")

var _ = json.Marshal
