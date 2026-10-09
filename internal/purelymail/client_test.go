package purelymail_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"mailhearth/internal/config"
	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/provider"
	"mailhearth/internal/purelymail"
)

type realClientProviderConfig struct {
	APIKey      string                     `json:"apiKey"`
	APIUsername string                     `json:"apiUsername,omitempty"`
	Domain      string                     `json:"domain"`
	IMAP        *provider.ProtocolTemplate `json:"imap"`
	SMTP        *provider.ProtocolTemplate `json:"smtp"`
	ManageSieve *provider.ProtocolTemplate `json:"managesieve"`
}

func readRealClientConfig(t *testing.T) realClientProviderConfig {
	t.Helper()
	path := os.Getenv("MAILHEARTH_MULTIPROVIDER_TEST_CONFIG")
	if path == "" {
		t.Skip("缺少 MAILHEARTH_MULTIPROVIDER_TEST_CONFIG；Purelymail 真实环境测试未执行")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "data", "integration", "multi-provider"))
	if err != nil {
		t.Fatal("无法确定真实环境测试配置目录")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal("无法读取真实环境测试配置目录")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal("无法读取真实环境测试配置文件")
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		t.Fatal("无法确定真实环境测试配置文件路径")
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		t.Fatal("测试配置必须位于项目 data/integration/multi-provider/ 目录")
	}
	f, err := os.Open(resolved)
	if err != nil {
		t.Fatal("无法打开真实环境测试配置文件")
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Error("无法关闭真实环境测试配置文件")
		}
	}()
	var input struct {
		Purelymail             *realClientProviderConfig `json:"purelymail"`
		Migadu                 json.RawMessage           `json:"migadu"`
		Manual                 json.RawMessage           `json:"manual"`
		DeliveryTimeoutSeconds int                       `json:"deliveryTimeoutSeconds"`
	}
	decoder := json.NewDecoder(io.LimitReader(f, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal("真实环境测试配置 JSON 无效或包含未知字段")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatal("真实环境测试配置只能包含一个 JSON 值")
	}
	if input.Purelymail == nil || input.Purelymail.APIKey == "" || input.Purelymail.Domain == "" || input.Purelymail.IMAP == nil {
		t.Fatal("已提供测试配置，但缺少 purelymail.apiKey、专用测试 domain 或 imap 模板")
	}
	cfg := *input.Purelymail
	domain, err := provider.CanonicalDomain(cfg.Domain)
	if err != nil {
		t.Fatal("Purelymail 专用测试域名无效")
	}
	cfg.Domain = domain
	if !cfg.IMAP.Enabled {
		t.Fatal("Purelymail 真实凭据验证需要启用 IMAP")
	}
	if err := provider.ValidateProtocolTemplate(*cfg.IMAP); err != nil {
		t.Fatal("Purelymail IMAP 模板无效")
	}
	return cfg
}

func requireRealClientLogin(t *testing.T, ctx context.Context, pool *imappool.Pool, cred imappool.Cred, accepted bool) {
	t.Helper()
	conn, err := pool.GetFresh(ctx, cred)
	if conn != nil {
		pool.Put(conn)
	}
	if accepted {
		if err != nil {
			t.Fatal("真实 Purelymail IMAP 认证没有通过")
		}
		return
	}
	var authErr *imappool.AuthError
	if !errors.As(err, &authErr) {
		t.Fatal("需要真实 Purelymail IMAP 明确拒绝已撤销或已替换的凭据")
	}
}

func TestClientAgainstRealPurelymail(t *testing.T) {
	cfg := readRealClientConfig(t)
	runtimeConfig, err := config.Load()
	if err != nil {
		t.Fatal("无法读取真实环境 TLS 配置")
	}
	tlsConfig := &tls.Config{ServerName: cfg.IMAP.Host, MinVersion: tls.VersionTLS12}
	if cfg.IMAP.CABundleID != nil {
		tlsConfig.RootCAs = runtimeConfig.CABundles[*cfg.IMAP.CABundleID]
		if tlsConfig.RootCAs == nil {
			t.Fatal("Purelymail IMAP 指定的 CA bundle 没有配置")
		}
	}
	pool := imappool.New(imappool.Config{MaxConns: 4})
	t.Cleanup(pool.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	t.Cleanup(cancel)

	const apiBaseURL = "https://purelymail.com/api/v0"
	bad := purelymail.New(apiBaseURL, "mailhearth-invalid-"+uuid.NewString())
	if _, err := bad.CheckAccountCredit(ctx); !purelymail.IsInvalidToken(err) {
		t.Fatal("真实 Purelymail API 没有明确返回 invalidToken")
	}
	c := purelymail.New(apiBaseURL, cfg.APIKey)
	credit, err := c.CheckAccountCredit(ctx)
	if err != nil || credit == "" {
		t.Fatal("无法读取真实 Purelymail 余额")
	}
	domains, err := c.ListDomains(ctx, false)
	if err != nil {
		t.Fatal("无法读取真实 Purelymail 域名")
	}
	foundDomain := false
	for _, domain := range domains {
		if strings.EqualFold(domain.Name, cfg.Domain) {
			if domain.IsShared {
				t.Fatal("Purelymail 测试必须使用自己拥有的专用域名")
			}
			foundDomain = true
		}
	}
	if !foundDomain {
		t.Fatal("Purelymail 账户没有配置中的专用测试域名")
	}
	if code, err := c.GetOwnershipCode(ctx); err != nil || code == "" {
		t.Fatal("无法读取真实 Purelymail ownershipCode")
	}

	localPart := "mailhearth-client-" + uuid.NewString()
	address := localPart + "@" + cfg.Domain
	ruleLocalPart := "mailhearth-rule-" + uuid.NewString()
	users, err := c.ListUsers(ctx)
	if err != nil {
		t.Fatal("无法读取创建前的 Purelymail 用户列表")
	}
	for _, user := range users {
		if strings.EqualFold(user, address) || strings.EqualFold(user, ruleLocalPart+"@"+cfg.Domain) {
			t.Fatal("测试地址已经存在，终止资源创建")
		}
	}
	rules, err := c.ListRoutingRules(ctx)
	if err != nil {
		t.Fatal("无法读取创建前的 Purelymail routing rules")
	}
	existingRuleIDs := map[int64]bool{}
	for _, rule := range rules {
		existingRuleIDs[rule.ID] = true
		if strings.EqualFold(rule.DomainName, cfg.Domain) && strings.EqualFold(rule.MatchUser, ruleLocalPart) {
			t.Fatal("测试 routing rule 地址已经存在，终止资源创建")
		}
	}
	password := uuid.NewString() + uuid.NewString() + "Aa1!"
	if err := c.CreateUser(ctx, purelymail.CreateUserRequest{
		UserName: localPart, DomainName: cfg.Domain, Password: password,
		EnableSearchIndexing: true, EnablePasswordReset: false, SendWelcomeEmail: false,
	}); err != nil {
		t.Fatal("真实 Purelymail 测试用户创建失败")
	}
	userActive := true
	t.Cleanup(func() {
		if userActive {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
			defer cleanupCancel()
			if err := c.DeleteUser(cleanupCtx, address); err != nil {
				t.Error("无法清理本次创建的 Purelymail 测试用户")
			}
		}
	})
	users, err = c.ListUsers(ctx)
	if err != nil {
		t.Fatal("无法读取创建后的 Purelymail 用户列表")
	}
	foundUser := false
	for _, user := range users {
		if strings.EqualFold(user, address) {
			foundUser = true
		}
	}
	if !foundUser {
		t.Fatal("创建的 Purelymail 测试用户没有出现在真实列表中")
	}
	info, err := c.GetUser(ctx, address)
	if err != nil || info == nil || !info.EnableSearchIndexing {
		t.Fatal("创建的 Purelymail 测试用户设置不符合请求")
	}
	cred := imappool.Cred{
		User: address, Pass: password,
		Addr:    net.JoinHostPort(cfg.IMAP.Host, strconv.Itoa(cfg.IMAP.Port)),
		TLSMode: cfg.IMAP.TLSMode, TLSConfig: tlsConfig,
	}
	requireRealClientLogin(t, ctx, pool, cred, true)
	pw, err := c.CreateAppPassword(ctx, address, localPart)
	if err != nil || pw == "" {
		t.Fatal("真实 Purelymail app password 创建失败")
	}
	appPasswordActive := true
	t.Cleanup(func() {
		if appPasswordActive {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
			defer cleanupCancel()
			if err := c.DeleteAppPassword(cleanupCtx, address, pw); err != nil {
				t.Error("无法清理本次创建的 Purelymail app password")
			}
		}
	})
	appCred := cred
	appCred.Pass = pw
	requireRealClientLogin(t, ctx, pool, appCred, true)
	if err := c.DeleteAppPassword(ctx, address, pw); err != nil {
		t.Fatal("真实 Purelymail app password 撤销失败")
	}
	appPasswordActive = false
	requireRealClientLogin(t, ctx, pool, appCred, false)
	np := uuid.NewString() + uuid.NewString() + "Aa1!"
	if err := c.ModifyUser(ctx, purelymail.ModifyUserRequest{UserName: address, NewPassword: &np}); err != nil {
		t.Fatal("真实 Purelymail 测试用户密码更新失败")
	}
	updatedCred := cred
	updatedCred.Pass = np
	requireRealClientLogin(t, ctx, pool, updatedCred, true)
	requireRealClientLogin(t, ctx, pool, cred, false)

	if err := c.CreateRoutingRule(ctx, purelymail.CreateRoutingRuleRequest{
		DomainName: cfg.Domain, MatchUser: ruleLocalPart, TargetAddresses: []string{address},
	}); err != nil {
		t.Fatal("真实 Purelymail routing rule 创建失败")
	}
	findOwnedRule := func(ctx context.Context) (int64, error) {
		current, err := c.ListRoutingRules(ctx)
		if err != nil {
			return 0, err
		}
		var id int64
		for _, rule := range current {
			if !strings.EqualFold(rule.DomainName, cfg.Domain) || !strings.EqualFold(rule.MatchUser, ruleLocalPart) {
				continue
			}
			if existingRuleIDs[rule.ID] || rule.ID < 1 || rule.Prefix || rule.Catchall || len(rule.TargetAddresses) != 1 || !strings.EqualFold(rule.TargetAddresses[0], address) || id != 0 {
				return 0, errors.New("无法唯一确认本次创建的 routing rule")
			}
			id = rule.ID
		}
		if id == 0 {
			return 0, errors.New("没有找到本次创建的 routing rule")
		}
		return id, nil
	}
	ruleActive := true
	t.Cleanup(func() {
		if ruleActive {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
			defer cleanupCancel()
			id, err := findOwnedRule(cleanupCtx)
			if err != nil {
				t.Error("无法确认需要清理的 Purelymail 测试 routing rule")
				return
			}
			if err := c.DeleteRoutingRule(cleanupCtx, id); err != nil {
				t.Error("无法清理本次创建的 Purelymail routing rule")
			}
		}
	})
	ruleID, err := findOwnedRule(ctx)
	if err != nil {
		t.Fatal("真实 Purelymail routing rule 的来源或目标不符合请求")
	}
	if err := c.DeleteRoutingRule(ctx, ruleID); err != nil {
		t.Fatal("真实 Purelymail 测试 routing rule 删除失败")
	}
	ruleActive = false
	rules, err = c.ListRoutingRules(ctx)
	if err != nil {
		t.Fatal("无法验证真实 Purelymail 测试 routing rule 已删除")
	}
	for _, rule := range rules {
		if rule.ID == ruleID || (strings.EqualFold(rule.DomainName, cfg.Domain) && strings.EqualFold(rule.MatchUser, ruleLocalPart)) {
			t.Fatal("删除的 Purelymail 测试 routing rule 仍然存在")
		}
	}
	if err := c.DeleteUser(ctx, address); err != nil {
		t.Fatal("真实 Purelymail 测试用户删除失败")
	}
	userActive = false
	users, err = c.ListUsers(ctx)
	if err != nil {
		t.Fatal("无法验证真实 Purelymail 测试用户已删除")
	}
	for _, user := range users {
		if strings.EqualFold(user, address) {
			t.Fatal("删除的 Purelymail 测试用户仍然存在")
		}
	}
}
