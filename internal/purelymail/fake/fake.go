// Package fake is an in-memory implementation of the Purelymail HTTP API.
// It powers the embedded dev stack and the service tests, so that the whole
// product can be exercised without a real Purelymail account.
package fake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// Hooks let the dev stack mirror account changes into its IMAP server.
type Hooks struct {
	UserCreated        func(user, password string)
	UserDeleted        func(user string)
	PasswordChanged    func(user, password string)
	AppPasswordCreated func(user, appPassword string)
	AppPasswordDeleted func(user, appPassword string)
}

// User is the record of a Purelymail user.
type User struct {
	Password             string
	EnableSearchIndexing bool
	EnablePasswordReset  bool
	RequireTwoFactor     bool
	AppPasswords         map[string]string // password -> name
}

// Domain is the record of a domain.
type Domain struct {
	Name                  string
	AllowAccountReset     bool
	SymbolicSubaddressing bool
	IsShared              bool
	MX, SPF, DKIM, DMARC  bool
}

// Rule is a routing rule.
type Rule struct {
	ID              int64    `json:"id"`
	DomainName      string   `json:"domainName"`
	Prefix          bool     `json:"prefix"`
	MatchUser       string   `json:"matchUser"`
	TargetAddresses []string `json:"targetAddresses"`
	Catchall        bool     `json:"catchall"`
}

// Server is the fake API state; it implements http.Handler.
type Server struct {
	mu            sync.Mutex
	Token         string
	OwnershipCode string
	Credit        string
	Users         map[string]*User
	Domains       map[string]*Domain
	Rules         map[int64]*Rule
	nextRule      int64
	nextAppPw     int
	Hooks         Hooks
	Calls         []string
}

// New returns an empty fake accepting the given API token.
func New(token string) *Server {
	return &Server{
		Token:         token,
		OwnershipCode: "purelymail_ownership_proof=fake-code-123",
		Credit:        "12.3400000000",
		Users:         map[string]*User{},
		Domains:       map[string]*Domain{},
		Rules:         map[int64]*Rule{},
		nextRule:      100,
	}
}

// AddDomain seeds a domain.
func (s *Server) AddDomain(d Domain) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dd := d
	dd.Name = strings.ToLower(d.Name)
	s.Domains[dd.Name] = &dd
}

// AddUser seeds a user (full address) with a password.
func (s *Server) AddUser(address, password string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Users[strings.ToLower(address)] = &User{Password: password, EnableSearchIndexing: true, AppPasswords: map[string]string{}}
}

// AddRule seeds a routing rule and returns its id.
func (s *Server) AddRule(r Rule) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextRule++
	r.ID = s.nextRule
	r.DomainName = strings.ToLower(r.DomainName)
	rr := r
	s.Rules[r.ID] = &rr
	return r.ID
}

// CredentialValid reports whether password is the main password or an app
// password for user; used by the dev IMAP/SMTP servers.
func (s *Server) CredentialValid(user, password string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.Users[strings.ToLower(user)]
	if u == nil {
		return false
	}
	if u.Password == password {
		return true
	}
	_, ok := u.AppPasswords[password]
	return ok
}

// UserExists reports whether the user exists.
func (s *Server) UserExists(user string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.Users[strings.ToLower(user)]
	return ok
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func success(w http.ResponseWriter, result any) {
	if result == nil {
		result = map[string]any{}
	}
	writeJSON(w, map[string]any{"type": "success", "result": result})
}

func fail(w http.ResponseWriter, code, msg string) {
	writeJSON(w, map[string]any{"type": "error", "code": code, "message": msg})
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Purelymail-Api-Token") != s.Token {
		fail(w, "invalidToken", "Token must be supplied in Purelymail-Api-Token header")
		return
	}
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	body := map[string]any{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		fail(w, "badRequest", "invalid JSON")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls = append(s.Calls, method)
	str := func(k string) string {
		v, _ := body[k].(string)
		return strings.TrimSpace(v)
	}
	boolean := func(k string) (bool, bool) {
		v, ok := body[k].(bool)
		return v, ok
	}
	switch method {
	case "checkAccountCredit":
		success(w, map[string]any{"credit": s.Credit})
	case "getOwnershipCode":
		success(w, map[string]any{"code": s.OwnershipCode})
	case "listDomains":
		includeShared, _ := boolean("includeShared")
		out := []map[string]any{}
		names := make([]string, 0, len(s.Domains))
		for n := range s.Domains {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			d := s.Domains[n]
			if d.IsShared && !includeShared {
				continue
			}
			out = append(out, map[string]any{
				"name": d.Name, "allowAccountReset": d.AllowAccountReset, "symbolicSubaddressing": d.SymbolicSubaddressing, "isShared": d.IsShared,
				"dnsSummary": map[string]bool{"passesMx": d.MX, "passesSpf": d.SPF, "passesDkim": d.DKIM, "passesDmarc": d.DMARC},
			})
		}
		success(w, map[string]any{"domains": out})
	case "addDomain":
		name := strings.ToLower(str("domainName"))
		if name == "" {
			fail(w, "badRequest", "domainName required")
			return
		}
		if _, ok := s.Domains[name]; ok {
			fail(w, "domainExists", "domain already added")
			return
		}
		s.Domains[name] = &Domain{Name: name}
		success(w, nil)
	case "updateDomainSettings":
		d := s.Domains[strings.ToLower(str("name"))]
		if d == nil {
			fail(w, "notFound", "no such domain")
			return
		}
		if v, ok := boolean("allowAccountReset"); ok {
			d.AllowAccountReset = v
		}
		if v, ok := boolean("symbolicSubaddressing"); ok {
			d.SymbolicSubaddressing = v
		}
		if v, ok := boolean("recheckDns"); ok && v {
			d.MX, d.SPF, d.DKIM, d.DMARC = true, true, true, true
		}
		success(w, nil)
	case "deleteDomain":
		name := strings.ToLower(str("name"))
		if _, ok := s.Domains[name]; !ok {
			fail(w, "notFound", "no such domain")
			return
		}
		delete(s.Domains, name)
		for u := range s.Users {
			if strings.HasSuffix(u, "@"+name) {
				delete(s.Users, u)
				if s.Hooks.UserDeleted != nil {
					s.Hooks.UserDeleted(u)
				}
			}
		}
		for id, r := range s.Rules {
			if strings.EqualFold(r.DomainName, name) {
				delete(s.Rules, id)
			}
		}
		success(w, nil)
	case "listUser":
		names := make([]string, 0, len(s.Users))
		for n := range s.Users {
			names = append(names, n)
		}
		sort.Strings(names)
		success(w, map[string]any{"users": names})
	case "getUser":
		u := s.Users[strings.ToLower(str("userName"))]
		if u == nil {
			fail(w, "notFound", "no such user")
			return
		}
		success(w, map[string]any{
			"enableSearchIndexing": u.EnableSearchIndexing, "recoveryEnabled": u.EnablePasswordReset,
			"requireTwoFactorAuthentication": u.RequireTwoFactor, "enableSpamFiltering": true, "resetMethods": []any{},
		})
	case "createUser":
		local, domain := strings.ToLower(str("userName")), strings.ToLower(str("domainName"))
		if local == "" || domain == "" {
			fail(w, "badRequest", "userName and domainName required")
			return
		}
		if _, ok := s.Domains[domain]; !ok {
			fail(w, "notFound", "domain not owned by account")
			return
		}
		addr := local + "@" + domain
		if _, ok := s.Users[addr]; ok {
			fail(w, "userExists", "user already exists")
			return
		}
		pw := str("password")
		if pw == "" {
			fail(w, "badRequest", "password required")
			return
		}
		idx, _ := boolean("enableSearchIndexing")
		reset, _ := boolean("enablePasswordReset")
		s.Users[addr] = &User{Password: pw, EnableSearchIndexing: idx, EnablePasswordReset: reset, AppPasswords: map[string]string{}}
		if s.Hooks.UserCreated != nil {
			s.Hooks.UserCreated(addr, pw)
		}
		success(w, nil)
	case "modifyUser":
		name := strings.ToLower(str("userName"))
		u := s.Users[name]
		if u == nil {
			fail(w, "notFound", "no such user")
			return
		}
		if pw := str("newPassword"); pw != "" {
			u.Password = pw
			if s.Hooks.PasswordChanged != nil {
				s.Hooks.PasswordChanged(name, pw)
			}
		}
		if v, ok := boolean("enableSearchIndexing"); ok {
			u.EnableSearchIndexing = v
		}
		if v, ok := boolean("enablePasswordReset"); ok {
			u.EnablePasswordReset = v
		}
		if v, ok := boolean("requireTwoFactorAuthentication"); ok {
			u.RequireTwoFactor = v
		}
		if nn := strings.ToLower(str("newUserName")); nn != "" && nn != name {
			s.Users[nn] = u
			delete(s.Users, name)
		}
		success(w, nil)
	case "deleteUser":
		name := strings.ToLower(str("userName"))
		if _, ok := s.Users[name]; !ok {
			fail(w, "notFound", "no such user")
			return
		}
		delete(s.Users, name)
		if s.Hooks.UserDeleted != nil {
			s.Hooks.UserDeleted(name)
		}
		success(w, nil)
	case "createAppPassword":
		name := strings.ToLower(str("userHandle"))
		u := s.Users[name]
		if u == nil {
			fail(w, "notFound", "no such user")
			return
		}
		s.nextAppPw++
		pw := fmt.Sprintf("apppw-%s-%d", strings.SplitN(name, "@", 2)[0], s.nextAppPw)
		u.AppPasswords[pw] = str("name")
		if s.Hooks.AppPasswordCreated != nil {
			s.Hooks.AppPasswordCreated(name, pw)
		}
		success(w, map[string]any{"appPassword": pw})
	case "deleteAppPassword":
		name := strings.ToLower(str("userName"))
		u := s.Users[name]
		if u == nil {
			fail(w, "notFound", "no such user")
			return
		}
		pw := str("appPassword")
		if _, ok := u.AppPasswords[pw]; !ok {
			fail(w, "notFound", "no such app password")
			return
		}
		delete(u.AppPasswords, pw)
		if s.Hooks.AppPasswordDeleted != nil {
			s.Hooks.AppPasswordDeleted(name, pw)
		}
		success(w, nil)
	case "listRoutingRules":
		ids := make([]int64, 0, len(s.Rules))
		for id := range s.Rules {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		out := make([]*Rule, 0, len(ids))
		for _, id := range ids {
			out = append(out, s.Rules[id])
		}
		success(w, map[string]any{"rules": out})
	case "createRoutingRule":
		domain := strings.ToLower(str("domainName"))
		if _, ok := s.Domains[domain]; !ok {
			fail(w, "notFound", "domain not owned by account")
			return
		}
		prefix, _ := boolean("prefix")
		catchall, _ := boolean("catchall")
		match := strings.ToLower(str("matchUser"))
		for _, r := range s.Rules {
			if r.DomainName == domain && r.MatchUser == match && r.Prefix == prefix && r.Catchall == catchall {
				fail(w, "ruleExists", "routing rule with same user/prefix already exists")
				return
			}
		}
		var targets []string
		if arr, ok := body["targetAddresses"].([]any); ok {
			for _, t := range arr {
				if ts, ok := t.(string); ok && strings.TrimSpace(ts) != "" {
					targets = append(targets, strings.TrimSpace(ts))
				}
			}
		}
		if len(targets) == 0 {
			fail(w, "badRequest", "targetAddresses required")
			return
		}
		s.nextRule++
		s.Rules[s.nextRule] = &Rule{ID: s.nextRule, DomainName: domain, Prefix: prefix, MatchUser: match, TargetAddresses: targets, Catchall: catchall}
		success(w, nil)
	case "deleteRoutingRule":
		idf, _ := body["routingRuleId"].(float64)
		id := int64(idf)
		if _, ok := s.Rules[id]; !ok {
			fail(w, "notFound", "no such routing rule")
			return
		}
		delete(s.Rules, id)
		success(w, nil)
	default:
		w.WriteHeader(http.StatusNotFound)
		fail(w, "notFound", "unknown method "+method)
	}
}
