# 对真实 Purelymail 账户的集成测试

[English](integration-testing.md) · **简体中文** · [繁體中文](integration-testing.zh-TW.md) · [日本語](integration-testing.ja.md) · [Español](integration-testing.es.md)

单元测试跑在内存中的 Purelymail API 假实现上。假实现能证明 Mailhearth 自身逻辑一致，却无法证明它与线上服务相符：假实现和客户端出自同一份对 API 的理解。只有真实账户才能发现写错的字段名、Purelymail 实际解释方式与我们假设不同的规则、没有真正被吊销的应用密码，或者根本没投递到的邮件。

`internal/integration` 里的套件补上这个缺口。它默认跳过，所以 `make test` 和 CI 仍然离线、快速。

## 覆盖范围

每个测试都通过 Mailhearth 自己的服务层操作，然后拿线上账户的实际状态核对，而不是核对 Mailhearth 自己的数据库。

| 测试 | 验证内容 |
| --- | --- |
| `TestImportExistingAccount` | 接入已有用户和路由规则的账户时，能如实导入、标记为导入项、不签发应用密码、且不改动上游。第二次同步是空操作。 |
| `TestMailboxLifecycle` | 创建信箱会生成真实用户和可用的应用密码；IMAP 登录、SMTP 投递、收件、渲染和已发送副本都正常；轮换会让旧密码在服务商侧失效；删除会移除用户。 |
| `TestRoutingRules` | 别名会变成服务商认可的路由规则，发往它的邮件能收到，删除地址会撤回规则。 |
| `TestGroupDistribution` | 群组地址能送达每个成员，移除成员会改写上游规则。 |
| `TestSharedMailboxAccess` | 授权决定谁能打开共享信箱。仅有管理员权限永远不等于邮件访问权。撤销后立即关门。 |
| `TestSieveRules` | Mailhearth 编译出的 Sieve 脚本被服务商接受、成为活动脚本，并且真的把邮件归入目标文件夹而不是收件箱。 |
| `TestOffboarding` | 离职者失去所有入口，包括其桌面邮件客户端持有的凭据；信箱及其历史邮件移交给接手人。 |
| `TestSuspendAndReactivate` | 停用会锁住所有人但继续收信；恢复后访问可用，期间到达的邮件都在。 |
| `TestPasswordResetForExternalClients` | 发给 Thunderbird 用的密码确实能认证，重置后 Mailhearth 自己的应用密码依然有效且与之不同。 |
| `TestExternalForwarding` | 转发到账户外地址会生成预期的规则。 |
| `TestTokenRejection` | 错误的 API 令牌会被拒绝，且不会破坏已存好的有效连接。 |

## 安全性

套件会创建和删除真实信箱与真实路由规则，而删除 Purelymail 用户会连带删除其邮件。两道机制把影响范围限制住。

套件创建的每个对象都命名为 `<前缀>-<运行号>-<角色><序号>`，前缀默认 `mh-it`。任何破坏性辅助函数在操作地址前都会调用 `guardOwned`：地址必须既在配置的测试域名下，**又**带有该前缀，否则直接中止整轮运行。即便代码改动要求某个测试去删它没创建过的信箱，也删不掉。

每个测试还会在创建对象的同时登记清理动作，所以中断或失败的运行仍会拆掉自己造出来的东西。

即使如此：**请把套件指向一个不含任何重要数据的域名。** 在账户上专门加一个测试域名是正确做法。上述防护针对的是套件自身行为失常，而不是把 `MAILHEARTH_IT_DOMAIN` 误写成生产域名、且前缀恰好撞上的情况。

每个测试都会在临时目录里建立自己的空数据库和独立主密钥，不会碰到你真实的部署。

## 运行

域名必须已存在于 Purelymail 账户中且 MX 记录正常，因为这些测试大多以投递为衡量对象。API 令牌需要完整权限。

```bash
export MAILHEARTH_IT_TOKEN=你的API令牌
export MAILHEARTH_IT_DOMAIN=test.example.com

go test ./internal/integration -v -timeout 40m
```

整套预计耗时数分钟：大部分时间在等邮件真正到达。调试时可只跑一个：

```bash
go test ./internal/integration -run TestMailboxLifecycle -v -timeout 15m
```

测试会在账户上创建并删除用户。Purelymail 按用户计费，跑完一整套花费不到一分钱。

## 配置

| 变量 | 默认值 | 用途 |
| --- | --- | --- |
| `MAILHEARTH_IT_TOKEN` | — | API 令牌。必填，未设置则跳过。 |
| `MAILHEARTH_IT_DOMAIN` | — | 测试域名。必填，未设置则跳过。 |
| `MAILHEARTH_IT_PREFIX` | `mh-it` | 标记套件所属对象的本地部分前缀，必须以 `mh` 开头。 |
| `MAILHEARTH_IT_EXTERNAL` | — | 账户外的一个地址，用于启用 `TestExternalForwarding`。 |
| `MAILHEARTH_IT_DELIVER_SECONDS` | `180` | 等待邮件到达多久后判定失败。 |
| `MAILHEARTH_IT_KEEP` | 未设置 | 保留创建的对象以便检查，需自行清理。 |
| `MAILHEARTH_IT_API_URL` | `https://purelymail.com/api/v0` | API 端点。 |
| `MAILHEARTH_IT_IMAP_ADDR` | `imap.purelymail.com:993` | IMAP 地址。 |
| `MAILHEARTH_IT_SMTP_ADDR` | `smtp.purelymail.com:465` | 投递地址。 |
| `MAILHEARTH_IT_SIEVE_ADDR` | `mailserver.purelymail.com:4190` | ManageSieve 地址，留空则跳过 Sieve 测试。 |
| `MAILHEARTH_IT_IMAP_TLS` | `tls` | `tls`、`starttls` 或 `none`。 |
| `MAILHEARTH_IT_SMTP_TLS` | `tls` | `tls`、`starttls` 或 `none`。 |
| `MAILHEARTH_IT_SIEVE_TLS` | `starttls` | `tls`、`starttls` 或 `none`。 |

## 测试失败时

投递超时是最常见的失败，通常说明域名的 DNS 不完整，而不是 Mailhearth 有问题。先检查 MX 和 SPF 记录，再考虑调高 `MAILHEARTH_IT_DELIVER_SECONDS`。Purelymail 的垃圾邮件过滤也可能把测试邮件归入 Junk；失败信息会写明它搜索了哪个文件夹、在里面看到多少封邮件。

用 `MAILHEARTH_IT_KEEP=1` 重跑可保留对象，在 Purelymail 网页界面里检查。记得事后删除：它们会持续产生费用，而下一轮运行不会接管它们。

如果运行被强杀导致清理没执行，残留物很好找：每一个都带着前缀。

## 不要放进 CI

不要把这套测试接到合并请求流水线上。它需要真实令牌、要花钱，而且投递等待让它远远太慢，不适合做合并前的门禁。应在发布前运行，以及在改动 Purelymail 客户端、IMAP/SMTP 路径或 Sieve 编译器之后运行。
