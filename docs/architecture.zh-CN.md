# 架构

[English](architecture.md) · **简体中文** · [繁體中文](architecture.zh-TW.md) · [日本語](architecture.ja.md) · [Español](architecture.es.md)

## 部署与组件

Mailhearth 使用单个 Go 二进制、内嵌 Preact 资源和 SQLite（modernc，无 cgo）。
邮件服务器保存正文并负责投递和过滤。SQLite 保存组织数据、加密凭据、资源关联、
管理操作、发送请求和协作数据。Node 用于前端构建。

- `cmd/mailhearth`：配置、主密钥、数据库、IMAP 连接池、执行器及 HTTP server。
- `internal/config`、`internal/secrets`：环境配置、AES-256-GCM/HKDF、argon2id 和 token。
- `internal/db`、`internal/model`：内嵌 migration、关联检查及持久化模型。
- `internal/provider`：公共管理接口及 Purelymail、Migadu、manual 适配器。
- `internal/purelymail`：供对应适配器使用的 Purelymail API 客户端。
- `internal/core`：连接、发现与导入、组织、资源生命周期、Operation 和 Submission。
- `internal/mailproto/imappool`：连接额度、endpoint 版本失效处理及 IDLE 监听。
- `internal/mailproto/mailops`、`mimeutil`、`sieve`：IMAP/SMTP、MIME 消毒、Sieve 编译和 ManageSieve。
- `internal/httpapi`、`internal/web`、`web/`：权限检查、JSON/SSE、内嵌资源及邮件、管理、设置界面。

## 组织与资源归属

每次部署有一个 Organization。Member 保存角色、部门和 `invited`、`active`、
`disabled`、`departed` 状态。Role 包含具名权限。personal Mailbox 的所有权和 shared
Mailbox 的明确授权（`full`、`send`、`read`）决定邮件访问；管理权限不授予邮件访问。

MailConnection 属于组织，保存服务商类型、名称、管理 API 认证、域名范围和三项协议
默认模板。Domain 表示逻辑域名；DomainBinding 保存连接关联、服务商设置和 DNS 状态。
Mailbox 地址在所属连接内唯一，不同连接的相同地址分别保存。

Address 包括 `primary`、`alias`、`forward`、`group`、`catchall`、`prefix` 和外部管理的
`external_rule`。邮箱转发独立保存在 `mailbox_forwardings`，导入保留 primary 地址。
Identity 显示设置和 SMTP 发件授权分别处理；收件别名不授予对应 From 的发送权限。
Group 计算全部符合条件的 personal 邮箱，检查连接与域名限制后消除重复目标。

ProviderResource 将每个远程引用关联到一个本地对象及用途，保存归属、远程状态和
安全的结构化观察结果。引用约束与 revision 检查保护连接、组织范围和历史记录。

## 协议配置

每个 Mailbox 有独立的 IMAP、SMTP 和 ManageSieve endpoint。network mode 为
`inherit`、`override` 或 `disabled`；启用的 endpoint 分别指定 username 和加密
Credential。凭据使用 `managed` 或明确输入方式。候选配置通过全部启用协议的认证
之后才能在一个事务内提交。endpoint、连接、凭据和访问版本变化会关闭旧连接。

Purelymail 默认 IMAP 为 `imap.purelymail.com:993` TLS，SMTP 为
`smtp.purelymail.com:465` TLS，ManageSieve 为 `mailserver.purelymail.com:4190` STARTTLS。
Migadu 默认 IMAP 为 `imap.migadu.com:993` TLS，SMTP 为 `smtp.migadu.com:465` TLS，
ManageSieve 停用。手动连接默认全部停用，需要明确配置。TLS 使用系统证书或明确指定
的私有 CA 证书集合，并验证 hostname。

## 发现、导入与管理操作

发现完整读取所选范围，生成具有期限和连接 revision 的快照。导入在一个本地事务内
检查归属、版本、期限和依赖。导入的 personal 邮箱没有 owner 和登录凭据。同步更新
已登记资源的观察结果；新增资源等待选择导入。owner、访问授权、协作数据、签名和
entered 凭据保留。

转发导入保存来源邮箱、所选目标、远程引用和 `active`、`pending_confirmation`、
`blocked`、`unknown` 确认状态。观察目标及投递方式与期望设置分别保存。未经验证的
方式保持 `unverified`；API 中存在资源及管理员报告均不证明实际投递。Migadu 转发
写入继续要求 V03 验证。

Operation 保存 requestId、内容摘要、加密载荷、资源占用和独立步骤。重复提交返回
原操作；版本变化和权限撤销阻止旧配置执行。未知远程写入需要核查，已确认步骤保持
完成。外部操作保存管理员报告及 `systemVerified=false`。凭据创建响应丢失且没有远程
ID 时需要明确的清理报告。暂停立即撤销本地访问；归档保留历史。离职分别记录移交、
群组处理和凭据撤销。

## 邮件与发送流程

1. HTTP 检查当前所有权或授权，解析对应协议 endpoint。
2. IMAP 连接默认总上限 24、每个连接 8、每个邮箱 3。IDLE 监听运行时为普通请求保留
   总计 4 个、每个连接 2 个名额。
3. `mailops` 通过 IMAP 读取文件夹、分页、搜索和 MIME 内容。明确映射、唯一
   SPECIAL-USE 和唯一名称识别用于确定特殊文件夹。
4. `mimeutil` 清理 HTML/CSS、解析 `cid:`、拦截远程图片。独立文档使用限制性 CSP，
   在无脚本的沙箱 iframe 中显示。
5. Submission 保存 requestId、固定 Message-ID、内容摘要和加密 envelope；正文保存
   在服务器 Drafts。SMTP accepted 和 Sent 副本分别记录。未知发送结果不会自动重试。
   重试副本保留 SMTP 状态；草稿清理要求 UID EXPUNGE。
6. 共享 IDLE 监听通过 SSE 通知浏览器。协作使用 `mid:<message-id>`，`message_state`
   保存负责人和状态，`mail_activity` 保存回复、转发、分配及备注，移动文件夹后保留。

## 规则与前端

结构化规则根据服务器声明的扩展编译为 Sieve。ManageSieve 使用邮箱 endpoint 和
go-managesieve。启用前检查当前脚本 hash，接管需要确认，读取独立候选脚本并核验
启用结果，随后更新本地设置。已有脚本保留。规则及自动回复能力分别依赖启用协议
和所需扩展。

Preact、`@preact/signals` 和 history router 提供 `/mail`、`/admin`、`/settings`、
`/login`、`/invite/:token`、`/setup`。860 px 以上使用三栏，以下使用抽屉和单栏。
English 源文案提供 zh-CN、zh-TW、日本語和 Español 词典。TypeScript AST 检查翻译
覆盖与插值参数。生产构建保留已有 hash 资源供已打开的客户端使用。

本地数据库、HTTP 和构建检查见[集成测试](integration-testing.zh-CN.md)。真实服务商
验收尚未完成。
