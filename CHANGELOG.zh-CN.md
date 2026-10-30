# 版本更新日志

[English](CHANGELOG.md) · **简体中文** · [繁體中文](CHANGELOG.zh-TW.md) · [日本語](CHANGELOG.ja.md) · [Español](CHANGELOG.es.md)

本文件详实记录 Mailhearth 的所有重要版本演进与功能变更。

版本格式严格遵从 [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) 规范，版本号命名遵循[语义化版本 2.0.0](https://semver.org/spec/v2.0.0.html)。

## [未发布]

### 变更（破坏性）— 计划作为 0.2.0 发布

Mailhearth 尚未部署给真实用户，因此本版本放弃对早期数据库、配置及客户端的全部兼容。

- **数据库**：数据库结构合并为单个 `0001_init.sql`，在一个事务中应用（版本记录在 `PRAGMA user_version` 中）。早期构建创建的数据库会在启动时被拒绝，请使用空的数据目录启动。移除了不再使用的旧表和旧字段；邮件历史（`message_state`、`mail_activity`）现在通过 `ON DELETE RESTRICT` 外键而非触发器阻止删除邮箱；并补齐了缺失的外键、`CHECK` 约束与索引。
- **配置**：移除了 `MAILHEARTH_PURELYMAIL_API_URL`、`MAILHEARTH_IMAP_ADDR`、`MAILHEARTH_IMAP_TLS`、`MAILHEARTH_SMTP_ADDR`、`MAILHEARTH_SMTP_TLS`、`MAILHEARTH_SIEVE_ADDR` 和 `MAILHEARTH_SIEVE_TLS`。新的安全默认值：始终需要初始化令牌（未设置 `MAILHEARTH_SETUP_TOKEN` 时自动生成并写入日志）；除非设置 `MAILHEARTH_SECURE_COOKIES=false`，否则启用 `Secure` Cookie；`MAILHEARTH_UPLOAD_QUOTA_MB` 默认为 120 且始终强制执行；会话默认滑动有效期 14 天、绝对有效期 30 天。
- **HTTP API**：移除了单连接与域名写入端点的 `410 api_replaced` 占位路由；`DELETE /api/mail/uploads/{id}` 对不属于调用者的 id 返回 404；`/api/setup/status` 移除了 `setupTokenRequired`；`Domain` 不再包含 `isShared`、`allowAccountReset`、`symbolicSubaddressing`、`dns`、`dnsCheckedAt` 和 `status`。
- **Web 客户端**：升级至 Vite 8、Preact 11 与 TypeScript 7；API 类型由 Go 响应类型生成。
- **发布**：镜像只针对 `vX.Y.Z` 标签发布（`X.Y.Z` 与 `X.Y`），不再提供 `latest`；`docker-compose.yml` 固定使用 `0.2` 发布线。漏洞扫描（govulncheck、npm audit）与 staticcheck 在 CI 中为阻断项。

### 新增与优化

- **五国语言本地化支持**：新增繁体中文（台湾）、日文及西班牙文的前台与管理界面完整翻译，可在登录页及个人设置页自由切换，界面词条与模板参数通过 TypeScript AST 静态测试 100% 覆盖。
- **多服务商架构扩展**：新增对 Migadu 服务商与通用手动 IMAP/SMTP 邮件服务器的连接接入能力，支持按连接独立配置管理凭据与各协议端点模板。
- **外部凭据清理与核查工作流**：针对外部服务商网络响应超时或创建凭据中断的场景，新增离线清理确认机制与管理员报告追踪，杜绝悬挂孤儿任务。
- **全套多语言文档重写**：系统架构、运维手册、安全规范、集成测试及项目说明文档全面人性化重构，五种语言实质内容高度对齐。

## [0.1.0] - 2026-09-20

首个正式公开版本：专为小团队量身打造的自托管商务邮箱平台，将邮件服务商账户平滑转化为现代化的团队协同邮箱系统。

### 新增功能特性

- **首次运行与极简引导向导**：快速创建组织管理员，校验邮件服务商 API 令牌，一键探索并只读导入远端已有域名、邮箱与转发路由规则，全程不破坏远端服务商原有配置。
- **以人为中心的团队组织模型**：成员支持头衔、部门、角色以及生命周期状态（已邀请、正常活跃、已停用、已离职）；内置所有者、管理员、普通成员角色，并支持基于具名权限点的自定义角色；所有敏感操作留存不可篡改的审计日志，支持平滑转让组织所有权。
- **邮箱全生命周期管理**：支持个人专属邮箱与团队共享邮箱，细粒度分配「完全访问」、「可读可发」与「只读」权限；服务端统一为邮箱安全生成与轮换应用密码；支持一步式离职交接流（Offboarding）——信箱移交继承人、转为共享、归档保留、新邮件转发、立即注销全部在线会话及凭据安全撤销。
- **灵活的邮件地址拓扑**：原生支持主地址、别名（Alias）、邮件转发（Forwarding）、全收兜底（Catch-all）与前缀地址（Prefix），以及自动随部门或团队成员变动而动态同步的邮件分发群组。
- **域名管理与 DNS 健康监测**：支持多自定义域名绑定，可视化实时监测 MX、SPF、DKIM 及 DMARC 的 DNS 解析健康状态，并提供标准 DNS 记录值一键复制功能。
- **现代响应式 Web 邮箱客户端**：标准与自定义文件夹、分页浏览、服务端极速全文检索、星标重点、批量归档删除、附件预览与分块下载、草稿实时自动保存、富文本编辑与个性化签名、多发信身份自由切换、基于 IMAP IDLE 的实时桌面推送、全屏与快捷键操作、浅色与深色主题自由切换。
- **共享邮箱团队协同作业**：邮件处理状态一目了然——实时呈现哪位队友已回复、邮件指派到人、协同标记解决状态，以及添加仅团队内部可见的私密备注；协同状态统一锚定于固定 `Message-ID`，邮件在文件夹之间移动后协作数据永久保留。
- **服务端过滤规则与自动回复**：支持通过可视化条件与动作设计邮件过滤规则，支持休假自动回复；规则自动编译为符合 RFC 规范的 Sieve 脚本，并通过 ManageSieve 协议部署至服务器端，即使所有成员离线也持续生效；接管已有脚本前提供确认提示。
- **深度纵深安全防御**：所有服务商 API 凭据与协议密码均采用 AES-256-GCM 高强度静态加密，密钥由服务器端 master.key 安全派生；基于 argon2id 的密码强哈希、严格的 CSRF 拦截与防爆破限流；邮件正文在服务端经过白名单深度消毒，渲染于严格 CSP 下的绝对无脚本沙箱 iframe 中，默认拦截外部追踪图片；附件强制以安全下载模式投递。
- **极简单体部署体验**：内嵌全部前端静态资源的独立 Go 二进制单文件，基于纯 Go 驱动 `modernc.org/sqlite` 的嵌入式 SQLite 存储；配套轻量级 `Dockerfile` 与 `docker-compose.yml`，以及自动化 CI 流水线。
- **完备的开发与测试套件**：内置进程内模拟邮件服务商与演示数据环境（`make dev`）；自动化浏览器截图测试脚本；以及面向真实邮件服务商的端到端集成测试套件。

[未发布]: https://github.com/yll682/Mailhearth/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/yll682/Mailhearth/releases/tag/v0.1.0
