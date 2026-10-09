package core

import (
	"context"
	"time"
	"github.com/google/uuid"
	"mailhearth/internal/db"
	"mailhearth/internal/secrets"
)

type accessRequest struct {
	memberID int64
	mailboxID int64
	sessionHash string
	cancel context.CancelFunc
}

func (s *Service) RegisterMailRequest(ctx context.Context,memberID,mailboxID int64,session string) (context.Context,context.CancelFunc) {
	hash:="";if session!=""{hash=secrets.HashToken(session)}
	return s.RegisterMailRequestSessionHash(ctx,memberID,mailboxID,hash)
}

func (s *Service) RegisterMailRequestSessionHash(ctx context.Context,memberID,mailboxID int64,sessionHash string) (context.Context,context.CancelFunc) {
	ctx,cancel:=context.WithCancel(ctx);id:=uuid.NewString()
	s.requestMu.Lock();s.requests[id]=accessRequest{memberID,mailboxID,sessionHash,cancel};s.requestMu.Unlock()
	return ctx,func(){cancel();s.requestMu.Lock();delete(s.requests,id);s.requestMu.Unlock()}
}

func (s *Service) SessionHashActive(ctx context.Context,memberID int64,hash string) (bool,error) {
	var active bool
	err:=s.DB.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM sessions s JOIN members m ON m.id=s.member_id WHERE s.token_hash=? AND s.member_id=? AND s.expires_at>? AND m.status='active')`,hash,memberID,db.Now()).Scan(&active)
	return active,err
}

func (s *Service) cancelMailRequests(memberID,mailboxID int64) {
	s.requestMu.Lock();defer s.requestMu.Unlock()
	for _,request:=range s.requests{if (memberID==0 || memberID==request.memberID) && (mailboxID==0 || mailboxID==request.mailboxID){request.cancel()}}
}

func (s *Service) cancelConnectionMailRequests(connectionID int64) error {
	ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second);defer cancel()
	rows,err:=s.DB.QueryContext(ctx,`SELECT id FROM mailboxes WHERE connection_id=?`,connectionID);if err!=nil{return err}
	var ids []int64
	for rows.Next(){var id int64;if err:=rows.Scan(&id);err!=nil{rows.Close();return err};ids=append(ids,id)}
	err=rows.Err();rows.Close();if err!=nil{return err}
	for _,id:=range ids{s.cancelMailRequests(0,id)}
	return nil
}
