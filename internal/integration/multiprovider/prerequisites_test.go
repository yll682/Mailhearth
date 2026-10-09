package multiprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"mailhearth/internal/config"
	"mailhearth/internal/core"
	"mailhearth/internal/db"
	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/model"
	"mailhearth/internal/provider"
	"mailhearth/internal/secrets"
)

type providerConfig struct {
	APIKey      string                     `json:"apiKey"`
	APIUsername string                     `json:"apiUsername,omitempty"`
	Domain      string                     `json:"domain"`
	IMAP        *provider.ProtocolTemplate `json:"imap"`
	SMTP        *provider.ProtocolTemplate `json:"smtp"`
	ManageSieve *provider.ProtocolTemplate `json:"managesieve"`
}

type mailboxConfig struct {
	Address     string                   `json:"address"`
	Credentials []core.EnteredCredential `json:"credentials"`
	Endpoints   core.EndpointInputs      `json:"endpoints"`
}

type manualConfig struct {
	PrimaryMailbox   *mailboxConfig `json:"primaryMailbox"`
	SecondaryMailbox *mailboxConfig `json:"secondaryMailbox"`
	IndependentSMTP  *mailboxConfig `json:"independentSmtp"`
	PrivateCAMailbox *mailboxConfig `json:"privateCaMailbox"`
	NoSieveMailbox   *mailboxConfig `json:"noSieveMailbox"`
}

type testConfig struct {
	Purelymail             *providerConfig `json:"purelymail"`
	Migadu                 *providerConfig `json:"migadu"`
	Manual                 *manualConfig   `json:"manual"`
	DeliveryTimeoutSeconds int             `json:"deliveryTimeoutSeconds"`
}

func readTestConfig(t *testing.T, root string) testConfig {
	t.Helper()
	path := os.Getenv("MAILHEARTH_MULTIPROVIDER_TEST_CONFIG")
	if path == "" {
		t.Skip("未设置 MAILHEARTH_MULTIPROVIDER_TEST_CONFIG；真实环境检查未执行")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal("无法读取真实验收配置")
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		t.Fatal("验收配置必须位于项目 data/integration/multi-provider/ 目录")
	}
	f, err := os.Open(resolved)
	if err != nil {
		t.Fatal("无法打开真实验收配置")
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 1<<20))
	decoder.DisallowUnknownFields()
	var cfg testConfig
	if err := decoder.Decode(&cfg); err != nil {
		t.Fatal("验收配置 JSON 无效或包含未知字段")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatal("验收配置只能包含一个 JSON 值")
	}
	var missing []string
	for _, item := range []struct {
		name  string
		value *providerConfig
	}{{"purelymail", cfg.Purelymail}, {"migadu", cfg.Migadu}} {
		if item.value == nil {
			missing = append(missing, item.name)
			continue
		}
		if item.value.APIKey == "" {
			missing = append(missing, item.name+".apiKey")
		}
		if item.name == "migadu" && item.value.APIUsername == "" {
			missing = append(missing, "migadu.apiUsername")
		}
		if item.value.Domain == "" {
			missing = append(missing, item.name+".domain")
		}
		if item.value.IMAP == nil || item.value.SMTP == nil || item.value.ManageSieve == nil {
			missing = append(missing, item.name+" 的三项网络模板")
		}
	}
	if cfg.Manual == nil {
		missing = append(missing, "manual")
	} else {
		for _, item := range []struct {
			name  string
			value *mailboxConfig
		}{{"primaryMailbox", cfg.Manual.PrimaryMailbox}, {"secondaryMailbox", cfg.Manual.SecondaryMailbox}, {"independentSmtp", cfg.Manual.IndependentSMTP}, {"privateCaMailbox", cfg.Manual.PrivateCAMailbox}, {"noSieveMailbox", cfg.Manual.NoSieveMailbox}} {
			if item.value == nil || item.value.Address == "" || len(item.value.Credentials) == 0 || item.value.Endpoints.IMAP == nil || item.value.Endpoints.SMTP == nil || item.value.Endpoints.ManageSieve == nil {
				missing = append(missing, "manual."+item.name)
			}
		}
	}
	if len(missing) > 0 {
		t.Fatal("缺少真实验收配置：" + strings.Join(missing, "、"))
	}
	if cfg.DeliveryTimeoutSeconds == 0 {
		cfg.DeliveryTimeoutSeconds = 180
	}
	if cfg.DeliveryTimeoutSeconds < 1 {
		t.Fatal("deliveryTimeoutSeconds 必须为正整数")
	}
	return cfg
}

func TestRealMultiProviderPrerequisites(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "data", "integration", "multi-provider"))
	if err != nil {
		t.Fatal(err)
	}
	input := readTestConfig(t, root)
	runID := time.Now().UTC().Format("20060102T150405") + "-" + uuid.NewString()
	dir := filepath.Join(root, runID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DevStack {
		t.Fatal("真实验收要求关闭 MAILHEARTH_DEV_STACK")
	}
	master, _, err := secrets.LoadMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.NewBox(master, "credentials")
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(dir, "mailhearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	pool := imappool.New(imappool.Config{MaxConns: 24, Logger: slog.Default()})
	defer pool.Close()
	svc := core.New(database, cfg, box, pool, slog.Default())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	member, err := svc.Init(ctx, core.InitRequest{OrgName: runID, AdminName: "验收管理员", AdminEmail: "integration@example.org", Password: uuid.NewString() + "Aa1!"})
	if err != nil {
		t.Fatal(err)
	}
	org, err := svc.Org(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		kind  provider.ProviderKind
		value *providerConfig
	}{{provider.Purelymail, input.Purelymail}, {provider.Migadu, input.Migadu}} {
		_, err := svc.CreateConnection(ctx, org.ID, member.ID, core.CreateConnectionInput{ProviderKind: item.kind, Label: runID + "-" + string(item.kind), APIAuth: &provider.APIAuth{APIKey: item.value.APIKey, Username: item.value.APIUsername}, DomainScope: &provider.DomainScope{Mode: "selected", Domains: []string{item.value.Domain}}, ProtocolDefaults: &provider.ProtocolTemplates{IMAP: *item.value.IMAP, SMTP: *item.value.SMTP, ManageSieve: *item.value.ManageSieve}})
		if err != nil {
			t.Fatalf("%s 真实管理认证失败", item.kind)
		}
	}
	for _, item := range []struct {
		name  string
		value *mailboxConfig
	}{{"primaryMailbox", input.Manual.PrimaryMailbox}, {"secondaryMailbox", input.Manual.SecondaryMailbox}, {"independentSmtp", input.Manual.IndependentSMTP}, {"privateCaMailbox", input.Manual.PrivateCAMailbox}, {"noSieveMailbox", input.Manual.NoSieveMailbox}} {
		connection, err := svc.CreateConnection(ctx, org.ID, member.ID, core.CreateConnectionInput{ProviderKind: provider.Manual, Label: runID + "-" + item.name})
		if err != nil {
			t.Fatal(err)
		}
		_, err = svc.AttachMailbox(ctx, org.ID, member.ID, core.AttachMailboxInput{Mode: "attach", ConnectionID: connection.ID, Kind: model.MailboxPersonal, Address: item.value.Address, DisplayName: item.name, OwnerMemberID: &member.ID, CredentialMode: "entered", Credentials: item.value.Credentials, Endpoints: item.value.Endpoints, SentCopyMode: "append"})
		if err != nil {
			t.Fatalf("manual.%s 真实协议认证失败", item.name)
		}
	}
	results, err := json.MarshalIndent(struct {
		RunID              string `json:"runId"`
		Prerequisites      string `json:"prerequisites"`
		AcceptanceComplete bool   `json:"acceptanceComplete"`
	}{runID, "passed", false}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prerequisites.json"), results, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log(fmt.Sprintf("真实管理和协议认证通过；记录目录：%s。T01–T40 与 V01–V07 继续独立验收。", dir))
}
