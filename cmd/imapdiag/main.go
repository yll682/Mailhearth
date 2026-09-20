// Command imapdiag probes a real IMAP server for the capabilities Mailhearth
// depends on, then runs Mailhearth's own folder-listing code against it. It
// exists to tell "our code is wrong" apart from "this server does not speak
// that extension" without guessing.
//
//	go run ./cmd/imapdiag -addr imap.purelymail.com:993 -user you@example.com
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"

	"mailhearth/internal/db"
	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/mailproto/mailops"
	"mailhearth/internal/secrets"
)

// credFromDataDir reads a mailbox app password out of an installation's own
// database, so that diagnosing a live server never needs a password typed on
// a command line or pasted into a chat.
func credFromDataDir(dataDir, user string) (imappool.Cred, error) {
	master, generated, err := secrets.LoadMasterKey(dataDir)
	if err != nil {
		return imappool.Cred{}, err
	}
	if generated {
		return imappool.Cred{}, fmt.Errorf("%s holds no master key; is that the right data dir?", dataDir)
	}
	box, err := secrets.NewBox(master, "credentials")
	if err != nil {
		return imappool.Cred{}, err
	}
	database, err := db.Open(filepath.Join(dataDir, "mailhearth.db"))
	if err != nil {
		return imappool.Cred{}, err
	}
	defer database.Close()

	var addr, enc string
	q := `SELECT pm_user, credential_enc FROM mailboxes WHERE credential_enc != '' AND (? = '' OR lower(pm_user) = lower(?)) ORDER BY id LIMIT 1`
	if err := database.QueryRow(q, user, user).Scan(&addr, &enc); err != nil {
		return imappool.Cred{}, fmt.Errorf("no connected mailbox found in %s: %w", dataDir, err)
	}
	pw, err := box.Open(enc)
	if err != nil {
		return imappool.Cred{}, err
	}
	return imappool.Cred{User: addr, Pass: pw}, nil
}

func main() {
	addr := flag.String("addr", "imap.purelymail.com:993", "IMAP address")
	tlsMode := flag.String("tls", "tls", "tls | starttls | none")
	user := flag.String("user", "", "mailbox address (default: first connected mailbox)")
	dataDir := flag.String("data", "", "read the app password from this installation's data dir")
	pass := flag.String("pass", "", "password (or set IMAPDIAG_PASS); prefer -data")
	flag.Parse()

	var cred imappool.Cred
	switch {
	case *dataDir != "":
		var err error
		cred, err = credFromDataDir(*dataDir, *user)
		if err != nil {
			fmt.Fprintln(os.Stderr, "credential:", err)
			os.Exit(2)
		}
	default:
		password := *pass
		if password == "" {
			password = os.Getenv("IMAPDIAG_PASS")
		}
		if *user == "" || password == "" {
			fmt.Fprintln(os.Stderr, "need -data <dir>, or -user plus a password via -pass or IMAPDIAG_PASS")
			os.Exit(2)
		}
		cred = imappool.Cred{User: *user, Pass: password}
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	pool := imappool.New(imappool.Config{
		Addr: *addr, TLSMode: *tlsMode, MaxConns: 4, PerCredIdle: 1,
		IdleTimeout: 60 * time.Second, Logger: log,
	})
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	conn, err := pool.Get(ctx, cred)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		os.Exit(1)
	}
	defer pool.Put(conn)

	fmt.Printf("server %s as %s\n\n", *addr, cred.User)

	caps := conn.C.Caps()
	fmt.Println("=== capabilities Mailhearth branches on ===")
	for _, c := range []imap.Cap{
		imap.CapIMAP4rev1, imap.CapIMAP4rev2, imap.CapListExtended, imap.CapListStatus,
		imap.CapSpecialUse, imap.CapMove, imap.CapUIDPlus, imap.CapIdle, imap.CapESearch,
		imap.CapUnselect, imap.CapSort, imap.CapCondStore,
	} {
		fmt.Printf("   %-16s %v\n", c, caps.Has(c))
	}

	fmt.Println("\n=== mailops.ListFolders (the code the sidebar runs) ===")
	folders, err := mailops.ListFolders(ctx, conn)
	if err != nil {
		fmt.Printf("   FAILED: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   OK, %d folders\n", len(folders))
	for _, f := range folders {
		role := f.Role
		if role == "" {
			role = "-"
		}
		fmt.Printf("   %-12s role=%-8s total=%-4d unseen=%-4d delim=%q depth=%d noselect=%v\n",
			f.Name, role, f.Total, f.Unseen, f.Delim, f.Depth, f.NoSelect)
	}

	special := mailops.SpecialFolders(folders)
	fmt.Println("\n=== special folder mapping ===")
	for _, role := range []string{
		mailops.RoleInbox, mailops.RoleDrafts, mailops.RoleSent,
		mailops.RoleArchive, mailops.RoleJunk, mailops.RoleTrash,
	} {
		name := special[role]
		if name == "" {
			name = "(none)"
		}
		fmt.Printf("   %-8s -> %s\n", role, name)
	}

	fmt.Println("\n=== mailops.ListMessages on the inbox ===")
	inbox := special[mailops.RoleInbox]
	if inbox == "" {
		inbox = "INBOX"
	}
	page, err := mailops.ListMessages(ctx, conn, inbox, 0, 10, "")
	if err != nil {
		fmt.Printf("   FAILED: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   OK, total=%d returned=%d uidvalidity=%d\n", page.Total, len(page.Messages), page.UIDValidity)
	for _, m := range page.Messages {
		from := "(none)"
		if len(m.From) > 0 {
			from = m.From[0].Address
		}
		fmt.Printf("   uid=%-6d %-28s %s\n", m.UID, truncate(from, 28), truncate(m.Subject, 50))
	}

	if len(page.Messages) > 0 {
		fmt.Println("\n=== mailops.GetMessage on the newest message ===")
		msg, err := mailops.GetMessage(ctx, conn, inbox, page.Messages[0].UID, mailops.RenderOptions{
			MaxBodyBytes: 1 << 20,
			PartURL:      func(p string) string { return "/parts/" + p },
		})
		if err != nil {
			fmt.Printf("   FAILED: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("   OK, text=%dB html=%dB attachments=%d inline=%d truncated=%v\n",
			len(msg.Text), len(msg.HTML), len(msg.Attachments), len(msg.Inline), msg.Truncated)
	}

	fmt.Println("\nall good")
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
