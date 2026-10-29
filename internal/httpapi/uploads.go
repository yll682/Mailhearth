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

// validUploadID matches ids minted by save (secrets.RandomToken(16)).
func validUploadID(id string) bool {
	if len(id) != 22 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// path maps an upload id to its file; ids that are not plain upload ids
// map to "" so that no caller can reach outside the uploads directory.
func (u *uploadStore) path(id string) string {
	if !validUploadID(id) {
		return ""
	}
	return filepath.Join(u.dir, id)
}

// save stores one upload. limit caps the single file; quota caps the
// member's pending (unsent, not yet cleaned up) uploads in total so that
// repeated uploads cannot fill the data volume. quota <= 0 disables it.
func (u *uploadStore) save(ctx context.Context, memberID int64, filename, mimeType string, r io.Reader, limit, quota int64) (*uploadInfo, error) {
	copyLimit := limit
	if quota > 0 {
		var used int64
		if err := u.svc.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(size),0) FROM uploads WHERE member_id=?`, memberID).Scan(&used); err != nil {
			return nil, err
		}
		if quota-used < copyLimit {
			copyLimit = quota - used
		}
		if copyLimit <= 0 {
			return nil, uploadQuotaError(quota)
		}
	}
	id := secrets.RandomToken(16)
	path := u.path(id)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	n, err := io.Copy(f, io.LimitReader(r, copyLimit+1))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil || n > copyLimit {
		os.Remove(path)
		if err != nil {
			return nil, err
		}
		if n > limit {
			return nil, &core.ValidationError{Msg: fmt.Sprintf("attachment exceeds the %d MB limit", limit>>20)}
		}
		return nil, uploadQuotaError(quota)
	}
	if mimeType == "" || mimeType == "application/octet-stream" {
		if byExt := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))); byExt != "" {
			mimeType = byExt
		}
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	// The quota is re-checked in the insert itself so that concurrent
	// uploads cannot overshoot it together.
	res, err := u.svc.DB.ExecContext(ctx, `INSERT INTO uploads(id, member_id, filename, mime, size, created_at) SELECT ?,?,?,?,?,? WHERE ? <= 0 OR (SELECT COALESCE(SUM(size),0) FROM uploads WHERE member_id=?) + ? <= ?`, id, memberID, filename, mimeType, n, db.Now(), quota, memberID, n, quota)
	if err != nil {
		os.Remove(path)
		return nil, err
	}
	if inserted, err := res.RowsAffected(); err != nil || inserted != 1 {
		os.Remove(path)
		if err != nil {
			return nil, err
		}
		return nil, uploadQuotaError(quota)
	}
	return &uploadInfo{ID: id, Filename: filename, MIME: mimeType, Size: n}, nil
}

func uploadQuotaError(quota int64) error {
	return &core.ValidationError{Msg: fmt.Sprintf("pending attachments exceed your %d MB limit; send or remove some attachments first", quota>>20)}
}

func (u *uploadStore) get(ctx context.Context, memberID int64, id string) (*uploadInfo, error) {
	if !validUploadID(id) {
		return nil, core.ErrNotFound
	}
	var info uploadInfo
	err := u.svc.DB.QueryRowContext(ctx, `SELECT id, filename, mime, size FROM uploads WHERE id = ? AND member_id = ?`, id, memberID).Scan(&info.ID, &info.Filename, &info.MIME, &info.Size)
	if err != nil {
		return nil, core.ErrNotFound
	}
	return &info, nil
}

func (u *uploadStore) open(id string) (io.ReadCloser, error) {
	path := u.path(id)
	if path == "" {
		return nil, core.ErrNotFound
	}
	return os.Open(path)
}

// delete removes the caller's own upload. The file is only touched after
// the database confirms that the id belongs to memberID.
func (u *uploadStore) delete(ctx context.Context, memberID int64, id string) error {
	path := u.path(id)
	if path == "" {
		return core.ErrNotFound
	}
	res, err := u.svc.DB.ExecContext(ctx, `DELETE FROM uploads WHERE id = ? AND member_id = ?`, id, memberID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		// Already gone (or not ours): deletion is idempotent, but only a
		// confirmed owner row ever leads to touching the filesystem.
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
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
