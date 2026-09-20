package httpapi

import (
	"net/http"
	"strings"
	"time"

	"mailhearth/internal/core"
	"mailhearth/internal/model"
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
	st, _ := s.Svc.Status(r.Context())
	writeJSON(w, http.StatusOK, meResponse{Member: p.Member, Org: org, RoleKey: p.RoleKey, Permissions: p.Perms, Mailboxes: mbs, Setup: st})
}

func (s *Server) routesAuth(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r, s.Cfg.TrustProxyHeader)
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
		m, err := s.Svc.VerifyLogin(r.Context(), in.Email, in.Password)
		if err != nil {
			if strings.Contains(err.Error(), "forbidden") {
				msg := "incorrect email or password"
				if strings.Contains(err.Error(), "account is") {
					msg = "this account is not active"
				}
				writeJSON(w, http.StatusUnauthorized, apiError{Error: msg, Code: "bad_credentials"})
				return
			}
			s.fail(w, r, err)
			return
		}
		token, err := s.Svc.CreateSession(r.Context(), m.ID, ip, r.UserAgent())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.setSessionCookie(w, token, int(s.Cfg.SessionTTL.Seconds()))
		perms, roleKey, _ := s.Svc.Permissions(r.Context(), m.ID)
		org, _ := s.Svc.Org(r.Context())
		p := &principal{Member: m, OrgID: org.ID, Perms: perms, RoleKey: roleKey, Token: token}
		s.me(w, r, p)
	})

	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if p := principalFrom(r); p != nil {
			s.Svc.DeleteSession(r.Context(), p.Token)
		}
		s.setSessionCookie(w, "", -1)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("GET /api/auth/me", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		s.me(w, r, principalFrom(r))
	}))

	mux.HandleFunc("POST /api/auth/password", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		var in struct {
			Current string `json:"current"`
			New     string `json:"new"`
		}
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		if _, err := s.Svc.VerifyLogin(r.Context(), p.Member.LoginEmail, in.Current); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "current password is incorrect", Code: "invalid"})
			return
		}
		if err := s.Svc.SetPassword(r.Context(), p.OrgID, p.Member.ID, p.Member.ID, in.New, false); err != nil {
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
		ip := clientIP(r, s.Cfg.TrustProxyHeader)
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
		token, err := s.Svc.CreateSession(r.Context(), m.ID, ip, r.UserAgent())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.setSessionCookie(w, token, int(s.Cfg.SessionTTL.Seconds()))
		perms, roleKey, _ := s.Svc.Permissions(r.Context(), m.ID)
		org, _ := s.Svc.Org(r.Context())
		s.me(w, r, &principal{Member: m, OrgID: org.ID, Perms: perms, RoleKey: roleKey, Token: token})
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
		token, err := s.Svc.CreateSession(r.Context(), m.ID, clientIP(r, s.Cfg.TrustProxyHeader), r.UserAgent())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.setSessionCookie(w, token, int(s.Cfg.SessionTTL.Seconds()))
		perms, roleKey, _ := s.Svc.Permissions(r.Context(), m.ID)
		org, _ := s.Svc.Org(r.Context())
		s.me(w, r, &principal{Member: m, OrgID: org.ID, Perms: perms, RoleKey: roleKey, Token: token})
	})

	mux.HandleFunc("POST /api/setup/connect", s.requirePerm(model.PermOrgManage, func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		var in struct {
			APIToken string `json:"apiToken"`
		}
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		d, err := s.Svc.Connect(r.Context(), p.OrgID, p.Member.ID, in.APIToken)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, d)
	}))

	mux.HandleFunc("GET /api/setup/discover", s.requirePerm(model.PermOrgManage, func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		d, err := s.Svc.Discover(r.Context(), p.OrgID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, d)
	}))

	mux.HandleFunc("POST /api/setup/complete", s.requirePerm(model.PermOrgManage, func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		var in struct {
			BindMailbox string `json:"bindMailbox"`
		}
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		res, err := s.Svc.CompleteSetup(r.Context(), p.OrgID, p.Member.ID, in.BindMailbox)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}))
}
