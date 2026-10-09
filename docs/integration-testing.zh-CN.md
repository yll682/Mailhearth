# 多服务商集成测试

[English](integration-testing.md) · **简体中文** · [繁體中文](integration-testing.zh-TW.md) · [日本語](integration-testing.ja.md) · [Español](integration-testing.es.md)

## 本地检查

在项目根目录执行。缓存和中间文件保存在已经忽略的 `data/` 目录。

```powershell
$env:GOCACHE=Join-Path (Get-Location) 'data/go-build-cache'
$env:GOTMPDIR=Join-Path (Get-Location) 'data/integration/go-work'
New-Item -ItemType Directory -Force $env:GOCACHE,$env:GOTMPDIR | Out-Null
go test ./... -run '^$'
go test -count=1 ./... -run '^TestMultiProvider' -timeout 120s
go vet ./...
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
git diff --check
```

全部包的编译检查使用 `-run '^$'`。指定测试使用真实 SQLite、应用 HTTP server 和
纯算法，检查关联归属、授权、操作、路由、转发导入与观察、发送状态和 Sieve。
前端测试使用 TypeScript parser 检查 requestId 稳定性、翻译覆盖和插值参数。这些
检查没有执行外部投递或浏览器交互。并发与转发检查可以使用 `-count=20` 重复执行；
race 检查需要 C 编译器和 cgo。

## 真实环境与安全

`internal/integration/multiprovider` 使用真实 API 和协议连接。关闭
`MAILHEARTH_DEV_STACK`；缺少配置时明确失败。使用专用测试账户、域名及没有重要
邮件的邮箱。协议测试发送真实邮件，并修改独立本地测试部署的 endpoint 设置。
执行前阅读所选测试；当前认证前提和协议检查不重置已有外部邮箱密码。后续生命周期
验收可能创建、删除资源、撤销凭据和修改规则，需要使用专用资源。

配置只保存在 `data/integration/multi-provider/`，程序解析路径并检查所属目录。
凭据不提交到 Git，也不发送到聊天或日志。每次运行在该目录下保存独立数据库、主密钥、
测试数据和报告。妥善保护这些文件，完成检查后按需要清理。

## 配置

JSON 读取拒绝未知字段和额外 JSON 值。以下环境全部需要配置：

| 字段 | 必要内容 |
|---|---|
| `purelymail` | `apiKey`、`domain`、`imap`、`smtp`、`managesieve` |
| `migadu` | 上述字段及 `apiUsername`；没有 ManageSieve 时明确停用 |
| 各协议模板 | `enabled`；启用时提供 `host`、`port`、`tlsMode`（`tls`/`starttls`），可选 `caBundleId` |
| `manual` | `primaryMailbox`、`secondaryMailbox`、`independentSmtp`、`privateCaMailbox`、`noSieveMailbox` |
| 各手动邮箱 | `address`、`credentials`（`clientKey`、`secret`），以及包含三项协议的 `endpoints` |
| 启用的手动 endpoint | `networkMode: override`、`network` 模板、`authMode: password`、`username`、`credential: {clientKey}` |
| 停用的手动 endpoint | `networkMode: disabled` |
| `deliveryTimeoutSeconds` | 正整数，默认 180 |

`independentSmtp` 的 IMAP 和 SMTP 必须使用不同 username 和 secret。私有 CA 检查
需要测试部署能够读取的 CA 及对应 `caBundleId`。`MAILHEARTH_CA_BUNDLES_FILE` 指向
将正整数 CA ID 映射到 PEM 文件路径的 JSON 文件。`noSieveMailbox` 停用 ManageSieve，
保留可用的 IMAP/SMTP。服务商管理认证不提供邮箱协议密码。

```powershell
$env:MAILHEARTH_MULTIPROVIDER_TEST_CONFIG=Join-Path (Get-Location) 'data/integration/multi-provider/config.json'
go test -count=1 -v ./internal/integration/multiprovider -timeout 40m
```

## 覆盖与结果

- `TestRealMultiProviderPrerequisites`：真实 Purelymail/Migadu 管理认证及手动协议
  凭据。成功后写入 `prerequisites.json`。
- `TestRealMultiProviderTransactions`：连接隔离、相同地址登记、候选认证拒绝、revision
  冲突、requestId 与内容检查及安全查询字段。对应部分 T01、T03、T08、T17–T19、
  T40，成功后写入 `transactions.json`。
- `TestRealMultiProviderProtocols`：独立凭据与实际投递（T04）、SMTP 停用后的读取
  （T05）、ManageSieve 停用后的读取与投递（T06）、endpoint 版本及旧连接关闭
  （T37），成功后写入 `protocols.json`。

报告保存 `acceptanceComplete=false`。完整 T01–T40 和 V01–V07 仍需要补充测试并
实际执行；`internal/integration` 的旧 Purelymail 套件仍需要迁移到当前接口。
本地测试通过或导入成功不表示真实服务商验收完成。Migadu 转发方式在 V03 通过前
保持 `unverified`。

投递失败时检查 MX/SPF、凭据、启用协议、目标确认状态、Junk 等文件夹及 Message-ID。
网络错误不证明凭据已经撤销或资源已经删除。保留 operationId/submissionId 并核查
未知结果，不自动重新发送。普通 CI 不使用真实凭据；服务商、SMTP/IMAP、migration
或 Sieve 变更后及发布前执行专用验收。
