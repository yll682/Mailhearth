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
| 磁盘 | 几 MB | SQLite 只存组织模型和协作数据，邮件留在 Purelymail |
| 前端 | gzip 后 51 KB | 一个 JS 分块、一个 CSS 文件，不可变缓存 |

把 `MAILHEARTH_IMAP_MAX_CONNS`（默认 24）调到大约「并发使用人数 × 2 加上大家
同时打开的邮箱数量」。每个 IDLE 监听占用一个连接。Purelymail 允许每个用户建立
相当多的 IMAP 连接，不过没有必要持有超出需要的数量。

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

migration 在启动时自动执行，且都是追加式的。拉取新镜像后执行
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

**提示「Purelymail 拒绝了这个 API 令牌」** — 令牌被吊销或输入有误。在
管理 → 连接 里更换。

**某个邮箱显示「未连接」** — 导入的邮箱只有在有人绑定（添加成员时）或管理员
点击 *连接* 之后才会获得应用密码。如果 Purelymail 拒绝（`createAppPassword`
失败），确认该用户仍存在于账户中，然后执行 *立即同步*。

**提示「邮件服务器拒绝了此邮箱凭据」** — 应用密码在 Purelymail 后台被删除，
或者用户密码在 Mailhearth 之外被重置。对该邮箱执行 *轮换凭据*。

**规则无法保存** — 主机需要能访问 `mailserver.purelymail.com:4190` 的
ManageSieve（STARTTLS）。部分 VPS 服务商会封锁出站端口，可以用
`openssl s_client -starttls sieve -connect mailserver.purelymail.com:4190` 测试。

**没有实时更新** — SSE 被代理缓冲了，参见上文的反向代理配置。此时客户端会退回
手动刷新，并且在发生操作时仍会重新拉取文件夹。

**超大文件夹首页加载慢** — 列表是一次序号区间 FETCH，取 50 封的 envelope 和
body structure。Purelymail 的服务端搜索索引（按用户开关）能让搜索很快；
Mailhearth 创建的邮箱默认开启该索引。

## 日志

结构化文本日志输出到 stderr。`MAILHEARTH_LOG_LEVEL=debug` 会额外输出每个请求的
记录和 IMAP 监听重连情况。日志中不包含任何凭据。

## 开发环境

`MAILHEARTH_DEV_STACK=1` 会把 Purelymail、IMAP 和 SMTP 换成进程内的模拟实现。
`-seed-demo` 会加入两个域名、五个用户、路由规则和示例邮件。模拟的 API 令牌是
`dev-token`，用户密码是 `<名字>-pass`。Go 测试使用同一套模拟实现，因此
`go test ./...` 会完整走过 IMAP IDLE、SMTP 投递和 HTML 消毒等路径。
