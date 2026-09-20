// Package purelymail is a typed client for the Purelymail management API
// (https://purelymail.com/api/v0). Every call is a POST with a JSON body and
// the response is an envelope of the form
//
//	{"type":"success","result":{...}} or {"type":"error","code":"...","message":"..."}
//
// The shapes below follow the published OpenAPI description plus the
// additional createUser fields accepted by the service.
package purelymail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// API is the subset of the Purelymail API Mailhearth uses. It is an
// interface so that services can be tested against the in-memory fake.
type API interface {
	ListUsers(ctx context.Context) ([]string, error)
	GetUser(ctx context.Context, userName string) (*UserInfo, error)
	CreateUser(ctx context.Context, req CreateUserRequest) error
	ModifyUser(ctx context.Context, req ModifyUserRequest) error
	DeleteUser(ctx context.Context, userName string) error
	CreateAppPassword(ctx context.Context, userHandle, name string) (string, error)
	DeleteAppPassword(ctx context.Context, userName, appPassword string) error

	ListDomains(ctx context.Context, includeShared bool) ([]Domain, error)
	AddDomain(ctx context.Context, name string) error
	UpdateDomainSettings(ctx context.Context, req UpdateDomainSettingsRequest) error
	DeleteDomain(ctx context.Context, name string) error
	GetOwnershipCode(ctx context.Context) (string, error)

	ListRoutingRules(ctx context.Context) ([]RoutingRule, error)
	CreateRoutingRule(ctx context.Context, req CreateRoutingRuleRequest) error
	DeleteRoutingRule(ctx context.Context, id int64) error

	CheckAccountCredit(ctx context.Context) (string, error)
}

// Error is a structured error returned by the Purelymail API.
type Error struct {
	Code    string
	Message string
	Status  int
}

func (e *Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("purelymail: %s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("purelymail: http %d: %s", e.Status, e.Message)
}

// IsInvalidToken reports whether err means the API token was rejected.
func IsInvalidToken(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == "invalidToken"
}

// Client talks to the live API.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// New returns a client for base (e.g. https://purelymail.com/api/v0).
func New(base, token string) *Client {
	return &Client{
		base:  base,
		token: token,
		http:  &http.Client{Timeout: 40 * time.Second},
	}
}

type envelope struct {
	Type    string          `json:"type"`
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Result  json.RawMessage `json:"result"`
}

func (c *Client) call(ctx context.Context, method string, req any, out any) error {
	if req == nil {
		req = struct{}{}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Purelymail-Api-Token", c.token)
	httpReq.Header.Set("User-Agent", "Mailhearth/1.0")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("purelymail: %s: %w", method, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	var env envelope
	if jsonErr := json.Unmarshal(raw, &env); jsonErr != nil {
		if resp.StatusCode/100 != 2 {
			return &Error{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
		}
		return fmt.Errorf("purelymail: %s: invalid JSON response: %w", method, jsonErr)
	}
	if env.Type == "error" || (env.Type != "success" && resp.StatusCode/100 != 2) {
		return &Error{Code: env.Code, Message: env.Message, Status: resp.StatusCode}
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("purelymail: %s: decode result: %w", method, err)
		}
	}
	return nil
}

// --- Users ---

// UserInfo is the response of getUser.
type UserInfo struct {
	EnableSearchIndexing           bool                  `json:"enableSearchIndexing"`
	RecoveryEnabled                bool                  `json:"recoveryEnabled"`
	RequireTwoFactorAuthentication bool                  `json:"requireTwoFactorAuthentication"`
	EnableSpamFiltering            bool                  `json:"enableSpamFiltering"`
	ResetMethods                   []PasswordResetMethod `json:"resetMethods"`
}

// PasswordResetMethod describes a recovery method on a user.
type PasswordResetMethod struct {
	Type          string `json:"type"`
	Target        string `json:"target"`
	Description   string `json:"description"`
	AllowMfaReset bool   `json:"allowMfaReset"`
}

// CreateUserRequest creates a mailbox user. UserName is the local part and
// DomainName the domain.
type CreateUserRequest struct {
	UserName                 string `json:"userName"`
	DomainName               string `json:"domainName"`
	Password                 string `json:"password"`
	EnablePasswordReset      bool   `json:"enablePasswordReset"`
	RecoveryEmail            string `json:"recoveryEmail,omitempty"`
	RecoveryEmailDescription string `json:"recoveryEmailDescription,omitempty"`
	RecoveryPhone            string `json:"recoveryPhone,omitempty"`
	RecoveryPhoneDescription string `json:"recoveryPhoneDescription,omitempty"`
	EnableSearchIndexing     bool   `json:"enableSearchIndexing"`
	SendWelcomeEmail         bool   `json:"sendWelcomeEmail"`
}

// ModifyUserRequest changes settings of an existing user. Nil fields are
// left untouched.
type ModifyUserRequest struct {
	UserName                       string  `json:"userName"`
	NewUserName                    *string `json:"newUserName,omitempty"`
	NewPassword                    *string `json:"newPassword,omitempty"`
	EnableSearchIndexing           *bool   `json:"enableSearchIndexing,omitempty"`
	EnablePasswordReset            *bool   `json:"enablePasswordReset,omitempty"`
	RequireTwoFactorAuthentication *bool   `json:"requireTwoFactorAuthentication,omitempty"`
}

func (c *Client) ListUsers(ctx context.Context) ([]string, error) {
	var out struct {
		Users []string `json:"users"`
	}
	if err := c.call(ctx, "listUser", nil, &out); err != nil {
		return nil, err
	}
	return out.Users, nil
}

func (c *Client) GetUser(ctx context.Context, userName string) (*UserInfo, error) {
	var out UserInfo
	if err := c.call(ctx, "getUser", map[string]string{"userName": userName}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreateUser(ctx context.Context, req CreateUserRequest) error {
	return c.call(ctx, "createUser", req, nil)
}

func (c *Client) ModifyUser(ctx context.Context, req ModifyUserRequest) error {
	return c.call(ctx, "modifyUser", req, nil)
}

func (c *Client) DeleteUser(ctx context.Context, userName string) error {
	return c.call(ctx, "deleteUser", map[string]string{"userName": userName}, nil)
}

func (c *Client) CreateAppPassword(ctx context.Context, userHandle, name string) (string, error) {
	var out struct {
		AppPassword string `json:"appPassword"`
	}
	req := map[string]string{"userHandle": userHandle, "name": name}
	if err := c.call(ctx, "createAppPassword", req, &out); err != nil {
		return "", err
	}
	if out.AppPassword == "" {
		return "", errors.New("purelymail: createAppPassword returned an empty password")
	}
	return out.AppPassword, nil
}

func (c *Client) DeleteAppPassword(ctx context.Context, userName, appPassword string) error {
	return c.call(ctx, "deleteAppPassword", map[string]string{"userName": userName, "appPassword": appPassword}, nil)
}

// --- Domains ---

// DNSSummary is the DNS health as seen by Purelymail.
type DNSSummary struct {
	PassesMx    bool `json:"passesMx"`
	PassesSpf   bool `json:"passesSpf"`
	PassesDkim  bool `json:"passesDkim"`
	PassesDmarc bool `json:"passesDmarc"`
}

// Domain is an entry of listDomains.
type Domain struct {
	Name                  string     `json:"name"`
	AllowAccountReset     bool       `json:"allowAccountReset"`
	SymbolicSubaddressing bool       `json:"symbolicSubaddressing"`
	IsShared              bool       `json:"isShared"`
	DNSSummary            DNSSummary `json:"dnsSummary"`
}

// UpdateDomainSettingsRequest updates an owned domain.
type UpdateDomainSettingsRequest struct {
	Name                  string `json:"name"`
	AllowAccountReset     *bool  `json:"allowAccountReset,omitempty"`
	SymbolicSubaddressing *bool  `json:"symbolicSubaddressing,omitempty"`
	RecheckDNS            bool   `json:"recheckDns"`
}

func (c *Client) ListDomains(ctx context.Context, includeShared bool) ([]Domain, error) {
	var out struct {
		Domains []Domain `json:"domains"`
	}
	if err := c.call(ctx, "listDomains", map[string]bool{"includeShared": includeShared}, &out); err != nil {
		return nil, err
	}
	return out.Domains, nil
}

func (c *Client) AddDomain(ctx context.Context, name string) error {
	return c.call(ctx, "addDomain", map[string]string{"domainName": name}, nil)
}

func (c *Client) UpdateDomainSettings(ctx context.Context, req UpdateDomainSettingsRequest) error {
	return c.call(ctx, "updateDomainSettings", req, nil)
}

func (c *Client) DeleteDomain(ctx context.Context, name string) error {
	return c.call(ctx, "deleteDomain", map[string]string{"name": name}, nil)
}

func (c *Client) GetOwnershipCode(ctx context.Context) (string, error) {
	var out struct {
		Code string `json:"code"`
	}
	if err := c.call(ctx, "getOwnershipCode", nil, &out); err != nil {
		return "", err
	}
	return out.Code, nil
}

// --- Routing ---

// RoutingRule is an account routing rule.
type RoutingRule struct {
	ID              int64    `json:"id"`
	DomainName      string   `json:"domainName"`
	Prefix          bool     `json:"prefix"`
	MatchUser       string   `json:"matchUser"`
	TargetAddresses []string `json:"targetAddresses"`
	Catchall        bool     `json:"catchall"`
}

// CreateRoutingRuleRequest creates a routing rule.
type CreateRoutingRuleRequest struct {
	DomainName      string   `json:"domainName"`
	Prefix          bool     `json:"prefix"`
	MatchUser       string   `json:"matchUser"`
	TargetAddresses []string `json:"targetAddresses"`
	Catchall        bool     `json:"catchall"`
}

func (c *Client) ListRoutingRules(ctx context.Context) ([]RoutingRule, error) {
	var out struct {
		Rules []RoutingRule `json:"rules"`
	}
	if err := c.call(ctx, "listRoutingRules", nil, &out); err != nil {
		return nil, err
	}
	return out.Rules, nil
}

func (c *Client) CreateRoutingRule(ctx context.Context, req CreateRoutingRuleRequest) error {
	if req.TargetAddresses == nil {
		req.TargetAddresses = []string{}
	}
	return c.call(ctx, "createRoutingRule", req, nil)
}

func (c *Client) DeleteRoutingRule(ctx context.Context, id int64) error {
	return c.call(ctx, "deleteRoutingRule", map[string]int64{"routingRuleId": id}, nil)
}

// --- Billing ---

func (c *Client) CheckAccountCredit(ctx context.Context) (string, error) {
	var out struct {
		Credit string `json:"credit"`
	}
	if err := c.call(ctx, "checkAccountCredit", nil, &out); err != nil {
		return "", err
	}
	return out.Credit, nil
}

var _ API = (*Client)(nil)
