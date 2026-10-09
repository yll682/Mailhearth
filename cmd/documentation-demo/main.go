package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"mailhearth/internal/config"
	"mailhearth/internal/core"
	"mailhearth/internal/db"
	"mailhearth/internal/devstack"
	"mailhearth/internal/httpapi"
	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/secrets"
	"mailhearth/internal/web"
)

const demoPassword = "Mailhearth-Demo-2026!"

func require(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	dir := flag.String("data-dir", "data/documentation-preview", "演示数据库目录")
	listen := flag.String("listen", "127.0.0.1:18080", "本地演示监听地址")
	check := flag.Bool("check", false, "初始化并检查演示数据后退出")
	flag.Parse()
	host, _, err := net.SplitHostPort(*listen)
	require(err)
	if host != "127.0.0.1" {
		panic("演示程序仅允许监听 127.0.0.1")
	}
	root, err := filepath.Abs("data")
	require(err)
	abs, err := filepath.Abs(*dir)
	require(err)
	if filepath.Dir(abs) != root || !strings.HasPrefix(filepath.Base(abs), "documentation-preview") {
		panic("演示目录必须位于项目 data 目录中，名称以 documentation-preview 开始")
	}
	require(os.MkdirAll(abs, 0o700))
	resolved, err := filepath.EvalSymlinks(abs)
	require(err)
	resolvedRoot, err := filepath.EvalSymlinks(root)
	require(err)
	if filepath.Dir(resolved) != resolvedRoot {
		panic("演示目录不能通过符号链接指向其他目录")
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	stack, err := devstack.Start(log)
	require(err)
	defer stack.Close()
	seedInfrastructure(stack)
	apiServer := httptest.NewTLSServer(stack.Handler())
	defer apiServer.Close()
	stack.APIURL = apiServer.URL
	imapBridge := startTLSBridge(stack.IMAPAddr, apiServer.TLS)
	defer imapBridge.Close()
	smtpBridge := startTLSBridge(stack.SMTPAddr, apiServer.TLS)
	defer smtpBridge.Close()
	stack.IMAPAddr, stack.SMTPAddr = imapBridge.Addr().String(), smtpBridge.Addr().String()
	roots := x509.NewCertPool()
	roots.AddCert(apiServer.Certificate())
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil, fmt.Errorf("演示程序仅允许连接本地 HTTP 服务：%s", address)
		}
		return (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, network, address)
	}
	http.DefaultTransport = transport
	defer transport.CloseIdleConnections()
	master, _, err := secrets.LoadMasterKey(abs)
	require(err)
	box, err := secrets.NewBox(master, "credentials")
	require(err)
	database, err := db.Open(filepath.Join(abs, "mailhearth.db"))
	require(err)
	defer database.Close()
	cfg := &config.Config{
		Listen: *listen, DataDir: abs, BaseURL: "http://" + *listen, DevStack: true,
		PurelymailAPIURL: stack.APIURL, IMAPAddr: stack.IMAPAddr, IMAPTLS: config.TLSImplicit,
		SMTPAddr: stack.SMTPAddr, SMTPTLS: config.TLSImplicit, CABundles: map[int64]*x509.CertPool{1: roots},
		IMAPMaxConns: 24, IMAPIdleTimeout: 90 * time.Second,
		MaxUploadBytes: 25 << 20, MaxMessageBytes: 40 << 20,
		SessionTTL: 30 * 24 * time.Hour, InviteTTL: 7 * 24 * time.Hour,
	}
	pool := imappool.New(imappool.Config{MaxConns: 24, PerCredIdle: 2, IdleTimeout: cfg.IMAPIdleTimeout, Logger: log})
	defer pool.Close()
	svc := core.New(database, cfg, box, pool, log)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var existing int
	require(database.QueryRowContext(ctx, "SELECT COUNT(*) FROM organizations").Scan(&existing))
	preserved := installationSnapshot(ctx, svc)
	seedDatabase(ctx, svc, stack)
	storeMessages(ctx, svc)
	seedMessages(ctx, svc, stack)
	api := httpapi.New(cfg, svc, pool, web.Handler(), master, log)
	verifyDemo(ctx, svc, api.Handler())
	if existing > 0 && preserved != installationSnapshot(ctx, svc) {
		panic("演示初始化改变了已有组织、管理员或会话")
	}
	if *check {
		return
	}
	require(svc.StartOperations(ctx))
	defer func() { stop(); svc.WaitOperations() }()
	require(svc.StartSubmissions(ctx))
	defer func() { stop(); svc.WaitSubmissions() }()
	listener, err := net.Listen("tcp", cfg.Listen)
	require(err)
	srv := &http.Server{Handler: api.Handler(), ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 120 * time.Second}
	defer srv.Close()
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- srv.Serve(listener) }()
	fmt.Println("演示页面：http://" + cfg.Listen + "；初始邮件保存于演示数据库，邮件操作使用本地协议服务。")
	select {
	case <-ctx.Done():
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			require(err)
		}
	case err := <-svc.OperationErrors():
		require(err)
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require(srv.Shutdown(shutdown))
}

func installationSnapshot(ctx context.Context, svc *core.Service) string {
	var snapshot string
	require(svc.DB.QueryRowContext(ctx, "SELECT json_object('organizations',(SELECT json_group_array(json_object('id',id,'name',name,'settings',settings_json,'createdAt',created_at)) FROM organizations),'owners',(SELECT json_group_array(json_object('id',m.id,'name',m.display_name,'email',m.login_email,'passwordHash',m.password_hash,'roleId',m.role_id,'status',m.status,'revision',m.revision)) FROM members m JOIN roles r ON r.id=m.role_id WHERE r.key='owner'),'sessions',(SELECT json_group_array(json_object('id',id,'memberId',member_id,'tokenHash',token_hash,'createdAt',created_at,'expiresAt',expires_at)) FROM sessions))").Scan(&snapshot))
	return snapshot
}

func startTLSBridge(target string, cfg *tls.Config) net.Listener {
	listener, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	require(err)
	go func() {
		for {
			client, err := listener.Accept()
			if errors.Is(err, net.ErrClosed) {
				return
			}
			require(err)
			go func() {
				defer client.Close()
				upstream, err := net.DialTimeout("tcp", target, 15*time.Second)
				require(err)
				defer upstream.Close()
				go func() {
					io.Copy(upstream, client)
					upstream.Close()
				}()
				io.Copy(client, upstream)
			}()
		}
	}()
	return listener
}
