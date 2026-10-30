// Package config loads Mailhearth runtime configuration from environment
// variables. Defaults are chosen for a new production deployment: the
// most protective setting is the default and loosening it is explicit.
package config

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the fully-resolved runtime configuration.
type Config struct {
	Listen     string
	DataDir    string
	BaseURL    string // optional; used to build invite links when set
	LogLevel   string
	DevStack   bool   // start the embedded fake Purelymail/IMAP/SMTP stack
	SetupToken string // required to create the organisation; generated at startup when unset
	SecureCook bool   // set Secure on cookies (default on; off only explicitly or with DevStack)

	IMAPMaxConns        int
	IMAPIdleTimeout     time.Duration
	MaxUploadBytes      int64
	MaxMessageBytes     int64
	UploadQuotaBytes    int64 // pending (unsent) uploads per member
	TrustedProxies      []*net.IPNet
	SessionTTL          time.Duration
	SessionMaxAge       time.Duration // absolute session lifetime
	InviteTTL           time.Duration
	TrustProxyHeader    bool
	AllowedMailNetworks []*net.IPNet
	CABundles           map[int64]*x509.CertPool

	// Warnings lists values that were adjusted while loading.
	Warnings []string
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

// loader collects every configuration problem so that startup reports
// them all at once instead of silently falling back to defaults.
type loader struct{ errs []error }

func (l *loader) fail(format string, args ...any) {
	l.errs = append(l.errs, fmt.Errorf(format, args...))
}

func (l *loader) bool(key string, def bool) bool {
	v := strings.ToLower(env(key, ""))
	switch v {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	l.fail("%s: %q is not a boolean (use true or false)", key, v)
	return def
}

// int parses an integer option and requires min <= value <= max.
func (l *loader) int(key string, def, min, max int) int {
	raw := env(key, "")
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		l.fail("%s: %q is not an integer", key, raw)
		return def
	}
	if v < min || v > max {
		l.fail("%s: %d is out of range (%d to %d)", key, v, min, max)
		return def
	}
	return v
}

// Default values, also used by Defaults.
const (
	DefaultSessionDays    = 14
	DefaultSessionMaxDays = 30
	DefaultUploadQuotaMB  = 120
)

// Defaults returns the configuration Load produces from an empty
// environment (apart from SecureCook, which Load turns off for the dev
// stack). Tests start from it so that every limit is set.
func Defaults() *Config {
	proxies, err := ParseCIDRs("", DefaultTrustedProxies)
	if err != nil {
		panic(err)
	}
	return &Config{
		Listen:           ":8080",
		DataDir:          "./data",
		LogLevel:         "info",
		SecureCook:       true,
		IMAPMaxConns:     24,
		IMAPIdleTimeout:  90 * time.Second,
		MaxUploadBytes:   25 << 20,
		MaxMessageBytes:  40 << 20,
		UploadQuotaBytes: DefaultUploadQuotaMB << 20,
		TrustedProxies:   proxies,
		SessionTTL:       DefaultSessionDays * 24 * time.Hour,
		SessionMaxAge:    DefaultSessionMaxDays * 24 * time.Hour,
		InviteTTL:        7 * 24 * time.Hour,
		CABundles:        map[int64]*x509.CertPool{},
	}
}

// Load reads configuration from the environment. Malformed or
// out-of-range values are errors; values the runtime adjusts (such as the
// IMAP connection cap) are reported in Warnings.
func Load() (*Config, error) {
	l := &loader{}
	d := Defaults()
	c := &Config{
		Listen:           env("MAILHEARTH_LISTEN", d.Listen),
		DataDir:          env("MAILHEARTH_DATA_DIR", d.DataDir),
		BaseURL:          strings.TrimRight(env("MAILHEARTH_BASE_URL", ""), "/"),
		LogLevel:         strings.ToLower(env("MAILHEARTH_LOG_LEVEL", d.LogLevel)),
		SetupToken:       env("MAILHEARTH_SETUP_TOKEN", ""),
		DevStack:         l.bool("MAILHEARTH_DEV_STACK", false),
		IMAPMaxConns:     l.int("MAILHEARTH_IMAP_MAX_CONNS", d.IMAPMaxConns, 1, 10000),
		IMAPIdleTimeout:  time.Duration(l.int("MAILHEARTH_IMAP_IDLE_SECONDS", int(d.IMAPIdleTimeout/time.Second), 10, 3600)) * time.Second,
		MaxUploadBytes:   int64(l.int("MAILHEARTH_MAX_UPLOAD_MB", int(d.MaxUploadBytes>>20), 1, 2048)) << 20,
		MaxMessageBytes:  int64(l.int("MAILHEARTH_MAX_MESSAGE_MB", int(d.MaxMessageBytes>>20), 1, 2048)) << 20,
		UploadQuotaBytes: int64(l.int("MAILHEARTH_UPLOAD_QUOTA_MB", DefaultUploadQuotaMB, 1, 1<<20)) << 20,
		SessionTTL:       time.Duration(l.int("MAILHEARTH_SESSION_DAYS", DefaultSessionDays, 1, 365)) * 24 * time.Hour,
		SessionMaxAge:    time.Duration(l.int("MAILHEARTH_SESSION_MAX_DAYS", DefaultSessionMaxDays, 1, 3650)) * 24 * time.Hour,
		InviteTTL:        time.Duration(l.int("MAILHEARTH_INVITE_DAYS", int(d.InviteTTL/(24*time.Hour)), 1, 90)) * 24 * time.Hour,
		TrustProxyHeader: l.bool("MAILHEARTH_TRUST_PROXY", false),
	}
	var err error
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		l.fail("MAILHEARTH_LOG_LEVEL: %q is not one of debug, info, warn, error", c.LogLevel)
	}
	if c.BaseURL != "" {
		if u, err := url.Parse(c.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			l.fail("MAILHEARTH_BASE_URL: %q must be an absolute http(s) URL", c.BaseURL)
		}
	}
	if c.SessionMaxAge < c.SessionTTL {
		l.fail("MAILHEARTH_SESSION_MAX_DAYS must not be shorter than MAILHEARTH_SESSION_DAYS")
	}
	if c.UploadQuotaBytes < c.MaxUploadBytes {
		l.fail("MAILHEARTH_UPLOAD_QUOTA_MB must not be smaller than MAILHEARTH_MAX_UPLOAD_MB")
	}
	// Secure cookies are on unless switched off explicitly; the dev stack
	// (plain HTTP on a developer machine) defaults to off.
	c.SecureCook = l.bool("MAILHEARTH_SECURE_COOKIES", !c.DevStack)
	if c.SecureCook && strings.HasPrefix(c.BaseURL, "http://") {
		l.fail("MAILHEARTH_BASE_URL is plain http:// but cookies are Secure-only: serve Mailhearth over https, or set MAILHEARTH_SECURE_COOKIES=false for a test instance")
	}
	// The IMAP pool supports 4 to 24 connections; other values are
	// clamped (as before) but now reported.
	if c.IMAPMaxConns < 4 {
		c.Warnings = append(c.Warnings, fmt.Sprintf("MAILHEARTH_IMAP_MAX_CONNS=%d raised to the minimum of 4", c.IMAPMaxConns))
		c.IMAPMaxConns = 4
	}
	if c.IMAPMaxConns > 24 {
		c.Warnings = append(c.Warnings, fmt.Sprintf("MAILHEARTH_IMAP_MAX_CONNS=%d lowered to the maximum of 24", c.IMAPMaxConns))
		c.IMAPMaxConns = 24
	}
	if networks, err := ParseCIDRs("MAILHEARTH_ALLOWED_MAIL_NETWORKS", os.Getenv("MAILHEARTH_ALLOWED_MAIL_NETWORKS")); err != nil {
		l.errs = append(l.errs, err)
	} else {
		c.AllowedMailNetworks = networks
	}
	if len(l.errs) > 0 {
		return nil, errors.Join(l.errs...)
	}
	c.TrustedProxies, err = ParseCIDRs("MAILHEARTH_TRUSTED_PROXIES", env("MAILHEARTH_TRUSTED_PROXIES", DefaultTrustedProxies))
	if err != nil {
		return nil, err
	}
	c.CABundles = map[int64]*x509.CertPool{}
	if path := os.Getenv("MAILHEARTH_CA_BUNDLES_FILE"); path != "" {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var paths map[string]string
		if err := json.Unmarshal(body, &paths); err != nil {
			return nil, err
		}
		for key, path := range paths {
			id, err := strconv.ParseInt(key, 10, 64)
			if err != nil || id <= 0 {
				return nil, fmt.Errorf("MAILHEARTH_CA_BUNDLES_FILE: %q is not a positive bundle id", key)
			}
			pem, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			roots, err := x509.SystemCertPool()
			if err != nil {
				return nil, err
			}
			if !roots.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("MAILHEARTH_CA_BUNDLES_FILE: bundle %d contains no valid certificate", id)
			}
			c.CABundles[id] = roots
		}
	}
	return c, nil
}

// DefaultTrustedProxies covers reverse proxies on the same host or on a
// private container network.
const DefaultTrustedProxies = "127.0.0.0/8,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,fc00::/7"

// ParseCIDRs parses a comma-separated list of CIDRs or bare IPs.
func ParseCIDRs(key, value string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !strings.Contains(item, "/") {
			if ip := net.ParseIP(item); ip != nil && ip.To4() != nil {
				item += "/32"
			} else {
				item += "/128"
			}
		}
		_, network, err := net.ParseCIDR(item)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid CIDR %q", key, item)
		}
		out = append(out, network)
	}
	return out, nil
}
