package purelymail_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"mailhearth/internal/purelymail"
)

// #26: a redirect to another host must not receive the API token.
func TestClientDoesNotForwardTokenAcrossHosts(t *testing.T) {
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Purelymail-Api-Token") != "" {
			leaked.Store(true)
		}
		w.Write([]byte(`{"type":"success","result":{"users":[]}}`))
	}))
	defer other.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer api.Close()
	if _, err := purelymail.New(api.URL, "secret-token").ListUsers(context.Background()); err == nil {
		t.Fatal("跨主机重定向不应作为成功响应处理")
	}
	if leaked.Load() {
		t.Fatal("API token 被转发到了其他主机")
	}
}
