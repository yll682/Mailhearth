package config

import (
	"strings"
	"testing"
	"time"
)

func clearEnv(t *testing.T) {
	for _, key := range []string{"MAILHEARTH_IMAP_MAX_CONNS", "MAILHEARTH_MAX_UPLOAD_MB", "MAILHEARTH_SESSION_DAYS", "MAILHEARTH_SESSION_MAX_DAYS", "MAILHEARTH_LOG_LEVEL", "MAILHEARTH_TRUST_PROXY", "MAILHEARTH_BASE_URL", "MAILHEARTH_TRUSTED_PROXIES", "MAILHEARTH_ALLOWED_MAIL_NETWORKS", "MAILHEARTH_UPLOAD_QUOTA_MB", "MAILHEARTH_MAX_MESSAGE_MB", "MAILHEARTH_SECURE_COOKIES", "MAILHEARTH_DEV_STACK", "MAILHEARTH_SETUP_TOKEN"} {
		t.Setenv(key, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.IMAPMaxConns != 24 || c.MaxUploadBytes != 25<<20 || c.UploadQuotaBytes != 120<<20 || len(c.TrustedProxies) == 0 || len(c.Warnings) != 0 {
		t.Fatalf("默认配置不正确：%+v", c)
	}
	// 新部署默认最安全：Secure cookie 开启，会话 14 天滑动、30 天绝对上限。
	if !c.SecureCook || c.SessionTTL != 14*24*time.Hour || c.SessionMaxAge != 30*24*time.Hour {
		t.Fatalf("安全默认值不正确：secure=%v session=%v max=%v", c.SecureCook, c.SessionTTL, c.SessionMaxAge)
	}
	d := Defaults()
	if d.UploadQuotaBytes != c.UploadQuotaBytes || d.SessionTTL != c.SessionTTL || d.SessionMaxAge != c.SessionMaxAge || d.SecureCook != c.SecureCook || d.MaxMessageBytes != c.MaxMessageBytes {
		t.Fatal("Defaults() 与空环境的 Load() 不一致")
	}
}

func TestSecureCookieDefaults(t *testing.T) {
	cases := []struct {
		name, devStack, secure, baseURL string
		want                            bool
		fails                           bool
	}{
		{name: "生产默认", want: true},
		{name: "https 地址", baseURL: "https://mail.example.org", want: true},
		{name: "dev stack 默认关闭", devStack: "1", want: false},
		{name: "显式关闭", secure: "false", baseURL: "http://mail.example.org", want: false},
		{name: "http 地址未关闭 Secure", baseURL: "http://mail.example.org", fails: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("MAILHEARTH_DEV_STACK", tc.devStack)
			t.Setenv("MAILHEARTH_SECURE_COOKIES", tc.secure)
			t.Setenv("MAILHEARTH_BASE_URL", tc.baseURL)
			c, err := Load()
			if tc.fails {
				if err == nil || !strings.Contains(err.Error(), "MAILHEARTH_SECURE_COOKIES") {
					t.Fatalf("应当拒绝：%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.SecureCook != tc.want {
				t.Fatalf("SecureCook=%v，要求 %v", c.SecureCook, tc.want)
			}
		})
	}
}

// 上传配额总是开启：0 不再表示关闭，也不能小于单个附件上限。
func TestUploadQuotaAlwaysEnforced(t *testing.T) {
	for _, value := range []string{"0", "10"} {
		clearEnv(t)
		t.Setenv("MAILHEARTH_UPLOAD_QUOTA_MB", value)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "MAILHEARTH_UPLOAD_QUOTA_MB") {
			t.Fatalf("MAILHEARTH_UPLOAD_QUOTA_MB=%s 应当被拒绝：%v", value, err)
		}
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
