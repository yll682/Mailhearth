# 文档截图与演示环境

## 页面滚动

- 初始化、登录和设置页面的内容超过窗口高度时，支持纵向滚动。
- 页面顶部、协议设置、提交按钮和语言切换器均可通过滚动到达。
- 管理页面、导航侧栏、邮件列表、阅读正文、资源导入弹窗和写邮件窗口分别提供滚动区域。
- 移动设备打开写邮件窗口时，导航侧栏自动关闭，收件人输入和发送按钮均可操作。

`scripts/browser-ui-check.mjs` 使用真实浏览器连接独立本地演示环境，通过管理 API 和页面操作检查 Domains 页面、转发目标及滚动行为。检查窗口尺寸为 1280 × 720、900 × 360、390 × 844、390 × 320 和 320 × 480；初始化向导另外在独立数据库中检查。

## 演示数据

演示环境仅用于页面展示和文档截图。账户、域名、邮件内容、DNS 状态和管理记录属于演示数据，不代表真实服务商验证结果。

程序仅监听 `127.0.0.1:18080`。数据库位于被 Git 忽略的 `data/documentation-preview/`；保留已创建的组织、管理员密码和会话。630 封初始演示邮件的完整 MIME 内容保存在数据库的 `documentation_demo_messages` 表中，页面通过本地 IMAP/SMTP 服务访问邮件。每次启动重新加载初始邮件；邮件移动、删除、标记以及新收到的邮件保存在当前进程中。

演示服务使用本地 HTTPS API 和 TLS 邮件连接，并验证本地证书。HTTP 请求仅允许访问本地地址。Purelymail 连接的名称和邮件正文均标注演示用途；Migadu 连接保持停用，需要真实凭据时另外配置。

### 页面数据

- 12 名成员：包含在职、待邀请、停用和离职状态；已创建的管理员保持原有信息。
- 15 个邮箱：12 个个人邮箱、3 个共享邮箱，包含正常、暂停和归档状态。
- 3 个连接、3 个域名、6 个角色、4 个群组。
- 25 个地址：包含主要地址、别名、转发、群组、前缀和 catch-all。
- 18 个发件人身份、15 项共享访问授权、2 项邀请记录。
- 每个邮箱包含 24 封收件箱邮件、5 封已发送邮件、3 封草稿、6 封归档邮件、2 封垃圾邮件和 2 封已删除邮件。
- 邮件包含中英文主题、HTML 正文、CSV/TXT 附件，以及未读、星标和回复状态。
- 共享邮箱包含 24 项负责人分配、24 条内部备注和 6 项已解决状态。
- 1 项邮箱转发登记、6 项演示操作历史及对应审计记录。

### 使用与截图

用户提供的截图按语言和页面名称保存在 `docs/screenshots/`，参见[截图目录](screenshots/README.zh-CN.md)。五份 README 使用对应的收件箱、阅读邮件、管理概览和成员列表图片。

刷新 `http://127.0.0.1:18080/`，使用已创建的管理员账号。新增的在职演示成员可以使用 `bob@acme.test` 等地址登录，演示密码为 `Mailhearth-Demo-2026!`。

阅读页面可以选择最新的「Project Aurora · 合作方案与预算明细」，展示 HTML 正文和两个附件。共享协作可以选择 `support@acme.test`，展示负责人、内部备注和已解决状态。管理页面可以展示概览、成员、邮箱、域名、地址、群组、角色和连接；Purelymail 资源发现包含邮箱转发条目。

```powershell
$env:GOCACHE=Join-Path (Get-Location) 'data/go-build-cache'
$env:GOTMPDIR=Join-Path (Get-Location) 'data/integration/go-work'
go build -o data/documentation-preview/documentation-demo.exe ./cmd/documentation-demo
data/documentation-preview/documentation-demo.exe
```

程序只在独立演示目录中建立演示数据。已有演示记录在重新启动时保留；本地协议地址按照新启动的服务更新。

### 自动检查

```powershell
data/documentation-preview/documentation-demo.exe -data-dir data/documentation-preview-check -check
```

启动时检查管理 API、邮件列表、HTML 阅读、附件实际内容、共享协作、资源发现和转发重复导入；执行一次本地 SMTP 发送并通过 IMAP 确认送达，因此 Bob 的收件箱额外包含一封检查邮件。已有组织、管理员身份、密码哈希和会话通过初始化前后的数据库查询进行比较，并检查 SQLite 完整性和关联完整性。

这些检查仅用于本地演示。真实服务商验收仍需要真实账户和凭据。

浏览器检查需要 Playwright 和 Chrome。设置 `MAILHEARTH_PLAYWRIGHT_PATH` 为已安装的 Playwright 包路径，`MAILHEARTH_BROWSER_PATH` 为 Chrome 可执行文件路径。`MAILHEARTH_UI_URL` 指向独立演示实例，使用 `admin@acme.test` 和演示密码；`MAILHEARTH_SETUP_UI_URL` 指向独立未完成初始化的应用实例。运行 `node scripts/browser-ui-check.mjs`，检查程序会创建专用初始化管理员。两个实例均应使用独立数据库，并监听本地地址。
