// Package mailops implements the mailbox operations the web client needs on
// top of a pooled IMAP connection: folder listing, paging, message
// rendering, attachment streaming and the usual flag/move/delete actions.
package mailops

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"

	"mailhearth/internal/mailproto/imappool"
)

// Folder roles.
const (
	RoleInbox   = "inbox"
	RoleSent    = "sent"
	RoleDrafts  = "drafts"
	RoleTrash   = "trash"
	RoleJunk    = "junk"
	RoleArchive = "archive"
)

// Folder is a mailbox (IMAP sense) as shown in the sidebar.
type Folder struct {
	Name        string `json:"name"`
	Display     string `json:"display"`
	Delim       string `json:"delim"`
	Role        string `json:"role"`
	Total       uint32 `json:"total"`
	Unseen      uint32 `json:"unseen"`
	NoSelect    bool   `json:"noSelect"`
	HasChildren bool   `json:"hasChildren"`
	Depth       int    `json:"depth"`
}

var roleNames = map[string]string{
	"inbox": RoleInbox,
	"sent":  RoleSent, "sent items": RoleSent, "sent messages": RoleSent, "sent mail": RoleSent, "outbox": RoleSent,
	"drafts": RoleDrafts, "draft": RoleDrafts,
	"trash": RoleTrash, "deleted items": RoleTrash, "deleted messages": RoleTrash, "deleted": RoleTrash, "bin": RoleTrash,
	"junk": RoleJunk, "spam": RoleJunk, "junk e-mail": RoleJunk, "junk email": RoleJunk, "bulk mail": RoleJunk,
	"archive": RoleArchive, "archives": RoleArchive, "all mail": RoleArchive,
}

var roleOrder = map[string]int{RoleInbox: 0, RoleDrafts: 1, RoleSent: 2, RoleArchive: 3, RoleJunk: 4, RoleTrash: 5}

func roleFromAttrs(attrs []imap.MailboxAttr) string {
	for _, a := range attrs {
		switch a {
		case imap.MailboxAttrSent:
			return RoleSent
		case imap.MailboxAttrDrafts:
			return RoleDrafts
		case imap.MailboxAttrTrash:
			return RoleTrash
		case imap.MailboxAttrJunk:
			return RoleJunk
		case imap.MailboxAttrArchive, imap.MailboxAttrAll:
			return RoleArchive
		}
	}
	return ""
}

// ListFolders lists all folders with counts, ordered INBOX first, then the
// special folders, then the rest alphabetically.
func ListFolders(ctx context.Context, conn *imappool.Conn) ([]Folder, error) {
	caps := conn.C.Caps()
	// Every RETURN clause of LIST belongs to LIST-EXTENDED (RFC 5258), folded
	// into IMAP4rev2. A plain IMAP4rev1 server answers "BAD LIST failed" to
	// any of them and drops the connection, so a server that does not
	// advertise the extension gets a bare LIST and a STATUS per folder.
	// Purelymail is such a server.
	listExtended := caps.Has(imap.CapListExtended) || caps.Has(imap.CapIMAP4rev2)
	listStatus := listExtended && (caps.Has(imap.CapListStatus) || caps.Has(imap.CapIMAP4rev2))
	var opts *imap.ListOptions
	if listExtended {
		opts = &imap.ListOptions{ReturnSubscribed: true}
		if listStatus {
			opts.ReturnStatus = &imap.StatusOptions{NumMessages: true, NumUnseen: true}
		}
		if caps.Has(imap.CapSpecialUse) {
			opts.ReturnSpecialUse = true
		}
	}
	var (
		list []*imap.ListData
		err  error
	)
	conn.Run(ctx, func() { list, err = conn.C.List("", "*", opts).Collect() })
	if err != nil {
		return nil, err
	}
	folders := make([]Folder, 0, len(list))
	seen := map[string]bool{}
	for _, ld := range list {
		if seen[ld.Mailbox] {
			continue
		}
		seen[ld.Mailbox] = true
		f := Folder{Name: ld.Mailbox, Delim: string(ld.Delim)}
		if ld.Delim == 0 {
			f.Delim = ""
		}
		for _, a := range ld.Attrs {
			switch a {
			case imap.MailboxAttrNoSelect, imap.MailboxAttrNonExistent:
				f.NoSelect = true
			case imap.MailboxAttrHasChildren:
				f.HasChildren = true
			}
		}
		if ld.Status != nil {
			if ld.Status.NumMessages != nil {
				f.Total = *ld.Status.NumMessages
			}
			if ld.Status.NumUnseen != nil {
				f.Unseen = *ld.Status.NumUnseen
			}
		}
		f.Role = roleFromAttrs(ld.Attrs)
		display := ld.Mailbox
		if f.Delim != "" {
			parts := strings.Split(ld.Mailbox, f.Delim)
			display = parts[len(parts)-1]
			f.Depth = len(parts) - 1
			if len(parts) == 2 && strings.EqualFold(parts[0], "INBOX") {
				// "INBOX.Sent" style hierarchies are shown flat.
				f.Depth = 0
			}
		}
		if strings.EqualFold(ld.Mailbox, "INBOX") {
			f.Role, f.Depth, display = RoleInbox, 0, "INBOX"
		} else if f.Role == "" {
			if r, ok := roleNames[strings.ToLower(display)]; ok && f.Depth == 0 {
				f.Role = r
			}
		}
		f.Display = display
		folders = append(folders, f)
	}
	if !listStatus {
		limit := 60
		for i := range folders {
			if folders[i].NoSelect || i >= limit {
				continue
			}
			var (
				st   *imap.StatusData
				serr error
			)
			conn.Run(ctx, func() {
				st, serr = conn.C.Status(folders[i].Name, &imap.StatusOptions{NumMessages: true, NumUnseen: true}).Wait()
			})
			if serr != nil {
				// One folder refusing STATUS must not blank the whole sidebar;
				// it shows without counts instead.
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				continue
			}
			if st.NumMessages != nil {
				folders[i].Total = *st.NumMessages
			}
			if st.NumUnseen != nil {
				folders[i].Unseen = *st.NumUnseen
			}
		}
	}
	// Ensure only one folder holds each special role (first wins).
	taken := map[string]bool{}
	for i := range folders {
		if r := folders[i].Role; r != "" {
			if taken[r] {
				folders[i].Role = ""
			} else {
				taken[r] = true
			}
		}
	}
	sort.SliceStable(folders, func(i, j int) bool {
		ri, iok := roleOrder[folders[i].Role]
		rj, jok := roleOrder[folders[j].Role]
		if iok != jok {
			return iok
		}
		if iok && jok && ri != rj {
			return ri < rj
		}
		return strings.ToLower(folders[i].Name) < strings.ToLower(folders[j].Name)
	})
	return folders, nil
}

// SpecialFolders maps roles to folder names.
func SpecialFolders(folders []Folder) map[string]string {
	m := map[string]string{}
	for _, f := range folders {
		if f.Role != "" {
			m[f.Role] = f.Name
		}
	}
	return m
}

// EnsureFolder returns the folder holding role, creating a conventional one
// when the account has none yet.
func EnsureFolder(ctx context.Context, conn *imappool.Conn, folders []Folder, role string) (string, error) {
	if name := SpecialFolders(folders)[role]; name != "" {
		return name, nil
	}
	name := map[string]string{RoleSent: "Sent", RoleDrafts: "Drafts", RoleTrash: "Trash", RoleJunk: "Junk", RoleArchive: "Archive"}[role]
	if name == "" {
		return "", fmt.Errorf("no folder for role %q", role)
	}
	if err := CreateFolder(ctx, conn, name); err != nil && !strings.Contains(strings.ToLower(err.Error()), "exist") {
		return "", err
	}
	return name, nil
}

// CreateFolder creates and subscribes a folder.
func CreateFolder(ctx context.Context, conn *imappool.Conn, name string) error {
	var err error
	conn.Run(ctx, func() {
		if err = conn.C.Create(name, nil).Wait(); err == nil {
			conn.C.Subscribe(name).Wait()
		}
	})
	conn.Invalidate()
	return err
}

// DeleteFolder deletes a folder.
func DeleteFolder(ctx context.Context, conn *impaoolConnAlias, name string) error {
	var err error
	conn.Run(ctx, func() {
		conn.C.Unsubscribe(name).Wait()
		err = conn.C.Delete(name).Wait()
	})
	conn.Invalidate()
	return err
}

type impaoolConnAlias = imappool.Conn

// RenameFolder renames a folder.
func RenameFolder(ctx context.Context, conn *imappool.Conn, name, newName string) error {
	var err error
	conn.Run(ctx, func() {
		err = conn.C.Rename(name, newName, nil).Wait()
		if err == nil {
			conn.C.Unsubscribe(name).Wait()
			conn.C.Subscribe(newName).Wait()
		}
	})
	conn.Invalidate()
	return err
}

// Addr is a parsed mailbox address.
type Addr struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

func toAddrs(list []imap.Address) []Addr {
	out := make([]Addr, 0, len(list))
	for _, a := range list {
		if a.IsGroupStart() || a.IsGroupEnd() {
			continue
		}
		out = append(out, Addr{Name: a.Name, Address: a.Addr()})
	}
	return out
}

// Summary is one row of the message list.
type Summary struct {
	UID           uint32    `json:"uid"`
	Subject       string    `json:"subject"`
	From          []Addr    `json:"from"`
	To            []Addr    `json:"to"`
	Date          time.Time `json:"date"`
	Size          int64     `json:"size"`
	Flags         []string  `json:"flags"`
	Seen          bool      `json:"seen"`
	Flagged       bool      `json:"flagged"`
	Answered      bool      `json:"answered"`
	Draft         bool      `json:"draft"`
	Forwarded     bool      `json:"forwarded"`
	HasAttachment bool      `json:"hasAttachment"`
	MessageID     string    `json:"messageId"`
	InReplyTo     []string  `json:"inReplyTo"`
}

// Page is a page of the message list.
type Page struct {
	Folder      string    `json:"folder"`
	UIDValidity uint32    `json:"uidValidity"`
	Total       uint32    `json:"total"`
	Page        int       `json:"page"`
	PageSize    int       `json:"pageSize"`
	Query       string    `json:"query"`
	Messages    []Summary `json:"messages"`
}

func flagsToStrings(flags []imap.Flag) []string {
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		out = append(out, string(f))
	}
	return out
}

func hasFlag(flags []imap.Flag, f imap.Flag) bool {
	for _, x := range flags {
		if strings.EqualFold(string(x), string(f)) {
			return true
		}
	}
	return false
}

func summaryFromBuffer(m *fetchBuffer) Summary {
	s := Summary{UID: uint32(m.UID), Size: m.RFC822Size, Flags: flagsToStrings(m.Flags)}
	s.Seen = hasFlag(m.Flags, imap.FlagSeen)
	s.Flagged = hasFlag(m.Flags, imap.FlagFlagged)
	s.Answered = hasFlag(m.Flags, imap.FlagAnswered)
	s.Draft = hasFlag(m.Flags, imap.FlagDraft)
	s.Forwarded = hasFlag(m.Flags, imap.FlagForwarded)
	if m.Envelope != nil {
		s.Subject = m.Envelope.Subject
		s.From = toAddrs(m.Envelope.From)
		s.To = toAddrs(m.Envelope.To)
		s.Date = m.Envelope.Date
		s.MessageID = m.Envelope.MessageID
		s.InReplyTo = m.Envelope.InReplyTo
	}
	if s.Date.IsZero() {
		s.Date = m.InternalDate
	}
	if m.BodyStructure != nil {
		s.HasAttachment = structureHasAttachment(m.BodyStructure)
	}
	return s
}

var listFetchOptions = &imap.FetchOptions{
	UID: true, Flags: true, Envelope: true, RFC822Size: true, InternalDate: true,
	BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
}

// ListMessages returns page (0-based) of a folder, newest first. When query
// is non-empty a server-side SEARCH narrows the set first.
func ListMessages(ctx context.Context, conn *imappool.Conn, folder string, page, size int, query string) (*Page, error) {
	if size <= 0 || size > 200 {
		size = 50
	}
	if page < 0 {
		page = 0
	}
	sel, err := conn.Select(ctx, folder, true)
	if err != nil {
		return nil, err
	}
	out := &Page{Folder: folder, UIDValidity: sel.UIDValidity, Page: page, PageSize: size, Query: query, Messages: []Summary{}}
	var numSet imap.NumSet
	if strings.TrimSpace(query) == "" {
		out.Total = sel.NumMessages
		hi := int64(sel.NumMessages) - int64(page*size)
		if hi < 1 {
			return out, nil
		}
		lo := hi - int64(size) + 1
		if lo < 1 {
			lo = 1
		}
		var set imap.SeqSet
		set.AddRange(uint32(lo), uint32(hi))
		numSet = set
	} else {
		criteria := ParseQuery(query)
		var sd *imap.SearchData
		conn.Run(ctx, func() { sd, err = conn.C.UIDSearch(criteria, nil).Wait() })
		if err != nil {
			return nil, err
		}
		uids := sd.AllUIDs()
		sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })
		out.Total = uint32(len(uids))
		start := page * size
		if start >= len(uids) {
			return out, nil
		}
		end := start + size
		if end > len(uids) {
			end = len(uids)
		}
		numSet = imap.UIDSetNum(uids[start:end]...)
	}
	msgs, err := collectFetch(ctx, conn, numSet, listFetchOptions)
	if err != nil {
		return nil, err
	}
	for _, m := range msgs {
		out.Messages = append(out.Messages, summaryFromBuffer(m))
	}
	sort.Slice(out.Messages, func(i, j int) bool { return out.Messages[i].UID > out.Messages[j].UID })
	return out, nil
}

// StoreFlags adds and removes flags on messages.
func StoreFlags(ctx context.Context, conn *imappool.Conn, folder string, uids []uint32, add, remove []string) error {
	if len(uids) == 0 {
		return nil
	}
	if _, err := conn.Select(ctx, folder, false); err != nil {
		return err
	}
	set := uidSet(uids)
	var err error
	conn.Run(ctx, func() {
		if len(add) > 0 {
			err = conn.C.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: toFlags(add)}, nil).Close()
		}
		if err == nil && len(remove) > 0 {
			err = conn.C.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsDel, Silent: true, Flags: toFlags(remove)}, nil).Close()
		}
	})
	return err
}

func toFlags(in []string) []imap.Flag {
	out := make([]imap.Flag, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, imap.Flag(s))
		}
	}
	return out
}

func uidSet(uids []uint32) imap.UIDSet {
	var set imap.UIDSet
	for _, u := range uids {
		set.AddNum(imap.UID(u))
	}
	return set
}

// Move moves messages to dest, using MOVE when available.
func Move(ctx context.Context, conn *imappool.Conn, folder string, uids []uint32, dest string) error {
	if len(uids) == 0 {
		return nil
	}
	if folder == dest {
		return nil
	}
	if _, err := conn.Select(ctx, folder, false); err != nil {
		return err
	}
	set := uidSet(uids)
	caps := conn.C.Caps()
	var err error
	conn.Run(ctx, func() {
		if caps.Has(imap.CapMove) || caps.Has(imap.CapIMAP4rev2) {
			_, err = conn.C.Move(set, dest).Wait()
			return
		}
		if _, err = conn.C.Copy(set, dest).Wait(); err != nil {
			return
		}
		if err = conn.C.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
			return
		}
		err = expunge(conn, set)
	})
	return err
}

func expunge(conn *imappool.Conn, set imap.UIDSet) error {
	caps := conn.C.Caps()
	if caps.Has(imap.CapUIDPlus) || caps.Has(imap.CapIMAP4rev2) {
		return conn.C.UIDExpunge(set).Close()
	}
	return conn.C.Expunge().Close()
}

// Delete moves messages to trash, or expunges them when they are already in
// trash (or permanent is set).
func Delete(ctx context.Context, conn *imappool.Conn, folder string, uids []uint32, trash string, permanent bool) error {
	if len(uids) == 0 {
		return nil
	}
	if !permanent && trash != "" && folder != trash {
		return Move(ctx, conn, folder, uids, trash)
	}
	if _, err := conn.Select(ctx, folder, false); err != nil {
		return err
	}
	set := uidSet(uids)
	var err error
	conn.Run(ctx, func() {
		if err = conn.C.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
			return
		}
		err = expunge(conn, set)
	})
	return err
}

// Append stores a raw message in folder and returns its UID (0 when the
// server does not report it).
func Append(ctx context.Context, conn *imappool.Conn, folder string, flags []string, when time.Time, raw []byte) (uint32, error) {
	var (
		data *imap.AppendData
		err  error
	)
	conn.Run(ctx, func() {
		cmd := conn.C.Append(folder, int64(len(raw)), &imap.AppendOptions{Flags: toFlags(flags), Time: when})
		if _, err = cmd.Write(raw); err != nil {
			return
		}
		if err = cmd.Close(); err != nil {
			return
		}
		data, err = cmd.Wait()
	})
	if err != nil {
		return 0, err
	}
	if data == nil {
		return 0, nil
	}
	return uint32(data.UID), nil
}

// ErrNotFound is returned when a UID no longer exists.
var ErrNotFound = errors.New("message not found")
