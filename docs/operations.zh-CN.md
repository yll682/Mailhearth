# 运维

[English](operations.md) · **简体中文** · [繁體中文](operations.zh-TW.md) · [日本語](operations.ja.md) · [Español](operations.es.md)

## 容量规划

Mailhearth 是为能买到的最小主机设计的。

| 资源 | 典型值 | 说明 |
|---|---|---|
| 二进制 | 约 25 MB 静态 | 无 cgo，无运行时依赖 |
| 空载 RSS | 25–40 MB | `GOMEMLIMIT` 默认 160 MiB |
| 繁忙 RSS（10 人） | 60–120 MB | 主要来自 IMAP 抓取缓冲 |
| CPU | 可忽略 | 登录时的 argon2id 是最重的一步 |
| 磁盘 | 按组织数据增长 | SQLite 保存组织、操作、发送状态和协作数据；邮件保存在所属 IMAP 服务 |
| 前端 | gzip 后约 100 KB | 2026-10-09 的 JS 与 CSS 构建结果；文件名包含内容摘要 |

把 `MAILHEARTH_IMAP_MAX_CONNS`（默认 24）调到大约「并发使用人数 × 2 加上大家
同时打开的邮箱数量」。全局默认上限为 24，每个连接上限为 8，每个邮箱上限为 3。
IDLE 使用独立额度，保留普通请求的连接额度。按照各服务商实际限制安排连接数量。

## 备份

全部状态就是数据目录：

| 路径 | 内容 |
|---|---|
| `/data/mailhearth.db` | SQLite 数据库（WAL 模式） |
| `/data/mailhearth.db-wal` | 预写日志，需要和数据库一起复制 |
| `/data/master.key` | 加密已保存凭据的密钥 |
| `/data/uploads/` | 等待发送的撰写附件（临时） |

停止容器后备份，或者用 `sqlite3 mailhearth.db ".backup out.db"` 做在线一致性
复制。把 `master.key` 保存在另一个安全位置。在新主机上恢复时，放好这两个文件，
启动二进制即可。

## 升级

migration 在启动时自动执行，包括表结构转换、关联检查和约束检查。升级前保存数据库
和 master key 的一致备份。拉取新镜像后执行
`docker compose up -d`。跨 migration 降级不受支持，需要恢复备份。

## 反向代理

Caddy：

```caddyfile
mail.example.com {
    reverse_proxy 127.0.0.1:8080 {
        flush_interval -1      # required for Server-Sent Events
    }
}
```

nginx：在 `/api/mail/` 上设置 `proxy_buffering off;` 和
`proxy_read_timeout 3600s;`，避免 SSE 流被缓冲；附件需要
`client_max_body_size 50m`。

## 故障排查

**`provider_auth_failed`** — 查看错误所属连接，在管理 → 连接中更新该连接的管理认证。
候选认证失败时保留当前可用配置。管理 API 与邮件协议认证分别检查。

**邮箱协议未配置** — 导入只登记选中的资源。为邮箱明确配置 IMAP、SMTP、ManageSieve
和 entered 凭据，或者使用该连接已提供的 managed 凭据接入能力。每个启用协议完成
实际认证检查后才保存配置。SMTP 或 ManageSieve 可以分别停用。

**`mailbox_auth_failed`** — 检查错误所属协议的用户名和密码。entered 凭据通过协议
配置页面更新；managed 凭据通过轮换操作更新。网络中断、临时拒绝和未明确分类的
ManageSieve 响应保留未确认状态。旧凭据的远程撤销需要使用新的连接核查。

**规则无法保存** — 检查邮箱的 ManageSieve endpoint、TLS 和服务器扩展。
Purelymail 默认使用 `mailserver.purelymail.com:4190` 和 STARTTLS；Migadu 的
ManageSieve 模板默认停用，启用时填写实际账户配置。已有其他 active script 时需要
明确接管，并提交当前脚本的内容摘要。自动回复还需要 `vacation` 扩展。

**Operation 为 `unknown`** — 查询步骤记录并执行核查。保留资源占用，确认远程结果后
才能显式重试。凭据创建响应丢失且没有保存远程 ID 时，在服务商界面处理新增凭据，
随后提交管理员清理报告。报告取消原操作，保留已经创建的资源；报告状态为
`external_reported`，`systemVerified` 为 `false`。

**发送为 `sent_copy_failed`** — SMTP 已接受邮件，只重试 Sent 副本。SMTP 或 APPEND
结果为 `unknown` 时，查询 Submission 和唯一提交标识。保留原 requestId。

**同步发现新增资源** — 在所属连接的同步结果中选择需要导入的资源。同步更新已有
资源的观察状态，保留凭据、授权和历史；读取失败时不会据此认定对象已删除。

**没有实时更新** — SSE 被代理缓冲了，参见上文的反向代理配置。此时客户端会退回
手动刷新，并且在发生操作时仍会重新拉取文件夹。

**超大文件夹首页加载慢** — 列表是一次序号区间 FETCH，取 50 封的 envelope 和
body structure。Purelymail 的服务端搜索索引（按用户开关）能让搜索很快；
Mailhearth 创建的邮箱默认开启该索引。

## 日志

结构化文本日志输出到 stderr。`MAILHEARTH_LOG_LEVEL=debug` 会额外输出每个请求的
记录和 IMAP 监听重连情况。日志中不包含任何凭据。

## 检查命令

```powershell
go test ./... -run '^$'
go test -count=1 ./internal/db ./internal/core ./internal/httpapi ./internal/mailproto/sieve ./internal/provider -run '^TestMultiProvider'
go vet ./...
npm --prefix web run test
npm --prefix web run build
```

真实环境测试要求关闭 `MAILHEARTH_DEV_STACK`。将认证配置保存在项目
`data/integration/multi-provider/` 目录，设置 `MAILHEARTH_MULTIPROVIDER_TEST_CONFIG`，
执行 `go test -count=1 -v ./internal/integration/multiprovider`。缺少配置会明确失败。
本地数据库检查与编译检查的结果分别记录，真实验收尚未执行的项目保持未完成状态。
