package mailops_test

import (
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"mailhearth/internal/mailproto/mailops"
	"mailhearth/internal/provider"
)

func TestListFoldersOnRev1Server(t *testing.T) {
	env := newRealMailEnvironment(t)
	mailbox := env.attach(t, env.input.Manual.PrimaryMailbox)
	endpoint := env.endpoint(t, mailbox.ID, provider.ProtocolIMAP)
	conn := env.connect(t, endpoint)
	defer env.svc.Pool.Put(conn)
	caps := conn.C.Caps()
	if !caps.Has(imap.CapIMAP4rev1) || caps.Has(imap.CapIMAP4rev2) || caps.Has(imap.CapListExtended) || caps.Has(imap.CapListStatus) || caps.Has(imap.CapSpecialUse) {
		t.Fatal("IMAP4rev1 测试要求 primaryMailbox 使用未提供 LIST-EXTENDED、LIST-STATUS 和 SPECIAL-USE 的真实服务器")
	}
	source := env.createFolder(t, conn, endpoint, "rev1-source")
	junk := env.createFolder(t, conn, endpoint, "rev1-junk")
	for _, item := range []struct {
		folder string
		count  int
		seen   int
	}{{*source, 3, 2}, {*junk, 5, 0}} {
		for i := 0; i < item.count; i++ {
			raw, err := mailops.Build(&mailops.Draft{From: mailops.Recipient{Address: mailbox.Address}, To: []mailops.Recipient{{Address: mailbox.Address}}, Subject: "IMAP4rev1 folder counts", Text: "真实 STATUS 数量检查", MessageID: mailops.NewMessageID("mailhearth.invalid")})
			if err != nil {
				t.Fatal("无法生成 IMAP4rev1 测试邮件")
			}
			var flags []string
			if i < item.seen {
				flags = []string{`\Seen`}
			}
			if _, err := mailops.Append(env.ctx, conn, item.folder, flags, time.Now(), raw); err != nil {
				t.Fatal("真实 IMAP4rev1 APPEND 失败")
			}
		}
	}
	folders, err := mailops.ListFolders(env.ctx, conn)
	if err != nil {
		t.Fatal("真实 IMAP4rev1 LIST 或逐文件夹 STATUS 失败")
	}
	if len(folders) == 0 || folders[0].Role != mailops.RoleInbox || !strings.EqualFold(folders[0].Name, "INBOX") {
		t.Fatal("真实 IMAP4rev1 文件夹列表没有优先返回 INBOX")
	}
	byName := map[string]mailops.Folder{}
	for _, folder := range folders {
		if _, exists := byName[folder.Name]; exists {
			t.Fatal("真实 IMAP4rev1 文件夹列表包含重复名称")
		}
		byName[folder.Name] = folder
	}
	if byName[*source].Total != 3 || byName[*source].Unseen != 1 || byName[*junk].Total != 5 || byName[*junk].Unseen != 5 {
		t.Fatal("真实 IMAP4rev1 逐文件夹 STATUS 数量无效")
	}
	var list []*imap.ListData
	conn.Run(env.ctx, func() { list, err = conn.C.List("", "*", nil).Collect() })
	if err != nil {
		t.Fatal("真实 IMAP4rev1 普通 LIST 验证失败")
	}
	listed := map[string]bool{}
	for _, entry := range list {
		listed[entry.Mailbox] = true
		folder, exists := byName[entry.Mailbox]
		if !exists {
			t.Fatal("真实 IMAP4rev1 文件夹查询遗漏 LIST 结果")
		}
		for _, attr := range entry.Attrs {
			role := map[imap.MailboxAttr]string{
				imap.MailboxAttrDrafts:  mailops.RoleDrafts,
				imap.MailboxAttrSent:    mailops.RoleSent,
				imap.MailboxAttrArchive: mailops.RoleArchive,
				imap.MailboxAttrAll:     mailops.RoleArchive,
				imap.MailboxAttrJunk:    mailops.RoleJunk,
				imap.MailboxAttrTrash:   mailops.RoleTrash,
			}[attr]
			if role != "" && folder.SpecialUse != role {
				t.Fatal("真实 IMAP4rev1 LIST 特殊用途属性未保留")
			}
		}
	}
	if len(listed) != len(byName) {
		t.Fatal("真实 IMAP4rev1 文件夹查询与普通 LIST 数量不一致")
	}
	special := mailops.SpecialFolders(folders)
	if special[mailops.RoleInbox] != "INBOX" {
		t.Fatal("真实 IMAP4rev1 INBOX 映射无效")
	}
	for _, folder := range folders {
		if folder.SpecialUse == "" || folder.NoSelect {
			continue
		}
		count := 0
		for _, candidate := range folders {
			if !candidate.NoSelect && candidate.SpecialUse == folder.SpecialUse {
				count++
			}
		}
		if count == 1 && special[folder.SpecialUse] != folder.Name {
			t.Fatal("真实 IMAP4rev1 唯一特殊用途文件夹映射无效")
		}
	}
}
