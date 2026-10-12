# 项目文档截图索引与说明

本目录归档 Mailhearth 官方技术文档与 README 中使用的界面功能截图，分为两个语言子目录：
- `zh-CN/`：简体中文界面截图；
- `en/`：英文界面截图。

文件命名采用统一的结构化规则：`{序号}-{模块}-{功能}.png`。以下截图均捕获自内置的本地文档演示环境（Documentation Demo），忠实呈现了 Web 客户端与管理工作台的布局排版与视觉设计。

## 核心功能界面截图一览

| 界面模块 | 说明与重点展示 | 简体中文版 | 英文版 |
|---|---|:---:|:---:|
| **邮件收件箱** | 系统文件夹导航、分页邮件列表、星标与未读标识、多语言界面展示 | [查看截图](zh-CN/05-mail-inbox.png) | [查看截图](en/05-mail-inbox.png) |
| **邮件阅读器** | 纯净安全的沙箱渲染正文、附件预览列表、内部回复状态与快捷操作栏 | [查看截图](zh-CN/06-mail-read.png) | [查看截图](en/06-mail-read.png) |
| **管理概览看板** | 组织基础信息、各邮件连接的在线状态、配额与余额监控、快速入口 | [查看截图](zh-CN/09-admin-overview.png) | [查看截图](en/09-admin-overview.png) |
| **团队成员管理** | 成员名册、所属部门、角色标签、生命周期状态及邀请链接管理 | [查看截图](zh-CN/10-admin-members.png) | [查看截图](en/10-admin-members.png) |
| **邮件连接配置** | Purelymail、Migadu 与手动 IMAP/SMTP 连接接入、协议端点健康度 | [查看截图](zh-CN/12-admin-connections.png) | [查看截图](en/12-admin-connections.png) |
| **远端资源导入** | 远端域名、邮箱与转发规则的自动发现、只读安全呈现与勾选导入 | [查看截图](zh-CN/13-admin-resource-import.png) | [查看截图](en/13-admin-resource-import.png) |
| **邮箱资产列表** | 个人专属邮箱与团队共享邮箱资产、所属连接、负责人与配额使用情况 | [查看截图](zh-CN/14-admin-mailboxes.png) | [查看截图](en/14-admin-mailboxes.png) |
| **自定义域名管理** | 团队绑定域名、实时 DNS 健康检查状态（MX、SPF、DKIM、DMARC） | 待补充 | [查看截图](en/15-admin-domains.png) |

## 说明与本地复现

1. **演示数据性质**：上述截图中展示的账户姓名、组织名称、邮件主题和 DNS 状态均为本地演示数据，不代表任何外部线上邮件系统的实际验证状态；
2. **如何本地重新捕获**：如需在本地启动包含上述演示数据的 Web 预览环境，请参阅[文档演示环境指南](../documentation-demo.zh-CN.md)。
