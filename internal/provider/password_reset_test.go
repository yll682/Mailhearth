package provider_test

import (
	"context"
	"errors"
	"testing"

	"mailhearth/internal/provider"
	"mailhearth/internal/provider/migadu"
	"mailhearth/internal/provider/purelymail"
)

func TestMultiProviderPasswordResetRequiresSavedCandidate(t *testing.T) {
	adapters:=[]provider.Provider{purelymail.New("https://purelymail.com/api/v0","",""),migadu.New("https://api.migadu.com/v1","","")}
	for _,adapter:=range adapters{t.Run(string(adapter.Kind()),func(t *testing.T){_,err:=adapter.ResetMailboxPassword(context.Background(),provider.UpdateMailboxRequest{Domain:"example.org",LocalPart:"member"});var typed *provider.TypedError;if !errors.As(err,&typed) || typed.Code!="invalid"{t.Fatalf("缺少候选密码的请求没有在网络操作前被拒绝：%v",err)}})}
}

func TestMultiProviderMailboxCreateRequiresSavedCandidate(t *testing.T) {
	adapters:=[]provider.Provider{purelymail.New("https://purelymail.com/api/v0","",""),migadu.New("https://api.migadu.com/v1","","")}
	for _,adapter:=range adapters{t.Run(string(adapter.Kind()),func(t *testing.T){_,err:=adapter.CreateMailbox(context.Background(),provider.CreateMailboxRequest{Domain:"example.org",LocalPart:"member"});var typed *provider.TypedError;if !errors.As(err,&typed) || typed.Code!="invalid"{t.Fatalf("缺少候选密码的创建请求没有在网络操作前被拒绝：%v",err)}})}
}
