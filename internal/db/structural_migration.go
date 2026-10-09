package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// 结构迁移在独立连接中保留索引、trigger 和 view，完成外键检查后提交。
func (d *DB) migrateStructural(ctx context.Context, version, body string) (resultErr error) {
	conn, err := d.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, conn.Close()) }()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer func() {
		_, err := conn.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`)
		resultErr = errors.Join(resultErr, err)
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	type schemaObject struct{ kind, name, statement string }
	rows, err := tx.QueryContext(ctx, `SELECT type,name,sql FROM sqlite_schema WHERE type IN ('view','trigger','index') AND sql IS NOT NULL ORDER BY CASE type WHEN 'view' THEN 0 WHEN 'index' THEN 1 ELSE 2 END,name`)
	if err != nil {
		return err
	}
	var objects []schemaObject
	for rows.Next() {
		var object schemaObject
		if err := rows.Scan(&object.kind, &object.name, &object.statement); err != nil {
			rows.Close()
			return err
		}
		objects = append(objects, object)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, object := range objects {
		identifier := `"` + strings.ReplaceAll(object.name, `"`, `""`) + `"`
		if _, err := tx.ExecContext(ctx, "DROP "+strings.ToUpper(object.kind)+" "+identifier); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, body); err != nil {
		return err
	}
	for _, object := range objects {
		if _, err := tx.ExecContext(ctx, object.statement); err != nil {
			return err
		}
	}
	if err := validateAssociationScopes(ctx, tx); err != nil {
		return err
	}
	var kind, id string
	var related sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT object_type,object_id,related_id FROM additional_scope_errors ORDER BY object_type,object_id LIMIT 1`).Scan(&kind, &id, &related)
	if err != nil && !IsNotFound(err) {
		return err
	}
	if err == nil {
		return fmt.Errorf("association_scope_conflict: %s id=%s relatedId=%s", kind, id, related.String)
	}
	rows, err = tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	var violations []string
	for rows.Next() {
		var table, parent string
		var rowID sql.NullInt64
		var keyID int64
		if err := rows.Scan(&table, &rowID, &parent, &keyID); err != nil {
			rows.Close()
			return err
		}
		violations = append(violations, fmt.Sprintf("%s row=%d parent=%s key=%d", table, rowID.Int64, parent, keyID))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(violations) > 0 {
		return fmt.Errorf("foreign key check failed: %s", strings.Join(violations, "; "))
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES (?,?)`, version, Now()); err != nil {
		return err
	}
	return tx.Commit()
}
