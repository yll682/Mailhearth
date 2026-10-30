package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mailhearth/internal/core"
	"mailhearth/internal/model"
	"mailhearth/internal/secrets"
)

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.Cfg.SecureCook,
		SameSite: http.SameSiteLaxMode, MaxAge: maxAge,
	})
}

type meResponse struct {
	Member      *model.Member            `json:"member"`
	Org         *model.Organization      `json:"org"`
	RoleKey     string                   `json:"roleKey"`
	Permissions []string                 `json:"permissions"`
	Mailboxes   []core.AccessibleMailbox `json:"mailboxes"`
	Setup       *core.SetupStatus        `json:"setup"`
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, p *principal) {
	org, err := s.Svc.Org(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	mbs, err := s.Svc.AccessibleMailboxes(r.Context(), p.OrgID, p.Member.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	st, err := s.Svc.Status(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, meResponse{Member: p.Member, Org: org, RoleKey: p.RoleKey, Permissions: p.Perms, Mailboxes: mbs, Setup: st})
}

// startSession signs m in: it creates the session, sets the cookie and
// answers with the same payload as /api/auth/me.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, m *model.Member, ip string) {
	token, err := s.Svc.CreateSession(r.Context(), m.ID, ip, r.UserAgent())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	perms, roleKey, err := s.Svc.Permissions(r.Context(), m.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	orgID, err := s.orgID(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.setSessionCookie(w, token, int(s.Cfg.SessionTTL.Seconds()))
	s.me(w, r, &principal{Member: m, OrgID: orgID, Perms: perms, RoleKey: roleKey, Token: token})
}

func (s *Server) routesAuth(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		ip := s.clientIP(r)
		if !s.limiter.allow("login:"+ip, 12, time.Minute) {
			writeJSON(w, http.StatusTooManyRequests, apiError{Error: "too many attempts, try again in a minute", Code: "rate_limited"})
			return
		}
		var in struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		// Per-account limit: spreading guesses over many source addresses
		// must not multiply the attempts available against one login.
		if !s.limiter.allow("login-account:"+strings.ToLower(strings.TrimSpace(in.Email)), 30, 15*time.Minute) {
			writeJSON(w, http.StatusTooManyRequests, apiError{Error: "too many attempts for this account, try again later", Code: "rate_limited"})
			return
		}
		m, err := s.Svc.VerifyLogin(r.Context(), in.Email, in.Password)
		if err != nil {
			switch {
			case errors.Is(err, core.ErrAccountInactive):
				writeJSON(w, http.StatusUnauthorized, apiError{Error: "this account is not active", Code: "bad_credentials"})
			case errors.Is(err, core.ErrBadCredentials):
				writeJSON(w, http.StatusUnauthorized, apiError{Error: "incorrect email or password", Code: "bad_credentials"})
			default:
				s.fail(w, r, err)
			}
			return
		}
		s.startSession(w, r, m, ip)
	})

	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if p := principalFrom(r); p != nil {
			if err := s.Svc.DeleteSession(r.Context(), p.Token); err != nil {
				s.fail(w, r, err)
				return
			}
		}
		s.setSessionCookie(w, "", -1)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("GET /api/auth/me", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		s.me(w, r, principalFrom(r))
	}))

	mux.HandleFunc("POST /api/auth/password", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		if !s.limiter.allow("password:"+strconv.FormatInt(p.Member.ID, 10), 10, 15*time.Minute) {
			writeJSON(w, http.StatusTooManyRequests, apiError{Error: "too many attempts, try again later", Code: "rate_limited"})
			return
		}
		var in struct {
			Current string `json:"current"`
			New     string `json:"new"`
		}
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		if _, err := s.Svc.VerifyLogin(r.Context(), p.Member.LoginEmail, in.Current); err != nil {
			if errors.Is(err, core.ErrForbidden) {
				writeJSON(w, http.StatusBadRequest, apiError{Error: "current password is incorrect", Code: "invalid"})
				return
			}
			s.fail(w, r, err)
			return
		}
		if err := s.Svc.ChangeOwnPassword(r.Context(), p.OrgID, p.Member.ID, in.New, secrets.HashToken(p.Token)); err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))

	mux.HandleFunc("GET /api/auth/invite/{token}", func(w http.ResponseWriter, r *http.Request) {
		info, err := s.Svc.Invite(r.Context(), r.PathValue("token"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, info)
	})

	mux.HandleFunc("POST /api/auth/invite/{token}/accept", func(w http.ResponseWriter, r *http.Request) {
		ip := s.clientIP(r)
		if !s.limiter.allow("invite:"+ip, 10, time.Minute) {
			writeJSON(w, http.StatusTooManyRequests, apiError{Error: "too many attempts", Code: "rate_limited"})
			return
		}
		var in struct {
			Password    string `json:"password"`
			DisplayName string `json:"displayName"`
		}
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		m, err := s.Svc.AcceptInvite(r.Context(), r.PathValue("token"), in.Password, in.DisplayName)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.startSession(w, r, m, ip)
	})
}

func (s *Server) routesSetup(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/setup/status", func(w http.ResponseWriter, r *http.Request) {
		st, err := s.Svc.Status(r.Context())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, st)
	})

	mux.HandleFunc("POST /api/setup/init", func(w http.ResponseWriter, r *http.Request) {
		if !s.limiter.allow("setup:"+s.clientIP(r), 10, time.Minute) {
			writeJSON(w, http.StatusTooManyRequests, apiError{Error: "too many attempts, try again in a minute", Code: "rate_limited"})
			return
		}
		var in core.InitRequest
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		m, err := s.Svc.Init(r.Context(), in)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.startSession(w, r, m, s.clientIP(r))
	})

	mux.HandleFunc("POST /api/setup/complete", s.requirePerm(model.PermOrgManage, func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		var in struct {
			ConnectionID  int64  `json:"connectionId"`
			BindMailboxID *int64 `json:"bindMailboxId"`
		}
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		err := s.Svc.CompleteConnectionSetup(r.Context(), p.OrgID, p.Member.ID, in.ConnectionID, in.BindMailboxID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))
}
