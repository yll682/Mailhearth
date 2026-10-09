package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"mailhearth/internal/db"
	"mailhearth/internal/provider"
)

func (s *Service) ClaimOperationSecret(ctx context.Context,orgID,actor int64,id string) (string,error) {
	var secret string
	expired:=false
	err:=s.DB.Tx(ctx,func(tx *sql.Tx)error{
		var owner int64;var sealed,claimed,expires sql.NullString
		err:=tx.QueryRowContext(ctx,`SELECT actor_member_id,claim_secret_enc,secret_claimed_at,claim_secret_expires_at FROM operations WHERE id=? AND org_id=?`,id,orgID).Scan(&owner,&sealed,&claimed,&expires)
		if db.IsNotFound(err){return ErrNotFound};if err!=nil{return err};if owner!=actor{return ErrForbidden}
		var active bool;if err:=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM members WHERE id=? AND org_id=? AND status='active')`,actor,orgID).Scan(&active);err!=nil{return err};if !active{return ErrForbidden}
		var required,permissionsJSON string;if err:=tx.QueryRowContext(ctx,`SELECT o.required_permission,r.permissions_json FROM operations o JOIN members m ON m.id=o.actor_member_id JOIN roles r ON r.id=m.role_id WHERE o.id=? AND o.org_id=?`,id,orgID).Scan(&required,&permissionsJSON);err!=nil{return err};var permissions []string;if err:=json.Unmarshal([]byte(permissionsJSON),&permissions);err!=nil{return err};if !HasPermission(permissions,required){return ErrForbidden}
		if claimed.Valid{return provider.Errorf("secret_already_claimed","主密码已经领取")}
		if !expires.Valid || !sealed.Valid || sealed.String==""{return provider.Errorf("secret_expired","没有可领取的主密码")}
		deadline,err:=time.Parse(time.RFC3339,expires.String);if err!=nil{return err};if !time.Now().Before(deadline){expired=true;_,err:=tx.ExecContext(ctx,`UPDATE operations SET claim_secret_enc=NULL WHERE id=?`,id);return err}
		secret,err=s.Box.Open(sealed.String);if err!=nil{return provider.Errorf("credential_decryption_failed","主密码解密失败")}
		res,err:=tx.ExecContext(ctx,`UPDATE operations SET claim_secret_enc=NULL,secret_claimed_at=?,updated_at=? WHERE id=? AND secret_claimed_at IS NULL`,db.Now(),db.Now(),id);if err!=nil{return err};n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return provider.Errorf("secret_already_claimed","主密码已经领取")};return nil
	});if err!=nil{return "",err};if expired{return "",provider.Errorf("secret_expired","主密码领取期限已结束")};return secret,nil
}

func (s *Service) expireOperationSecrets(ctx context.Context) error {
	_,err:=s.DB.ExecContext(ctx,`UPDATE operations SET claim_secret_enc=NULL WHERE claim_secret_enc IS NOT NULL AND claim_secret_expires_at<=?`,db.Now());return err
}
