# 架构

[English](architecture.md) · **简体中文**

## 目标与范围

Mailhearth 把 Purelymail 可靠而低成本的邮件基础设施包装成一个小型组织可以
直接部署、管理和日常使用的产品。它**不**运行 SMTP 服务器，不保存邮件的主副本，
不做反垃圾，也不实现 DLP / eDiscovery / MDM。它也不是 Purelymail 后台的换皮，
管理的单位是组织里的人，而非账户上的一个「用户」。

塑造这套设计的约束条件：

- **主机很小。** 目标部署环境是最便宜的 VPS。服务端是一个静态 Go 二进制加
  SQLite，进程内有一个有上限的 IMAP 连接池，前端 gzip 后 51 KB。没有 Redis，
  没有 Postgres，运行时没有 Node，除了少量 goroutine 也没有后台 worker。
- **邮件以 Purelymail 为准。** 邮件从不复制进 Mailhearth 的数据库。客户端显示
  的一切都通过 IMAP 按需获取；数据库只保存组织模型，以及以 `Message-ID` 为键
  的少量协作数据。
- **浏览器里没有机密。** API 令牌和邮箱的应用密码加密存放在 SQLite 里。浏览器
  只和 Mailhearth 通信。

## 组件

| Package | 职责 |
|---|---|
| `cmd/mailhearth` | 入口：配置、master key、数据库、IMAP 连接池、HTTP 服务器 |
| `internal/config` | 环境变量配置 |
| `internal/db` | SQLite（modernc，无 cgo）和内嵌 migration |
| `internal/secrets` | AES-256-GCM 加密盒（由 master key 经 HKDF 派生）、argon2id、token |
| `internal/purelymail` | 类型化 API 客户端；`fake/` 是内存版 Purelymail |
| `internal/model` | 服务层和 API 共用的组织模型类型 |
| `internal/core` | 服务：初始化与导入、成员、角色、域名、邮箱、地址、群组、离职交接、团队状态 |
| `internal/mailproto/imappool` | 有上限的 IMAP 连接池与 IDLE 监听 |
| `internal/mailproto/mailops` | 文件夹、列表、渲染、操作、撰写、SMTP |
| `internal/mailproto/mimeutil` | HTML 消毒、文本与 HTML 互转、解码 |
| `internal/mailproto/sieve` | 规则模型到 Sieve 的编译器；ManageSieve 客户端 |
| `internal/httpapi` | JSON API、会话、CSRF、上传、SSE、沙箱邮件视图 |
| `internal/web` | 内嵌 SPA，带 gzip 和不可变缓存 |
| `internal/devstack` | 供开发和测试使用的模拟 Purelymail、IMAP、SMTP |
| `web/` | Preact + Vite 单页应用（邮件、管理、设置） |

## 组织模型

| 概念 | 含义 | 对应存储 |
|---|---|---|
| **Organization** | 一次部署里唯一的租户。 | `organizations` |
| **Member** | 登录 Mailhearth 的真人。有角色、状态（invited/active/disabled/departed）、职位、部门。 | `members` |
| **Role** | 一组具名权限（`members.manage`、`shared.manage` 等）。内置 owner、admin、member，允许自定义角色。 | `roles` |
| **Domain** | Purelymail 账户上的一个域名，附带 DNS 健康状态。 | `domains` ↔ Purelymail domain |
| **Mailbox** | 能登录、保存邮件的账户。`personal` 归某个成员所有，`shared` 归组织所有、由多名成员共同处理。 | `mailboxes` ↔ Purelymail user |
| **Address** | 能收到邮件的东西：邮箱自身地址（`primary`）、指向一个邮箱的 `alias`、指向任意目标的 `forward`、群组分发地址 `group`、`catchall` 或 `prefix` 规则。 | `addresses` ↔ Purelymail routing rule |
| **Identity** | 邮箱可以使用的发件地址、显示名和签名。 | `identities` |
| **Group** | 成员的集合，可选配一个分发地址，其目标跟随成员变化。 | `groups`、`group_members` |
| **Access grant** | 成员对邮箱的访问授权，级别为 `full`/`send`/`read`。 | `mailbox_access` |

一个成员可以拥有多个邮箱；一个邮箱可以有多个地址；一个地址可以通过转发或群组
送达多个成员。成员换岗或离职时，邮箱和地址仍归组织所有：所有权会被重新指派，
不会被隐式删除。

## 与 Purelymail 的对应关系

| Mailhearth 操作 | 调用的 Purelymail API |
|---|---|
| 连接账户 | `checkAccountCredit`（校验令牌） |
| 导入与同步 | `listDomains`、`listUser`、`listRoutingRules` — 只读且幂等 |
| 创建邮箱 | `createUser`（随机密码，不发欢迎邮件）+ `createAppPassword` |
| 连接已导入的邮箱 | `createAppPassword`（从不需要原有密码） |
| 轮换凭据 | `createAppPassword`，然后 `deleteAppPassword` 删掉旧的 |
| 为外部客户端重置密码 | `modifyUser{newPassword}` + 轮换 |
| 挂起 / 离职时切断访问 | `modifyUser{newPassword}` + `deleteAppPassword` |
| 别名 / 转发 / 全收 / 前缀 / 群组地址 | `createRoutingRule` / `deleteRoutingRule` |
| 邮箱转发 | 在邮箱自身地址上建路由规则（Purelymail 的语义：规则优先于投递） |
| 添加域名 / 重新检查 DNS / 域名设置 | `addDomain`、`updateDomainSettings`、`getOwnershipCode` |

Mailhearth 为每个邮箱只持有一个名为 "Mailhearth" 的应用密码。成员永远看不到它；
服务端在确认该成员拥有这个邮箱或已获授权之后，代表成员用它连接 IMAP、SMTP 和
ManageSieve。管理权限并不等于邮件访问权：读取共享邮箱始终需要显式授权。

## 邮件路径

1. `httpapi` 为已登录成员解析目标邮箱（`core.ResolveMailbox`）并取得凭据。
2. `imappool.Get` 返回一个池化连接（总数上限为 `MAILHEARTH_IMAP_MAX_CONNS`，
   每份凭据保留 2 个空闲连接，空闲 90 秒后回收）。
3. `mailops` 执行 IMAP 命令：用带 `LIST-STATUS` 的 `LIST` 取文件夹，用序号区间
   `FETCH` 取 envelope、flags 和 `BODYSTRUCTURE` 做分页，用 `UID SEARCH` 做查询，
   用 `BODY.PEEK[part]` 做渲染，在服务器支持时使用 `MOVE`/`UIDPLUS` 并保留回退路径。
4. HTML 正文经过 `mimeutil.SanitizeHTML`（bluemonday 白名单、CSS 清洗、`cid:`
   解析、远程图片拦截），在一个 `default-src 'none'` CSP 的独立文档里输出，由
   沙箱 iframe 显示。
5. 发信用 go-message 构造 RFC 5322 报文，用同一份凭据经 SMTP 提交，随后写入
   Sent 文件夹，并把原邮件标记为已回复或已转发。
6. IDLE 监听（每个邮箱加文件夹一个，所有打开的标签页共享）通过 Server-Sent
   Events 把变化推送到浏览器。

## 共享邮箱协作

协作状态以 `mid:<message-id>` 为键，因此邮件在文件夹之间移动后依然保留。
`message_state` 保存负责人和处理状态；`mail_activity` 是只追加的记录（回复、
转发、分配、备注等），既由显式的团队操作写入，也在成员用共享邮箱发信时自动写入。
邮件列表会在行上显示「谁已回复」、负责人和处理状态标记。

## 规则

成员以结构化的条件和动作编辑规则。`sieve.Compile` 把它们（连同自动回复）编译成
Sieve 脚本，只使用 Purelymail 声明支持的扩展（`fileinto imap4flags copy body
vacation`）。脚本以 `mailhearth` 为名经 ManageSieve 上传到
`mailserver.purelymail.com:4190`（STARTTLS）并激活。结构化形式保存在
`mailboxes.settings_json` 里。

## 前端

Preact 加 `@preact/signals`，一个 60 行的 history 路由，没有 UI 框架。路由为
`/mail/:mailbox/:folder/:uid`、`/admin/:section/:id`、`/settings/:tab`、
`/login`、`/invite/:token`、`/setup`。文案以英文为键，配一份 zh-CN 词典。布局在
860 px 以上是三栏邮件客户端，以下是抽屉加单栏。
