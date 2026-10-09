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
	"mailhearth/internal/provider"
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
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &core.ValidationError{Msg: "invalid JSON body: " + err.Error()}
	}
	var extra any
	if err:=dec.Decode(&extra);err!=io.EOF{return &core.ValidationError{Msg:"请求必须仅包含一个 JSON 值"}}
	return nil
}

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
	Details any `json:"details"`
	OperationID *string `json:"operationId"`
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *core.ValidationError
	var se *sieve.ValidationError
	var ue *core.UpstreamError
	var ae *imappool.AuthError
	var sae *mailops.SendAuthError
	var sieveAuth *sieve.AuthError
	var pe *purelymail.Error
	var typed *provider.TypedError
	switch {
	case errors.As(err,&typed):
		status:=http.StatusInternalServerError
		switch typed.Code {
		case "invalid","unsupported_auth_mode":status=http.StatusBadRequest
		case "forbidden":status=http.StatusForbidden
		case "not_found":status=http.StatusNotFound
		case "revision_conflict","operation_in_progress","idempotency_conflict","endpoint_unconfigured","endpoint_disabled","verification_required","external_action_required","unsupported_operation","connection_in_use","domain_in_use","mailbox_in_use","member_in_use","mailbox_has_history","folder_mapping_required","sieve_takeover_required","secret_expired","secret_already_claimed":status=http.StatusConflict
		case "identity_exists","submission_not_retryable","submission_unknown","submission_copy_conflict","submission_copy_unknown","submission_content_changed","draft_locator_changed","staging_missing","operation_not_retryable","operation_not_cancellable","operation_not_reconcilable":status=http.StatusConflict
		case "target_constraint_failed","sieve_extension_missing","unsupported_auth_mechanism":status=http.StatusUnprocessableEntity
		case "provider_auth_failed","mailbox_auth_failed","upstream_failed":status=http.StatusBadGateway
		case "upstream_rate_limited","mail_connection_capacity","notification_capacity_reached":status=http.StatusServiceUnavailable
		case "timeout":status=http.StatusGatewayTimeout
		case "api_replaced":status=http.StatusGone
		}
		writeJSON(w,status,apiError{Error:typed.Message,Code:typed.Code,OperationID:typed.OperationID,Details:typed.Details})
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
		writeJSON(w, http.StatusConflict, apiError{Error: "邮件连接尚未配置", Code: "setup_required"})
	case errors.As(err, &ae):
		writeJSON(w, http.StatusBadGateway, apiError{Error: "邮件服务器拒绝了此邮箱的凭据，请更新并验证协议配置", Code: "mailbox_auth_failed"})
	case errors.As(err, &sae):
		writeJSON(w, http.StatusBadGateway, apiError{Error: "SMTP 服务器拒绝了此邮箱的凭据，请更新并验证协议配置", Code: "mailbox_auth_failed"})
	case errors.As(err,&sieveAuth):
		status:=http.StatusBadGateway
		switch sieveAuth.Code{case "unsupported_auth_mechanism":status=http.StatusUnprocessableEntity;case "endpoint_unconfigured","verification_required":status=http.StatusConflict}
		writeJSON(w,status,apiError{Error:"ManageSieve 认证结果需要检查",Code:sieveAuth.Code})
	case errors.As(err, &ue), errors.As(err, &pe):
		s.Log.Warn("服务商请求失败", "path", r.URL.Path)
		writeJSON(w, http.StatusBadGateway, apiError{Error: "服务商请求失败", Code: "upstream_failed"})
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeJSON(w, http.StatusGatewayTimeout, apiError{Error: "the mail server took too long to respond", Code: "timeout"})
	default:
		s.Log.Error("内部请求失败", "path", r.URL.Path)
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

type viewClaims struct {
	MemberID int64 `json:"memberId"`
	MailboxID int64 `json:"mailboxId"`
	Folder string `json:"folder"`
	UID uint32 `json:"uid"`
	Expires int64 `json:"expires"`
	SessionHash string `json:"sessionHash"`
}

func (s *Server) viewToken(memberID, mailboxID int64, folder string, uid uint32, ttl time.Duration,sessionHash string) string {
	payload,err:=json.Marshal(viewClaims{memberID,mailboxID,folder,uid,time.Now().Add(ttl).Unix(),sessionHash});if err!=nil{panic(err)}
	mac := hmac.New(sha256.New, s.signKey)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) checkViewToken(token string, mailboxID int64, folder string, uid uint32) (memberID int64,sessionHash string, ok bool) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return 0,"", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0,"", false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0,"", false
	}
	mac := hmac.New(sha256.New, s.signKey)
	mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return 0,"", false
	}
	var claims viewClaims;if err:=json.Unmarshal(payload,&claims);err!=nil{return 0,"",false}
	if claims.MemberID<=0 || claims.SessionHash=="" || claims.MailboxID!=mailboxID || claims.Folder!=folder || claims.UID!=uid || time.Now().Unix()>=claims.Expires{return 0,"",false}
	return claims.MemberID,claims.SessionHash,true
}

// Handler assembles the full router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routesAuth(mux)
	s.routesSetup(mux)
	s.routesAdmin(mux)
	s.routesConnections(mux)
	s.routesDomainBindings(mux)
	s.routesResourceOptions(mux)
	s.routesOperations(mux)
	s.routesEndpoints(mux)
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
