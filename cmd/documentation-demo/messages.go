package main

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"io"
	"strings"
	"time"

	"mailhearth/internal/core"
	"mailhearth/internal/db"
	"mailhearth/internal/devstack"
	"mailhearth/internal/mailproto/mailops"
	"mailhearth/internal/provider"
)

var subjects = []string{
	"Project Aurora · 合作方案与预算明细",
	"欢迎加入 Acme 团队 / Welcome aboard",
	"客户支持 #1042：账户设置与权限确认",
	"October roadmap review",
	"关于下周产品演示会议的安排",
	"设计评审：共享收件箱与移动页面",
	"Invoice ACME-2026-1018",
	"季度业务报告 / Quarterly business review",
	"RE: Project Aurora · 更新会议时间",
	"客户反馈：导入邮箱与转发设置",
	"Engineering release notes · 2026.10",
	"培训通知：团队邮箱协作流程",
	"Contract renewal · Northwind",
	"报销审批与付款计划",
	"Design Weekly · Typography and color",
	"服务器维护窗口通知",
	"Sales pipeline · October opportunities",
	"邀请函：年度客户交流活动",
	"资料确认：域名与发件人身份",
	"附件：联系人清单与工作计划",
	"Customer onboarding checklist",
	"会议纪要：市场推广项目",
	"RE: 技术支持 · 问题已经解决",
	"Product newsletter · October edition",
}

func demoMessage(local, folder string, index int, now time.Time) *mailops.Draft {
	subject := subjects[index%len(subjects)]
	sender := mailops.Recipient{Name: "Dana Winters", Address: "dana@northwind.example"}
	if index%3 == 1 {
		sender = mailops.Recipient{Name: "李文", Address: "liwen@partner.example"}
	} else if index%3 == 2 {
		sender = mailops.Recipient{Name: "Acme Team", Address: "bob@acme.test"}
	}
	to := mailops.Recipient{Name: local, Address: local + "@acme.test"}
	if folder == "Sent" || folder == "Drafts" {
		sender, to = to, sender
	}
	text := "你好，\n\n这是用于 Mailhearth 文档截图的演示邮件。\n\nProject Aurora 的合作方案已准备完成，请确认以下安排：\n1. 10 月 16 日完成方案评审。\n2. 10 月 20 日开始客户培训。\n3. 本月演示预算为 USD 12,800。\n\n附件包含演示预算和联系人清单。欢迎在共享邮箱中添加内部备注、分配负责人和标记处理状态。\n\nBest regards,\nDana Winters\nNorthwind Partnerships"
	body := "<div style='font-family:Arial,sans-serif;line-height:1.7;color:#26364a;max-width:680px'><h2>" + html.EscapeString(subject) + "</h2><p>你好，</p><p>Project Aurora 的合作方案已经准备完成。以下内容和附件用于 <strong>Mailhearth 本地演示</strong>。</p><table style='border-collapse:collapse;width:100%'><tr><th style='text-align:left;padding:10px;background:#edf3ff'>项目</th><th style='text-align:left;padding:10px;background:#edf3ff'>安排</th></tr><tr><td style='padding:10px'>方案评审</td><td>October 16</td></tr><tr><td style='padding:10px'>客户培训</td><td>October 20</td></tr><tr><td style='padding:10px'>演示预算</td><td>USD 12,800</td></tr></table><p>请确认会议安排。共享邮箱中的同事可以添加内部备注、分配负责人，并标记处理状态。</p><p>Best regards,<br><strong>Dana Winters</strong><br>Northwind Partnerships</p><p style='color:#748299;font-size:12px'>演示数据 · Demo content</p></div>"
	message := &mailops.Draft{
		From: sender, To: []mailops.Recipient{to}, Subject: subject,
		Text: text, HTML: body, Date: now.Add(-time.Duration(index) * 5 * time.Hour),
		MessageID: fmt.Sprintf("<demo-%s-%s-%02d@acme.test>", local, strings.ToLower(folder), index),
	}
	if index%6 == 0 {
		for _, attachment := range []struct{ Name, MIME, Content string }{
			{"aurora-budget.csv", "text/csv", "Item,Owner,Amount USD\r\nDesign,Emma Martin,3200\r\nEngineering,Bob Wilson,6400\r\nCustomer training,Carol Garcia,3200\r\n"},
			{"project-notes.txt", "text/plain", "Project Aurora\r\n本文件用于文档演示。\r\nReview: October 16\r\nTraining: October 20\r\n"},
		} {
			content := attachment.Content
			message.Attachments = append(message.Attachments, mailops.Attachment{
				Filename: attachment.Name, MIME: attachment.MIME, Size: int64(len(content)),
				Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(content)), nil },
			})
		}
	}
	if index == 8 {
		message.InReplyTo = fmt.Sprintf("<demo-%s-inbox-00@acme.test>", local)
		message.References = []string{message.InReplyTo}
	}
	return message
}

func seedMessages(ctx context.Context, svc *core.Service, stack *devstack.Stack) {
	org, err := svc.Org(ctx)
	require(err)
	mailboxes, err := svc.Mailboxes(ctx, org.ID)
	require(err)
	stateVersion, err := db.GetSetting(ctx, svc.DB, "documentation.demo.collaboration")
	require(err)
	for _, mailbox := range mailboxes {
		local, _, found := strings.Cut(mailbox.Address, "@")
		if !found {
			panic("演示邮箱地址无效")
		}
		for _, folder := range []struct {
			Name  string
			Count int
		}{{"INBOX", 24}, {"Sent", 5}, {"Drafts", 3}, {"Archive", 6}, {"Trash", 2}, {"Junk", 2}} {
			// 按时间顺序导入，使最近邮件在列表顶部显示。
			rows, err := svc.DB.QueryContext(ctx, "SELECT raw FROM documentation_demo_messages WHERE address=? AND folder=? ORDER BY ordinal DESC", mailbox.Address, folder.Name)
			require(err)
			count := 0
			for rows.Next() {
				var raw []byte
				require(rows.Scan(&raw))
				require(stack.Deliver(mailbox.Address, folder.Name, raw))
				count++
			}
			require(rows.Err())
			require(rows.Close())
			if count != folder.Count {
				panic("演示邮件存储数量不正确")
			}
		}
		if mailbox.Status != "active" {
			continue
		}
		endpoint, err := svc.ResolveEndpoint(ctx, org.ID, mailbox.ID, provider.ProtocolIMAP)
		require(err)
		conn, err := svc.Pool.GetFresh(ctx, endpoint.IMAPCredential())
		require(err)
		for _, folder := range []string{"INBOX", "Sent", "Drafts", "Archive", "Trash", "Junk"} {
			page, err := mailops.ListMessages(ctx, conn, folder, 0, 50, "")
			require(err)
			for i, message := range page.Messages {
				flags := []string{}
				if folder != "INBOX" || i%3 != 0 {
					flags = append(flags, "\\Seen")
				}
				if folder == "INBOX" && i%5 == 0 {
					flags = append(flags, "\\Flagged")
				}
				if folder == "Drafts" {
					flags = append(flags, "\\Draft")
				}
				if folder == "INBOX" && i%7 == 2 {
					flags = append(flags, "\\Answered")
				}
				if len(flags) > 0 {
					require(mailops.StoreFlags(ctx, conn, folder, []uint32{message.UID}, flags, nil))
				}
			}
		}
		svc.Pool.Put(conn)
		versions := core.CheckedVersions{
			ConnectionRevision: endpoint.ConnectionRevision, EndpointRevision: endpoint.EndpointRevision,
			CredentialID: endpoint.CredentialID, CredentialGeneration: endpoint.CredentialGeneration,
		}
		_, err = svc.DB.ExecContext(ctx, "UPDATE mailbox_endpoints SET check_status='passed',checked_at=?,checked_versions_json=?,updated_at=? WHERE mailbox_id=? AND protocol='imap'", db.Now(), jsonText(versions), db.Now(), mailbox.ID)
		require(err)
		smtp, err := svc.ResolveEndpoint(ctx, org.ID, mailbox.ID, provider.ProtocolSMTP)
		require(err)
		require(mailops.ValidateSMTP(ctx, mailops.SMTPConfig{Addr: smtp.Address(), TLSMode: smtp.Network.TLSMode, TLSConfig: smtp.TLSConfig, Dialer: smtp.Dialer, Timeout: 15 * time.Second}, smtp.Username, smtp.Secret))
		versions.EndpointRevision, versions.CredentialID, versions.CredentialGeneration = smtp.EndpointRevision, smtp.CredentialID, smtp.CredentialGeneration
		_, err = svc.DB.ExecContext(ctx, "UPDATE mailbox_endpoints SET check_status='passed',checked_at=?,checked_versions_json=?,updated_at=? WHERE mailbox_id=? AND protocol='smtp'", db.Now(), jsonText(versions), db.Now(), mailbox.ID)
		require(err)
		if mailbox.Kind == "shared" && stateVersion == "" {
			var owner, carol int64
			require(svc.DB.QueryRowContext(ctx, "SELECT m.id FROM members m JOIN roles r ON r.id=m.role_id WHERE m.org_id=? AND r.key='owner'", org.ID).Scan(&owner))
			require(svc.DB.QueryRowContext(ctx, "SELECT id FROM members WHERE org_id=? AND login_email='carol@acme.test'", org.ID).Scan(&carol))
			for i := 0; i < 8; i++ {
				key := core.MessageKey(fmt.Sprintf("<demo-%s-inbox-%02d@acme.test>", local, i), "", 0, 0)
				require(svc.Assign(ctx, mailbox.ID, owner, key, &carol))
				require(svc.AddNote(ctx, mailbox.ID, carol, key, "演示备注：已联系客户，等待确认会议时间。"))
				if i%3 == 2 {
					require(svc.SetStatus(ctx, mailbox.ID, carol, key, "resolved"))
				}
			}
		}
	}
	require(db.SetSetting(ctx, svc.DB, "documentation.demo.collaboration", "1"))
	fmt.Printf("演示邮件初始化：%d 个邮箱，每个邮箱 42 封邮件。\n", len(mailboxes))
}

func storeMessages(ctx context.Context, svc *core.Service) {
	_, err := svc.DB.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS documentation_demo_messages(address TEXT NOT NULL,folder TEXT NOT NULL,ordinal INTEGER NOT NULL,raw BLOB NOT NULL,PRIMARY KEY(address,folder,ordinal))")
	require(err)
	var count int
	require(svc.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM documentation_demo_messages").Scan(&count))
	if count > 0 {
		return
	}
	org, err := svc.Org(ctx)
	require(err)
	mailboxes, err := svc.Mailboxes(ctx, org.ID)
	require(err)
	now := time.Now().UTC()
	require(svc.DB.Tx(ctx, func(tx *sql.Tx) error {
		for _, mailbox := range mailboxes {
			local, _, found := strings.Cut(mailbox.Address, "@")
			if !found {
				return fmt.Errorf("演示邮箱地址无效")
			}
			for _, folder := range []struct {
				Name  string
				Count int
			}{{"INBOX", 24}, {"Sent", 5}, {"Drafts", 3}, {"Archive", 6}, {"Trash", 2}, {"Junk", 2}} {
				for i := 0; i < folder.Count; i++ {
					raw, err := mailops.Build(demoMessage(local, folder.Name, i, now))
					if err != nil {
						return err
					}
					if _, err := tx.ExecContext(ctx, "INSERT INTO documentation_demo_messages(address,folder,ordinal,raw) VALUES (?,?,?,?)", mailbox.Address, folder.Name, i, raw); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}))
}
