package httpapi

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mailhearth/internal/config"
)

func testServer(cfg *config.Config) *Server {
	return &Server{Cfg: cfg, Log: slog.Default(), signKey: []byte("0123456789abcdef0123456789abcdef"), limiter: newRateLimiter()}
}

func TestClientIPIgnoresSpoofedForwardedFor(t *testing.T) {
	cases := []struct {
		name   string
		trust  bool
		remote string
		xff    []string
		want   string
	}{
		{"headers ignored when not trusted", false, "203.0.113.9:1234", []string{"198.51.100.1"}, "203.0.113.9"},
		{"headers ignored from untrusted peer", true, "203.0.113.9:1234", []string{"198.51.100.1"}, "203.0.113.9"},
		{"proxy-appended address wins over spoofed left entries", true, "127.0.0.1:1234", []string{"198.51.100.1, 203.0.113.7"}, "203.0.113.7"},
		{"trusted hops are skipped", true, "10.0.0.2:1234", []string{"198.51.100.1, 203.0.113.7, 10.0.0.3"}, "203.0.113.7"},
		{"repeated headers are joined", true, "127.0.0.1:1234", []string{"198.51.100.1", "203.0.113.7"}, "203.0.113.7"},
		{"no header uses the peer", true, "127.0.0.1:1234", nil, "127.0.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := testServer(&config.Config{TrustProxyHeader: tc.trust})
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remote
			for _, v := range tc.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := s.clientIP(r); got != tc.want {
				t.Fatalf("clientIP = %q，要求 %q", got, tc.want)
			}
		})
	}
}

func TestViewTokenBinding(t *testing.T) {
	s := testServer(&config.Config{})
	token := s.viewToken(7, 3, "INBOX", 42, time.Minute, "session-hash")
	if id, hash, ok := s.checkViewToken(token, 3, "INBOX", 42); !ok || id != 7 || hash != "session-hash" {
		t.Fatal("有效令牌没有通过校验")
	}
	for name, check := range map[string]func() bool{
		"other mailbox": func() bool { _, _, ok := s.checkViewToken(token, 4, "INBOX", 42); return ok },
		"other folder":  func() bool { _, _, ok := s.checkViewToken(token, 3, "Sent", 42); return ok },
		"other uid":     func() bool { _, _, ok := s.checkViewToken(token, 3, "INBOX", 43); return ok },
		"tampered": func() bool {
			parts := strings.SplitN(token, ".", 2)
			_, _, ok := s.checkViewToken(parts[0]+"x."+parts[1], 3, "INBOX", 42)
			return ok
		},
		"expired": func() bool {
			_, _, ok := s.checkViewToken(s.viewToken(7, 3, "INBOX", 42, -time.Second, "session-hash"), 3, "INBOX", 42)
			return ok
		},
		"other key": func() bool {
			other := testServer(&config.Config{})
			other.signKey = []byte("another-signing-key-another-key!!")
			_, _, ok := other.checkViewToken(token, 3, "INBOX", 42)
			return ok
		},
	} {
		if check() {
			t.Fatalf("%s：令牌不应通过校验", name)
		}
	}
}

func TestCSRF(t *testing.T) {
	s := testServer(&config.Config{})
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := s.csrf(ok)
	cases := []struct {
		method, path, header, origin string
		want                         int
	}{
		{"GET", "/api/auth/me", "", "", 204},
		{"POST", "/api/auth/login", "", "", 403},
		{"POST", "/api/auth/login", "Mailhearth", "", 204},
		{"POST", "/api/auth/login", "Mailhearth", "http://mail.example.org", 204},
		{"POST", "/api/auth/login", "Mailhearth", "https://evil.example", 403},
		{"DELETE", "/api/mail/uploads/x", "", "", 403},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, "http://mail.example.org"+tc.path, nil)
		if tc.header != "" {
			r.Header.Set("X-Requested-With", tc.header)
		}
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s %s header=%q origin=%q 返回 %d，要求 %d", tc.method, tc.path, tc.header, tc.origin, w.Code, tc.want)
		}
	}
}

func TestRequestIDHeader(t *testing.T) {
	s := testServer(&config.Config{})
	var seen string
	h := s.logging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = requestID(r) }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/x", nil))
	if seen == "" || w.Header().Get("X-Request-ID") != seen {
		t.Fatalf("请求 ID 没有同时进入上下文和响应头：%q / %q", seen, w.Header().Get("X-Request-ID"))
	}
}

// #20: the part type is normalised before the allow-list check, so no
// spelling of an active type is ever served as itself.
func TestPartHeaders(t *testing.T) {
	cases := []struct {
		declared, charset string
		partInline, want  bool
		ct, disposition   string
	}{
		{"Text/HTML", "", false, false, "application/octet-stream", "attachment"},
		{"image/SVG+xml", "", true, true, "application/octet-stream", "attachment"},
		{"application/xhtml+xml", "", false, true, "application/octet-stream", "attachment"},
		{"application/zip", "", false, false, "application/octet-stream", "attachment"},
		{"not a type", "", false, false, "application/octet-stream", "attachment"},
		{"IMAGE/PNG", "", true, false, "image/png", "inline"},
		{"image/png", "", false, false, "image/png", "attachment"},
		{"application/pdf", "", false, true, "application/pdf", "inline"},
		{"text/plain", "iso-8859-1", false, false, "text/plain; charset=iso-8859-1", "attachment"},
	}
	for _, tc := range cases {
		ct, disposition := partHeaders(tc.declared, tc.charset, tc.partInline, tc.want)
		if ct != tc.ct || disposition != tc.disposition {
			t.Errorf("partHeaders(%q) = %q %q，要求 %q %q", tc.declared, ct, disposition, tc.ct, tc.disposition)
		}
	}
}

// #19: URLs and filenames are fully escaped.
func TestURLAndDispositionEscaping(t *testing.T) {
	u := partURL("/api/mail/mailboxes/1/messages/2", "1.2", "Projects/#1 ?a&b+c 项目", "tok-_en")
	want := "/api/mail/mailboxes/1/messages/2/parts/1.2?folder=Projects%2F%231+%3Fa%26b%2Bc+%E9%A1%B9%E7%9B%AE&t=tok-_en"
	if u != want {
		t.Fatalf("partURL = %q，要求 %q", u, want)
	}
	cd := contentDisposition("attachment", "报告 (final).pdf")
	if cd != `attachment; filename="__ (final).pdf"; filename*=UTF-8''%E6%8A%A5%E5%91%8A%20%28final%29.pdf` {
		t.Fatalf("Content-Disposition = %q", cd)
	}
}
