package mailops_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/mailproto/mailops"
)

// rev1Server is a scripted IMAP server that behaves like Purelymail: it
// advertises IMAP4rev1 only, and answers "BAD LIST failed" to any LIST
// carrying a RETURN clause, because every RETURN option belongs to
// LIST-EXTENDED (RFC 5258).
//
// The in-memory server used by the other tests advertises IMAP4rev2 and
// tolerates RETURN clauses regardless of what it advertises, so it cannot
// catch a client that asks for an extension the server never offered. This
// one can.
type rev1Server struct {
	ln net.Listener

	mu       sync.Mutex
	listCmds []string // every LIST command line the client sent
	rejected int      // how many were refused for carrying RETURN
}

var rev1Folders = []struct {
	attrs    string
	name     string
	messages int
	unseen   int
}{
	{`\HasNoChildren`, "INBOX", 3, 1},
	{`\Drafts \HasNoChildren`, "Drafts", 0, 0},
	{`\Sent \HasNoChildren`, "Sent", 2, 0},
	{`\Archive \HasNoChildren`, "Archive", 0, 0},
	{`\Junk \HasNoChildren`, "Junk", 5, 5},
	{`\Trash \HasNoChildren`, "Trash", 1, 0},
}

func startRev1Server(t *testing.T) *rev1Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &rev1Server{ln: ln}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *rev1Server) addr() string { return s.ln.Addr().String() }

func (s *rev1Server) commands() ([]string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.listCmds...), s.rejected
}

func (s *rev1Server) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	send := func(format string, args ...any) error {
		if _, err := fmt.Fprintf(w, format+"\r\n", args...); err != nil {
			return err
		}
		return w.Flush()
	}

	// Purelymail's capability set, trimmed to what matters here: rev1 with no
	// LIST-EXTENDED, no LIST-STATUS and no SPECIAL-USE.
	if send("* OK [CAPABILITY IMAP4rev1 LITERAL+ SASL-IR AUTH=PLAIN CHILDREN MOVE UIDPLUS IDLE UNSELECT] rev1 test server ready") != nil {
		return
	}

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		tag, rest, _ := strings.Cut(line, " ")
		verb, args, _ := strings.Cut(rest, " ")

		switch strings.ToUpper(verb) {
		case "CAPABILITY":
			send("* CAPABILITY IMAP4rev1 LITERAL+ SASL-IR AUTH=PLAIN CHILDREN MOVE UIDPLUS IDLE UNSELECT")
			send("%s OK CAPABILITY completed", tag)

		case "LOGIN":
			send("%s OK LOGIN completed", tag)

		case "LIST":
			s.mu.Lock()
			s.listCmds = append(s.listCmds, rest)
			carriesReturn := strings.Contains(strings.ToUpper(args), "RETURN")
			if carriesReturn {
				s.rejected++
			}
			s.mu.Unlock()
			if carriesReturn {
				// Exactly what Purelymail answers, down to the wording.
				send("%s BAD LIST failed. Illegal arguments.", tag)
				continue
			}
			for _, f := range rev1Folders {
				send(`* LIST (%s) "." %s`, f.attrs, f.name)
			}
			send("%s OK LIST completed", tag)

		case "STATUS":
			name := strings.TrimSpace(args)
			if i := strings.Index(name, " ("); i >= 0 {
				name = name[:i]
			}
			name = strings.Trim(name, `"`)
			found := false
			for _, f := range rev1Folders {
				if strings.EqualFold(f.name, name) {
					send("* STATUS %s (MESSAGES %d UNSEEN %d)", f.name, f.messages, f.unseen)
					found = true
					break
				}
			}
			if !found {
				send("%s NO STATUS no such mailbox", tag)
				continue
			}
			send("%s OK STATUS completed", tag)

		case "LOGOUT":
			send("* BYE logging out")
			send("%s OK LOGOUT completed", tag)
			return

		case "NOOP":
			send("%s OK NOOP completed", tag)

		default:
			send("%s BAD unsupported command %q", tag, verb)
		}
	}
}

// TestListFoldersOnRev1Server pins the fix for a bug that only a real server
// exposed: ListFolders used to send RETURN (SUBSCRIBED) unconditionally, which
// an IMAP4rev1-only server rejects outright, leaving the sidebar empty with an
// internal error while the message list loaded fine.
func TestListFoldersOnRev1Server(t *testing.T) {
	srv := startRev1Server(t)
	pool := imappool.New(imappool.Config{
		Addr: srv.addr(), TLSMode: imappool.TLSNone, MaxConns: 2,
		IdleTimeout: time.Minute,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pool.Get(ctx, imappool.Cred{User: "alice@acme.test", Pass: "pw"})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Put(conn)

	folders, err := mailops.ListFolders(ctx, conn)
	if err != nil {
		t.Fatalf("ListFolders on an IMAP4rev1-only server: %v", err)
	}

	cmds, rejected := srv.commands()
	if rejected != 0 {
		t.Fatalf("sent %d LIST command(s) with a RETURN clause to a server without LIST-EXTENDED: %v", rejected, cmds)
	}
	if len(cmds) != 1 {
		t.Fatalf("expected exactly one LIST, got %v", cmds)
	}

	// Counts have to come from the per-folder STATUS fallback, since the
	// server cannot return them inline.
	if len(folders) != len(rev1Folders) {
		t.Fatalf("got %d folders, want %d: %+v", len(folders), len(rev1Folders), folders)
	}
	if folders[0].Role != mailops.RoleInbox {
		t.Fatalf("first folder should be the inbox: %+v", folders[0])
	}
	if folders[0].Total != 3 || folders[0].Unseen != 1 {
		t.Fatalf("inbox counts should come from STATUS, got total=%d unseen=%d", folders[0].Total, folders[0].Unseen)
	}

	// Special-use attributes still arrive on the LIST responses themselves,
	// so roles must be detected even without the SPECIAL-USE return option.
	special := mailops.SpecialFolders(folders)
	for role, want := range map[string]string{
		mailops.RoleInbox:   "INBOX",
		mailops.RoleDrafts:  "Drafts",
		mailops.RoleSent:    "Sent",
		mailops.RoleArchive: "Archive",
		mailops.RoleJunk:    "Junk",
		mailops.RoleTrash:   "Trash",
	} {
		if special[role] != want {
			t.Errorf("role %s mapped to %q, want %q", role, special[role], want)
		}
	}

	var junk *mailops.Folder
	for i := range folders {
		if folders[i].Name == "Junk" {
			junk = &folders[i]
		}
	}
	if junk == nil {
		t.Fatal("Junk folder missing")
	}
	if junk.Total != 5 || junk.Unseen != 5 {
		t.Errorf("junk counts should come from STATUS, got total=%d unseen=%d", junk.Total, junk.Unseen)
	}
}
