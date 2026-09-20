package mailops_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"mailhearth/internal/devstack"
	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/mailproto/mailops"
)

func TestMailOpsAgainstDevStack(t *testing.T) {
	stack, err := devstack.Start(slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	stack.API.AddDomain(struct {
		Name                  string
		AllowAccountReset     bool
		SymbolicSubaddressing bool
		IsShared              bool
		MX, SPF, DKIM, DMARC  bool
	}{Name: "acme.test", MX: true, SPF: true, DKIM: true, DMARC: true})
	stack.SeedUser("alice@acme.test", "alice-pw")
	stack.SeedUser("bob@acme.test", "bob-pw")
	now := time.Now()
	for i := 0; i < 7; i++ {
		raw := devstack.SampleMessage("Sender <sender@example.com>", "alice@acme.test", "Hello "+string(rune('A'+i)),
			"plain body "+string(rune('A'+i))+" see https://example.com", `<p>html <b>body</b> <img src="http://track.example/p.gif"></p>`, now.Add(time.Duration(i)*time.Minute))
		if err := stack.Deliver("alice@acme.test", "INBOX", raw); err != nil {
			t.Fatal(err)
		}
	}
	pool := imappool.New(imappool.Config{Addr: stack.IMAPAddr, TLSMode: imappool.TLSNone, MaxConns: 4, IdleTimeout: time.Minute})
	defer pool.Close()
	ctx := context.Background()
	cred := imappool.Cred{User: "alice@acme.test", Pass: "alice-pw"}

	if _, err := pool.Get(ctx, imappool.Cred{User: "alice@acme.test", Pass: "wrong"}); err == nil {
		t.Fatal("wrong password should fail")
	}
	conn, err := pool.Get(ctx, cred)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Put(conn)

	folders, err := mailops.ListFolders(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) < 5 || folders[0].Role != mailops.RoleInbox || folders[0].Total != 7 || folders[0].Unseen != 7 {
		t.Fatalf("folders: %+v", folders)
	}
	special := mailops.SpecialFolders(folders)
	if special[mailops.RoleTrash] != "Trash" || special[mailops.RoleSent] != "Sent" {
		t.Fatalf("special folders: %v", special)
	}

	page, err := mailops.ListMessages(ctx, conn, "INBOX", 0, 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 7 || len(page.Messages) != 5 || page.Messages[0].Subject != "Hello G" || page.Messages[4].Subject != "Hello C" {
		t.Fatalf("page0: total=%d n=%d first=%q last=%q", page.Total, len(page.Messages), page.Messages[0].Subject, page.Messages[len(page.Messages)-1].Subject)
	}
	page1, err := mailops.ListMessages(ctx, conn, "INBOX", 1, 5, "")
	if err != nil || len(page1.Messages) != 2 || page1.Messages[1].Subject != "Hello A" {
		t.Fatalf("page1: %v %+v", err, page1)
	}
	if page.Messages[0].Seen || page.Messages[0].From[0].Address != "sender@example.com" || page.Messages[0].HasAttachment {
		t.Fatalf("summary: %+v", page.Messages[0])
	}

	found, err := mailops.ListMessages(ctx, conn, "INBOX", 0, 10, `subject:"Hello C" is:unread`)
	if err != nil || found.Total != 1 || found.Messages[0].Subject != "Hello C" {
		t.Fatalf("search: %v %+v", err, found)
	}

	uid := page.Messages[0].UID
	msg, err := mailops.GetMessage(ctx, conn, "INBOX", uid, mailops.RenderOptions{PartURL: func(p string) string { return "/p/" + p }})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.HTML, "<b>body</b>") || !strings.Contains(msg.HTML, "data-mh-src") || !msg.HasRemote {
		t.Fatalf("html render: %q remote=%v", msg.HTML, msg.HasRemote)
	}
	if !strings.Contains(msg.Text, "plain body G") {
		t.Fatalf("text: %q", msg.Text)
	}

	// Flags
	if err := mailops.StoreFlags(ctx, conn, "INBOX", []uint32{uid}, []string{`\Seen`, `\Flagged`}, nil); err != nil {
		t.Fatal(err)
	}
	page, _ = mailops.ListMessages(ctx, conn, "INBOX", 0, 1, "")
	if !page.Messages[0].Seen || !page.Messages[0].Flagged {
		t.Fatalf("flags not applied: %+v", page.Messages[0])
	}
	folders, _ = mailops.ListFolders(ctx, conn)
	if folders[0].Unseen != 6 {
		t.Fatalf("unseen should be 6, got %d", folders[0].Unseen)
	}

	// Move to Trash then delete permanently
	if err := mailops.Delete(ctx, conn, "INBOX", []uint32{uid}, "Trash", false); err != nil {
		t.Fatal(err)
	}
	trash, _ := mailops.ListMessages(ctx, conn, "Trash", 0, 10, "")
	if trash.Total != 1 || trash.Messages[0].Subject != "Hello G" {
		t.Fatalf("trash: %+v", trash)
	}
	inbox, _ := mailops.ListMessages(ctx, conn, "INBOX", 0, 10, "")
	if inbox.Total != 6 {
		t.Fatalf("inbox after move: %d", inbox.Total)
	}
	if err := mailops.Delete(ctx, conn, "Trash", []uint32{trash.Messages[0].UID}, "Trash", false); err != nil {
		t.Fatal(err)
	}
	trash, _ = mailops.ListMessages(ctx, conn, "Trash", 0, 10, "")
	if trash.Total != 0 {
		t.Fatalf("trash should be empty: %d", trash.Total)
	}

	// Compose with attachment, append to Drafts, read back, stream part.
	draft := &mailops.Draft{
		From:    mailops.Recipient{Name: "Alice", Address: "alice@acme.test"},
		To:      []mailops.Recipient{{Name: "Bob", Address: "bob@acme.test"}},
		Subject: "Report 报告",
		Text:    "Hi Bob,\nsee attached.",
		HTML:    "<p>Hi Bob,<br>see <b>attached</b>.</p>",
		Attachments: []mailops.Attachment{{Filename: "report.csv", MIME: "text/csv", Size: 11, Open: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("a,b\n1,2\n3,4")), nil
		}}},
	}
	raw, err := mailops.Build(draft)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("Content-Type: multipart/mixed")) || !bytes.Contains(raw, []byte("report.csv")) {
		t.Fatalf("built message:\n%s", raw)
	}
	duid, err := mailops.Append(ctx, conn, "Drafts", []string{`\Draft`, `\Seen`}, time.Now(), raw)
	if err != nil || duid == 0 {
		t.Fatalf("append: %v uid=%d", err, duid)
	}
	dm, err := mailops.GetMessage(ctx, conn, "Drafts", duid, mailops.RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if dm.Subject != "Report 报告" || !dm.HasAttachment || len(dm.Attachments) != 1 || dm.Attachments[0].Filename != "report.csv" || !strings.Contains(dm.HTML, "<b>attached</b>") {
		t.Fatalf("draft render: %+v html=%q", dm, dm.HTML)
	}
	part, err := mailops.FindPart(ctx, conn, "Drafts", duid, dm.Attachments[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := mailops.StreamPart(ctx, conn, "Drafts", duid, part, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "a,b\n1,2\n3,4" {
		t.Fatalf("attachment content: %q", out.String())
	}
	var rawOut bytes.Buffer
	if err := mailops.StreamRaw(ctx, conn, "Drafts", duid, &rawOut); err != nil || !bytes.Contains(rawOut.Bytes(), []byte("Subject:")) {
		t.Fatalf("raw: %v", err)
	}

	// SMTP send delivers to bob's INBOX in the dev stack.
	if err := mailops.Send(ctx, mailops.SMTPConfig{Addr: stack.SMTPAddr, TLSMode: "none"}, "alice@acme.test", "alice-pw", "alice@acme.test", []string{"bob@acme.test"}, raw); err != nil {
		t.Fatal(err)
	}
	if err := mailops.Send(ctx, mailops.SMTPConfig{Addr: stack.SMTPAddr, TLSMode: "none"}, "alice@acme.test", "bad", "alice@acme.test", []string{"bob@acme.test"}, raw); err == nil {
		t.Fatal("bad smtp auth should fail")
	}
	bconn, err := pool.Get(ctx, imappool.Cred{User: "bob@acme.test", Pass: "bob-pw"})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Put(bconn)
	bpage, err := mailops.ListMessages(ctx, bconn, "INBOX", 0, 10, "")
	if err != nil || bpage.Total != 1 || bpage.Messages[0].Subject != "Report 报告" {
		t.Fatalf("bob inbox: %v %+v", err, bpage)
	}

	// Folder management
	if err := mailops.CreateFolder(ctx, conn, "Projects"); err != nil {
		t.Fatal(err)
	}
	if err := mailops.RenameFolder(ctx, conn, "Projects", "Clients"); err != nil {
		t.Fatal(err)
	}
	folders, _ = mailops.ListFolders(ctx, conn)
	names := map[string]bool{}
	for _, f := range folders {
		names[f.Name] = true
	}
	if !names["Clients"] || names["Projects"] {
		t.Fatalf("rename failed: %v", names)
	}
	if err := mailops.DeleteFolder(ctx, conn, "Clients"); err != nil {
		t.Fatal(err)
	}

	// IDLE watcher notices new mail.
	events, cancel := pool.Subscribe(cred, "INBOX")
	defer cancel()
	time.Sleep(300 * time.Millisecond)
	if err := stack.Deliver("alice@acme.test", "INBOX", devstack.SampleMessage("x@example.com", "alice@acme.test", "Ping", "ping", "", time.Now())); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		if ev.Mailbox != "INBOX" || ev.Err != "" {
			t.Fatalf("unexpected event %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no IDLE event received")
	}
	open, _, watchers := pool.Stats()
	if watchers != 1 || open < 2 {
		t.Fatalf("stats: open=%d watchers=%d", open, watchers)
	}
}

func TestParseQuery(t *testing.T) {
	c := mailops.ParseQuery(`from:boss subject:"quarterly report" is:unread has:attachment since:2024-01-02 larger:2M invoice`)
	if len(c.Header) != 2 || c.Header[0].Key != "From" || c.Header[1].Value != "quarterly report" {
		t.Fatalf("headers: %+v", c.Header)
	}
	if len(c.NotFlag) != 1 || len(c.Or) != 1 || c.Since.IsZero() || c.Larger != 2<<20 || len(c.Text) != 1 || c.Text[0] != "invoice" {
		t.Fatalf("criteria: %+v", c)
	}
}
