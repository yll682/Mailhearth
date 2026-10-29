# 系统运维与生产部署指南

[English](operations.md) · **简体中文** · [繁體中文](operations.zh-TW.md) · [日本語](operations.ja.md) · [Español](operations.es.md)

## 容量规划与资源占用

Mailhearth 专为极低资源消耗的轻量级主机设计，能够在市面上规格最小的 VPS 上稳定运行。

| 资源维度 | 典型占用 | 运维说明 |
|---|---|---|
| 可执行二进制 | 约 25 MB 静态文件 | 零 cgo 依赖，无外部动态库与环境运行时依赖 |
| 空载常驻内存 (RSS) | 25–40 MB | 默认启用 Go 内存上限 `GOMEMLIMIT=160MiB`，防止突发峰值 |
| 繁忙常驻内存 (10 人并发) | 60–120 MB | 内存主要被 IMAP 批次数据抓取与 MIME 解析缓冲占用 |
| CPU 算力消耗 | 日常几乎为 0% | 最密集的计算发生在用户登录时的 argon2id 密码哈希推导 |
| 磁盘存储开销 | 仅随团队组织元数据增长 | SQLite 保存组织、权限、操作记录与团队协作备注；邮件内容永久保留在服务商端 |
| Web 前端资源体积 | gzip 后约 100 KB | 包含完整 Webmail 与管理后台，文件名自带内容指纹哈希，支持长久缓存 |

### IMAP 连接池与并发调优

通过环境变量 `MAILHEARTH_IMAP_MAX_CONNS`（系统默认值为 24）调优全局并发连接池。建议配置公式：`预估同时在线人数 × 2 + 团队常驻打开的邮箱总数`。

系统内部实施精细的分级配额保护：
- **全局连接池上限**：默认 24 个连接；
- **单服务商连接上限**：每个邮件连接最多占用 8 个并发连接；
- **单邮箱上限**：每个邮箱最多复用 3 个连接；
- **IDLE 监听保障**：后台 IMAP IDLE 实时监听器占用独立配额，在 IDLE 运行时始终为前台普通交互请求保留至少 4 个全局插槽及单连接 2 个插槽。

> **提示**：调优时请务必核对上游邮件服务商（如 Migadu 或 Purelymail）对单个 IP 或单个账户设定的最大并发连接数限制。

## 配置项参考

所有配置项均通过环境变量设置。取值格式错误或超出范围时，系统会在启动时报错并指明对应变量；被自动调整的取值会以警告记录，启动时还会输出生效配置（不含机密）。

| 变量 | 默认值 | 用途 |
| --- | --- | --- |
| `MAILHEARTH_BASE_URL` | 空 | 对外公开 URL，用于生成邀请链接。使用 `https://` 地址时会启用 `Secure` Cookie。 |
| `MAILHEARTH_LISTEN` | `:8080` | HTTP 监听地址。 |
| `MAILHEARTH_DATA_DIR` | `./data`（容器内为 `/data`） | SQLite 数据库、主密钥、上传文件与迁移前备份。 |
| `MAILHEARTH_MASTER_KEY` | 自动生成到 `<data>/master.key` | 32 字节密钥（base64 或 hex），用于加密保存的凭据。 |
| `MAILHEARTH_SETUP_TOKEN` | 空 | 设置后，在全新实例上创建组织时必须提供该令牌，避免抢先访问实例的人占用所有者身份。若实例在完成初始化前即可被访问，建议设置。 |
| `MAILHEARTH_LOG_LEVEL` | `info` | `debug`、`info`、`warn` 或 `error`。 |
| `MAILHEARTH_TRUST_PROXY` | `false` | 从受信代理读取 `X-Forwarded-For` / `X-Forwarded-Host`。 |
| `MAILHEARTH_TRUSTED_PROXIES` | 本机回环与私有网段 | 反向代理的 CIDR 或地址，以逗号分隔。来自其他对端的转发标头会被忽略。 |
| `MAILHEARTH_SECURE_COOKIES` | `MAILHEARTH_BASE_URL` 为 `https://` 时为 `true` | 强制开启或关闭 Cookie 的 `Secure` 属性。 |
| `MAILHEARTH_ALLOWED_MAIL_NETWORKS` | 空 | 允许邮件服务器所在的 CIDR。除非列在此处，否则系统会拒绝连接私有、回环与链路本地地址；IMAP/SMTP/ManageSieve 服务器位于内网时需要设置。 |
| `MAILHEARTH_CA_BUNDLES_FILE` | 空 | 将 CA 证书包编号映射到 PEM 文件的 JSON 文件，例如 `{"1": "/etc/mailhearth/ca.pem"}`。协议端点通过编号引用以信任私有 CA。 |
| `MAILHEARTH_IMAP_MAX_CONNS` | `24` | IMAP 连接上限。连接池支持 4 到 24，超出范围的取值会被调整并记录警告。 |
| `MAILHEARTH_IMAP_IDLE_SECONDS` | `90` | 空闲 IMAP 连接在多少秒后关闭（10 到 3600）。 |
| `MAILHEARTH_MAX_UPLOAD_MB` | `25` | 单个附件上传的最大大小。 |
| `MAILHEARTH_MAX_MESSAGE_MB` | `40` | 编码后邮件的最大大小。附件在发送编码后会增大约三分之一。 |
| `MAILHEARTH_UPLOAD_QUOTA_MB` | 3 × `MAILHEARTH_MAX_MESSAGE_MB` | 单个成员未发送上传文件的总大小。 |
| `MAILHEARTH_SESSION_DAYS` | `30` | 会话滑动过期时间。 |
| `MAILHEARTH_SESSION_MAX_DAYS` | `90` | 会话绝对有效期，不得短于 `MAILHEARTH_SESSION_DAYS`。 |
| `MAILHEARTH_INVITE_DAYS` | `7` | 邀请链接有效天数（1 到 90）。 |
| `MAILHEARTH_DEV_STACK` | `false` | 仅用于开发：进程内模拟服务商、IMAP 与 SMTP。 |
| `GOMEMLIMIT` | 未设置时为 `160MiB` | Go 堆内存上限。 |

`MAILHEARTH_PURELYMAIL_API_URL`、`MAILHEARTH_IMAP_ADDR`、`MAILHEARTH_IMAP_TLS`、`MAILHEARTH_SMTP_ADDR`、`MAILHEARTH_SMTP_TLS`、`MAILHEARTH_SIEVE_ADDR` 与 `MAILHEARTH_SIEVE_TLS` 只会在升级“邮件连接”功能出现之前创建的安装时读取一次，并被转换为其第一个连接。除此之外，连接与协议端点均在管理后台中配置。

## 数据备份与灾难恢复

Mailhearth 的全量运行时状态均保存在持久化挂载的数据目录（默认为 `/data`）中：

| 文件 / 路径 | 作用与说明 | 备份要求 |
|---|---|---|
| `/data/mailhearth.db` | 系统核心 SQLite 数据库（运行于 WAL 预写日志模式） | 核心数据 |
| `/data/mailhearth.db-wal` | 预写日志缓存，包含最新写入事务 | 必须与 `.db` 同时备份 |
| `/data/master.key` | 主加密密钥，用于加解密保存在数据库中的 API 凭据与协议密码 | 核心机密，需异地安全备份 |
| `/data/uploads/` | 邮件撰写器中临时暂存的待发送附件 | 临时文件，重启后可自动重建 |

### 在线热备份方案
推荐使用 SQLite 内建的在线安全备份机制，无需停机即可生成事务一致性镜像：
```bash
docker compose exec mailhearth sqlite3 /data/mailhearth.db ".backup /data/backup_$(date +%Y%m%d_%H%M%S).db"
```

### 冷备份方案
停止容器后直接打包数据目录：
```bash
docker compose stop
tar -czvf mailhearth-backup-$(date +%Y%m%d).tar.gz /data/mailhearth.db /data/mailhearth.db-wal /data/master.key
docker compose start
```

### 异地灾难恢复流程
在全新的服务器上恢复运行仅需三步：
1. 准备好目标服务器的 Docker 环境；
2. 将备份的 `mailhearth.db`（或热备生成的 `.db` 文件重命名为 `mailhearth.db`）与 `master.key` 放置在新服务器的数据目录中；
3. 启动 `docker compose up -d`，系统启动时将自动识别数据、验证主密钥并平滑接管运行。

## 系统升级与数据迁移

Mailhearth 内置了声明式的数据库迁移引擎（`internal/db/migrations`）。

1. **自动迁移机制**：当启动新版本二进制时，系统会自动探测数据库架构版本，按序执行所需的表结构演进、外键关系重构与数据完整性校验。修改已有数据库之前，系统会在同一目录写入一份副本，命名为 `mailhearth.db.pre-<迁移名>-<时间戳>.bak`。
2. **升级前准备**：在拉取新版本镜像之前，务必按上述步骤先完成一次完整的数据库热备份与 `master.key` 备份。
3. **执行升级**：
   ```bash
   git pull
   docker compose pull
   docker compose up -d --build
   ```
4. **版本回滚说明**：Mailhearth 的数据库迁移仅向前兼容。若升级后遇到不可预期的异常需要回滚旧版本，**不支持跨迁移版本直接降级运行**；请切回旧版本镜像，并还原升级前保存的数据库备份。也可以直接还原自动生成的 `.bak` 副本；确认升级无误后可删除旧的 `.bak` 副本。

## 反向代理与 TLS 配置

生产环境中，Mailhearth 前端应部署反向代理（如 Caddy 或 Nginx）负责公开域名访问与 TLS 证书自动化终结。

### Caddy（强烈推荐）
Caddy 能够全自动申请与续期 Let's Encrypt 证书，配置简洁：
```caddyfile
mail.example.com {
    reverse_proxy 127.0.0.1:8080 {
        # 必须禁用缓冲以确保 Server-Sent Events (SSE) 即时推播畅通
        flush_interval -1
    }
}
```

### Nginx
若使用 Nginx，必须特别注意关闭 SSE 推流缓冲并配置合理的超时时间：
```nginx
server {
    listen 443 ssl http2;
    server_name mail.example.com;

    ssl_certificate /path/to/fullchain.pem;
    ssl_certificate_key /path/to/privkey.pem;

    # 允许上传较大附件
    client_max_body_size 50m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # 针对实时推送接口禁用缓冲
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

并在 `.env` 中设置 `MAILHEARTH_TRUST_PROXY=true`，以确保审计日志与速率限制使用真实的访问端 IP。若反向代理并非从本机回环或私有网段连接，请将其地址加入 `MAILHEARTH_TRUSTED_PROXIES`。

## 常见问题排查与应急处置

### 1. 服务商管理连接报错：`provider_auth_failed`
- **原因**：服务商 API 令牌失效、已被重置或权限范围受限。
- **处理**：登录进入「管理控制台 → 邮件连接」，点击编辑并更新该连接的管理 API 令牌。修改未提交或测试不通过前，系统将安全保留当前原有配置，不会破坏运行。

### 2. 邮箱显示「协议尚未配置」
- **原因**：初次导入资源仅拉取了远端元数据，并未自动注入登录密码。
- **处理**：进入该邮箱的详情页面，分别配置 IMAP、SMTP 及 ManageSieve 的协议参数与凭据。系统在保存前会执行真实的连接握手测试，只有握手成功的配置才会被原子化保存。若该服务商无需发信或不支持 Sieve，可直接停用相应协议。

### 3. 邮箱协议连接报错：`mailbox_auth_failed`
- **原因**：对应协议的用户名、邮箱密码或应用专用密码填写有误，或服务商侧已修改密码。
- **处理**：若为手动输入模式（`entered`），在邮箱协议配置弹窗中直接录入新密码；若为托管凭据（`managed`），在管理控制台发起凭据轮换操作。

### 4. 邮件过滤规则无法保存
- **原因**：邮箱的 ManageSieve 协议端点未配置、TLS 握手失败，或服务器缺少关键 Sieve 扩展。
- **处理**：
  - Purelymail 默认应为 `mailserver.purelymail.com:4190` 并开启 STARTTLS；
  - Migadu 的 ManageSieve 模板默认停用，若需使用请手动开启并填写正确的 Migadu 凭据；
  - 若远端服务器已存在活跃的过滤脚本，系统为保护已有逻辑会弹出确认对话框，请明确确认接管；
  - 若保存假期自动回复，请确认服务器支持 `vacation` 扩展指令。

### 5. 异步操作任务（Operation）显示为 `unknown`
- **原因**：在向远端邮件服务商写入数据时遭遇网络断连或响应超时，无法确定远端是否已成功创建资源。
- **处理**：
  - 点击查看该操作的详细分步日志，系统会保留资源排他锁防止状态进一步错乱；
  - 若创建凭据中断且未返回远端凭据 ID，请登录邮件服务商的官方网页控制台，检查并删除可能残留的应用密码；
  - 清理完毕后，在 Mailhearth 界面中点击「提交凭据清理报告」，系统将安全取消该挂起任务，状态记录为 `external_reported`，`systemVerified=false`，同时完整保留先前已就绪的其他资源。

### 6. 发件状态显示为 `sent_copy_failed`
- **原因**：邮件已成功被底层 SMTP 服务器接收并开始投递，但在将邮件写入发件箱（Sent 文件夹）时 IMAP 发生网络抖动。
- **处理**：无需担心重复发信给收件人！系统设计了严格的状态解耦机制，直接点击「重新保存副本」即可，系统仅重新执行 IMAP 追加保存，绝不会重新向收件人发起 SMTP 投递。

### 7. 页面收信没有实时推播，必须手动刷新
- **原因**：反向代理服务器缓冲了 Server-Sent Events (SSE) 数据流。
- **处理**：请检查前文的反向代理配置，确保 Caddy 启用了 `flush_interval -1`，或 Nginx 针对 `/api/mail/` 接口关闭了 `proxy_buffering`。

### 8. 超大文件夹首次加载较慢
- **说明**：Mailhearth 在打开文件夹时执行单次区间 FETCH 批量读取 50 封邮件的摘要元数据与 MIME 结构。在 Purelymail 环境中，系统默认会为新建邮箱开启服务端全文搜索索引以保障长久极速体验。

## 系统日志与可观测性

Mailhearth 输出标准结构化文本日志至 `stderr`，可直接通过容器日志系统收集：
```bash
docker compose logs -f --tail=100 mailhearth
```

- 默认日志级别为 `info`；
- 若需排查网络握手与 IMAP 监听断线重连详情，可在 `.env` 中设置 `MAILHEARTH_LOG_LEVEL=debug`；
- 每个 API 响应都带有 `X-Request-ID` 标头，错误日志中的 `requestId` 与之相同；服务端错误记录为 `error` 级别，其他客户端错误记录为 `info` 级别；
- **安全保障**：所有日志输出均经过安全脱敏，绝不输出任何用户明文密码、API 密钥或解密凭据。

## 常用测试与自检命令

```powershell
# 运行全部基准测试
go test ./... -run '^$'

# 运行多服务商模型与核心业务测试
go test -count=1 ./internal/db ./internal/core ./internal/httpapi ./internal/mailproto/sieve ./internal/provider -run '^TestMultiProvider'

# 代码静态质量检查
go vet ./...

# 前端自动化测试与生产打包校验
npm --prefix web run test
npm --prefix web run build
```

如需对真实环境进行自动化集成测试，请关闭 `MAILHEARTH_DEV_STACK`，在 `data/integration/multi-provider/` 中按规范提供真实凭据并配置 `MAILHEARTH_MULTIPROVIDER_TEST_CONFIG`，随后执行 `go test -count=1 -v ./internal/integration/multiprovider`。
