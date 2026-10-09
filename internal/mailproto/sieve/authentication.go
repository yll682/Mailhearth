package sieve

import (
	"context"
	"errors"
	"net"

	managesieve "github.com/hstern/go-managesieve"
)

type AuthError struct { Code string }

func (e *AuthError) Error() string { return "ManageSieve 认证检查失败："+e.Code }

func classifyAuthenticationError(err error) error {
	if err==nil{return nil}
	if errors.Is(err,context.Canceled) || errors.Is(err,context.DeadlineExceeded){return err}
	var network *net.OpError;if errors.As(err,&network){return &AuthError{Code:"upstream_failed"}}
	var referral *managesieve.ReferralError;if errors.As(err,&referral){return &AuthError{Code:"endpoint_unconfigured"}}
	var server *managesieve.ServerError
	if errors.As(err,&server){
		switch server.CodeName(){
		case managesieve.CodeAuthTooWeak,managesieve.CodeEncryptNeeded,managesieve.CodeTransitionNeeded:return &AuthError{Code:"unsupported_auth_mechanism"}
		case managesieve.CodeTryLater,managesieve.CodeQuota:return &AuthError{Code:"upstream_failed"}
		}
		if server.Status=="BYE"{return &AuthError{Code:"upstream_failed"}}
		return &AuthError{Code:"verification_required"}
	}
	return &AuthError{Code:"upstream_failed"}
}
