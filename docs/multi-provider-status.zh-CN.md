# 多服务商实施与验证记录

更新日期：2026-10-09。

## 当前代码

- SQLite 升级保留旧 mailbox ID，新增 MailConnection、独立协议 endpoint、加密 Credential、ProviderResource、Operation、Submission。
- 数据库检查组织、连接、邮箱、凭据、身份和操作的关联归属；邮箱、凭据、endpoint 等对象的归属字段禁止修改。具有发送或协作历史的邮箱禁止删除。
- 连接和 endpoint 配置使用 revision 检查，候选协议配置完成认证验证后才能提交。邮箱停用、会话撤销和邮箱访问授权更改会终止对应请求。
- 邮箱归档保留历史。解除本地登记检查依赖、完整地址和 revision，并记录需要外部撤销的凭据信息。
- 发件身份使用独立发送授权。更新身份地址后需要重新授权；显示设置和删除操作检查当前权限与 revision。
- 特殊文件夹按照明确映射、唯一 SPECIAL-USE、唯一名称识别确定。设置界面读取真实文件夹列表；保存时重新读取并验证文件夹、当前权限和配置版本。
- Submission 保存 requestId、Message-ID、内容摘要、加密投递目标以及 SMTP 和 Sent 的独立状态。发送正文保存在邮件服务器的 Drafts。
- SMTP accepted 后的副本重试保留原 SMTP 状态。发送结果 unknown 不会自动重新提交。草稿清理要求 UID EXPUNGE，清理错误单独保存。
- 设置界面提供最近发送请求查询。Composer 在已建立请求后禁止编辑邮件，重复请求保留原 requestId。
- 管理操作界面显示持久化步骤，并提供取消、核查和显式重试。资源占用错误提供对应 operationId。
- 成员创建统一保存四种互斥邮箱操作、群组版本和共享授权。全部步骤完成后生成邀请；进行中的创建阻止提前启用成员、修改资料和生成邀请。
- 群组计算全部符合条件的 personal 邮箱，检查每个候选的连接、域名和版本。成员状态与邮箱所有者、类型、暂停、归档及恢复通过 Operation 更新相关群组；暂停与成员停用立即撤销本地访问。
- 邮箱创建与登记保存所有者、所属连接及所有关联群组的版本，预检查新增地址的投递限制，分别保存当前目标与待更新目标。邮箱提交和群组更新具有独立步骤，恢复时读取已保存的邮箱 ID 与版本。
- 远程邮箱删除在排队事务中停止本地访问，处理关联群组并检查其他收件地址依赖。成员删除需要当前 revision、适用成员状态和完整依赖检查，保留协作与操作历史。
- 成员资料修改需要 expectedRevision；相同版本的并发更新只允许一项提交。管理操作的附加权限在排队事务、执行、重复提交和操作控制时检查。
- 邮箱管理操作保存原连接与域名关联版本。managed 凭据启用保存实际邮箱版本；重试检查已保存版本。凭据核查使用新的协议认证连接，网络错误保留未确认状态。
- 管理页面的重复提交保留相同 requestId；修改请求内容时生成新的 requestId。
- DomainBinding 按连接登记和管理。域名写入使用 binding ID、requestId 和 revision；解除关联检查依赖。管理页面分别提供解除关联与远程删除。
- 管理操作具有独立的远程步骤记录。已确认成功的步骤在重试时保持完成状态；未知结果保留资源占用。部分远程影响需要处理时显示 needs_action。
- 提供 managed 邮箱创建、凭据接入与轮换、密码重置、远程删除的执行流程。候选凭据通过启用协议的认证后提交。远程主密码通过受限的一次性接口领取，具有期限和权限检查。
- managed 凭据创建响应丢失且没有保存远程 ID 时，提供管理员清理报告。报告记录 administrator_report、external_reported 和 systemVerified=false，取消原操作、保留已经创建的资源并释放资源占用。
- 连接同步检查完整发现范围；邮箱和域名通过独立读取核查缺失状态。缩小管理范围保留邮件 endpoint 与凭据，将范围之外的管理对象设为 external。
- 发现结果包含地址规则、identity 和域名的安全结构化信息，转发结果保留确认状态。规则与 identity 支持选择导入；复杂规则保持外部管理，custom-password identity 按登录凭据用途登记。
- 转发支持选择导入，检查来源邮箱和域名依赖，独立保存 mailbox_forwardings、远程引用和观察结果。Purelymail 主地址规则按 forwarding 用途登记并保留 primary 地址；Migadu 按目标保存确认状态。重复导入保留对象和期望设置；同步记录目标差异、缺失和外部状态。未经投递验证的方式保留 unverified。
- 同步更新规则的 observedTargets、域名设置和 DNS 状态，保留 desiredTargets。新发现资源保存到所属连接的快照并等待管理员选择导入；缺少实际 DNS 检查结果时保持 unknown。
- 邮箱对象与连接返回结构化能力信息，包含支持方式、就绪状态、当前权限及类型明确的限制。规则管理使用当前邮箱的 ManageSieve endpoint。
- ManageSieve 使用 go-managesieve。规则保存检查服务器扩展、确认现有 active script 的内容摘要，使用独立候选脚本，读取候选内容并核验启用结果。全部确认后更新本地生效设置；保留已有脚本。
- ManageSieve 根据结构化响应区分临时错误、认证机制限制、endpoint 调整和未明确的认证结果。未明确的认证拒绝保留 verification_required，远程撤销状态继续等待核查。
- 初始化页面提供管理连接的资源发现、选择导入和明确的协议凭据配置。初始化完成状态与邮箱绑定在同一事务中保存；选择不绑定邮箱时可以完成初始化。
- Overview 使用 connections 数组，按连接显示管理状态。没有 org.manage 权限时不返回管理认证状态。
- 余额和用量使用独立 billing.read 权限，按连接读取并展示原单位、域名和统计期间。Purelymail 提供账户余额，Migadu 提供各域名用量；每个连接具有独立错误与重试显示。Overview 按 DomainBinding 显示实际 DNS 状态。
- core 的 address、group、成员邮箱、离职和初始化业务入口使用公共 provider 接口与明确的连接 ID。连接配置和成员资料入口检查当前管理权限、revision 和资源占用。
- 连接、endpoint、文件夹、发送请求、身份授权、资源选择、转发确认状态及操作管理文案提供 English、简体中文、繁体中文、日本語和 Español。管理错误按错误码显示本地化说明，保留原 code、operationId 和服务端信息用于检查。TypeScript AST 检查词典覆盖和插值参数。
- 五种语言的 README、architecture、security、operations 和 integration-testing 已更新连接模型、协议配置、凭据领取、Operation、Submission、转发观察及验证方法。

## 已执行检查

以下检查在当前项目中通过：

```powershell
go test ./... -run '^$'
go test -count=1 ./... -run '^TestMultiProvider'
go test -count=20 ./internal/core -run '^TestMultiProviderStorage(Forwarding|ConcurrentConnectionWorkers)' -timeout 120s
go vet ./...
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
git diff --check
```

指定的 82 项 Go 测试包括真实 SQLite、应用的实际 HTTP server、Sieve 编译器、JSON/CSV 处理和请求前提检查。验证内容包括迁移原子性、连接隔离、请求幂等、关联归属、身份授权、访问权限、会话撤销、发送状态保存、邮箱归档、本地解除登记、域名关联隔离、秘密领取、并发操作、初始化完成、成员创建、群组完整候选、邮箱移交、成员状态、凭据管理前提、管理入口权限、成员删除依赖、远程删除前提、新增邮箱群组候选、余额权限、凭据清理报告、域名观察、转发导入与观察、转发记录迁移、配置版本和 Sieve 扩展检查。另有六项前端测试，包含 requestId、翻译覆盖、插值参数及中文界面常量检查。转发及独立连接的并发操作测试重复执行 20 次通过。全包编译检查使用 `-run '^$'`。

这些数据库和 HTTP 检查没有验证外部 SMTP 投递、IMAP 文件夹操作、服务商写入或 ManageSieve 脚本切换。前端检查包含 TypeScript 和生产构建，尚无浏览器交互验收。

构建保留已有输出文件，测试数据保存在项目 data 目录。

## 真实环境验收

用户已选择跳过真实服务商核验与真实协议环境检查。尚未验证的能力保持 unverified；本地检查没有确认这些能力在真实服务商中可用。

```powershell
go test -count=1 -v ./internal/integration/multiprovider
```

当前未设置 `MAILHEARTH_MULTIPROVIDER_TEST_CONFIG`。配置需要包含 Purelymail、Migadu，以及手动配置的主邮箱、第二个邮箱、独立 SMTP、私有 CA 和没有 ManageSieve 的邮箱。密钥与密码仅保存在项目 data/integration/multi-provider 目录中的配置文件。

真实环境测试代码检查管理与协议认证前提、连接隔离、相同地址登记、候选认证失败、revision 并发更新、Operation 幂等及公开字段。新增协议测试代码检查 T04 独立 IMAP/SMTP 凭据的实际投递、T05 SMTP 停用后的读取、T06 ManageSieve 停用后的读取与投递、T37 配置变化后的旧连接关闭。这些源码通过编译检查，尚未执行真实测试；尚未覆盖规格全部 T01–T40 与 V01–V07。CGO race 检查尚未通过，当前环境缺少 C 编译器。

五种语言的项目文档保存本地检查与真实环境验收的独立范围；完整真实环境验收仍未执行。

## 尚需完成

- 使用真实投递确认主地址规则和转发的 deliveryMode。Migadu 尚未通过 V03 的模式保持 unverified。
- 完成其余业务入口、恢复分支与设置页面的逐项检查，补充对应测试。
- 完成全部适用 T01–T40 与 V01–V07 的真实环境测试代码。
- 将现有 Purelymail 集成测试的初始化、发现导入、连接参数和版本参数迁移到当前接口。
- 使用真实服务商和协议环境执行全部规定验收，验证 SMTP 投递、Sent 副本、IMAP 连接额度、取消并发行为、身份授权及 Sieve 接管。

当前代码仍有以上必要流程未完成，尚不满足规格的完整交付条件。
