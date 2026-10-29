package config

import (
	"strings"
	"testing"
)

func clearEnv(t *testing.T) {
	for _, key := range []string{"MAILHEARTH_IMAP_MAX_CONNS", "MAILHEARTH_MAX_UPLOAD_MB", "MAILHEARTH_SESSION_DAYS", "MAILHEARTH_SESSION_MAX_DAYS", "MAILHEARTH_LOG_LEVEL", "MAILHEARTH_TRUST_PROXY", "MAILHEARTH_BASE_URL", "MAILHEARTH_TRUSTED_PROXIES", "MAILHEARTH_ALLOWED_MAIL_NETWORKS", "MAILHEARTH_UPLOAD_QUOTA_MB", "MAILHEARTH_MAX_MESSAGE_MB"} {
		t.Setenv(key, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.IMAPMaxConns != 24 || c.MaxUploadBytes != 25<<20 || c.UploadQuotaBytes != 3*c.MaxMessageBytes || len(c.TrustedProxies) == 0 || len(c.Warnings) != 0 {
		t.Fatalf("默认配置不正确：%+v", c)
	}
}

// #27: malformed values fail loudly and all problems are reported together.
func TestLoadRejectsInvalidValues(t *testing.T) {
	clearEnv(t)
	t.Setenv("MAILHEARTH_MAX_UPLOAD_MB", "lots")
	t.Setenv("MAILHEARTH_SESSION_DAYS", "0")
	t.Setenv("MAILHEARTH_TRUST_PROXY", "maybe")
	t.Setenv("MAILHEARTH_LOG_LEVEL", "verbose")
	t.Setenv("MAILHEARTH_BASE_URL", "mail.example.org")
	t.Setenv("MAILHEARTH_TRUSTED_PROXIES", "10.0.0.0/33")
	_, err := Load()
	if err == nil {
		t.Fatal("无效配置没有报错")
	}
	for _, key := range []string{"MAILHEARTH_MAX_UPLOAD_MB", "MAILHEARTH_SESSION_DAYS", "MAILHEARTH_TRUST_PROXY", "MAILHEARTH_LOG_LEVEL", "MAILHEARTH_BASE_URL"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("错误信息缺少 %s：%v", key, err)
		}
	}
}

func TestLoadClampsIMAPConnectionsWithWarning(t *testing.T) {
	clearEnv(t)
	t.Setenv("MAILHEARTH_IMAP_MAX_CONNS", "64")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.IMAPMaxConns != 24 || len(c.Warnings) != 1 {
		t.Fatalf("连接上限没有带警告地调整：%d %v", c.IMAPMaxConns, c.Warnings)
	}
}

func TestParseCIDRsAcceptsBareAddresses(t *testing.T) {
	networks, err := ParseCIDRs("X", "192.0.2.10, 2001:db8::1, 10.0.0.0/8")
	if err != nil || len(networks) != 3 || networks[0].String() != "192.0.2.10/32" || networks[1].String() != "2001:db8::1/128" {
		t.Fatalf("解析结果不正确：%v %v", networks, err)
	}
}
