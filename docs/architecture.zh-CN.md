# 系统架构指南

[English](architecture.md) · **简体中文** · [繁體中文](architecture.zh-TW.md) · [日本語](architecture.ja.md) · [Español](architecture.es.md)

## 整体设计与模块组件

Mailhearth 采用极简高效的单体架构设计，后端使用 Go 编写，前端采用轻量级响应式框架 Preact，数据库采用嵌入式 SQLite（基于 pure-Go `modernc.org/sqlite` 驱动，完全摆脱对 cgo 的依赖）。所有前端构建产物均通过 Go `embed` 静态打包进最终的单一可执行文件中。

在系统职责划分上：
- **外部邮件服务器**：专注于邮件协议传输、投递链路、底层文件夹树及邮件正文物理存储。
- **Mailhearth 系统**：在上层持久化维护组织结构、用户与角色、凭据加密存储、资源映射关系、管理控制任务、邮件发送记录以及团队协作状态数据。

代码库核心组件职责如下：
- `cmd/mailhearth`：系统启动入口，负责配置加载、主密钥读取与校验、数据库初始化、IMAP 智能连接池初始化、后台异步工作协程组装以及 HTTP/SSE 服务器监听。
- `internal/config`、`internal/secrets`：环境参数加载与校验；基于 AES-256-GCM 与 HKDF 的多级密钥派生；argon2id 密码安全哈希计算及令牌生命周期管理。
- `internal/db`、`internal/model`：嵌入式数据库结构（单事务应用）、外键与租户范围约束、核心领域实体及持久化模型定义。
- `cmd/apitypes`、`internal/apitypes`：根据 Go 响应类型生成 `web/src/lib/api-types.ts`（按 `encoding/json` 规则进行反射，字符串联合类型通过 `tstype` 标签声明）。Web 客户端只导入这些生成的类型；已提交的文件过期时，`go test` 与 CI 会失败。
- `internal/provider`：多邮件服务商抽象层统一接口规范，以及面向 Purelymail、Migadu 和标准手动 IMAP/SMTP 协议的驱动适配器。
- `internal/purelymail`：专用于 Purelymail 管理 API 的底层传输客户端与错误处理包装。
- `internal/core`：核心业务逻辑服务，涵盖服务商连接管理、资源自动发现与导入、组织权限控制、邮箱全生命周期流转、异步持久化操作（Operation）及邮件投递管理（Submission）。
- `internal/mailproto/imappool`：具备配额上限保护的 IMAP 连接复用池、基于版本号（Revision）的主动连接失效机制以及高效的 IMAP IDLE 实时监听器。
- `internal/mailproto/mailops`、`mimeutil`、`sieve`：IMAP/SMTP 底层协议通讯；MIME 邮件内容安全消毒与附件提取；基于 RFC 规范的 Sieve 过滤脚本编译器及 ManageSieve 协议客户端。
- `internal/httpapi`、`internal/web`、`web/`：基于会话 Cookie 的鉴权中间件、RESTful JSON 接口与 Server-Sent Events (SSE) 实时推流；内嵌静态前端资产以及现代化 Web 邮箱客户端与管理控制台。

## 组织模型与资源归属

每个 Mailhearth 运行实例服务于一个独立的团队组织（`Organization`）。

1. **成员与身份（Member & Role）**：
   - 团队成员包含姓名、所属部门、关联角色及生命周期状态（`invited` 待激活、`active` 正常、`disabled` 停用、`departed` 离职）。
   - 权限体系采用具名权限点（如 `mailboxes.manage`、`domains.manage` 等）组成的精细化角色。
   - **安全原则**：管理权限与邮件阅读权限严格隔离。管理员仅负责组织配置，个人邮箱（Personal Mailbox）的所有权以及共享邮箱（Shared Mailbox）的明确授权（`full` 完全访问、`send` 可读可发、`read` 只读）是访问邮件内容的唯一凭证。

2. **邮件连接与域名绑定（MailConnection & DomainBinding）**：
   - `MailConnection` 属于组织，维护邮件服务商类型（Purelymail、Migadu 或手动）、连接名称、管理 API Token/访问密钥、适用的域名范围以及三项核心协议的默认连接模板。
   - `Domain` 代表团队的逻辑域名；`DomainBinding` 将域名绑定至具体的邮件连接，记录服务商特定参数及实时 DNS 解析健康状态（MX、SPF、DKIM、DMARC）。
   - 邮箱地址的唯一性作用域限定在所属的邮件连接内部，不同连接下配置相同的邮箱地址完全相互独立、互不干扰。

3. **地址体系与发件身份（Addresses & Identities）**：
   - 地址支持多种业务类型：主地址（`primary`）、别名（`alias`）、转发（`forward`）、邮件组（`group`）、全收兜底（`catchall`）、前缀规则（`prefix`）以及外部规则（`external_rule`）。
   - 邮箱转发独立持久化保存在 `mailbox_forwardings` 表中，资源导入时会自动关联其所属的主邮箱。
   - 发信身份（Identity）的显示名称与 SMTP 发信授权严格解耦：拥有收件别名并不默认赋予使用该别名作为发件人（From）发送邮件的权限，发件身份必须显式授予。
   - 邮件群组根据所属部门或规则动态聚合所有合格的个人邮箱，在投递前系统会自动校验连接与域名限制并消除重复收件目标。

4. **远端资源映射（ProviderResource）**：
   - 将邮件服务商的每一个远端实体对象（如远程邮箱 ID、别名 ID）唯一映射至本地实体，记录其远程状态及安全的结构化元数据。
   - 结合版本号（Revision）检查与外键依赖约束，防止非法跨越组织或连接边界，保障历史记录不可被破坏。

## 协议配置与凭据隔离

每个邮箱（Mailbox）均拥有独立的 IMAP、SMTP 及 ManageSieve 三个协议端点（Endpoint）：
- **网络模式（Network Mode）**：每个端点支持配置为 `inherit`（继承所属邮件连接的全局模板）、`override`（单独指定独立的主机与端口）或 `disabled`（明确停用该协议）。
- **凭据模式（Credential Mode）**：支持 `managed`（由平台统一根据模板自动生成或管理凭据）与 `entered`（手动录入用户已有凭据）。启用的端点各自存储加密后的密码。
- **原子化生效保障**：在修改协议配置时，系统会先在后台针对所有启用的协议进行实际认证握手测试，只有所有协议均握手成功后，才会在单个本地事务中原子化更新数据库。
- **连接平滑失效**：一旦端点配置、连接参数、凭据或访问授权版本发生变更，IMAP 连接池将立即关闭对应的存量旧连接，强制下次请求重新建立安全连接。
- **服务商预设**：
  - Purelymail：默认启用 IMAP (`imap.purelymail.com:993` TLS)、SMTP (`smtp.purelymail.com:465` TLS) 及 ManageSieve (`mailserver.purelymail.com:4190` STARTTLS)。
  - Migadu：默认启用 IMAP (`imap.migadu.com:993` TLS) 与 SMTP (`smtp.migadu.com:465` TLS)，ManageSieve 默认停用。
  - 手动连接：所有协议端点默认处于停用状态，需管理员逐项按需配置。
  - TLS 证书校验：默认遵循宿主系统的受信 CA 证书链，同时支持管理员挂载私有 CA 证书包，严格核验主机名（SNI）。

## 资源发现、导入与持久化运维操作

1. **资源发现与增量导入**：
   - 资源发现（Discovery）会完整拉取选定连接下的远端全量资源，生成带有时间有效期的快照，并与当前连接的 Revision 绑定。
   - 执行导入时，系统在单个本地事务中严格校验归属、版本号、有效期及依赖约束。导入的个人邮箱默认处于未分配状态（无 Owner 且无本地登录凭据），需管理员后续分配。
   - 同步（Sync）动作会安全更新已登记资源的观测数据，新增的远端资源将保持待导入状态；系统会完整保留本地已分配的 Owner、访问授权、协作备注、发信签名以及手动输入的凭据。
   - 转发规则导入保留原始邮箱、各目标确认状态（`active`、`pending_confirmation`、`blocked`、`unknown`）及远端引用。未经验证的转发保持 `unverified`，其中 Migadu 转发写入受严格的交付校验保护。

2. **持久化异步操作引擎（Operation Engine）**：
   - 任何涉及远端服务商或耗时较长的管理任务均通过 `Operation` 模型执行，记录唯一的 `requestId`、请求内容指纹、加密载荷、资源排他锁及详细的分步状态。
   - **严格幂等性**：相同 `requestId` 的重复提交将直接幂等返回已有操作状态；若配置版本发生改变或执行权限已被撤销，系统将主动阻止旧操作继续执行。
   - **未知状态防扩散**：向远端发起写操作若遇到网络超时或未明确响应，操作会被置为需要核查状态，已完成确认的步骤绝不重复执行。
   - **外部动作报告**：当某些操作必须由管理员在服务商控制台手动完成时，系统会保存管理员的提交报告，并清晰标记为 `systemVerified=false`；若创建凭据时响应中断且无远端 ID，必须由管理员核实清理后提交确认。
   - **离职交接流（Offboarding）**：以持久化任务跟踪邮箱移交或保留归档、群组关系清理、本地会话注销及外部凭据撤销的全流程。

## 邮件收发与实时渲染链路

1. **权限解析与连接分配**：
   - HTTP 请求进入后，中间件首先解析当前登录用户的权限、会话有效性以及针对目标邮箱的访问授权，动态确定应使用的协议端点。
   - IMAP 连接池实施三级配额控制：系统全局上限默认 24，单个邮件连接上限 8，单个邮箱上限 3。在后台 IDLE 实时监听开启时，连接池会严格预留全局 4 个、单连接 2 个插槽供前台交互请求优先使用。

2. **邮件拉取与特殊文件夹识别**：
   - `mailops` 模块通过高效的 IMAP 命令执行文件夹遍历、分页查询、服务端关键词检索以及 MIME 结构解析。
   - 系统按照「管理员显式配置映射 > 唯一的 RFC 6154 SPECIAL-USE 属性 > 唯一的标准英文/本地化文件夹名称」的严谨优先级，精准识别收件箱、草稿箱、发件箱、垃圾箱与归档文件夹。

3. **安全沙箱渲染防御**：
   - 邮件正文在服务端由 `mimeutil` 模块执行深度消毒，剥离恶意脚本、非法样式与嵌入式追踪代码，自动将内联附件重定向解析为受控的 `cid:` 资源。
   - 邮件正文渲染在受严格 Content Security Policy (CSP) 保护的无脚本独立沙箱 `<iframe>` 中，外部远程图片默认全部阻断，防止发件人刺探用户 IP 与阅读状态。

4. **双重保证的发信状态机（Submission Pipeline）**：
   - 发送请求在数据库中持久化记录：生成全局唯一且固定的 `Message-ID`、请求内容指纹及加密信封；邮件完整正文先行暂存在邮件服务器的 Drafts 草稿箱中。
   - **状态独立解耦**：SMTP 接受投递与保存发件箱副本（Sent Copy）作为两个独立步骤分别记录。若网络中断导致 SMTP 投递结果未知，系统绝不自动盲目重试；若 Sent 副本写入失败，重试仅重新执行副本保存，绝不重复提交 SMTP。草稿清理通过精确的 UID EXPUNGE 完成。

5. **实时协同与动态更新**：
   - 共享邮箱的 IMAP IDLE 监听器实时感知新邮件，并通过轻量级 Server-Sent Events (SSE) 推送至浏览器。
   - 团队协作上下文统一以 `mid:<message-id>` 标识绑定，`message_state` 记录负责人指派与解决状态，`mail_activity` 记录回复状态、内部团队备注及流转记录，即使邮件在不同文件夹之间移动，协作数据也完全不受影响。

## 邮件规则系统与前端架构

1. **Sieve 邮件过滤与自动回复**：
   - 结构化过滤规则在保存时，会根据邮件服务器通过 ManageSieve 协议声明的扩展能力，动态编译为标准的 Sieve 脚本。
   - 在部署规则前，系统会校验服务器上现存活跃脚本的哈希指纹；若需接管已有脚本，前端会弹出明确确认提示；确认后系统拉取新脚本并核验启用状态，确保零配置丢失。

2. **前端现代化单页架构（SPA）**：
   - 前端采用 Preact + `@preact/signals` 构建响应式状态流，通过轻量级 History Router 驱动 `/mail`（邮件）、`/admin`（管理）、`/settings`（设置）、`/login`（登录）、`/invite/:token`（邀请激活）及 `/setup`（初始化向导）等页面。
   - 响应式设计：屏幕宽度在 860px 以上时呈现标准的三栏工作台布局（文件夹、邮件列表、正文阅读器），在移动端或窄屏下自动切换为单栏与轻量抽屉导航。
   - **多语言体系**：以英文作为源词典，提供简体中文（zh-CN）、繁体中文（zh-TW）、日文（ja）和西班牙文（es）四套词典。测试框架通过 TypeScript 抽象语法树（AST）自动化核验词条覆盖率与插值参数的一致性。
