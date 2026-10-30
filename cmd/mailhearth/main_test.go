package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer collects the server log while request goroutines write to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureLog(t *testing.T) *syncBuffer {
	t.Helper()
	saved := logOutput
	buf := &syncBuffer{}
	logOutput = buf
	t.Cleanup(func() { logOutput = saved })
	return buf
}

var loggedToken = regexp.MustCompile(`setupToken=([A-Za-z0-9_-]+)`)

// startDev runs the binary's entry point against a data directory with the
// embedded dev stack, as `make dev` does, and returns its base URL.
func startDev(t *testing.T, dataDir string) (base string, stop func()) {
	t.Helper()
	t.Setenv("MAILHEARTH_DEV_STACK", "1")
	t.Setenv("MAILHEARTH_DATA_DIR", dataDir)
	t.Setenv("MAILHEARTH_LISTEN", "127.0.0.1:0")
	t.Setenv("MAILHEARTH_LOG_LEVEL", "warn")
	t.Setenv("GOMEMLIMIT", "off")
	ctx, cancel := context.WithCancel(context.Background())
	addr := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() { done <- run(ctx, true, func(a net.Addr) { addr <- a }) }()
	select {
	case a := <-addr:
		base = "http://" + a.String()
	case err := <-done:
		cancel()
		t.Fatalf("server did not start: %v", err)
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("server did not start in time")
	}
	return base, func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("server stopped with %v", err)
		}
	}
}

func call(t *testing.T, c *http.Client, method, url string, body any, status int, out any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "Mailhearth")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != status {
		t.Fatalf("%s %s: status %d, want %d: %s", method, url, resp.StatusCode, status, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatal(err)
		}
	}
}

// A brand-new data directory initialises the database, serves the setup
// flow, persists the organisation and reopens cleanly. Without
// MAILHEARTH_SETUP_TOKEN the setup token is generated and logged.
func TestDevStackFreshInstall(t *testing.T) {
	t.Setenv("MAILHEARTH_SETUP_TOKEN", "")
	logs := captureLog(t)
	dataDir := t.TempDir()
	base, stop := startDev(t, dataDir)
	match := loggedToken.FindStringSubmatch(logs.String())
	if match == nil || !strings.Contains(logs.String(), "FIRST-TIME SETUP") {
		t.Fatalf("no setup token in the startup log:\n%s", logs)
	}
	token := match[1]
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 10 * time.Second}

	var status struct {
		NeedsSetup bool   `json:"needsSetup"`
		Step       string `json:"step"`
		DevStack   bool   `json:"devStack"`
	}
	call(t, c, "GET", base+"/api/setup/status", nil, 200, &status)
	if !status.NeedsSetup || status.Step != "org" || !status.DevStack {
		t.Fatalf("unexpected fresh status: %+v", status)
	}
	init := map[string]string{"orgName": "Acme", "adminName": "Alice", "adminEmail": "alice@acme.test", "password": "correct horse battery"}
	call(t, c, "POST", base+"/api/setup/init", init, 403, nil)
	init["setupToken"] = token + "x"
	call(t, c, "POST", base+"/api/setup/init", init, 403, nil)
	init["setupToken"] = token
	call(t, c, "POST", base+"/api/setup/init", init, 200, nil)
	var me struct {
		Member struct {
			LoginEmail string `json:"loginEmail"`
		} `json:"member"`
		Org struct {
			Name string `json:"name"`
		} `json:"org"`
	}
	call(t, c, "GET", base+"/api/auth/me", nil, 200, &me)
	if me.Member.LoginEmail != "alice@acme.test" || me.Org.Name != "Acme" {
		t.Fatalf("unexpected me: %+v", me)
	}
	stop()

	// Second start on the same directory: no re-initialisation, data kept,
	// and no setup token once the organisation exists.
	before := len(loggedToken.FindAllString(logs.String(), -1))
	base, stop = startDev(t, dataDir)
	defer stop()
	if after := len(loggedToken.FindAllString(logs.String(), -1)); after != before {
		t.Fatal("a setup token was generated for an instance that is already set up")
	}
	call(t, c, "GET", base+"/api/setup/status", nil, 200, &status)
	if status.Step != "connection" {
		t.Fatalf("organisation not persisted: %+v", status)
	}
	jar, _ = cookiejar.New(nil)
	c = &http.Client{Jar: jar, Timeout: 10 * time.Second}
	call(t, c, "POST", base+"/api/auth/login", map[string]string{"email": "alice@acme.test", "password": "correct horse battery"}, 200, nil)
}

// A configured MAILHEARTH_SETUP_TOKEN is used as is and never logged.
func TestConfiguredSetupToken(t *testing.T) {
	t.Setenv("MAILHEARTH_SETUP_TOKEN", "operator-chosen-token")
	logs := captureLog(t)
	base, stop := startDev(t, t.TempDir())
	defer stop()
	if strings.Contains(logs.String(), "operator-chosen-token") || loggedToken.MatchString(logs.String()) {
		t.Fatalf("configured setup token leaked into the log:\n%s", logs)
	}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	init := map[string]string{"orgName": "Acme", "adminName": "Alice", "adminEmail": "alice@acme.test", "password": "correct horse battery", "setupToken": "wrong"}
	call(t, c, "POST", base+"/api/setup/init", init, 403, nil)
	init["setupToken"] = "operator-chosen-token"
	call(t, c, "POST", base+"/api/setup/init", init, 200, nil)
}
