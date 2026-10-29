package imappool_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"mailhearth/internal/devstack"
	"mailhearth/internal/mailproto/imappool"
)

func startStack(t *testing.T) (*devstack.Stack, *imappool.Pool, imappool.Cred) {
	t.Helper()
	stack, err := devstack.Start(slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.Close)
	stack.SeedUser("pool@acme.test", "pool-pass")
	pool := imappool.New(imappool.Config{Addr: stack.IMAPAddr, TLSMode: imappool.TLSNone, MaxConns: 8, Logger: slog.Default()})
	t.Cleanup(pool.Close)
	return stack, pool, imappool.Cred{User: "pool@acme.test", Pass: "pool-pass"}
}

func deliver(t *testing.T, stack *devstack.Stack, subject string) {
	t.Helper()
	raw := devstack.SampleMessage("Sender <sender@example.org>", "pool@acme.test", subject, "body", "", time.Now())
	if err := stack.Deliver("pool@acme.test", "INBOX", raw); err != nil {
		t.Fatal(err)
	}
}

// #9: a connection reused from the idle list must not report the message
// count cached from before mail arrived while it sat idle.
func TestReusedConnectionSeesNewMail(t *testing.T) {
	stack, pool, cred := startStack(t)
	deliver(t, stack, "first")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pool.Get(ctx, cred)
	if err != nil {
		t.Fatal(err)
	}
	sel, err := conn.Select(ctx, "INBOX", true)
	if err != nil {
		t.Fatal(err)
	}
	if sel.NumMessages != 1 {
		t.Fatalf("初始邮件数为 %d，要求 1", sel.NumMessages)
	}
	pool.Put(conn)
	deliver(t, stack, "second")
	again, err := pool.Get(ctx, cred)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Put(again)
	if again != conn {
		t.Fatal("测试要求复用同一条空闲连接")
	}
	sel, err = again.Select(ctx, "INBOX", true)
	if err != nil {
		t.Fatal(err)
	}
	if sel.NumMessages != 2 {
		t.Fatalf("复用连接返回过期的邮件数 %d，要求 2", sel.NumMessages)
	}
}
