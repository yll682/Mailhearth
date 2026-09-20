// Package httpapi exposes the product over HTTP: a JSON API under /api and
// the embedded single-page application for everything else.
package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"mailhearth/internal/config"
	"mailhearth/internal/core"
	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/mailproto/mailops"
	"mailhearth/internal/mailproto/sieve"
	"mailhearth/internal/model"
	"mailhearth/internal/purelymail"
)

const sessionCookie = "mh_session"

// Server holds handler dependencies.
type Server struct {
	Cfg     *config.Config
	Svc     *core.Service
	Pool    *imappool.Pool
	Log     *slog.Logger
	Static  http.Handler
	signKey []byte
	limiter *rateLimiter
	uploads *uploadStore
}

// New builds the server. signKey signs short-lived view tokens.
func New(cfg *config.Config, svc *core.Service, pool *imappool.Pool, static http.Handler, signKey []byte, log *slog.Logger) *Server {
	s := &Server{Cfg: cfg, Svc: svc, Pool: pool, Log: log, Static: static, signKey: signKey, limiter: newRateLimiter()}
	s.uploads = newUploadStore(cfg.DataDir, svc, log)
	return s
}

type ctxKey int

const principalKey ctxKey = 1

// principal is the authenticated member for a request.
type principal struct {
	Member  *model.Member
	OrgID   int64
	Perms   []string
	RoleKey string
	Token   string
}

func (p *principal) can(perm string) bool { return core.HasPermission(p.Perms, perm) }

func principalFrom(r *http.Request) *principal {
	p, _ := r.Context().Value(principalKey).(*principal)
	return p
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v != nil {
		json.NewEncoder(w).Encode(v)
	}
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	if err := dec.Decode(v); err != nil {
		return &core.ValidationError{Msg: "invalid JSON body: " + err.Error()}
	}
	return nil
}

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *core.ValidationError
	var se *sieve.ValidationError
	var ue *core.UpstreamError
	var ae *imappool.AuthError
	var sae *mailops.SendAuthError
	var pe *purelymail.Error
	switch {
	case errors.As(err, &ve):
		writeJSON(w, http.StatusBadRequest, apiError{Error: ve.Msg, Code: "invalid"})
	case errors.As(err, &se):
		writeJSON(w, http.StatusBadRequest, apiError{Error: se.Error(), Code: "invalid"})
	case errors.Is(err, core.ErrNotFound), errors.Is(err, mailops.ErrNotFound):
		writeJSON(w, http.StatusNotFound, apiError{Error: "not found", Code: "not_found"})
	case errors.Is(err, core.ErrForbidden):
		writeJSON(w, http.StatusForbidden, apiError{Error: strings.TrimPrefix(err.Error(), "forbidden: "), Code: "forbidden"})
	case errors.Is(err, core.ErrConflict):
		writeJSON(w, http.StatusConflict, apiError{Error: err.Error(), Code: "conflict"})
	case errors.Is(err, core.ErrNoSetup):
		writeJSON(w, http.StatusConflict, apiError{Error: "Purelymail connection is not configured", Code: "setup_required"})
	case errors.As(err, &ae):
		writeJSON(w, http.StatusBadGateway, apiError{Error: "the mail server rejected this mailbox credential; an administrator needs to reconnect the mailbox", Code: "mailbox_auth"})
	case errors.As(err, &sae):
		writeJSON(w, http.StatusBadGateway, apiError{Error: "the outgoing mail server rejected this mailbox credential; an administrator needs to reconnect the mailbox", Code: "mailbox_auth"})
	case errors.As(err, &ue), errors.As(err, &pe):
		s.Log.Warn("upstream error", "path", r.URL.Path, "err", err)
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error(), Code: "upstream"})
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeJSON(w, http.StatusGatewayTimeout, apiError{Error: "the mail server took too long to respond", Code: "timeout"})
	default:
		s.Log.Error("internal error", "path", r.URL.Path, "err", err)
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "internal error", Code: "internal"})
	}
}

func pathInt(r *http.Request, name string) (int64, error) {
	v, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || v <= 0 {
		return 0, &core.ValidationError{Msg: "invalid " + name}
	}
	return v, nil
}

func queryInt(r *http.Request, name string, def int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(name)); err == nil {
		return v
	}
	return def
}

func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
		if rip := r.Header.Get("X-Real-IP"); rip != "" {
			return rip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// --- middleware ---

func (s *Server) withSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err == nil && c.Value != "" {
			m, err := s.Svc.LookupSession(r.Context(), c.Value)
			if err == nil && m != nil {
				perms, roleKey, err := s.Svc.Permissions(r.Context(), m.ID)
				if err == nil {
					org, err := s.Svc.Org(r.Context())
					if err == nil {
						p := &principal{Member: m, OrgID: org.ID, Perms: perms, RoleKey: roleKey, Token: c.Value}
						r = r.WithContext(context.WithValue(r.Context(), principalKey, p))
					}
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if principalFrom(r) == nil {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "sign in required", Code: "unauthenticated"})
			return
		}
		next(w, r)
	}
}

func (s *Server) requirePerm(perm string, next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if !principalFrom(r).can(perm) {
			writeJSON(w, http.StatusForbidden, apiError{Error: "you do not have permission to do this", Code: "forbidden"})
			return
		}
		next(w, r)
	})
}

// csrf rejects state-changing requests that lack the custom header (which
// browsers cannot attach cross-origin without a CORS preflight) or come
// from a foreign Origin.
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("X-Requested-With") != "Mailhearth" {
			writeJSON(w, http.StatusForbidden, apiError{Error: "missing request header", Code: "csrf"})
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			host := r.Host
			if s.Cfg.TrustProxyHeader {
				if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
					host = fh
				}
			}
			if !strings.EqualFold(strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://"), host) {
				writeJSON(w, http.StatusForbidden, apiError{Error: "cross-origin request rejected", Code: "csrf"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		if strings.HasPrefix(r.URL.Path, "/api/") && (rw.status >= 400 || s.Log.Enabled(r.Context(), slog.LevelDebug)) {
			s.Log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", rw.status, "ms", time.Since(start).Milliseconds())
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// --- rate limiter (per key, fixed window) ---

type rateLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newRateLimiter() *rateLimiter { return &rateLimiter{hits: map[string][]time.Time{}} }

func (rl *rateLimiter) allow(key string, limit int, window time.Duration) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-window)
	list := rl.hits[key]
	keep := list[:0]
	for _, t := range list {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= limit {
		rl.hits[key] = keep
		return false
	}
	rl.hits[key] = append(keep, now)
	if len(rl.hits) > 10000 {
		for k, v := range rl.hits {
			if len(v) == 0 || v[len(v)-1].Before(cutoff) {
				delete(rl.hits, k)
			}
		}
	}
	return true
}

// --- signed view tokens (cookie-less access for the sandboxed message frame) ---

func (s *Server) viewToken(memberID, mailboxID int64, folder string, uid uint32, ttl time.Duration) string {
	exp := time.Now().Add(ttl).Unix()
	payload := fmt.Sprintf("%d|%d|%s|%d|%d", memberID, mailboxID, folder, uid, exp)
	mac := hmac.New(sha256.New, s.signKey)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) checkViewToken(token string, mailboxID int64, folder string, uid uint32) (memberID int64, ok bool) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return 0, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, false
	}
	mac := hmac.New(sha256.New, s.signKey)
	mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return 0, false
	}
	var pm, pb, puid, exp int64
	var pf string
	fields := strings.Split(string(payload), "|")
	if len(fields) != 5 {
		return 0, false
	}
	pm, _ = strconv.ParseInt(fields[0], 10, 64)
	pb, _ = strconv.ParseInt(fields[1], 10, 64)
	pf = fields[2]
	puid, _ = strconv.ParseInt(fields[3], 10, 64)
	exp, _ = strconv.ParseInt(fields[4], 10, 64)
	if pb != mailboxID || pf != folder || uint32(puid) != uid || time.Now().Unix() > exp {
		return 0, false
	}
	return pm, true
}

// Handler assembles the full router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routesAuth(mux)
	s.routesSetup(mux)
	s.routesAdmin(mux)
	s.routesMail(mux)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, apiError{Error: "no such endpoint", Code: "not_found"})
	})
	if s.Static != nil {
		mux.Handle("/", s.Static)
	}
	var h http.Handler = mux
	h = s.csrf(h)
	h = s.withSession(h)
	h = securityHeaders(h)
	h = s.logging(h)
	return h
}
