// Package config loads Mailhearth runtime configuration from environment
// variables. Every option has a sensible default so a fresh deployment only
// needs a data directory and (optionally) a master key.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// TLSMode describes how a protocol connection is secured.
type TLSMode string

const (
	TLSImplicit TLSMode = "tls"      // TLS from the first byte (IMAPS 993, SMTPS 465)
	TLSStart    TLSMode = "starttls" // plaintext upgrade (SMTP 587, ManageSieve 4190)
	TLSNone     TLSMode = "none"     // only for the embedded dev stack
)

// Config is the fully-resolved runtime configuration.
type Config struct {
	Listen     string
	DataDir    string
	BaseURL    string // optional; used to build invite links when set
	MasterKey  []byte // 32 bytes
	LogLevel   string
	DevStack   bool // start the embedded fake Purelymail/IMAP/SMTP stack
	SecureCook bool // set Secure on cookies (auto: true when BaseURL is https)

	PurelymailAPIURL string

	IMAPAddr  string
	IMAPTLS   TLSMode
	SMTPAddr  string
	SMTPTLS   TLSMode
	SieveAddr string
	SieveTLS  TLSMode

	IMAPMaxConns     int
	IMAPIdleTimeout  time.Duration
	MaxUploadBytes   int64
	MaxMessageBytes  int64
	SessionTTL       time.Duration
	InviteTTL        time.Duration
	TrustProxyHeader bool
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envBool(key string, def bool) bool {
	v := strings.ToLower(env(key, ""))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(env(key, "")); err == nil {
		return v
	}
	return def
}

func envTLS(key string, def TLSMode) (TLSMode, error) {
	v := TLSMode(strings.ToLower(env(key, string(def))))
	switch v {
	case TLSImplicit, TLSStart, TLSNone:
		return v, nil
	}
	return "", fmt.Errorf("%s: unknown TLS mode %q (want tls|starttls|none)", key, v)
}

// Load reads configuration from the environment.
func Load() (*Config, error) {
	c := &Config{
		Listen:           env("MAILHEARTH_LISTEN", ":8080"),
		DataDir:          env("MAILHEARTH_DATA_DIR", "./data"),
		BaseURL:          strings.TrimRight(env("MAILHEARTH_BASE_URL", ""), "/"),
		LogLevel:         env("MAILHEARTH_LOG_LEVEL", "info"),
		DevStack:         envBool("MAILHEARTH_DEV_STACK", false),
		PurelymailAPIURL: strings.TrimRight(env("MAILHEARTH_PURELYMAIL_API_URL", "https://purelymail.com/api/v0"), "/"),
		IMAPAddr:         env("MAILHEARTH_IMAP_ADDR", "imap.purelymail.com:993"),
		SMTPAddr:         env("MAILHEARTH_SMTP_ADDR", "smtp.purelymail.com:465"),
		SieveAddr:        env("MAILHEARTH_SIEVE_ADDR", "mailserver.purelymail.com:4190"),
		IMAPMaxConns:     envInt("MAILHEARTH_IMAP_MAX_CONNS", 24),
		IMAPIdleTimeout:  time.Duration(envInt("MAILHEARTH_IMAP_IDLE_SECONDS", 90)) * time.Second,
		MaxUploadBytes:   int64(envInt("MAILHEARTH_MAX_UPLOAD_MB", 25)) << 20,
		MaxMessageBytes:  int64(envInt("MAILHEARTH_MAX_MESSAGE_MB", 40)) << 20,
		SessionTTL:       time.Duration(envInt("MAILHEARTH_SESSION_DAYS", 30)) * 24 * time.Hour,
		InviteTTL:        time.Duration(envInt("MAILHEARTH_INVITE_DAYS", 7)) * 24 * time.Hour,
		TrustProxyHeader: envBool("MAILHEARTH_TRUST_PROXY", false),
	}
	var err error
	if c.IMAPTLS, err = envTLS("MAILHEARTH_IMAP_TLS", TLSImplicit); err != nil {
		return nil, err
	}
	if c.SMTPTLS, err = envTLS("MAILHEARTH_SMTP_TLS", TLSImplicit); err != nil {
		return nil, err
	}
	if c.SieveTLS, err = envTLS("MAILHEARTH_SIEVE_TLS", TLSStart); err != nil {
		return nil, err
	}
	c.SecureCook = envBool("MAILHEARTH_SECURE_COOKIES", strings.HasPrefix(c.BaseURL, "https://"))
	if c.IMAPMaxConns < 4 {
		c.IMAPMaxConns = 4
	}
	return c, nil
}
