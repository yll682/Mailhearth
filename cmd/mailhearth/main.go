// Command mailhearth is the single binary that runs the whole product:
// HTTP API, embedded web client, SQLite storage and the IMAP/SMTP/Sieve
// bridges to Purelymail.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"mailhearth/internal/config"
	"mailhearth/internal/core"
	"mailhearth/internal/db"
	"mailhearth/internal/devstack"
	"mailhearth/internal/httpapi"
	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/purelymail/fake"
	"mailhearth/internal/secrets"
	"mailhearth/internal/web"
)

var version = "dev"

func main() {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		seedDemo    = flag.Bool("seed-demo", false, "with MAILHEARTH_DEV_STACK=1: seed a demo domain, users and mail")
	)
	flag.Parse()
	if *showVersion {
		fmt.Println("mailhearth", version)
		return
	}
	if err := run(*seedDemo); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run(seedDemo bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	// Keep the Go heap modest by default; operators can override via GOMEMLIMIT.
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(160 << 20)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("data dir: %w", err)
	}

	var stack *devstack.Stack
	if cfg.DevStack {
		stack, err = devstack.Start(log)
		if err != nil {
			return err
		}
		defer stack.Close()
		cfg.PurelymailAPIURL = stack.APIURL
		cfg.IMAPAddr, cfg.IMAPTLS = stack.IMAPAddr, config.TLSNone
		cfg.SMTPAddr, cfg.SMTPTLS = stack.SMTPAddr, config.TLSNone
		cfg.SieveAddr = ""
		log.Warn("DEV STACK ENABLED: using an in-memory fake Purelymail/IMAP/SMTP; mail is not persisted", "apiToken", stack.Token)
		if seedDemo {
			seed(stack, log)
		}
	}

	master, generated, err := secrets.LoadMasterKey(cfg.DataDir)
	if err != nil {
		return err
	}
	if generated {
		log.Warn("generated a new master key; back up " + filepath.Join(cfg.DataDir, "master.key") + " together with the database")
	}
	box, err := secrets.NewBox(master, "credentials")
	if err != nil {
		return err
	}
	signBox, err := secrets.NewBox(master, "signing")
	if err != nil {
		return err
	}
	_ = signBox
	signKey, err := deriveSignKey(master)
	if err != nil {
		return err
	}

	database, err := db.Open(filepath.Join(cfg.DataDir, "mailhearth.db"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	pool := imappool.New(imappool.Config{
		Addr: cfg.IMAPAddr, TLSMode: string(cfg.IMAPTLS), MaxConns: cfg.IMAPMaxConns, PerCredIdle: 2, IdleTimeout: cfg.IMAPIdleTimeout, Logger: log,
	})
	defer pool.Close()

	svc := core.New(database, cfg, box, pool, log)
	api := httpapi.New(cfg, svc, pool, web.Handler(), signKey, log)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       5 * time.Minute, // uploads
		WriteTimeout:      0,               // SSE and downloads manage their own deadlines
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	go func() {
		log.Info("mailhearth listening", "addr", cfg.Listen, "version", version, "data", cfg.DataDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func deriveSignKey(master []byte) ([]byte, error) {
	b, err := secrets.NewBox(master, "signing-derive")
	if err != nil {
		return nil, err
	}
	// Use the sealed form of a constant as key material: deterministic per
	// installation without exposing the master key.
	sealed, err := b.Seal("view-token")
	if err != nil {
		return nil, err
	}
	return []byte(secrets.HashToken(sealed[:8] + string(master))), nil
}

func seed(stack *devstack.Stack, log *slog.Logger) {
	stack.API.AddDomain(fake.Domain{Name: "acme.test", MX: true, SPF: true, DKIM: true, DMARC: true})
	stack.API.AddDomain(fake.Domain{Name: "acme-studio.test", MX: true, SPF: true})
	users := map[string]string{
		"alice@acme.test": "alice-pass", "bob@acme.test": "bob-pass", "carol@acme.test": "carol-pass",
		"support@acme.test": "support-pass", "hello@acme-studio.test": "hello-pass",
	}
	for u, p := range users {
		stack.SeedUser(u, p)
	}
	stack.API.AddRule(fake.Rule{DomainName: "acme.test", MatchUser: "sales", TargetAddresses: []string{"alice@acme.test"}})
	stack.API.AddRule(fake.Rule{DomainName: "acme.test", MatchUser: "team", TargetAddresses: []string{"alice@acme.test", "bob@acme.test"}})
	now := time.Now()
	samples := []struct{ from, subject, text, html string }{
		{"Dana Winters <dana@northwind.example>", "Q4 partnership proposal", "Hi Alice,\n\nAttached is the proposal we discussed. Let me know what you think.\n\nBest,\nDana", `<p>Hi Alice,</p><p>Attached is the proposal we discussed. Let me know what you think.</p><p>Best,<br>Dana</p>`},
		{"GitHub <noreply@github.example>", "[acme/app] Deployment succeeded", "Deployment to production succeeded.", `<div style="font-family:sans-serif"><h2>Deployment succeeded</h2><p>Build <b>#412</b> is live. <img src="https://tracker.example/pixel.gif"></p></div>`},
		{"Invoices <billing@cloudhost.example>", "Invoice #88213 for September", "Your invoice total is $42.10.", `<table><tr><td>Invoice</td><td>#88213</td></tr><tr><td>Total</td><td>$42.10</td></tr></table>`},
		{"Li Wei <liwei@partner.example>", "=?UTF-8?B?5YWz5LqO5LiL5ZGo55qE5Lya6K6u5a6J5o6S?=", "你好，\n\n下周三下午三点是否方便开会？\n\n李伟", ""},
		{"Newsletter <news@designweekly.example>", "Design Weekly #203", "This week: color systems, type scales and more.", `<p>This week: <a href="https://designweekly.example/203">color systems</a>, type scales and more.</p>`},
	}
	for i, smp := range samples {
		for _, rcpt := range []string{"alice@acme.test", "support@acme.test"} {
			raw := devstack.SampleMessage(smp.from, rcpt, smp.subject, smp.text, smp.html, now.Add(-time.Duration(i)*7*time.Hour))
			stack.Deliver(rcpt, "INBOX", raw)
		}
	}
	log.Info("demo data seeded", "apiToken", stack.Token, "users", "alice@acme.test (alice-pass), bob@acme.test (bob-pass), carol@acme.test (carol-pass), support@acme.test (support-pass)")
}
