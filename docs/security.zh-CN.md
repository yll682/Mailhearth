# 安全

[English](security.md) · **简体中文**

## 威胁模型

Mailhearth 位于员工浏览器和 Purelymail 之间，手里握着一个可以创建、删除和重置
账户上任意邮箱的 API 令牌。按敏感程度排列，需要保护的资产是：

1. Purelymail API 令牌。
2. 为访问 IMAP/SMTP/Sieve 而持有的邮箱应用密码。
3. 邮件内容和组织通讯录。
4. 成员的会话。

考虑的攻击者包括：互联网上的攻击者、恶意邮件或钓鱼邮件、已离职的员工，以及试图
读取未获授权邮箱的成员。主机本身被攻陷不在防护范围内，只做影响范围的限制——机密
数据加密存储，因此仅仅拿到数据库副本没有用处。

## 防护措施

**静态加密。** `secrets.Box` 用 AES-256-GCM 加密，密钥由本次部署的 master key 经
HKDF-SHA256 派生。master key 来自 `MAILHEARTH_MASTER_KEY`，或首次启动时生成的
`<data>/master.key`（权限 0600）。不同用途使用不同的派生密钥。API 令牌保存后只以
掩码形式展示，应用密码从不展示。

**身份认证。** 成员密码使用 argon2id（19 MiB，t=2）。会话是 256 位随机 token，
以 SHA-256 哈希后存储；cookie 带 `HttpOnly`、`SameSite=Lax`，在 base URL 为 HTTPS
时带 `Secure`，有效期 30 天并随访问延长。登录和接受邀请按 IP 限流。停用或办理离职
会立即注销该成员的全部会话。

**权限校验。** 每个管理接口都检查成员角色中的对应权限；所有者专属的操作（转让）
要求 `org.owner`。邮件接口通过 `core.ResolveMailbox` 解析邮箱，要求成员拥有该邮箱
或持有显式授权，并按授权级别限制操作（`read` 不能加星标也不能发信，`send` 不能
删除或移动）。管理权限永远不蕴含邮件访问权。

**CSRF。** 会改变状态的 `/api` 请求必须携带 `X-Requested-With: Mailhearth`
（浏览器在没有 CORS 的情况下无法跨源添加该头），并且在存在 `Origin` 头时该头必须
与主机匹配。

**不可信的邮件内容。**
- HTML 在服务端用白名单（bluemonday）消毒，再经过一次 DOM 处理：去掉危险 CSS
  （`expression`、`url()`、`position:fixed`），把 `cid:` 图片改写成带认证的分段
  URL，在未经请求时拦截远程图片，并给链接强制加上
  `target=_blank rel=noopener noreferrer`。
- 消毒后的文档从独立 URL 提供，带
  `Content-Security-Policy: default-src 'none'; img-src 'self' data:; style-src
  'unsafe-inline'; script-src 'none'; form-action 'none'`，并在
  `<iframe sandbox="allow-same-origin allow-popups …">` 中显示，不含
  `allow-scripts`。保留 `allow-same-origin` 只是为了让父页面测量高度；无论如何
  CSP 都保证里面不可能执行脚本。
- iframe 通过一个短期 HMAC view token 认证，该 token 绑定成员、邮箱、文件夹和
  UID，因此不需要 cookie。
- 附件一律以 `Content-Disposition: attachment` 提供，除非类型属于可安全内联的
  范围（位图、PDF、音频视频、text/plain）；HTML、SVG 和 XML 从不内联渲染。所有
  响应都带 `X-Content-Type-Options: nosniff`。
- 撰写的 HTML 和签名在发送前经过同一套消毒流程，因此即使浏览器会话被攻陷也无法
  把脚本注入邮件。
- 邮件和附件大小有上限（`MAILHEARTH_MAX_UPLOAD_MB`、`MAILHEARTH_MAX_MESSAGE_MB`）；
  超过 2 MB 的文本分段在显示时截断。

**应用层响应头。** `X-Frame-Options: DENY`、`Referrer-Policy: no-referrer`、
针对 SPA 的 CSP（`script-src 'self'`）、只对带哈希的静态资源使用不可变缓存、
API 响应一律 `no-store`。

**Purelymail 凭据。** 每个邮箱一个应用密码。移交、转为共享、挂起和离职处理都会
轮换它，同时重置 Purelymail 密码，这样离职者在手机和桌面客户端上配置的连接也会
失效。旧的应用密码会在 Purelymail 侧删除。

**Sieve。** 规则由结构化模型编译而来，编译时校验邮件头名称、大小、地址和标记；
用户无法提交原始 Sieve 脚本。

**审计。** 每一次管理操作都记录操作者、对象和详情，写入 `audit_log`。

## 部署建议

- 在反向代理上终止 TLS，并把 `MAILHEARTH_BASE_URL` 设成 HTTPS 地址，这样 cookie
  才会带 `Secure`。只有在代理确实设置了 `X-Forwarded-For` 时才开启
  `MAILHEARTH_TRUST_PROXY=true`。
- 把 `master.key` 与数据库分开备份，存进你的密码管理器。没有它，所有已保存的
  凭据都需要重新建立（产品本身支持这样做：轮换每个邮箱的凭据）。
- 为 Mailhearth 单独申请一个 Purelymail API 令牌，以便可以独立吊销。
- 在 Purelymail 账户本身开启两步验证；API 令牌会绕过它，这正是它排在资产首位的
  原因。
