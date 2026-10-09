// Tiny i18n: source strings are English; translations are looked up by the
// English text. Unknown strings fall back to English.
import { signal } from "@preact/signals";

export type Lang = "en" | "zh-CN" | "zh-TW" | "ja" | "es";

export const LANGS: { id: Lang; label: string }[] = [
  { id: "en", label: "English" },
  { id: "zh-CN", label: "简体中文" },
  { id: "zh-TW", label: "繁體中文" },
  { id: "ja", label: "日本語" },
  { id: "es", label: "Español" },
];

// BCP 47 tags handed to Intl for dates and numbers.
const locales: Record<Lang, string> = { en: "en-GB", "zh-CN": "zh-CN", "zh-TW": "zh-TW", ja: "ja-JP", es: "es-ES" };

export function localeFor(l: Lang): string {
  return locales[l];
}

function detect(): Lang {
  const saved = localStorage.getItem("mh.lang");
  const known = LANGS.find((l) => l.id === saved);
  if (known) return known.id;
  const tags = navigator.languages?.length ? navigator.languages : [navigator.language];
  for (const tag of tags) {
    if (/^zh\b/i.test(tag)) return /hant|tw|hk|mo/i.test(tag) ? "zh-TW" : "zh-CN";
    if (/^ja\b/i.test(tag)) return "ja";
    if (/^es\b/i.test(tag)) return "es";
  }
  return "en";
}

export const lang = signal<Lang>(detect());

export function setLang(l: Lang) {
  lang.value = l;
  localStorage.setItem("mh.lang", l);
  document.documentElement.lang = l;
}

const zhCN: Record<string, string> = {
  "Full mailbox address": "完整邮箱地址",
  "Report lost credential cleanup": "报告未知凭据清理结果", "Remove the unknown application credential in the provider console before reporting. This cancels the operation and retains already created resources.": "请在服务商管理界面清理未知的应用凭据后报告。报告将取消操作，并保留已经创建的资源。",
  "Balance and usage by connection": "各连接的余额与用量", "Values retain the provider's units and scope.": "数值保留服务商提供的单位和范围。", "Usage": "用量", "balance": "余额", "incoming": "收件数量", "outgoing": "发件数量", "storage": "存储用量", "messages": "封邮件", "provider_credit": "服务商余额单位", "current_date": "服务商当前日期",
  "Auto-reply is not available for this server.": "当前服务器无法使用自动回复。",
  "Operations": "管理操作",
  "All operations": "全部操作",
  "Cancel operation": "取消操作",
  "Retry operation": "重试操作",
  "Check operation result": "核查操作结果",
  "No operations.": "暂无管理操作。",
  "View the active operation": "查看正在进行的操作",
  "Retries preserve the original request and operation ID. Unknown results require checking.": "重试保留原请求与操作标识。结果未知的操作需要核查。",
  "Sending requests": "发送请求",
  "Recent sending requests remain available after closing the composer.": "关闭撰写窗口后仍可查询最近的发送请求。",
  "No sending requests.": "暂无发送请求。",
  "Check the mail server using the Message-ID and creation time before sending again.": "重新发送前，请使用 Message-ID 和创建时间查询邮件服务器。",
  "Only the Sent copy is retried. SMTP acceptance is retained.": "仅重新保存 Sent 副本。已确认的 SMTP 接受状态保持不变。",
  "Sending request result unavailable. Retry keeps the same request ID.": "暂时无法获取发送请求结果。再次请求会使用同一个请求标识。",
  "Special folders": "特殊文件夹",
  "Choose existing folders. Automatic selection requires a unique match.": "请选择已有文件夹。自动选择需要唯一匹配的文件夹。",
  "Automatic selection": "自动选择",
  "Folder unavailable": "文件夹不可用",
  "Folder mapping saved.": "已保存文件夹映射。",
  "Sent copy handling": "发送副本保存方式",
  "Mailhearth saves the Sent copy": "由 Mailhearth 保存发送副本",
  "Mail server saves the Sent copy": "由邮件服务器保存发送副本",
  "Archive mailbox": "归档邮箱",
  "Unregister mailbox": "解除邮箱登记",
  "Removes local registration. Remote mail remains on the server.": "解除本地登记。远程邮件继续保存在服务器上。",
  "Revoke these credentials in the external mail service:": "请在外部邮件服务中撤销以下凭据：",
  "Authorize sending": "授权发送",
  "Revoke sending authorization": "撤销发送授权",
  // Short labels; the long form lives in an ⓘ popover.
  "Search mail": "搜索邮件",
  "Nothing on Purelymail is changed.": "不会改动 Purelymail 上的任何数据。",
  "Rules run on the mail server, before mail reaches your inbox.": "规则在邮件服务器上执行，先于邮件进入收件箱。",
  "Repeat interval": "重复间隔",
  "For use in other mail apps. Shown once.": "用于其他邮件客户端，只显示一次。",
  "Everyone loses access. Mail keeps arriving and is kept.": "所有人将无法访问。邮件仍会继续接收并保留。",
  "Deletes the mailbox and all its mail. This cannot be undone.": "将删除该邮箱及其全部邮件，无法恢复。",
  "Access ends immediately and every credential is rotated.": "访问权限立即终止，所有凭据都会轮换。",
  "Deletes every mailbox and rule on this domain.": "将删除该域名下的所有邮箱和规则。",
  "Imports changes made outside Mailhearth.": "导入在 Mailhearth 之外所做的更改。",
  "Mail to this address reaches every member.": "发到此地址的邮件会送达所有成员。",
  "New mail skips this mailbox while forwarding is on.": "开启转发后，新邮件不会进入此邮箱。",
  "Your sign-in address. It can be an address on your own domain.": "你的登录地址，可以是你自己域名下的地址。",
  "Create it in the Purelymail portal under Account → API. Stored encrypted on this server and never sent to browsers.": "在 Purelymail 后台 Account → API 创建。令牌在本服务器加密保存，不会发送到浏览器。",
  "Importing reads your domains, mailboxes and routing rules, and builds the organisation model from them.": "导入会读取你的域名、邮箱和路由规则，据此建立组织模型。",
  "Pick the mailbox you use yourself, so you can read mail as soon as setup finishes.": "选择你自己使用的邮箱，初始化完成后即可直接收发。",
  "Stored as Sieve. Saving replaces any filters this mailbox has in other mail clients.": "以 Sieve 形式保存。保存后会替换此邮箱在其他客户端配置的过滤器。",
  "Reply at most once every N days to the same sender.": "对同一发件人最多每 N 天回复一次。",
  "Re-reads domains, mailboxes and routing rules from Purelymail and updates the organisation model.": "重新读取 Purelymail 的域名、邮箱和路由规则，更新组织模型。",
  "Another address for one mailbox.": "同一个邮箱的另一个地址。",
  "Sends mail on to one or more addresses, inside or outside the organisation.": "转发给一个或多个地址，可以在组织外。",
  "Receives anything at this domain that has no mailbox of its own.": "接收此域名下所有没有对应邮箱的地址。",
  "Receives every address starting with this prefix.": "接收以此前缀开头的所有地址。",

  // Common
  "Mail": "邮件", "Admin": "管理", "Settings": "设置", "Sign out": "退出登录", "Sign in": "登录", "Cancel": "取消", "Save": "保存",
  "Delete": "删除", "Close": "关闭", "Create": "创建", "Edit": "编辑", "Back": "返回", "Next": "下一步", "Done": "完成", "Search": "搜索",
  "Loading…": "加载中…", "Nothing here yet.": "这里还没有内容。", "Yes": "是", "No": "否", "Name": "名称", "Actions": "操作", "Status": "状态",
  "Copy": "复制", "Copied": "已复制", "Refresh": "刷新", "Confirm": "确认", "Optional": "可选", "Required": "必填", "Unknown": "未知",
  "Something went wrong.": "出了点问题。", "Retry": "重试", "Continue": "继续", "Password": "密码", "Email": "邮箱", "Language": "语言",
  "Active": "活跃", "Invited": "已邀请", "Disabled": "已停用", "Departed": "已离职", "Suspended": "已挂起", "Archived": "已归档",
  "Personal": "个人", "Shared": "共享", "personal": "个人", "shared": "共享", "Full": "完全", "Send": "发送", "Read": "只读",
  "full": "完全访问", "send": "可读可发", "read": "只读",

  // Auth
  "Welcome back": "欢迎回来", "Email or mailbox address": "登录邮箱或邮箱地址", "Incorrect email or password": "邮箱或密码不正确",
  "Signing in…": "登录中…", "You have been invited": "你收到了一份邀请", "Set a password to activate your account.": "设置密码以激活账户。",
  "Your name": "你的姓名", "Choose a password": "设置密码", "At least 10 characters.": "至少 10 个字符。", "Activate account": "激活账户",
  "This invite link is invalid or has expired.": "邀请链接无效或已过期。", "Current password": "当前密码", "New password": "新密码",
  "Change password": "修改密码", "Password changed.": "密码已修改。",

  // Setup
  "Set up Mailhearth": "初始化 Mailhearth", "Your organisation": "你的组织", "Connect Purelymail": "连接 Purelymail", "Import": "导入",
  "Organisation name": "组织名称", "Administrator name": "管理员姓名", "Administrator email": "管理员邮箱",
  "Create organisation": "创建组织", "Purelymail API token": "Purelymail API 令牌",
  "Checking…": "校验中…", "Found on your Purelymail account": "在你的 Purelymail 账户中发现", "domains": "个域名", "addresses": "个地址", "mailboxes": "个邮箱",
  "routing rules": "条路由规则", "credit": "余额",
  "Connect my own mailbox": "连接我自己的邮箱",
  "None for now": "暂不连接", "Import and finish": "导入并完成", "Importing…": "导入中…",
  "Imported {d} domains, {m} mailboxes and {a} addresses.": "已导入 {d} 个域名、{m} 个邮箱和 {a} 个地址。", "Warnings": "警告",
  "Go to your mail": "进入邮箱", "Open the admin console": "打开管理控制台",
  "Development stack is active: this instance talks to a fake Purelymail and an in-memory mail server.": "开发模式已启用：本实例连接的是模拟的 Purelymail 和内存邮件服务器。",

  // Mail
  "Compose": "写邮件", "Inbox": "收件箱", "Drafts": "草稿箱", "Sent": "已发送", "Trash": "已删除", "Junk": "垃圾邮件", "Archive": "归档",
  "Folders": "文件夹", "New folder": "新建文件夹", "Rename folder": "重命名文件夹", "Delete folder": "删除文件夹", "Folder name": "文件夹名称",
  "Delete folder “{name}” and all its messages?": "删除文件夹“{name}”及其所有邮件？", "Mailboxes": "邮箱", "Shared mailboxes": "共享邮箱",
  "No messages": "没有邮件", "No results for “{q}”": "没有找到与“{q}”匹配的邮件", "Load more": "加载更多", "Select all": "全选",
  "{n} selected": "已选择 {n} 封", "Mark as read": "标为已读", "Mark as unread": "标为未读", "Flag": "加星标", "Unflag": "取消星标",
  "Move to": "移动到", "Archive message": "归档", "Delete permanently": "永久删除", "Reply": "回复", "Reply all": "回复全部",
  "Forward": "转发", "Forward as attachment": "作为附件转发", "Download": "下载", "Download original (.eml)": "下载原始邮件 (.eml)",
  "Print": "打印", "Attachments": "附件", "attachment": "个附件", "This message has remote images.": "此邮件包含远程图片。",
  "Show images": "显示图片", "Message is too large to show completely.": "邮件过大，仅显示了一部分。", "From": "发件人", "To": "收件人",
  "Cc": "抄送", "Bcc": "密送", "Date": "日期", "Subject": "主题", "(no subject)": "（无主题）", "Show details": "显示详情",
  "Hide details": "隐藏详情", "Select a message to read": "选择一封邮件开始阅读", "Read-only access": "只读访问",
  "Search results": "搜索结果", "Clear": "清除", "Today": "今天", "Yesterday": "昨天", "Moved to {f}": "已移动到 {f}", "Deleted": "已删除",
  "Undo": "撤销", "Sending…": "发送中…", "Sent.": "已发送。", "Draft saved.": "草稿已保存。", "Draft saved": "草稿已保存",
  "Save draft": "保存草稿", "Discard": "丢弃", "Discard this message?": "丢弃这封邮件？", "Add recipients": "添加收件人",
  "Attach files": "添加附件", "Uploading…": "上传中…", "Remove": "移除", "Priority": "优先级", "High": "高", "Normal": "普通", "Low": "低",
  "Write your message…": "写点什么…", "Bold": "粗体", "Italic": "斜体", "Underline": "下划线", "Bulleted list": "项目符号", "Numbered list": "编号列表",
  "Link": "链接", "Quote": "引用", "Remove formatting": "清除格式", "Link URL": "链接地址", "wrote:": "写道：", "Forwarded message": "转发的邮件",
  "New message": "新邮件", "Re: ": "回复：", "Fwd: ": "转发：", "Original attachments": "原邮件附件", "Add at least one recipient.": "请至少添加一个收件人。",
  "Team": "团队", "Assigned to": "负责人", "Unassigned": "未分配", "Assign to me": "分配给我", "Resolve": "标记已处理",
  "Reopen": "重新打开", "Resolved": "已处理", "Open": "处理中", "Internal note": "内部备注", "Add note": "添加备注", "Notes are only visible to your team.": "备注仅团队内部可见。",
  "Activity": "动态", "replied": "回复了", "forwarded": "转发了", "assigned": "分配给", "unassigned": "取消了分配", "resolved": "标记为已处理",
  "reopened": "重新打开", "note": "备注", "Replied by {name}": "{name} 已回复", "New mail in {folder}": "{folder} 有新邮件",
  "{n} new messages": "{n} 封新邮件", "Notifications": "通知", "Enable desktop notifications": "启用桌面通知", "Notifications are blocked in your browser.": "浏览器已禁止通知。",
  "Keyboard shortcuts": "键盘快捷键", "Nothing selected": "未选择邮件", "Message not found. It may have been moved or deleted.": "邮件不存在，可能已被移动或删除。",
  "Rules": "规则", "Mail rules": "邮件规则", "Auto-reply": "自动回复", "Add rule": "添加规则", "Rule name": "规则名称", "When": "当", "all": "全部",
  "any": "任一", "of these match": "条件满足时", "Then": "则", "Add condition": "添加条件", "Add action": "添加动作", "Enabled": "已启用",
  "from": "发件人", "to": "收件人", "subject": "主题", "body": "正文", "header": "邮件头", "size": "大小", "contains": "包含", "not_contains": "不包含",
  "is": "等于", "matches": "匹配通配符", "over": "大于", "under": "小于", "move": "移动到文件夹", "copy": "复制到文件夹", "flag": "加星标",
  "markread": "标为已读", "forward": "转发副本到", "redirect": "重定向到", "discard": "丢弃", "stop": "停止处理后续规则",
  "Rules saved and installed on the server.": "规则已保存并安装到服务器。", "Rules saved.": "规则已保存。", "Auto-reply subject": "自动回复主题",
  "Auto-reply message": "自动回复内容",
  "Rule management is not available for this server.": "当前服务器不支持规则管理。", "Preview Sieve script": "预览 Sieve 脚本",
  "Identities & signatures": "身份与签名", "Display name": "显示名", "Reply-To": "回复地址", "Signature": "签名", "Default": "默认",
  "Make default": "设为默认", "Sender identity saved.": "发件身份已保存。",
  "Signed in as": "当前登录", "Appearance": "外观", "Account": "账户", "Mailbox": "邮箱", "for": "对于",

  // Admin
  "Overview": "概览", "Members": "成员", "Addresses": "地址", "Groups": "群组", "Domains": "域名", "Roles": "角色", "Audit log": "审计日志",
  "Connection": "连接", "Organisation": "组织", "People": "人员", "active": "活跃", "invited": "已邀请", "disabled": "已停用", "departed": "已离职",
  "Needs attention": "需要关注", "No owner": "无归属", "{n} with DNS problems": "{n} 个域名 DNS 未通过", "Connect": "连接",
  "Assign": "分配", "Recent activity": "最近操作", "Everything looks good.": "一切正常。", "Purelymail credit": "Purelymail 余额", "Last sync": "上次同步",
  "Sync now": "立即同步", "Syncing…": "同步中…", "Connections": "连接数", "open": "已打开", "idle": "空闲", "watchers": "监听",
  "Add member": "添加成员", "Onboard a new member": "添加新成员", "Title": "职位", "Department": "部门", "Role": "角色", "Login email": "登录邮箱",
  "Same as the mailbox address if left blank.": "留空则使用邮箱地址。", "Create a new mailbox": "创建新邮箱",
  "Use an existing mailbox": "使用现有邮箱", "No mailbox for now": "暂不设置邮箱", "Mailbox name": "邮箱名", "Domain": "域名",
  "Access to shared mailboxes": "可访问的共享邮箱", "Add to groups": "加入群组", "How will they sign in?": "如何登录？",
  "Send an invite link": "生成邀请链接", "Set a password now": "现在设置密码", "Invite link": "邀请链接",
  "Share this link with {name}. It expires in {days} days.": "把这个链接发给 {name}，{days} 天内有效。", "Member created.": "成员已创建。",
  "New invite link": "生成新邀请链接", "Disable": "停用", "Enable": "启用", "Reset password": "重置密码", "Offboard": "办理离职",
  "Delete member": "删除成员", "Transfer ownership": "转让所有权", "Set a temporary password for {name}. They will be signed out everywhere.": "为 {name} 设置临时密码，其所有会话将被注销。",
  "Temporary password": "临时密码", "Owner": "所有者", "Administrator": "管理员", "Member": "成员", "Last sign-in": "上次登录", "Never": "从未",
  "Offboard {name}": "为 {name} 办理离职",
  "Hand over to": "移交给", "Convert to a shared mailbox": "转为共享邮箱", "Keep, unassigned": "保留但不分配", "Suspend (lock)": "挂起（锁定）",
  "Forward new mail to": "新邮件转发给", "Grant access to": "授予访问权限给", "Remove from groups": "从群组中移除", "Revoke shared mailbox access": "撤销共享邮箱访问",
  "Complete offboarding": "完成离职处理", "Offboarding complete.": "离职处理完成。", "No mailboxes": "没有邮箱",
  "Add mailbox": "添加邮箱", "Shared mailbox": "共享邮箱", "Personal mailbox": "个人邮箱", "Not connected": "未连接", "Connected": "已连接",
  "Kind": "类型", "Access": "访问", "Forwarding": "转发", "Credential": "凭据", "Rotate credential": "轮换凭据",
  "Reset mailbox password": "重置邮箱密码", "Suspend mailbox": "挂起邮箱", "Reactivate": "重新启用", "Delete mailbox": "删除邮箱",
  "Mailhearth opens this mailbox with its own app password. Rotate it if you suspect it leaked.": "Mailhearth 使用独立的应用密码打开此邮箱。如怀疑泄露可轮换。",
  "Password for external clients": "外部客户端密码", "IMAP server": "IMAP 服务器", "SMTP server": "SMTP 服务器", "Username": "用户名",
  "Type the address to confirm": "输入地址以确认",
  "Grant access": "授予访问权限", "Level": "级别", "Revoke": "撤销", "Nobody else has access.": "目前没有其他人可访问。",
  "Forward incoming mail to": "将新邮件转发到",
  "Comma-separated addresses": "多个地址用逗号分隔", "Stop forwarding": "停止转发", "Related addresses": "关联地址",
  "Add address": "添加地址", "Alias": "别名", "Catch-all": "全收地址", "Prefix": "前缀匹配", "Group": "群组", "Primary": "主地址",
  "member": "成员", "Time": "时间",
  "Delivers to": "投递到", "Targets": "目标", "Local part": "@ 前的部分",
  "Delivers to mailbox": "投递到邮箱", "Note": "备注",
  "Delete address {a}?": "删除地址 {a}？", "Address created.": "地址已创建。", "Address updated.": "地址已更新。",
  "Add group": "添加群组", "Description": "描述", "Distribution address": "分发地址",
  "No distribution address": "不设置分发地址", "members": "名成员", "Delete group {g}?": "删除群组 {g}？", "Group saved.": "群组已保存。",
  "Add domain": "添加域名", "DNS records": "DNS 记录", "Recheck DNS": "重新检查 DNS", "Ownership": "所有权", "MX": "MX", "SPF": "SPF", "DKIM": "DKIM", "DMARC": "DMARC",
  "Add these records at your DNS provider, then add the domain.": "先在你的 DNS 服务商处添加这些记录，再添加域名。",
  "Type": "类型", "Host": "主机", "Value": "值", "Purpose": "用途", "Shared Purelymail domain": "Purelymail 共享域名", "Passing": "正常", "Failing": "未通过",
  "Account password reset": "允许账户密码找回", "Symbolic subaddressing": "符号子地址（user+tag）", "Remove domain": "移除域名",
  "Domain added.": "域名已添加。", "Checked": "已检查", "Add role": "添加角色", "Permissions": "权限", "Built-in": "内置", "custom": "自定义",
  "org.owner": "组织所有者", "org.manage": "管理组织设置与连接", "domains.manage": "管理域名", "members.manage": "管理成员", "mailboxes.manage": "管理个人邮箱",
  "shared.manage": "管理共享邮箱", "addresses.manage": "管理地址", "groups.manage": "管理群组", "audit.read": "查看审计日志", "billing.read": "查看余额",
  "Who": "操作者", "What": "操作", "Target": "对象", "System": "系统", "Load older": "加载更早",
  "API token": "API 令牌", "Replace token": "更换令牌", "Token updated.": "令牌已更新。", "The token is never shown again after saving.": "保存后令牌不会再次显示。",
  "Sync finished: {n} new mailboxes, {a} new addresses.": "同步完成：新增 {n} 个邮箱、{a} 个地址。", "API endpoint": "API 地址",
  "Rename organisation": "重命名组织", "Organisation renamed.": "组织已重命名。", "Choose the new owner": "选择新的所有者",
  "You will become an administrator.": "你将变为管理员。", "Ownership transferred.": "所有权已转让。",
  "You do not have permission to do this": "你没有权限执行此操作", "sign in required": "需要登录",

  // Audit log
  "created the organisation": "创建了组织", "renamed the organisation": "重命名了组织", "transferred ownership": "转让了所有权", "connected Purelymail": "连接了 Purelymail", "synced with Purelymail": "同步了 Purelymail",
  "added a member": "添加了成员", "updated a member": "更新了成员", "changed member status": "更改了成员状态", "created an invite": "创建了邀请", "reset a password": "重置了密码", "offboarded a member": "为成员办理了离职", "deleted a member": "删除了成员",
  "created a mailbox": "创建了邮箱", "assigned a mailbox": "分配了邮箱", "connected a mailbox": "连接了邮箱", "rotated a mailbox credential": "轮换了邮箱凭据", "reset a mailbox password": "重置了邮箱密码", "suspended a mailbox": "挂起了邮箱", "reactivated a mailbox": "重新启用了邮箱", "updated a mailbox": "更新了邮箱", "deleted a mailbox": "删除了邮箱", "granted mailbox access": "授予了邮箱访问权限", "revoked mailbox access": "撤销了邮箱访问权限", "set mailbox forwarding": "设置了邮箱转发", "cleared mailbox forwarding": "取消了邮箱转发", "handed over a mailbox": "移交了邮箱", "converted a mailbox to shared": "将邮箱转为共享",
  "created an address": "创建了地址", "updated an address": "更新了地址", "deleted an address": "删除了地址", "created a group": "创建了群组", "updated a group": "更新了群组", "deleted a group": "删除了群组", "set a group address": "设置了群组地址", "removed a group address": "移除了群组地址",
  "added a domain": "添加了域名", "updated a domain": "更新了域名", "removed a domain": "移除了域名", "created a role": "创建了角色", "updated a role": "更新了角色", "deleted a role": "删除了角色",
};

const zhTW: Record<string, string> = {
  "Full mailbox address": "完整信箱地址",
  "Report lost credential cleanup": "回報未知憑證清理結果", "Remove the unknown application credential in the provider console before reporting. This cancels the operation and retains already created resources.": "請在服務商管理介面清理未知的應用程式憑證後回報。回報將取消操作，並保留已建立的資源。",
  "Balance and usage by connection": "各連線的餘額與用量", "Values retain the provider's units and scope.": "數值保留服務商提供的單位和範圍。", "Usage": "用量", "balance": "餘額", "incoming": "收件數量", "outgoing": "寄件數量", "storage": "儲存用量", "messages": "封郵件", "provider_credit": "服務商餘額單位", "current_date": "服務商目前日期",
  "Auto-reply is not available for this server.": "目前伺服器無法使用自動回覆。",
  "Operations": "管理操作",
  "All operations": "全部操作",
  "Cancel operation": "取消操作",
  "Retry operation": "重試操作",
  "Check operation result": "核查操作結果",
  "No operations.": "目前沒有管理操作。",
  "View the active operation": "查看進行中的操作",
  "Retries preserve the original request and operation ID. Unknown results require checking.": "重試保留原始請求與操作識別碼。結果未知的操作需要核查。",
  "Sending requests": "寄件請求",
  "Recent sending requests remain available after closing the composer.": "關閉撰寫視窗後仍可查詢最近的寄件請求。",
  "No sending requests.": "目前沒有寄件請求。",
  "Check the mail server using the Message-ID and creation time before sending again.": "重新寄送前，請使用 Message-ID 和建立時間查詢郵件伺服器。",
  "Only the Sent copy is retried. SMTP acceptance is retained.": "僅重新儲存 Sent 副本。已確認的 SMTP 接受狀態保持不變。",
  "Sending request result unavailable. Retry keeps the same request ID.": "暫時無法取得寄件請求結果。重試會使用同一個請求識別碼。",
  "Special folders": "特殊資料夾",
  "Choose existing folders. Automatic selection requires a unique match.": "請選擇現有資料夾。自動選擇需要唯一符合的資料夾。",
  "Automatic selection": "自動選擇",
  "Folder unavailable": "資料夾無法使用",
  "Folder mapping saved.": "已儲存資料夾對應。",
  "Sent copy handling": "寄件副本儲存方式",
  "Mailhearth saves the Sent copy": "由 Mailhearth 儲存寄件副本",
  "Mail server saves the Sent copy": "由郵件伺服器儲存寄件副本",
  "Archive mailbox": "封存信箱",
  "Unregister mailbox": "解除信箱登記",
  "Removes local registration. Remote mail remains on the server.": "解除本機登記。遠端郵件繼續保存在伺服器上。",
  "Revoke these credentials in the external mail service:": "請在外部郵件服務中撤銷以下憑證：",
  "Authorize sending": "授權寄件",
  "Revoke sending authorization": "撤銷寄件授權",
  // Short labels; the long form lives in an ⓘ popover.
  "Search mail": "搜尋郵件",
  "Nothing on Purelymail is changed.": "不會變更 Purelymail 上的任何資料。",
  "Rules run on the mail server, before mail reaches your inbox.": "規則在郵件伺服器上執行，先於郵件進入收件匣。",
  "Repeat interval": "重複間隔",
  "For use in other mail apps. Shown once.": "供其他郵件應用程式使用，只顯示一次。",
  "Everyone loses access. Mail keeps arriving and is kept.": "所有人都會失去存取權。郵件仍會繼續送達並保留。",
  "Deletes the mailbox and all its mail. This cannot be undone.": "將刪除該信箱及其所有郵件，無法復原。",
  "Access ends immediately and every credential is rotated.": "存取權立即終止，所有憑證都會更換。",
  "Deletes every mailbox and rule on this domain.": "將刪除此網域下的所有信箱和規則。",
  "Imports changes made outside Mailhearth.": "匯入在 Mailhearth 之外所做的變更。",
  "Mail to this address reaches every member.": "寄到此地址的郵件會送達所有成員。",
  "New mail skips this mailbox while forwarding is on.": "開啟轉寄後，新郵件不會進入此信箱。",
  "Your sign-in address. It can be an address on your own domain.": "你的登入地址，可以是你自己網域下的地址。",
  "Create it in the Purelymail portal under Account → API. Stored encrypted on this server and never sent to browsers.": "在 Purelymail 後台的 Account → API 建立。權杖在本伺服器加密保存，不會傳送到瀏覽器。",
  "Importing reads your domains, mailboxes and routing rules, and builds the organisation model from them.": "匯入會讀取你的網域、信箱和路由規則，據此建立組織模型。",
  "Pick the mailbox you use yourself, so you can read mail as soon as setup finishes.": "選擇你自己使用的信箱，初始化完成後即可直接收發。",
  "Stored as Sieve. Saving replaces any filters this mailbox has in other mail clients.": "以 Sieve 形式保存。儲存後會取代此信箱在其他郵件軟體設定的篩選器。",
  "Reply at most once every N days to the same sender.": "對同一位寄件者最多每 N 天回覆一次。",
  "Re-reads domains, mailboxes and routing rules from Purelymail and updates the organisation model.": "重新讀取 Purelymail 的網域、信箱和路由規則，更新組織模型。",
  "Another address for one mailbox.": "同一個信箱的另一個地址。",
  "Sends mail on to one or more addresses, inside or outside the organisation.": "轉寄給一個或多個地址，可以在組織外。",
  "Receives anything at this domain that has no mailbox of its own.": "接收此網域下所有沒有對應信箱的地址。",
  "Receives every address starting with this prefix.": "接收以此前綴開頭的所有地址。",

  // Common
  "Mail": "郵件", "Admin": "管理", "Settings": "設定", "Sign out": "登出", "Sign in": "登入", "Cancel": "取消", "Save": "儲存",
  "Delete": "刪除", "Close": "關閉", "Create": "建立", "Edit": "編輯", "Back": "返回", "Next": "下一步", "Done": "完成", "Search": "搜尋",
  "Loading…": "載入中…", "Nothing here yet.": "這裡還沒有內容。", "Yes": "是", "No": "否", "Name": "名稱", "Actions": "操作", "Status": "狀態",
  "Copy": "複製", "Copied": "已複製", "Refresh": "重新整理", "Confirm": "確認", "Optional": "選填", "Required": "必填", "Unknown": "未知",
  "Something went wrong.": "出了點問題。", "Retry": "重試", "Continue": "繼續", "Password": "密碼", "Email": "電子郵件", "Language": "語言",
  "Active": "使用中", "Invited": "已邀請", "Disabled": "已停用", "Departed": "已離職", "Suspended": "已暫停", "Archived": "已封存",
  "Personal": "個人", "Shared": "共用", "personal": "個人", "shared": "共用", "Full": "完整", "Send": "傳送", "Read": "唯讀",
  "full": "完整存取", "send": "可讀可寄", "read": "唯讀",

  // Auth
  "Welcome back": "歡迎回來", "Email or mailbox address": "登入信箱或信箱地址", "Incorrect email or password": "信箱或密碼不正確",
  "Signing in…": "登入中…", "You have been invited": "你收到了一份邀請", "Set a password to activate your account.": "設定密碼以啟用你的帳戶。",
  "Your name": "你的姓名", "Choose a password": "設定密碼", "At least 10 characters.": "至少 10 個字元。", "Activate account": "啟用帳戶",
  "This invite link is invalid or has expired.": "邀請連結無效或已過期。", "Current password": "目前密碼", "New password": "新密碼",
  "Change password": "修改密碼", "Password changed.": "密碼已修改。",

  // Setup
  "Set up Mailhearth": "初始化 Mailhearth", "Your organisation": "你的組織", "Connect Purelymail": "連接 Purelymail", "Import": "匯入",
  "Organisation name": "組織名稱", "Administrator name": "管理員姓名", "Administrator email": "管理員信箱",
  "Create organisation": "建立組織", "Purelymail API token": "Purelymail API 權杖",
  "Checking…": "檢查中…", "Found on your Purelymail account": "在你的 Purelymail 帳戶中發現", "domains": "個網域", "addresses": "個地址", "mailboxes": "個信箱",
  "routing rules": "條路由規則", "credit": "餘額",
  "Connect my own mailbox": "連接我自己的信箱",
  "None for now": "暫不連接", "Import and finish": "匯入並完成", "Importing…": "匯入中…",
  "Imported {d} domains, {m} mailboxes and {a} addresses.": "已匯入 {d} 個網域、{m} 個信箱和 {a} 個地址。", "Warnings": "警告",
  "Go to your mail": "進入信箱", "Open the admin console": "開啟管理主控台",
  "Development stack is active: this instance talks to a fake Purelymail and an in-memory mail server.": "開發模式已啟用：本實例連接的是模擬的 Purelymail 和記憶體郵件伺服器。",

  // Mail
  "Compose": "撰寫郵件", "Inbox": "收件匣", "Drafts": "草稿", "Sent": "寄件備份", "Trash": "垃圾桶", "Junk": "垃圾郵件", "Archive": "封存",
  "Folders": "資料夾", "New folder": "新增資料夾", "Rename folder": "重新命名資料夾", "Delete folder": "刪除資料夾", "Folder name": "資料夾名稱",
  "Delete folder “{name}” and all its messages?": "刪除資料夾「{name}」及其所有郵件？", "Mailboxes": "信箱", "Shared mailboxes": "共用信箱",
  "No messages": "沒有郵件", "No results for “{q}”": "沒有找到與「{q}」相符的郵件", "Load more": "載入更多", "Select all": "全選",
  "{n} selected": "已選擇 {n} 封", "Mark as read": "標示為已讀", "Mark as unread": "標示為未讀", "Flag": "加星號", "Unflag": "取消星號",
  "Move to": "移動到", "Archive message": "封存", "Delete permanently": "永久刪除", "Reply": "回覆", "Reply all": "回覆全部",
  "Forward": "轉寄", "Forward as attachment": "以附件轉寄", "Download": "下載", "Download original (.eml)": "下載原始郵件 (.eml)",
  "Print": "列印", "Attachments": "附件", "attachment": "個附件", "This message has remote images.": "此郵件包含遠端圖片。",
  "Show images": "顯示圖片", "Message is too large to show completely.": "郵件過大，僅顯示部分內容。", "From": "寄件者", "To": "收件者",
  "Cc": "副本", "Bcc": "密件副本", "Date": "日期", "Subject": "主旨", "(no subject)": "（無主旨）", "Show details": "顯示詳細資料",
  "Hide details": "隱藏詳細資料", "Select a message to read": "選擇一封郵件開始閱讀", "Read-only access": "唯讀存取",
  "Search results": "搜尋結果", "Clear": "清除", "Today": "今天", "Yesterday": "昨天", "Moved to {f}": "已移動到 {f}", "Deleted": "已刪除",
  "Undo": "復原", "Sending…": "傳送中…", "Sent.": "已傳送。", "Draft saved.": "草稿已儲存。", "Draft saved": "草稿已儲存",
  "Save draft": "儲存草稿", "Discard": "捨棄", "Discard this message?": "捨棄這封郵件？", "Add recipients": "新增收件者",
  "Attach files": "加入附件", "Uploading…": "上傳中…", "Remove": "移除", "Priority": "優先順序", "High": "高", "Normal": "一般", "Low": "低",
  "Write your message…": "撰寫訊息…", "Bold": "粗體", "Italic": "斜體", "Underline": "底線", "Bulleted list": "項目符號", "Numbered list": "編號清單",
  "Link": "連結", "Quote": "引用", "Remove formatting": "清除格式", "Link URL": "連結網址", "wrote:": "寫道：", "Forwarded message": "轉寄的郵件",
  "New message": "新郵件", "Re: ": "回覆：", "Fwd: ": "轉寄：", "Original attachments": "原郵件附件", "Add at least one recipient.": "請至少加入一位收件者。",
  "Team": "團隊", "Assigned to": "負責人", "Unassigned": "未指派", "Assign to me": "指派給我", "Resolve": "標示為已處理",
  "Reopen": "重新開啟", "Resolved": "已處理", "Open": "處理中", "Internal note": "內部備註", "Add note": "新增備註", "Notes are only visible to your team.": "備註僅團隊內部可見。",
  "Activity": "動態", "replied": "回覆了", "forwarded": "轉寄了", "assigned": "指派給", "unassigned": "取消指派", "resolved": "標示為已處理",
  "reopened": "重新開啟", "note": "備註", "Replied by {name}": "{name} 已回覆", "New mail in {folder}": "{folder} 有新郵件",
  "{n} new messages": "{n} 封新郵件", "Notifications": "通知", "Enable desktop notifications": "啟用桌面通知", "Notifications are blocked in your browser.": "瀏覽器已封鎖通知。",
  "Keyboard shortcuts": "鍵盤快速鍵", "Nothing selected": "未選擇郵件", "Message not found. It may have been moved or deleted.": "找不到郵件，可能已被移動或刪除。",
  "Rules": "規則", "Mail rules": "郵件規則", "Auto-reply": "自動回覆", "Add rule": "新增規則", "Rule name": "規則名稱", "When": "當", "all": "全部",
  "any": "任一", "of these match": "符合以下條件時", "Then": "則", "Add condition": "新增條件", "Add action": "新增動作", "Enabled": "已啟用",
  "from": "寄件者", "to": "收件者", "subject": "主旨", "body": "內文", "header": "郵件標頭", "size": "大小", "contains": "包含", "not_contains": "不包含",
  "is": "等於", "matches": "符合萬用字元", "over": "大於", "under": "小於", "move": "移動到資料夾", "copy": "複製到資料夾", "flag": "加星號",
  "markread": "標示為已讀", "forward": "轉寄副本到", "redirect": "重導到", "discard": "捨棄", "stop": "停止處理後續規則",
  "Rules saved and installed on the server.": "規則已儲存並安裝到伺服器。", "Rules saved.": "規則已儲存。", "Auto-reply subject": "自動回覆主旨",
  "Auto-reply message": "自動回覆內容",
  "Rule management is not available for this server.": "目前伺服器不支援規則管理。", "Preview Sieve script": "預覽 Sieve 指令碼",
  "Identities & signatures": "身分與簽名檔", "Display name": "顯示名稱", "Reply-To": "回覆地址", "Signature": "簽名檔", "Default": "預設",
  "Make default": "設為預設", "Sender identity saved.": "寄件身分已儲存。",
  "Signed in as": "目前登入", "Appearance": "外觀", "Account": "帳戶", "Mailbox": "信箱", "for": "對於",

  // Admin
  "Overview": "總覽", "Members": "成員", "Addresses": "地址", "Groups": "群組", "Domains": "網域", "Roles": "角色", "Audit log": "稽核記錄",
  "Connection": "連線", "Organisation": "組織", "People": "人員", "active": "使用中", "invited": "已邀請", "disabled": "已停用", "departed": "已離職",
  "Needs attention": "需要關注", "No owner": "沒有擁有者", "{n} with DNS problems": "{n} 個網域 DNS 有問題", "Connect": "連線",
  "Assign": "指派", "Recent activity": "最近活動", "Everything looks good.": "一切正常。", "Purelymail credit": "Purelymail 餘額", "Last sync": "上次同步",
  "Sync now": "立即同步", "Syncing…": "同步中…", "Connections": "連線數", "open": "使用中", "idle": "閒置", "watchers": "監聽",
  "Add member": "新增成員", "Onboard a new member": "加入新成員", "Title": "職稱", "Department": "部門", "Role": "角色", "Login email": "登入信箱",
  "Same as the mailbox address if left blank.": "留空則使用信箱地址。", "Create a new mailbox": "建立新信箱",
  "Use an existing mailbox": "使用現有信箱", "No mailbox for now": "暫不設定信箱", "Mailbox name": "信箱名稱", "Domain": "網域",
  "Access to shared mailboxes": "可存取的共用信箱", "Add to groups": "加入群組", "How will they sign in?": "如何登入？",
  "Send an invite link": "產生邀請連結", "Set a password now": "現在設定密碼", "Invite link": "邀請連結",
  "Share this link with {name}. It expires in {days} days.": "把這個連結發給 {name}，{days} 天內有效。", "Member created.": "成員已建立。",
  "New invite link": "產生新邀請連結", "Disable": "停用", "Enable": "啟用", "Reset password": "重設密碼", "Offboard": "辦理離職",
  "Delete member": "刪除成員", "Transfer ownership": "轉讓所有權", "Set a temporary password for {name}. They will be signed out everywhere.": "為 {name} 設定臨時密碼，其所有工作階段將被登出。",
  "Temporary password": "臨時密碼", "Owner": "擁有者", "Administrator": "管理員", "Member": "成員", "Last sign-in": "上次登入", "Never": "從未",
  "Offboard {name}": "為 {name} 辦理離職",
  "Hand over to": "移交給", "Convert to a shared mailbox": "轉為共用信箱", "Keep, unassigned": "保留但不指派", "Suspend (lock)": "暫停（鎖定）",
  "Forward new mail to": "新郵件轉寄給", "Grant access to": "授予存取權限給", "Remove from groups": "從群組中移除", "Revoke shared mailbox access": "撤銷共用信箱存取",
  "Complete offboarding": "完成離職處理", "Offboarding complete.": "離職處理完成。", "No mailboxes": "沒有信箱",
  "Add mailbox": "新增信箱", "Shared mailbox": "共用信箱", "Personal mailbox": "個人信箱", "Not connected": "未連線", "Connected": "已連線",
  "Kind": "類型", "Access": "存取", "Forwarding": "轉寄", "Credential": "憑證", "Rotate credential": "更換憑證",
  "Reset mailbox password": "重設信箱密碼", "Suspend mailbox": "暫停信箱", "Reactivate": "重新啟用", "Delete mailbox": "刪除信箱",
  "Mailhearth opens this mailbox with its own app password. Rotate it if you suspect it leaked.": "Mailhearth 使用專屬的應用程式密碼開啟此信箱。如懷疑外洩可更換。",
  "Password for external clients": "外部用戶端密碼", "IMAP server": "IMAP 伺服器", "SMTP server": "SMTP 伺服器", "Username": "使用者名稱",
  "Type the address to confirm": "輸入地址以確認",
  "Grant access": "授予存取權限", "Level": "層級", "Revoke": "撤銷", "Nobody else has access.": "目前沒有其他人可存取。",
  "Forward incoming mail to": "將新郵件轉寄到",
  "Comma-separated addresses": "多個地址用逗號分隔", "Stop forwarding": "停止轉寄", "Related addresses": "關聯地址",
  "Add address": "新增地址", "Alias": "別名", "Catch-all": "全收地址", "Prefix": "前綴比對", "Group": "群組", "Primary": "主地址",
  "member": "成員", "Time": "時間",
  "Delivers to": "投遞到", "Targets": "目標", "Local part": "@ 前面的部分",
  "Delivers to mailbox": "投遞到信箱", "Note": "備註",
  "Delete address {a}?": "刪除地址 {a}？", "Address created.": "地址已建立。", "Address updated.": "地址已更新。",
  "Add group": "新增群組", "Description": "說明", "Distribution address": "分發地址",
  "No distribution address": "不設定分發地址", "members": "名成員", "Delete group {g}?": "刪除群組 {g}？", "Group saved.": "群組已儲存。",
  "Add domain": "新增網域", "DNS records": "DNS 記錄", "Recheck DNS": "重新檢查 DNS", "Ownership": "所有權", "MX": "MX", "SPF": "SPF", "DKIM": "DKIM", "DMARC": "DMARC",
  "Add these records at your DNS provider, then add the domain.": "先在你的 DNS 服務商新增這些記錄，再新增網域。",
  "Type": "類型", "Host": "主機", "Value": "值", "Purpose": "用途", "Shared Purelymail domain": "Purelymail 共用網域", "Passing": "正常", "Failing": "未通過",
  "Account password reset": "允許帳戶密碼重設", "Symbolic subaddressing": "符號子地址（user+tag）", "Remove domain": "移除網域",
  "Domain added.": "網域已新增。", "Checked": "已檢查", "Add role": "新增角色", "Permissions": "權限", "Built-in": "內建", "custom": "自訂",
  "org.owner": "組織擁有者", "org.manage": "管理組織設定與連線", "domains.manage": "管理網域", "members.manage": "管理成員", "mailboxes.manage": "管理個人信箱",
  "shared.manage": "管理共用信箱", "addresses.manage": "管理地址", "groups.manage": "管理群組", "audit.read": "檢視稽核記錄", "billing.read": "檢視餘額",
  "Who": "操作者", "What": "操作", "Target": "對象", "System": "系統", "Load older": "載入更早",
  "API token": "API 權杖", "Replace token": "更換權杖", "Token updated.": "權杖已更新。", "The token is never shown again after saving.": "儲存後權杖不會再次顯示。",
  "Sync finished: {n} new mailboxes, {a} new addresses.": "同步完成：新增 {n} 個信箱、{a} 個地址。", "API endpoint": "API 位址",
  "Rename organisation": "重新命名組織", "Organisation renamed.": "組織已重新命名。", "Choose the new owner": "選擇新的擁有者",
  "You will become an administrator.": "你將變為管理員。", "Ownership transferred.": "所有權已轉讓。",
  "You do not have permission to do this": "你沒有權限執行此操作", "sign in required": "需要登入",

  // Audit log
  "created the organisation": "建立了組織", "renamed the organisation": "重新命名了組織", "transferred ownership": "轉讓了所有權", "connected Purelymail": "連線了 Purelymail", "synced with Purelymail": "與 Purelymail 同步了",
  "added a member": "新增了成員", "updated a member": "更新了成員", "changed member status": "變更了成員狀態", "created an invite": "建立了邀請", "reset a password": "重設了密碼", "offboarded a member": "為成員辦理了離職", "deleted a member": "刪除了成員",
  "created a mailbox": "建立了信箱", "assigned a mailbox": "指派了信箱", "connected a mailbox": "連線了信箱", "rotated a mailbox credential": "更換了信箱憑證", "reset a mailbox password": "重設了信箱密碼", "suspended a mailbox": "暫停了信箱", "reactivated a mailbox": "重新啟用了信箱", "updated a mailbox": "更新了信箱", "deleted a mailbox": "刪除了信箱", "granted mailbox access": "授予了信箱存取權限", "revoked mailbox access": "撤銷了信箱存取權限", "set mailbox forwarding": "設定了信箱轉寄", "cleared mailbox forwarding": "取消了信箱轉寄", "handed over a mailbox": "移交了信箱", "converted a mailbox to shared": "將信箱轉為共用",
  "created an address": "建立了地址", "updated an address": "更新了地址", "deleted an address": "刪除了地址", "created a group": "建立了群組", "updated a group": "更新了群組", "deleted a group": "刪除了群組", "set a group address": "設定了群組地址", "removed a group address": "移除了群組地址",
  "added a domain": "新增了網域", "updated a domain": "更新了網域", "removed a domain": "移除了網域", "created a role": "建立了角色", "updated a role": "更新了角色", "deleted a role": "刪除了角色",
};

const ja: Record<string, string> = {
  "Full mailbox address": "完全なメールアドレス",
  "Report lost credential cleanup": "不明な認証情報の削除を報告", "Remove the unknown application credential in the provider console before reporting. This cancels the operation and retains already created resources.": "プロバイダーの管理画面で不明なアプリ認証情報を削除してから報告してください。操作はキャンセルされ、作成済みのリソースは保持されます。",
  "Balance and usage by connection": "接続別の残高と使用量", "Values retain the provider's units and scope.": "数値はプロバイダーの単位と範囲で表示されます。", "Usage": "使用量", "balance": "残高", "incoming": "受信数", "outgoing": "送信数", "storage": "ストレージ使用量", "messages": "通", "provider_credit": "プロバイダーの残高単位", "current_date": "プロバイダーの現在日付",
  "Auto-reply is not available for this server.": "このサーバーでは自動返信を利用できません。",
  "Operations": "管理操作",
  "All operations": "すべての操作",
  "Cancel operation": "操作をキャンセル",
  "Retry operation": "操作を再試行",
  "Check operation result": "操作結果を確認",
  "No operations.": "管理操作はありません。",
  "View the active operation": "進行中の操作を表示",
  "Retries preserve the original request and operation ID. Unknown results require checking.": "再試行では元のリクエストと操作 ID を維持します。結果が不明な操作は確認が必要です。",
  "Sending requests": "送信リクエスト",
  "Recent sending requests remain available after closing the composer.": "作成画面を閉じた後も最近の送信リクエストを確認できます。",
  "No sending requests.": "送信リクエストはありません。",
  "Check the mail server using the Message-ID and creation time before sending again.": "再送信する前に Message-ID と作成時刻でメールサーバーを確認してください。",
  "Only the Sent copy is retried. SMTP acceptance is retained.": "Sent コピーの保存のみを再試行します。SMTP の受理状態は維持されます。",
  "Sending request result unavailable. Retry keeps the same request ID.": "送信リクエストの結果を取得できません。再試行では同じ ID を使用します。",
  "Special folders": "特殊フォルダー",
  "Choose existing folders. Automatic selection requires a unique match.": "既存のフォルダーを選択してください。自動選択には一意の一致が必要です。",
  "Automatic selection": "自動選択",
  "Folder unavailable": "フォルダーを利用できません",
  "Folder mapping saved.": "フォルダーの割り当てを保存しました。",
  "Sent copy handling": "送信済みコピーの保存方法",
  "Mailhearth saves the Sent copy": "Mailhearth が送信済みコピーを保存",
  "Mail server saves the Sent copy": "メールサーバーが送信済みコピーを保存",
  "Archive mailbox": "メールボックスをアーカイブ",
  "Unregister mailbox": "メールボックスの登録を解除",
  "Removes local registration. Remote mail remains on the server.": "ローカル登録を解除します。メールはサーバー上に保存されます。",
  "Revoke these credentials in the external mail service:": "外部メールサービスで次の認証情報を取り消してください：",
  "Authorize sending": "送信を許可",
  "Revoke sending authorization": "送信許可を取り消す",
  // Short labels; the long form lives in an ⓘ popover.
  "Search mail": "メールを検索",
  "Nothing on Purelymail is changed.": "Purelymail 上のデータは一切変更されません。",
  "Rules run on the mail server, before mail reaches your inbox.": "ルールはメールサーバー上で、メールが受信トレイに届く前に実行されます。",
  "Repeat interval": "繰り返し間隔",
  "For use in other mail apps. Shown once.": "他のメールアプリで使用します。表示されるのは一度だけです。",
  "Everyone loses access. Mail keeps arriving and is kept.": "全員がアクセスできなくなります。メールは引き続き受信され、保存されます。",
  "Deletes the mailbox and all its mail. This cannot be undone.": "メールボックスとそのすべてのメールを削除します。元に戻せません。",
  "Access ends immediately and every credential is rotated.": "アクセスは直ちに終了し、すべての認証情報が更新されます。",
  "Deletes every mailbox and rule on this domain.": "このドメインのすべてのメールボックスとルールを削除します。",
  "Imports changes made outside Mailhearth.": "Mailhearth の外部で行われた変更を取り込みます。",
  "Mail to this address reaches every member.": "このアドレス宛のメールは全メンバーに届きます。",
  "New mail skips this mailbox while forwarding is on.": "転送が有効な間、新しいメールはこのメールボックスに入りません。",
  "Your sign-in address. It can be an address on your own domain.": "サインインに使うアドレスです。独自ドメインのアドレスも使えます。",
  "Create it in the Purelymail portal under Account → API. Stored encrypted on this server and never sent to browsers.": "Purelymail のポータルで Account → API から作成します。このサーバーに暗号化して保存され、ブラウザーには送信されません。",
  "Importing reads your domains, mailboxes and routing rules, and builds the organisation model from them.": "取り込みではドメイン、メールボックス、ルーティングルールを読み取り、それらから組織モデルを構築します。",
  "Pick the mailbox you use yourself, so you can read mail as soon as setup finishes.": "自分で使うメールボックスを選ぶと、セットアップの完了後すぐにメールを読めます。",
  "Stored as Sieve. Saving replaces any filters this mailbox has in other mail clients.": "Sieve として保存されます。保存すると、このメールボックスが他のメールソフトで持つフィルターは置き換わります。",
  "Reply at most once every N days to the same sender.": "同じ送信者には N 日に一度だけ返信します。",
  "Re-reads domains, mailboxes and routing rules from Purelymail and updates the organisation model.": "Purelymail からドメイン、メールボックス、ルーティングルールを読み直し、組織モデルを更新します。",
  "Another address for one mailbox.": "1 つのメールボックスに対する別のアドレスです。",
  "Sends mail on to one or more addresses, inside or outside the organisation.": "組織の内外を問わず、1 つ以上のアドレスへメールを転送します。",
  "Receives anything at this domain that has no mailbox of its own.": "このドメインで専用のメールボックスがないアドレスをすべて受信します。",
  "Receives every address starting with this prefix.": "この接頭辞で始まるすべてのアドレスを受信します。",

  // Common
  "Mail": "メール", "Admin": "管理", "Settings": "設定", "Sign out": "サインアウト", "Sign in": "サインイン", "Cancel": "キャンセル", "Save": "保存",
  "Delete": "削除", "Close": "閉じる", "Create": "作成", "Edit": "編集", "Back": "戻る", "Next": "次へ", "Done": "完了", "Search": "検索",
  "Loading…": "読み込み中…", "Nothing here yet.": "まだ何もありません。", "Yes": "はい", "No": "いいえ", "Name": "名前", "Actions": "操作", "Status": "状態",
  "Copy": "コピー", "Copied": "コピーしました", "Refresh": "更新", "Confirm": "確認", "Optional": "任意", "Required": "必須", "Unknown": "不明",
  "Something went wrong.": "問題が発生しました。", "Retry": "再試行", "Continue": "続行", "Password": "パスワード", "Email": "メールアドレス", "Language": "言語",
  "Active": "有効", "Invited": "招待済み", "Disabled": "無効", "Departed": "退職済み", "Suspended": "停止中", "Archived": "アーカイブ済み",
  "Personal": "個人", "Shared": "共有", "personal": "個人", "shared": "共有", "Full": "フル", "Send": "送信", "Read": "閲覧のみ",
  "full": "フルアクセス", "send": "閲覧と送信", "read": "閲覧のみ",

  // Auth
  "Welcome back": "おかえりなさい", "Email or mailbox address": "メールアドレスまたはメールボックスのアドレス", "Incorrect email or password": "メールアドレスまたはパスワードが正しくありません",
  "Signing in…": "サインイン中…", "You have been invited": "招待が届いています", "Set a password to activate your account.": "パスワードを設定してアカウントを有効にします。",
  "Your name": "あなたの名前", "Choose a password": "パスワードを設定", "At least 10 characters.": "10 文字以上。", "Activate account": "アカウントを有効にする",
  "This invite link is invalid or has expired.": "この招待リンクは無効か、有効期限が切れています。", "Current password": "現在のパスワード", "New password": "新しいパスワード",
  "Change password": "パスワードを変更", "Password changed.": "パスワードを変更しました。",

  // Setup
  "Set up Mailhearth": "Mailhearth のセットアップ", "Your organisation": "組織", "Connect Purelymail": "Purelymail に接続", "Import": "取り込み",
  "Organisation name": "組織名", "Administrator name": "管理者の名前", "Administrator email": "管理者のメールアドレス",
  "Create organisation": "組織を作成", "Purelymail API token": "Purelymail API トークン",
  "Checking…": "確認中…", "Found on your Purelymail account": "Purelymail アカウントで見つかったもの", "domains": "ドメイン", "addresses": "アドレス", "mailboxes": "メールボックス",
  "routing rules": "ルーティングルール", "credit": "残高",
  "Connect my own mailbox": "自分のメールボックスを接続",
  "None for now": "今は接続しない", "Import and finish": "取り込んで完了", "Importing…": "取り込み中…",
  "Imported {d} domains, {m} mailboxes and {a} addresses.": "{d} 個のドメイン、{m} 個のメールボックス、{a} 個のアドレスを取り込みました。", "Warnings": "警告",
  "Go to your mail": "メールへ移動", "Open the admin console": "管理コンソールを開く",
  "Development stack is active: this instance talks to a fake Purelymail and an in-memory mail server.": "開発用スタックが有効です。このインスタンスは疑似 Purelymail とメモリ上のメールサーバーに接続しています。",

  // Mail
  "Compose": "作成", "Inbox": "受信トレイ", "Drafts": "下書き", "Sent": "送信済み", "Trash": "ゴミ箱", "Junk": "迷惑メール", "Archive": "アーカイブ",
  "Folders": "フォルダー", "New folder": "新しいフォルダー", "Rename folder": "フォルダー名を変更", "Delete folder": "フォルダーを削除", "Folder name": "フォルダー名",
  "Delete folder “{name}” and all its messages?": "フォルダー「{name}」とそのすべてのメールを削除しますか？", "Mailboxes": "メールボックス", "Shared mailboxes": "共有メールボックス",
  "No messages": "メールはありません", "No results for “{q}”": "「{q}」に一致する結果はありません", "Load more": "さらに読み込む", "Select all": "すべて選択",
  "{n} selected": "{n} 件を選択中", "Mark as read": "既読にする", "Mark as unread": "未読にする", "Flag": "スターを付ける", "Unflag": "スターを外す",
  "Move to": "移動先", "Archive message": "アーカイブ", "Delete permanently": "完全に削除", "Reply": "返信", "Reply all": "全員に返信",
  "Forward": "転送", "Forward as attachment": "添付ファイルとして転送", "Download": "ダウンロード", "Download original (.eml)": "元のメールをダウンロード (.eml)",
  "Print": "印刷", "Attachments": "添付ファイル", "attachment": "件の添付ファイル", "This message has remote images.": "このメールには外部画像が含まれています。",
  "Show images": "画像を表示", "Message is too large to show completely.": "メールが大きすぎるため、全体を表示できません。", "From": "差出人", "To": "宛先",
  "Cc": "Cc", "Bcc": "Bcc", "Date": "日付", "Subject": "件名", "(no subject)": "（件名なし）", "Show details": "詳細を表示",
  "Hide details": "詳細を隠す", "Select a message to read": "読むメールを選択してください", "Read-only access": "閲覧のみ",
  "Search results": "検索結果", "Clear": "クリア", "Today": "今日", "Yesterday": "昨日", "Moved to {f}": "{f} に移動しました", "Deleted": "削除しました",
  "Undo": "元に戻す", "Sending…": "送信中…", "Sent.": "送信しました。", "Draft saved.": "下書きを保存しました。", "Draft saved": "下書きを保存しました",
  "Save draft": "下書きを保存", "Discard": "破棄", "Discard this message?": "このメッセージを破棄しますか？", "Add recipients": "宛先を追加",
  "Attach files": "ファイルを添付", "Uploading…": "アップロード中…", "Remove": "削除", "Priority": "優先度", "High": "高", "Normal": "標準", "Low": "低",
  "Write your message…": "メッセージを入力…", "Bold": "太字", "Italic": "斜体", "Underline": "下線", "Bulleted list": "箇条書き", "Numbered list": "番号付きリスト",
  "Link": "リンク", "Quote": "引用", "Remove formatting": "書式を解除", "Link URL": "リンク URL", "wrote:": "さんのメッセージ:", "Forwarded message": "転送されたメッセージ",
  "New message": "新規メッセージ", "Re: ": "Re: ", "Fwd: ": "Fwd: ", "Original attachments": "元の添付ファイル", "Add at least one recipient.": "宛先を 1 件以上追加してください。",
  "Team": "チーム", "Assigned to": "担当者", "Unassigned": "未割り当て", "Assign to me": "自分に割り当てる", "Resolve": "解決済みにする",
  "Reopen": "再開する", "Resolved": "解決済み", "Open": "対応中", "Internal note": "社内メモ", "Add note": "メモを追加", "Notes are only visible to your team.": "メモはチーム内だけに表示されます。",
  "Activity": "履歴", "replied": "返信しました", "forwarded": "転送しました", "assigned": "割り当てました", "unassigned": "割り当てを解除しました", "resolved": "解決済みにしました",
  "reopened": "再開しました", "note": "メモ", "Replied by {name}": "{name} が返信しました", "New mail in {folder}": "{folder} に新着メール",
  "{n} new messages": "新着メール {n} 件", "Notifications": "通知", "Enable desktop notifications": "デスクトップ通知を有効にする", "Notifications are blocked in your browser.": "ブラウザーで通知がブロックされています。",
  "Keyboard shortcuts": "キーボードショートカット", "Nothing selected": "何も選択されていません", "Message not found. It may have been moved or deleted.": "メッセージが見つかりません。移動または削除された可能性があります。",
  "Rules": "ルール", "Mail rules": "メールルール", "Auto-reply": "自動返信", "Add rule": "ルールを追加", "Rule name": "ルール名", "When": "条件", "all": "すべて",
  "any": "いずれか", "of these match": "が一致したとき", "Then": "実行", "Add condition": "条件を追加", "Add action": "アクションを追加", "Enabled": "有効",
  "from": "差出人", "to": "宛先", "subject": "件名", "body": "本文", "header": "ヘッダー", "size": "サイズ", "contains": "含む", "not_contains": "含まない",
  "is": "等しい", "matches": "ワイルドカード一致", "over": "より大きい", "under": "より小さい", "move": "フォルダーへ移動", "copy": "フォルダーへコピー", "flag": "スターを付ける",
  "markread": "既読にする", "forward": "転送先", "redirect": "リダイレクト先", "discard": "破棄", "stop": "以降のルールを停止",
  "Rules saved and installed on the server.": "ルールを保存し、サーバーにインストールしました。", "Rules saved.": "ルールを保存しました。", "Auto-reply subject": "自動返信の件名",
  "Auto-reply message": "自動返信の本文",
  "Rule management is not available for this server.": "このサーバーではルール管理を利用できません。", "Preview Sieve script": "Sieve スクリプトを表示",
  "Identities & signatures": "アイデンティティと署名", "Display name": "表示名", "Reply-To": "返信先", "Signature": "署名", "Default": "既定",
  "Make default": "既定にする", "Sender identity saved.": "送信元の情報を保存しました。",
  "Signed in as": "サインイン中", "Appearance": "外観", "Account": "アカウント", "Mailbox": "メールボックス", "for": "対象",

  // Admin
  "Overview": "概要", "Members": "メンバー", "Addresses": "アドレス", "Groups": "グループ", "Domains": "ドメイン", "Roles": "ロール", "Audit log": "監査ログ",
  "Connection": "接続", "Organisation": "組織", "People": "メンバー", "active": "有効", "invited": "招待済み", "disabled": "無効", "departed": "退職済み",
  "Needs attention": "対応が必要", "No owner": "所有者なし", "{n} with DNS problems": "DNS に問題があるドメイン {n} 件", "Connect": "接続",
  "Assign": "割り当て", "Recent activity": "最近の操作", "Everything looks good.": "問題ありません。", "Purelymail credit": "Purelymail の残高", "Last sync": "最終同期",
  "Sync now": "今すぐ同期", "Syncing…": "同期中…", "Connections": "接続数", "open": "使用中", "idle": "待機", "watchers": "監視",
  "Add member": "メンバーを追加", "Onboard a new member": "新しいメンバーを追加", "Title": "役職", "Department": "部署", "Role": "ロール", "Login email": "ログイン用メールアドレス",
  "Same as the mailbox address if left blank.": "空欄の場合はメールボックスのアドレスを使用します。", "Create a new mailbox": "新しいメールボックスを作成",
  "Use an existing mailbox": "既存のメールボックスを使う", "No mailbox for now": "今はメールボックスを設定しない", "Mailbox name": "メールボックス名", "Domain": "ドメイン",
  "Access to shared mailboxes": "共有メールボックスへのアクセス", "Add to groups": "グループに追加", "How will they sign in?": "サインイン方法",
  "Send an invite link": "招待リンクを送る", "Set a password now": "今すぐパスワードを設定", "Invite link": "招待リンク",
  "Share this link with {name}. It expires in {days} days.": "このリンクを {name} に渡してください。有効期限は {days} 日です。", "Member created.": "メンバーを作成しました。",
  "New invite link": "新しい招待リンク", "Disable": "無効にする", "Enable": "有効にする", "Reset password": "パスワードをリセット", "Offboard": "退職手続き",
  "Delete member": "メンバーを削除", "Transfer ownership": "所有権を移譲", "Set a temporary password for {name}. They will be signed out everywhere.": "{name} の一時パスワードを設定します。すべてのセッションからサインアウトされます。",
  "Temporary password": "一時パスワード", "Owner": "所有者", "Administrator": "管理者", "Member": "メンバー", "Last sign-in": "最終サインイン", "Never": "なし",
  "Offboard {name}": "{name} の退職手続き",
  "Hand over to": "引き継ぎ先", "Convert to a shared mailbox": "共有メールボックスに変換", "Keep, unassigned": "保持（未割り当て）", "Suspend (lock)": "停止（ロック）",
  "Forward new mail to": "新着メールの転送先", "Grant access to": "アクセス権を付与する相手", "Remove from groups": "グループから削除", "Revoke shared mailbox access": "共有メールボックスへのアクセスを取り消す",
  "Complete offboarding": "退職手続きを完了", "Offboarding complete.": "退職手続きが完了しました。", "No mailboxes": "メールボックスがありません",
  "Add mailbox": "メールボックスを追加", "Shared mailbox": "共有メールボックス", "Personal mailbox": "個人メールボックス", "Not connected": "未接続", "Connected": "接続済み",
  "Kind": "種類", "Access": "アクセス", "Forwarding": "転送", "Credential": "認証情報", "Rotate credential": "認証情報を更新",
  "Reset mailbox password": "メールボックスのパスワードをリセット", "Suspend mailbox": "メールボックスを停止", "Reactivate": "再有効化", "Delete mailbox": "メールボックスを削除",
  "Mailhearth opens this mailbox with its own app password. Rotate it if you suspect it leaked.": "Mailhearth は専用のアプリパスワードでこのメールボックスを開きます。漏えいが疑われる場合は更新してください。",
  "Password for external clients": "外部クライアント用パスワード", "IMAP server": "IMAP サーバー", "SMTP server": "SMTP サーバー", "Username": "ユーザー名",
  "Type the address to confirm": "確認のためアドレスを入力",
  "Grant access": "アクセスを許可", "Level": "レベル", "Revoke": "取り消す", "Nobody else has access.": "他にアクセスできる人はいません。",
  "Forward incoming mail to": "受信メールの転送先",
  "Comma-separated addresses": "アドレスはカンマ区切り", "Stop forwarding": "転送を停止", "Related addresses": "関連アドレス",
  "Add address": "アドレスを追加", "Alias": "エイリアス", "Catch-all": "キャッチオール", "Prefix": "接頭辞", "Group": "グループ", "Primary": "メイン",
  "member": "メンバー", "Time": "日時",
  "Delivers to": "配信先", "Targets": "転送先", "Local part": "@ より前の部分",
  "Delivers to mailbox": "メールボックスに配信", "Note": "メモ",
  "Delete address {a}?": "アドレス {a} を削除しますか？", "Address created.": "アドレスを作成しました。", "Address updated.": "アドレスを更新しました。",
  "Add group": "グループを追加", "Description": "説明", "Distribution address": "配信用アドレス",
  "No distribution address": "配信用アドレスなし", "members": "名", "Delete group {g}?": "グループ {g} を削除しますか？", "Group saved.": "グループを保存しました。",
  "Add domain": "ドメインを追加", "DNS records": "DNS レコード", "Recheck DNS": "DNS を再確認", "Ownership": "所有権", "MX": "MX", "SPF": "SPF", "DKIM": "DKIM", "DMARC": "DMARC",
  "Add these records at your DNS provider, then add the domain.": "DNS プロバイダーでこれらのレコードを追加してから、ドメインを追加してください。",
  "Type": "種類", "Host": "ホスト", "Value": "値", "Purpose": "用途", "Shared Purelymail domain": "Purelymail の共有ドメイン", "Passing": "正常", "Failing": "異常",
  "Account password reset": "アカウントのパスワード再設定を許可", "Symbolic subaddressing": "記号によるサブアドレス（user+tag）", "Remove domain": "ドメインを削除",
  "Domain added.": "ドメインを追加しました。", "Checked": "確認済み", "Add role": "ロールを追加", "Permissions": "権限", "Built-in": "組み込み", "custom": "カスタム",
  "org.owner": "組織の所有者", "org.manage": "組織の設定と接続を管理", "domains.manage": "ドメインを管理", "members.manage": "メンバーを管理", "mailboxes.manage": "個人メールボックスを管理",
  "shared.manage": "共有メールボックスを管理", "addresses.manage": "アドレスを管理", "groups.manage": "グループを管理", "audit.read": "監査ログを閲覧", "billing.read": "残高を閲覧",
  "Who": "実行者", "What": "操作", "Target": "対象", "System": "システム", "Load older": "さらに前を読み込む",
  "API token": "API トークン", "Replace token": "トークンを更新", "Token updated.": "トークンを更新しました。", "The token is never shown again after saving.": "保存後、トークンは二度と表示されません。",
  "Sync finished: {n} new mailboxes, {a} new addresses.": "同期が完了しました。新しいメールボックス {n} 件、新しいアドレス {a} 件。", "API endpoint": "API エンドポイント",
  "Rename organisation": "組織名を変更", "Organisation renamed.": "組織名を変更しました。", "Choose the new owner": "新しい所有者を選択",
  "You will become an administrator.": "あなたは管理者になります。", "Ownership transferred.": "所有権を移譲しました。",
  "You do not have permission to do this": "この操作を行う権限がありません", "sign in required": "サインインが必要です",

  // Audit log
  "created the organisation": "組織を作成しました", "renamed the organisation": "組織名を変更しました", "transferred ownership": "所有権を移譲しました", "connected Purelymail": "Purelymail に接続しました", "synced with Purelymail": "Purelymail と同期しました",
  "added a member": "メンバーを追加しました", "updated a member": "メンバーを更新しました", "changed member status": "メンバーの状態を変更しました", "created an invite": "招待を作成しました", "reset a password": "パスワードをリセットしました", "offboarded a member": "メンバーの退職手続きを行いました", "deleted a member": "メンバーを削除しました",
  "created a mailbox": "メールボックスを作成しました", "assigned a mailbox": "メールボックスを割り当てました", "connected a mailbox": "メールボックスを接続しました", "rotated a mailbox credential": "メールボックスの認証情報を更新しました", "reset a mailbox password": "メールボックスのパスワードをリセットしました", "suspended a mailbox": "メールボックスを停止しました", "reactivated a mailbox": "メールボックスを再有効化しました", "updated a mailbox": "メールボックスを更新しました", "deleted a mailbox": "メールボックスを削除しました", "granted mailbox access": "メールボックスへのアクセスを許可しました", "revoked mailbox access": "メールボックスへのアクセスを取り消しました", "set mailbox forwarding": "メールボックスの転送を設定しました", "cleared mailbox forwarding": "メールボックスの転送を解除しました", "handed over a mailbox": "メールボックスを引き継ぎました", "converted a mailbox to shared": "メールボックスを共有に変換しました",
  "created an address": "アドレスを作成しました", "updated an address": "アドレスを更新しました", "deleted an address": "アドレスを削除しました", "created a group": "グループを作成しました", "updated a group": "グループを更新しました", "deleted a group": "グループを削除しました", "set a group address": "グループのアドレスを設定しました", "removed a group address": "グループのアドレスを削除しました",
  "added a domain": "ドメインを追加しました", "updated a domain": "ドメインを更新しました", "removed a domain": "ドメインを削除しました", "created a role": "ロールを作成しました", "updated a role": "ロールを更新しました", "deleted a role": "ロールを削除しました",
};

const es: Record<string, string> = {
  "Full mailbox address": "Dirección completa del buzón",
  "Report lost credential cleanup": "Informar de la eliminación de credenciales desconocidas", "Remove the unknown application credential in the provider console before reporting. This cancels the operation and retains already created resources.": "Elimina la credencial de aplicación desconocida en el proveedor antes de informar. La operación se cancela y se conservan los recursos ya creados.",
  "Balance and usage by connection": "Saldo y uso por conexión", "Values retain the provider's units and scope.": "Los valores conservan las unidades y el alcance del proveedor.", "Usage": "Uso", "balance": "Saldo", "incoming": "Recibidos", "outgoing": "Enviados", "storage": "Almacenamiento", "messages": "mensajes", "provider_credit": "unidades de saldo del proveedor", "current_date": "fecha actual del proveedor",
  "Auto-reply is not available for this server.": "Las respuestas automáticas no están disponibles en este servidor.",
  "Operations": "Operaciones de administración",
  "All operations": "Todas las operaciones",
  "Cancel operation": "Cancelar operación",
  "Retry operation": "Reintentar operación",
  "Check operation result": "Comprobar resultado",
  "No operations.": "No hay operaciones de administración.",
  "View the active operation": "Ver operación activa",
  "Retries preserve the original request and operation ID. Unknown results require checking.": "Los reintentos conservan la solicitud y el ID originales. Los resultados desconocidos requieren comprobación.",
  "Sending requests": "Solicitudes de envío",
  "Recent sending requests remain available after closing the composer.": "Las solicitudes recientes siguen disponibles después de cerrar el editor.",
  "No sending requests.": "No hay solicitudes de envío.",
  "Check the mail server using the Message-ID and creation time before sending again.": "Consulta el servidor con el Message-ID y la fecha de creación antes de enviar de nuevo.",
  "Only the Sent copy is retried. SMTP acceptance is retained.": "Solo se reintenta guardar la copia Sent. Se conserva la aceptación SMTP.",
  "Sending request result unavailable. Retry keeps the same request ID.": "El resultado del envío no está disponible. El reintento conserva el mismo ID.",
  "Special folders": "Carpetas especiales",
  "Choose existing folders. Automatic selection requires a unique match.": "Elige carpetas existentes. La selección automática requiere una coincidencia única.",
  "Automatic selection": "Selección automática",
  "Folder unavailable": "Carpeta no disponible",
  "Folder mapping saved.": "Asignación de carpetas guardada.",
  "Sent copy handling": "Guardado de copias enviadas",
  "Mailhearth saves the Sent copy": "Mailhearth guarda la copia enviada",
  "Mail server saves the Sent copy": "El servidor guarda la copia enviada",
  "Archive mailbox": "Archivar buzón",
  "Unregister mailbox": "Quitar registro del buzón",
  "Removes local registration. Remote mail remains on the server.": "Quita el registro local. El correo permanece en el servidor.",
  "Revoke these credentials in the external mail service:": "Revoca estas credenciales en el servicio de correo externo:",
  "Authorize sending": "Autorizar envío",
  "Revoke sending authorization": "Revocar autorización de envío",
  // Short labels; the long form lives in an ⓘ popover.
  "Search mail": "Buscar correo",
  "Nothing on Purelymail is changed.": "No se modifica nada en Purelymail.",
  "Rules run on the mail server, before mail reaches your inbox.": "Las reglas se ejecutan en el servidor de correo, antes de que el correo llegue a la bandeja de entrada.",
  "Repeat interval": "Intervalo de repetición",
  "For use in other mail apps. Shown once.": "Para usar en otras aplicaciones de correo. Se muestra una sola vez.",
  "Everyone loses access. Mail keeps arriving and is kept.": "Todos pierden el acceso. El correo sigue llegando y se conserva.",
  "Deletes the mailbox and all its mail. This cannot be undone.": "Elimina el buzón y todo su correo. No se puede deshacer.",
  "Access ends immediately and every credential is rotated.": "El acceso termina de inmediato y se renuevan todas las credenciales.",
  "Deletes every mailbox and rule on this domain.": "Elimina todos los buzones y todas las reglas de este dominio.",
  "Imports changes made outside Mailhearth.": "Importa los cambios realizados fuera de Mailhearth.",
  "Mail to this address reaches every member.": "El correo enviado a esta dirección llega a todos los miembros.",
  "New mail skips this mailbox while forwarding is on.": "El correo nuevo omite este buzón mientras el reenvío está activo.",
  "Your sign-in address. It can be an address on your own domain.": "Tu dirección de acceso. Puede ser una dirección de tu propio dominio.",
  "Create it in the Purelymail portal under Account → API. Stored encrypted on this server and never sent to browsers.": "Créalo en el portal de Purelymail, en Account → API. Se guarda cifrado en este servidor y nunca se envía a los navegadores.",
  "Importing reads your domains, mailboxes and routing rules, and builds the organisation model from them.": "La importación lee tus dominios, buzones y reglas de enrutamiento, y con ellos construye el modelo de la organización.",
  "Pick the mailbox you use yourself, so you can read mail as soon as setup finishes.": "Elige el buzón que usas tú, para poder leer el correo en cuanto termine la configuración.",
  "Stored as Sieve. Saving replaces any filters this mailbox has in other mail clients.": "Se guarda como Sieve. Al guardar se sustituyen los filtros que este buzón tenga en otros clientes de correo.",
  "Reply at most once every N days to the same sender.": "Responde como máximo una vez cada N días al mismo remitente.",
  "Re-reads domains, mailboxes and routing rules from Purelymail and updates the organisation model.": "Vuelve a leer los dominios, buzones y reglas de enrutamiento de Purelymail y actualiza el modelo de la organización.",
  "Another address for one mailbox.": "Otra dirección para un mismo buzón.",
  "Sends mail on to one or more addresses, inside or outside the organisation.": "Reenvía el correo a una o varias direcciones, dentro o fuera de la organización.",
  "Receives anything at this domain that has no mailbox of its own.": "Recibe todo lo que llegue a este dominio y no tenga un buzón propio.",
  "Receives every address starting with this prefix.": "Recibe todas las direcciones que empiezan por este prefijo.",

  // Common
  "Mail": "Correo", "Admin": "Administración", "Settings": "Ajustes", "Sign out": "Cerrar sesión", "Sign in": "Iniciar sesión", "Cancel": "Cancelar", "Save": "Guardar",
  "Delete": "Eliminar", "Close": "Cerrar", "Create": "Crear", "Edit": "Editar", "Back": "Atrás", "Next": "Siguiente", "Done": "Listo", "Search": "Buscar",
  "Loading…": "Cargando…", "Nothing here yet.": "Aún no hay nada aquí.", "Yes": "Sí", "No": "No", "Name": "Nombre", "Actions": "Acciones", "Status": "Estado",
  "Copy": "Copiar", "Copied": "Copiado", "Refresh": "Actualizar", "Confirm": "Confirmar", "Optional": "Opcional", "Required": "Obligatorio", "Unknown": "Desconocido",
  "Something went wrong.": "Algo salió mal.", "Retry": "Reintentar", "Continue": "Continuar", "Password": "Contraseña", "Email": "Correo electrónico", "Language": "Idioma",
  "Active": "Activo", "Invited": "Invitado", "Disabled": "Desactivado", "Departed": "Baja", "Suspended": "Suspendido", "Archived": "Archivado",
  "Personal": "Personal", "Shared": "Compartido", "personal": "personal", "shared": "compartido", "Full": "Completo", "Send": "Enviar", "Read": "Lectura",
  "full": "acceso completo", "send": "lectura y envío", "read": "solo lectura",

  // Auth
  "Welcome back": "Te damos la bienvenida", "Email or mailbox address": "Correo electrónico o dirección de buzón", "Incorrect email or password": "Correo electrónico o contraseña incorrectos",
  "Signing in…": "Iniciando sesión…", "You have been invited": "Te han invitado", "Set a password to activate your account.": "Establece una contraseña para activar tu cuenta.",
  "Your name": "Tu nombre", "Choose a password": "Elige una contraseña", "At least 10 characters.": "Al menos 10 caracteres.", "Activate account": "Activar cuenta",
  "This invite link is invalid or has expired.": "Este enlace de invitación no es válido o ha caducado.", "Current password": "Contraseña actual", "New password": "Nueva contraseña",
  "Change password": "Cambiar contraseña", "Password changed.": "Contraseña cambiada.",

  // Setup
  "Set up Mailhearth": "Configurar Mailhearth", "Your organisation": "Tu organización", "Connect Purelymail": "Conectar Purelymail", "Import": "Importar",
  "Organisation name": "Nombre de la organización", "Administrator name": "Nombre del administrador", "Administrator email": "Correo del administrador",
  "Create organisation": "Crear organización", "Purelymail API token": "Token de la API de Purelymail",
  "Checking…": "Comprobando…", "Found on your Purelymail account": "Encontrado en tu cuenta de Purelymail", "domains": "dominios", "addresses": "direcciones", "mailboxes": "buzones",
  "routing rules": "reglas de enrutamiento", "credit": "saldo",
  "Connect my own mailbox": "Conectar mi propio buzón",
  "None for now": "Ninguno por ahora", "Import and finish": "Importar y terminar", "Importing…": "Importando…",
  "Imported {d} domains, {m} mailboxes and {a} addresses.": "Se importaron {d} dominios, {m} buzones y {a} direcciones.", "Warnings": "Avisos",
  "Go to your mail": "Ir a tu correo", "Open the admin console": "Abrir la consola de administración",
  "Development stack is active: this instance talks to a fake Purelymail and an in-memory mail server.": "El entorno de desarrollo está activo: esta instancia se comunica con un Purelymail simulado y un servidor de correo en memoria.",

  // Mail
  "Compose": "Redactar", "Inbox": "Bandeja de entrada", "Drafts": "Borradores", "Sent": "Enviados", "Trash": "Papelera", "Junk": "Spam", "Archive": "Archivo",
  "Folders": "Carpetas", "New folder": "Nueva carpeta", "Rename folder": "Cambiar el nombre de la carpeta", "Delete folder": "Eliminar carpeta", "Folder name": "Nombre de la carpeta",
  "Delete folder “{name}” and all its messages?": "¿Eliminar la carpeta «{name}» y todos sus mensajes?", "Mailboxes": "Buzones", "Shared mailboxes": "Buzones compartidos",
  "No messages": "No hay mensajes", "No results for “{q}”": "No hay resultados para «{q}»", "Load more": "Cargar más", "Select all": "Seleccionar todo",
  "{n} selected": "{n} seleccionados", "Mark as read": "Marcar como leído", "Mark as unread": "Marcar como no leído", "Flag": "Destacar", "Unflag": "Quitar destacado",
  "Move to": "Mover a", "Archive message": "Archivar", "Delete permanently": "Eliminar definitivamente", "Reply": "Responder", "Reply all": "Responder a todos",
  "Forward": "Reenviar", "Forward as attachment": "Reenviar como archivo adjunto", "Download": "Descargar", "Download original (.eml)": "Descargar el original (.eml)",
  "Print": "Imprimir", "Attachments": "Archivos adjuntos", "attachment": "adjunto", "This message has remote images.": "Este mensaje tiene imágenes remotas.",
  "Show images": "Mostrar imágenes", "Message is too large to show completely.": "El mensaje es demasiado grande para mostrarlo completo.", "From": "De", "To": "Para",
  "Cc": "Cc", "Bcc": "Cco", "Date": "Fecha", "Subject": "Asunto", "(no subject)": "(sin asunto)", "Show details": "Mostrar detalles",
  "Hide details": "Ocultar detalles", "Select a message to read": "Selecciona un mensaje para leerlo", "Read-only access": "Acceso de solo lectura",
  "Search results": "Resultados de la búsqueda", "Clear": "Borrar", "Today": "Hoy", "Yesterday": "Ayer", "Moved to {f}": "Movido a {f}", "Deleted": "Eliminado",
  "Undo": "Deshacer", "Sending…": "Enviando…", "Sent.": "Enviado.", "Draft saved.": "Borrador guardado.", "Draft saved": "Borrador guardado",
  "Save draft": "Guardar borrador", "Discard": "Descartar", "Discard this message?": "¿Descartar este mensaje?", "Add recipients": "Añadir destinatarios",
  "Attach files": "Adjuntar archivos", "Uploading…": "Subiendo…", "Remove": "Quitar", "Priority": "Prioridad", "High": "Alta", "Normal": "Normal", "Low": "Baja",
  "Write your message…": "Escribe tu mensaje…", "Bold": "Negrita", "Italic": "Cursiva", "Underline": "Subrayado", "Bulleted list": "Lista con viñetas", "Numbered list": "Lista numerada",
  "Link": "Enlace", "Quote": "Cita", "Remove formatting": "Quitar formato", "Link URL": "URL del enlace", "wrote:": "escribió:", "Forwarded message": "Mensaje reenviado",
  "New message": "Nuevo mensaje", "Re: ": "RE: ", "Fwd: ": "RV: ", "Original attachments": "Archivos adjuntos originales", "Add at least one recipient.": "Añade al menos un destinatario.",
  "Team": "Equipo", "Assigned to": "Asignado a", "Unassigned": "Sin asignar", "Assign to me": "Asignármelo", "Resolve": "Marcar como resuelto",
  "Reopen": "Reabrir", "Resolved": "Resuelto", "Open": "Abierto", "Internal note": "Nota interna", "Add note": "Añadir nota", "Notes are only visible to your team.": "Las notas solo las ve tu equipo.",
  "Activity": "Actividad", "replied": "respondió", "forwarded": "reenvió", "assigned": "asignó", "unassigned": "desasignó", "resolved": "marcó como resuelto",
  "reopened": "reabrió", "note": "nota", "Replied by {name}": "{name} respondió", "New mail in {folder}": "Correo nuevo en {folder}",
  "{n} new messages": "{n} mensajes nuevos", "Notifications": "Notificaciones", "Enable desktop notifications": "Activar las notificaciones de escritorio", "Notifications are blocked in your browser.": "Tu navegador bloquea las notificaciones.",
  "Keyboard shortcuts": "Atajos de teclado", "Nothing selected": "Nada seleccionado", "Message not found. It may have been moved or deleted.": "Mensaje no encontrado. Es posible que se haya movido o eliminado.",
  "Rules": "Reglas", "Mail rules": "Reglas de correo", "Auto-reply": "Respuesta automática", "Add rule": "Añadir regla", "Rule name": "Nombre de la regla", "When": "Cuando", "all": "todas",
  "any": "cualquiera", "of these match": "de estas coincidan", "Then": "Entonces", "Add condition": "Añadir condición", "Add action": "Añadir acción", "Enabled": "Activado",
  "from": "de", "to": "para", "subject": "asunto", "body": "cuerpo", "header": "cabecera", "size": "tamaño", "contains": "contiene", "not_contains": "no contiene",
  "is": "es", "matches": "coincide con comodín", "over": "mayor que", "under": "menor que", "move": "mover a carpeta", "copy": "copiar a carpeta", "flag": "destacar",
  "markread": "marcar como leído", "forward": "reenviar copia a", "redirect": "redirigir a", "discard": "descartar", "stop": "detener las reglas siguientes",
  "Rules saved and installed on the server.": "Reglas guardadas e instaladas en el servidor.", "Rules saved.": "Reglas guardadas.", "Auto-reply subject": "Asunto de la respuesta automática",
  "Auto-reply message": "Mensaje de la respuesta automática",
  "Rule management is not available for this server.": "La gestión de reglas no está disponible en este servidor.", "Preview Sieve script": "Vista previa del script de Sieve",
  "Identities & signatures": "Identidades y firmas", "Display name": "Nombre para mostrar", "Reply-To": "Responder a", "Signature": "Firma", "Default": "Predeterminado",
  "Make default": "Usar como predeterminado", "Sender identity saved.": "Identidad de envío guardada.",
  "Signed in as": "Sesión iniciada como", "Appearance": "Apariencia", "Account": "Cuenta", "Mailbox": "Buzón", "for": "para",

  // Admin
  "Overview": "Resumen", "Members": "Miembros", "Addresses": "Direcciones", "Groups": "Grupos", "Domains": "Dominios", "Roles": "Roles", "Audit log": "Registro de auditoría",
  "Connection": "Conexión", "Organisation": "Organización", "People": "Personas", "active": "activo", "invited": "invitado", "disabled": "desactivado", "departed": "baja",
  "Needs attention": "Requiere atención", "No owner": "Sin propietario", "{n} with DNS problems": "{n} con problemas de DNS", "Connect": "Conectar",
  "Assign": "Asignar", "Recent activity": "Actividad reciente", "Everything looks good.": "Todo parece correcto.", "Purelymail credit": "Saldo de Purelymail", "Last sync": "Última sincronización",
  "Sync now": "Sincronizar ahora", "Syncing…": "Sincronizando…", "Connections": "Conexiones", "open": "abiertas", "idle": "inactivas", "watchers": "vigilantes",
  "Add member": "Añadir miembro", "Onboard a new member": "Dar de alta a un miembro", "Title": "Puesto", "Department": "Departamento", "Role": "Rol", "Login email": "Correo de acceso",
  "Same as the mailbox address if left blank.": "Si se deja en blanco, se usa la dirección del buzón.", "Create a new mailbox": "Crear un buzón nuevo",
  "Use an existing mailbox": "Usar un buzón existente", "No mailbox for now": "Sin buzón por ahora", "Mailbox name": "Nombre del buzón", "Domain": "Dominio",
  "Access to shared mailboxes": "Acceso a buzones compartidos", "Add to groups": "Añadir a grupos", "How will they sign in?": "¿Cómo iniciará sesión?",
  "Send an invite link": "Enviar un enlace de invitación", "Set a password now": "Establecer una contraseña ahora", "Invite link": "Enlace de invitación",
  "Share this link with {name}. It expires in {days} days.": "Comparte este enlace con {name}. Caduca en {days} días.", "Member created.": "Miembro creado.",
  "New invite link": "Nuevo enlace de invitación", "Disable": "Desactivar", "Enable": "Activar", "Reset password": "Restablecer la contraseña", "Offboard": "Dar de baja",
  "Delete member": "Eliminar miembro", "Transfer ownership": "Transferir la propiedad", "Set a temporary password for {name}. They will be signed out everywhere.": "Establece una contraseña temporal para {name}. Se cerrará su sesión en todas partes.",
  "Temporary password": "Contraseña temporal", "Owner": "Propietario", "Administrator": "Administrador", "Member": "Miembro", "Last sign-in": "Último acceso", "Never": "Nunca",
  "Offboard {name}": "Dar de baja a {name}",
  "Hand over to": "Ceder a", "Convert to a shared mailbox": "Convertir en buzón compartido", "Keep, unassigned": "Conservar, sin asignar", "Suspend (lock)": "Suspender (bloquear)",
  "Forward new mail to": "Reenviar el correo nuevo a", "Grant access to": "Conceder acceso a", "Remove from groups": "Quitar de los grupos", "Revoke shared mailbox access": "Revocar el acceso al buzón compartido",
  "Complete offboarding": "Completar la baja", "Offboarding complete.": "Baja completada.", "No mailboxes": "No hay buzones",
  "Add mailbox": "Añadir buzón", "Shared mailbox": "Buzón compartido", "Personal mailbox": "Buzón personal", "Not connected": "Sin conectar", "Connected": "Conectado",
  "Kind": "Tipo", "Access": "Acceso", "Forwarding": "Reenvío", "Credential": "Credencial", "Rotate credential": "Renovar la credencial",
  "Reset mailbox password": "Restablecer la contraseña del buzón", "Suspend mailbox": "Suspender el buzón", "Reactivate": "Reactivar", "Delete mailbox": "Eliminar buzón",
  "Mailhearth opens this mailbox with its own app password. Rotate it if you suspect it leaked.": "Mailhearth abre este buzón con su propia contraseña de aplicación. Renuévala si sospechas que se ha filtrado.",
  "Password for external clients": "Contraseña para clientes externos", "IMAP server": "Servidor IMAP", "SMTP server": "Servidor SMTP", "Username": "Nombre de usuario",
  "Type the address to confirm": "Escribe la dirección para confirmar",
  "Grant access": "Conceder acceso", "Level": "Nivel", "Revoke": "Revocar", "Nobody else has access.": "Nadie más tiene acceso.",
  "Forward incoming mail to": "Reenviar el correo entrante a",
  "Comma-separated addresses": "Direcciones separadas por comas", "Stop forwarding": "Detener el reenvío", "Related addresses": "Direcciones relacionadas",
  "Add address": "Añadir dirección", "Alias": "Alias", "Catch-all": "Catch-all", "Prefix": "Prefijo", "Group": "Grupo", "Primary": "Principal",
  "member": "miembro", "Time": "Hora",
  "Delivers to": "Entrega en", "Targets": "Destinos", "Local part": "Parte local",
  "Delivers to mailbox": "Entrega al buzón", "Note": "Nota",
  "Delete address {a}?": "¿Eliminar la dirección {a}?", "Address created.": "Dirección creada.", "Address updated.": "Dirección actualizada.",
  "Add group": "Añadir grupo", "Description": "Descripción", "Distribution address": "Dirección de distribución",
  "No distribution address": "Sin dirección de distribución", "members": "miembros", "Delete group {g}?": "¿Eliminar el grupo {g}?", "Group saved.": "Grupo guardado.",
  "Add domain": "Añadir dominio", "DNS records": "Registros DNS", "Recheck DNS": "Volver a comprobar el DNS", "Ownership": "Propiedad", "MX": "MX", "SPF": "SPF", "DKIM": "DKIM", "DMARC": "DMARC",
  "Add these records at your DNS provider, then add the domain.": "Añade estos registros en tu proveedor de DNS y después añade el dominio.",
  "Type": "Tipo", "Host": "Host", "Value": "Valor", "Purpose": "Uso", "Shared Purelymail domain": "Dominio compartido de Purelymail", "Passing": "Correcto", "Failing": "Con errores",
  "Account password reset": "Permitir restablecer la contraseña de la cuenta", "Symbolic subaddressing": "Subdireccionamiento simbólico (user+tag)", "Remove domain": "Quitar dominio",
  "Domain added.": "Dominio añadido.", "Checked": "Comprobado", "Add role": "Añadir rol", "Permissions": "Permisos", "Built-in": "Integrado", "custom": "personalizado",
  "org.owner": "Propietario de la organización", "org.manage": "Gestionar los ajustes y las conexiones de la organización", "domains.manage": "Gestionar dominios", "members.manage": "Gestionar miembros", "mailboxes.manage": "Gestionar buzones personales",
  "shared.manage": "Gestionar buzones compartidos", "addresses.manage": "Gestionar direcciones", "groups.manage": "Gestionar grupos", "audit.read": "Ver el registro de auditoría", "billing.read": "Ver el saldo",
  "Who": "Quién", "What": "Qué", "Target": "Objetivo", "System": "Sistema", "Load older": "Cargar anteriores",
  "API token": "Token de la API", "Replace token": "Sustituir el token", "Token updated.": "Token actualizado.", "The token is never shown again after saving.": "El token no se vuelve a mostrar después de guardarlo.",
  "Sync finished: {n} new mailboxes, {a} new addresses.": "Sincronización terminada: {n} buzones nuevos, {a} direcciones nuevas.", "API endpoint": "Punto de acceso de la API",
  "Rename organisation": "Cambiar el nombre de la organización", "Organisation renamed.": "Nombre de la organización cambiado.", "Choose the new owner": "Elige al nuevo propietario",
  "You will become an administrator.": "Pasarás a ser administrador.", "Ownership transferred.": "Propiedad transferida.",
  "You do not have permission to do this": "No tienes permiso para hacer esto", "sign in required": "se requiere iniciar sesión",

  // Audit log
  "created the organisation": "creó la organización", "renamed the organisation": "cambió el nombre de la organización", "transferred ownership": "transfirió la propiedad", "connected Purelymail": "conectó Purelymail", "synced with Purelymail": "sincronizó con Purelymail",
  "added a member": "añadió un miembro", "updated a member": "actualizó un miembro", "changed member status": "cambió el estado de un miembro", "created an invite": "creó una invitación", "reset a password": "restableció una contraseña", "offboarded a member": "dio de baja a un miembro", "deleted a member": "eliminó un miembro",
  "created a mailbox": "creó un buzón", "assigned a mailbox": "asignó un buzón", "connected a mailbox": "conectó un buzón", "rotated a mailbox credential": "renovó la credencial de un buzón", "reset a mailbox password": "restableció la contraseña de un buzón", "suspended a mailbox": "suspendió un buzón", "reactivated a mailbox": "reactivó un buzón", "updated a mailbox": "actualizó un buzón", "deleted a mailbox": "eliminó un buzón", "granted mailbox access": "concedió acceso a un buzón", "revoked mailbox access": "revocó el acceso a un buzón", "set mailbox forwarding": "configuró el reenvío de un buzón", "cleared mailbox forwarding": "desactivó el reenvío de un buzón", "handed over a mailbox": "cedió un buzón", "converted a mailbox to shared": "convirtió un buzón en compartido",
  "created an address": "creó una dirección", "updated an address": "actualizó una dirección", "deleted an address": "eliminó una dirección", "created a group": "creó un grupo", "updated a group": "actualizó un grupo", "deleted a group": "eliminó un grupo", "set a group address": "configuró la dirección de un grupo", "removed a group address": "eliminó la dirección de un grupo",
  "added a domain": "añadió un dominio", "updated a domain": "actualizó un dominio", "removed a domain": "eliminó un dominio", "created a role": "creó un rol", "updated a role": "actualizó un rol", "deleted a role": "eliminó un rol",
};

Object.assign(zhCN, {
  "Revoke all remote access": "撤销全部远程访问", "Remote access methods must be reviewed individually. Completion is recorded as an administrator report.": "需要逐项检查远程访问方式。处理完成将记录为管理员报告。",
  "Delivery mode": "投递方式", "Redirect": "转移投递", "Keep a copy and forward": "保留副本并转发",
  "Sent, but saving the copy failed.": "已发送，保存副本失败。",
  "Delivery result unknown. Sending again may deliver a duplicate.": "发送结果未知。再次发送可能重复投递。",
  "Submission failed.": "提交失败。", "Submission queued.": "提交正在等待处理。",
  "Retry saving the sent copy": "重试保存已发送副本", "Create a new sending request": "创建新的发送请求",
});
Object.assign(zhTW, {
  "Revoke all remote access": "撤銷全部遠端存取", "Remote access methods must be reviewed individually. Completion is recorded as an administrator report.": "需要逐項檢查遠端存取方式。處理完成將記錄為管理員報告。",
  "Delivery mode": "投遞方式", "Redirect": "轉移投遞", "Keep a copy and forward": "保留副本並轉寄",
  "Sent, but saving the copy failed.": "已傳送，儲存副本失敗。",
  "Delivery result unknown. Sending again may deliver a duplicate.": "傳送結果未知。再次傳送可能重複投遞。",
  "Submission failed.": "提交失敗。", "Submission queued.": "提交正在等待處理。",
  "Retry saving the sent copy": "重試儲存已傳送副本", "Create a new sending request": "建立新的傳送請求",
});
Object.assign(ja, {
  "Revoke all remote access": "すべてのリモートアクセスを取り消す", "Remote access methods must be reviewed individually. Completion is recorded as an administrator report.": "リモートアクセス方法を個別に確認してください。完了は管理者の報告として記録されます。",
  "Delivery mode": "配信方法", "Redirect": "転送のみ", "Keep a copy and forward": "コピーを保持して転送",
  "Sent, but saving the copy failed.": "送信済みですが、コピーの保存に失敗しました。",
  "Delivery result unknown. Sending again may deliver a duplicate.": "送信結果が不明です。再送すると重複して配信される可能性があります。",
  "Submission failed.": "送信要求に失敗しました。", "Submission queued.": "送信要求は処理待ちです。",
  "Retry saving the sent copy": "送信済みコピーの保存を再試行", "Create a new sending request": "新しい送信要求を作成",
});
Object.assign(es, {
  "Revoke all remote access": "Revocar todo acceso remoto", "Remote access methods must be reviewed individually. Completion is recorded as an administrator report.": "Es necesario revisar cada método de acceso remoto. La finalización se registra como un informe del administrador.",
  "Delivery mode": "Modo de entrega", "Redirect": "Redirigir", "Keep a copy and forward": "Conservar copia y reenviar",
  "Sent, but saving the copy failed.": "Enviado, pero no se pudo guardar la copia.",
  "Delivery result unknown. Sending again may deliver a duplicate.": "El resultado del envío es desconocido. Volver a enviar puede duplicar la entrega.",
  "Submission failed.": "La solicitud de envío ha fallado.", "Submission queued.": "La solicitud de envío está en espera.",
  "Retry saving the sent copy": "Reintentar guardar la copia enviada", "Create a new sending request": "Crear una nueva solicitud de envío",
});
Object.assign(zhCN, {
  "Confirm active script takeover": "确认接管当前脚本", "The existing script will be retained.": "已有脚本将被保留。", "Confirm takeover": "确认接管",
  "The active script is verified before the new rules are activated.": "新规则启用前将核验当前脚本。", "Delete remote mailbox": "删除远程邮箱", "The remote mailbox and its messages will be deleted.": "远程邮箱及其邮件将被删除。",
});
Object.assign(zhTW, {
  "Confirm active script takeover": "確認接管目前腳本", "The existing script will be retained.": "現有腳本將被保留。", "Confirm takeover": "確認接管",
  "The active script is verified before the new rules are activated.": "新規則啟用前將驗證目前腳本。", "Delete remote mailbox": "刪除遠端信箱", "The remote mailbox and its messages will be deleted.": "遠端信箱及其郵件將被刪除。",
});
Object.assign(ja, {
  "Confirm active script takeover": "有効なスクリプトの引き継ぎを確認", "The existing script will be retained.": "既存のスクリプトは保持されます。", "Confirm takeover": "引き継ぎを確認",
  "The active script is verified before the new rules are activated.": "新しいルールの有効化前に、現在のスクリプトを検証します。", "Delete remote mailbox": "リモートメールボックスを削除", "The remote mailbox and its messages will be deleted.": "リモートメールボックスとメールが削除されます。",
});
Object.assign(es, {
  "Confirm active script takeover": "Confirmar sustitución del script activo", "The existing script will be retained.": "Se conservará el script existente.", "Confirm takeover": "Confirmar sustitución",
  "The active script is verified before the new rules are activated.": "El script activo se verifica antes de activar las nuevas reglas.", "Delete remote mailbox": "Eliminar buzón remoto", "The remote mailbox and its messages will be deleted.": "Se eliminarán el buzón remoto y sus mensajes.",
});
const dicts: Record<Exclude<Lang, "en">, Record<string, string>> = { "zh-CN": zhCN, "zh-TW": zhTW, ja, es };

Object.assign(zhCN, {
  "Discover resources": "发现资源", "Import selected resources": "导入所选资源", "The discovery response is incomplete.": "资源发现响应缺少必要信息。", "Select each mailbox and its domain. Configure login credentials after import.": "请选择邮箱及其所属域名。导入后请配置登录凭据。",
  "Register existing mailbox": "登记已有邮箱", "Administrator mailbox": "管理员邮箱", "Do not bind a mailbox": "不绑定邮箱", "Complete setup": "完成初始化", "Setup complete": "初始化完成", "Open mail": "打开邮件", "Manage mail connections": "管理邮件连接",
});
Object.assign(zhTW, {
  "Discover resources": "探索資源", "Import selected resources": "匯入所選資源", "The discovery response is incomplete.": "資源探索回應缺少必要資訊。", "Select each mailbox and its domain. Configure login credentials after import.": "請選擇信箱及其所屬網域。匯入後請設定登入憑證。",
  "Register existing mailbox": "登記現有信箱", "Administrator mailbox": "管理員信箱", "Do not bind a mailbox": "不綁定信箱", "Complete setup": "完成初始化", "Setup complete": "初始化完成", "Open mail": "開啟郵件", "Manage mail connections": "管理郵件連線",
});
Object.assign(ja, {
  "Discover resources": "リソースを検出", "Import selected resources": "選択したリソースを取り込む", "The discovery response is incomplete.": "リソース検出の応答に必要な情報がありません。", "Select each mailbox and its domain. Configure login credentials after import.": "メールボックスと所属ドメインを選択してください。取り込み後にログイン認証情報を設定してください。",
  "Register existing mailbox": "既存メールボックスを登録", "Administrator mailbox": "管理者のメールボックス", "Do not bind a mailbox": "メールボックスを関連付けない", "Complete setup": "セットアップを完了", "Setup complete": "セットアップ完了", "Open mail": "メールを開く", "Manage mail connections": "メール接続を管理",
});
Object.assign(es, {
  "Discover resources": "Descubrir recursos", "Import selected resources": "Importar recursos seleccionados", "The discovery response is incomplete.": "La respuesta del descubrimiento carece de información necesaria.", "Select each mailbox and its domain. Configure login credentials after import.": "Seleccione cada buzón y su dominio. Configure las credenciales de acceso después de importar.",
  "Register existing mailbox": "Registrar buzón existente", "Administrator mailbox": "Buzón del administrador", "Do not bind a mailbox": "No vincular un buzón", "Complete setup": "Completar configuración", "Setup complete": "Configuración completada", "Open mail": "Abrir correo", "Manage mail connections": "Administrar conexiones de correo",
});

Object.assign(zhCN, {
  "Mail connection": "邮件连接", "Select a connection": "选择邮件连接", "Action": "操作", "Register existing domain": "登记已有域名", "Create remote domain": "创建远程域名",
  "Activate domain": "启用域名", "Unregister domain binding": "解除域名关联", "Delete remote domain": "删除远程域名", "Mailboxes and address rules must be removed first.": "需要先解除相关邮箱和地址规则。",
  "Manage this domain in the provider console.": "请在服务商管理页面配置此域名。", "Passed": "通过", "Unverified": "尚未验证",
});
Object.assign(zhTW, {
  "Mail connection": "郵件連線", "Select a connection": "選擇郵件連線", "Action": "操作", "Register existing domain": "登記現有網域", "Create remote domain": "建立遠端網域",
  "Activate domain": "啟用網域", "Unregister domain binding": "解除網域關聯", "Delete remote domain": "刪除遠端網域", "Mailboxes and address rules must be removed first.": "需要先解除相關信箱和位址規則。",
  "Manage this domain in the provider console.": "請在服務商管理頁面設定此網域。", "Passed": "通過", "Unverified": "尚未驗證",
});
Object.assign(ja, {
  "Mail connection": "メール接続", "Select a connection": "メール接続を選択", "Action": "操作", "Register existing domain": "既存ドメインを登録", "Create remote domain": "リモートドメインを作成",
  "Activate domain": "ドメインを有効化", "Unregister domain binding": "ドメインの関連付けを解除", "Delete remote domain": "リモートドメインを削除", "Mailboxes and address rules must be removed first.": "関連するメールボックスとアドレスルールを先に解除してください。",
  "Manage this domain in the provider console.": "このドメインはプロバイダーの管理画面で設定してください。", "Passed": "合格", "Unverified": "未検証",
});
Object.assign(es, {
  "Mail connection": "Conexión de correo", "Select a connection": "Seleccionar conexión", "Action": "Acción", "Register existing domain": "Registrar dominio existente", "Create remote domain": "Crear dominio remoto",
  "Activate domain": "Activar dominio", "Unregister domain binding": "Desvincular dominio", "Delete remote domain": "Eliminar dominio remoto", "Mailboxes and address rules must be removed first.": "Primero se deben desvincular los buzones y las reglas de dirección relacionados.",
  "Manage this domain in the provider console.": "Configure este dominio en la consola del proveedor.", "Passed": "Verificado", "Unverified": "Sin verificar",
});

Object.assign(zhCN, {
  "Offboarding immediately revokes local sessions and mailbox access. Handover and remote access actions remain visible in the operation.": "离职立即撤销本地会话和邮箱访问。交接与远程访问处理进度保存在操作记录中。",
  "All shared mailbox access is revoked during offboarding.": "离职时撤销全部共享邮箱访问授权。",
  "Administrator reported completion": "管理员已报告完成", "Report external completion": "报告外部处理完成", "Report completion": "报告完成", "Completion note": "完成说明",
  "This records the administrator's report. Mailhearth has not verified remote access revocation.": "此操作记录管理员的完成报告。Mailhearth 尚未验证远程访问已经撤销。",
});
Object.assign(zhTW, {
  "Offboarding immediately revokes local sessions and mailbox access. Handover and remote access actions remain visible in the operation.": "離職立即撤銷本地工作階段和信箱存取。交接與遠端存取處理進度保存在操作記錄中。",
  "All shared mailbox access is revoked during offboarding.": "離職時撤銷全部共用信箱存取授權。",
  "Administrator reported completion": "管理員已報告完成", "Report external completion": "報告外部處理完成", "Report completion": "報告完成", "Completion note": "完成說明",
  "This records the administrator's report. Mailhearth has not verified remote access revocation.": "此操作記錄管理員的完成報告。Mailhearth 尚未驗證遠端存取已經撤銷。",
});
Object.assign(ja, {
  "Offboarding immediately revokes local sessions and mailbox access. Handover and remote access actions remain visible in the operation.": "退職処理でローカルセッションとメールボックスへのアクセスを即時失効させます。引き継ぎとリモートアクセスの処理状況は操作記録に表示されます。",
  "All shared mailbox access is revoked during offboarding.": "退職処理で共有メールボックスへのアクセス権をすべて取り消します。",
  "Administrator reported completion": "管理者が完了を報告", "Report external completion": "外部処理の完了を報告", "Report completion": "完了を報告", "Completion note": "完了の説明",
  "This records the administrator's report. Mailhearth has not verified remote access revocation.": "管理者の完了報告を記録します。Mailhearth はリモートアクセスの失効を検証していません。",
});
Object.assign(es, {
  "Offboarding immediately revokes local sessions and mailbox access. Handover and remote access actions remain visible in the operation.": "La baja revoca inmediatamente las sesiones locales y el acceso a los buzones. La transferencia y las acciones de acceso remoto se muestran en la operación.",
  "All shared mailbox access is revoked during offboarding.": "La baja revoca todo acceso a los buzones compartidos.",
  "Administrator reported completion": "El administrador informó de la finalización", "Report external completion": "Informar de la finalización externa", "Report completion": "Informar de la finalización", "Completion note": "Nota de finalización",
  "This records the administrator's report. Mailhearth has not verified remote access revocation.": "Esto registra el informe del administrador. Mailhearth no ha verificado la revocación del acceso remoto.",
});

Object.assign(zhCN, {
  "Desired targets": "预期目标", "Observed targets": "读取到的目标", "Management mode": "管理方式", "Manage remote rule": "管理远程规则", "Register external configuration": "登记外部配置", "No distribution targets": "没有分发目标",
  "External registration does not verify server delivery or sender authorization.": "外部登记没有验证服务器投递或发件授权。", "Mail connection": "邮件连接", "Mailbox action": "邮箱操作", "Register existing mailbox": "登记已有邮箱", "Create remote mailbox": "创建远程邮箱", "Domain binding": "域名关联", "Credential mode": "凭据方式", "Managed credentials": "平台管理凭据", "Entered credentials": "输入已有凭据", "Mailbox address": "完整邮箱地址",
  "Managed credentials use the enabled connection templates.": "平台管理凭据使用已启用的连接模板。", "Create and verify mailbox": "创建并验证邮箱", "Verify and register mailbox": "验证并登记邮箱",
});
Object.assign(zhTW, {
  "Desired targets": "預期目標", "Observed targets": "讀取到的目標", "Management mode": "管理方式", "Manage remote rule": "管理遠端規則", "Register external configuration": "登記外部設定", "No distribution targets": "沒有分發目標",
  "External registration does not verify server delivery or sender authorization.": "外部登記沒有驗證伺服器投遞或寄件授權。", "Mail connection": "郵件連線", "Mailbox action": "信箱操作", "Register existing mailbox": "登記現有信箱", "Create remote mailbox": "建立遠端信箱", "Domain binding": "網域關聯", "Credential mode": "憑證方式", "Managed credentials": "平台管理憑證", "Entered credentials": "輸入現有憑證", "Mailbox address": "完整信箱地址",
  "Managed credentials use the enabled connection templates.": "平台管理憑證使用已啟用的連線範本。", "Create and verify mailbox": "建立並驗證信箱", "Verify and register mailbox": "驗證並登記信箱",
});
Object.assign(ja, {
  "Desired targets": "設定予定の宛先", "Observed targets": "確認された宛先", "Management mode": "管理方法", "Manage remote rule": "リモートルールを管理", "Register external configuration": "外部設定を登録", "No distribution targets": "配信先がありません",
  "External registration does not verify server delivery or sender authorization.": "外部登録ではサーバーの配信や送信者の権限は検証されません。", "Mail connection": "メール接続", "Mailbox action": "メールボックス操作", "Register existing mailbox": "既存のメールボックスを登録", "Create remote mailbox": "リモートメールボックスを作成", "Domain binding": "ドメイン関連付け", "Credential mode": "認証情報の方式", "Managed credentials": "管理対象の認証情報", "Entered credentials": "既存の認証情報を入力", "Mailbox address": "メールボックスの完全なアドレス",
  "Managed credentials use the enabled connection templates.": "管理対象の認証情報には有効な接続テンプレートを使用します。", "Create and verify mailbox": "メールボックスを作成して検証", "Verify and register mailbox": "メールボックスを検証して登録",
});
Object.assign(es, {
  "Desired targets": "Destinos solicitados", "Observed targets": "Destinos observados", "Management mode": "Modo de gestión", "Manage remote rule": "Gestionar regla remota", "Register external configuration": "Registrar configuración externa", "No distribution targets": "Sin destinos de distribución",
  "External registration does not verify server delivery or sender authorization.": "El registro externo no verifica la entrega del servidor ni la autorización del remitente.", "Mail connection": "Conexión de correo", "Mailbox action": "Acción del buzón", "Register existing mailbox": "Registrar buzón existente", "Create remote mailbox": "Crear buzón remoto", "Domain binding": "Asociación de dominio", "Credential mode": "Modo de credenciales", "Managed credentials": "Credenciales gestionadas", "Entered credentials": "Introducir credenciales existentes", "Mailbox address": "Dirección completa del buzón",
  "Managed credentials use the enabled connection templates.": "Las credenciales gestionadas utilizan las plantillas de conexión habilitadas.", "Create and verify mailbox": "Crear y verificar buzón", "Verify and register mailbox": "Verificar y registrar buzón",
});

Object.assign(zhCN, {
  "Server hostname": "服务器 hostname", "Port": "端口", "Use a configured CA bundle ID, or leave blank to use system certificates.": "使用部署者配置的证书编号；留空使用系统证书。",
  "Provider": "服务商", "Manual connection": "手动连接", "Manual IMAP/SMTP": "手动 IMAP/SMTP", "Connection name": "连接名称", "Migadu account email": "Migadu 账户邮箱",
  "Domain scope": "域名范围", "Leave blank for all domains; separate domains with commas or whitespace.": "留空选择全部域名；多个域名使用逗号或空白分隔。", "Save connection": "保存连接", "Mailbox type": "邮箱类型",
  "{protocol} username": "{protocol} 用户名", "{protocol} password or application password": "{protocol} 密码或应用密码", "Sent copy": "已发送副本", "Saved by Mailhearth": "Mailhearth 保存", "Saved by mail server": "邮件服务器保存", "Create member": "创建成员",
  "Mail connections": "邮件连接", "Add connection": "添加连接", "Management check": "管理验证", "Configuration revision": "配置版本", "Edit connection configuration": "编辑连接配置", "Verify connection": "验证连接",
  "Disable connection": "停用连接", "Enable connection": "启用连接", "Enter the full connection name to remove this empty connection.": "请输入完整连接名称，移除空连接。", "Remove empty connection": "移除空连接",
  "Add mail connection": "添加邮件连接", "Edit mail connection": "编辑邮件连接", "Mailbox registered": "邮箱登记成功", "Select resources to import": "选择导入资源", "Resources imported": "资源导入成功",
  "Changing the API username requires its API key.": "更改 API 用户名需要提供对应 API key。", "Update API key": "更新 API key", "Leave blank to keep the current API key.": "留空保留当前 API key。", "All domains": "全部域名", "Selected domains": "指定域名", "Domain list": "域名列表", "Verify and update connection": "验证并更新连接",
  "Protocol configuration · mailbox revision {revision}": "协议配置 · 邮箱版本 {revision}", "Network configuration": "网络配置", "Inherit connection template": "继承连接模板", "Use independent network configuration": "使用独立网络配置", "Disable protocol": "停用协议",
  "Authentication credential": "认证凭据", "Keep credential {id}": "保留凭据 {id}", "Enter a new password or application password": "输入新密码或应用密码", "Password or application password": "密码或应用密码", "Verify and update protocol configuration": "验证并更新协议配置",
});
Object.assign(zhTW, {
  "Server hostname": "伺服器 hostname", "Port": "連接埠", "Use a configured CA bundle ID, or leave blank to use system certificates.": "使用部署者設定的憑證編號；留空使用系統憑證。",
  "Provider": "服務商", "Manual connection": "手動連線", "Manual IMAP/SMTP": "手動 IMAP/SMTP", "Connection name": "連線名稱", "Migadu account email": "Migadu 帳戶信箱",
  "Domain scope": "網域範圍", "Leave blank for all domains; separate domains with commas or whitespace.": "留空選擇全部網域；多個網域使用逗號或空白分隔。", "Save connection": "儲存連線", "Mailbox type": "信箱類型",
  "{protocol} username": "{protocol} 使用者名稱", "{protocol} password or application password": "{protocol} 密碼或應用程式密碼", "Sent copy": "寄件副本", "Saved by Mailhearth": "Mailhearth 儲存", "Saved by mail server": "郵件伺服器儲存", "Create member": "建立成員",
  "Mail connections": "郵件連線", "Add connection": "新增連線", "Management check": "管理驗證", "Configuration revision": "設定版本", "Edit connection configuration": "編輯連線設定", "Verify connection": "驗證連線",
  "Disable connection": "停用連線", "Enable connection": "啟用連線", "Enter the full connection name to remove this empty connection.": "請輸入完整連線名稱，移除空連線。", "Remove empty connection": "移除空連線",
  "Add mail connection": "新增郵件連線", "Edit mail connection": "編輯郵件連線", "Mailbox registered": "信箱登記成功", "Select resources to import": "選擇匯入資源", "Resources imported": "資源匯入成功",
  "Changing the API username requires its API key.": "變更 API 使用者名稱需要提供對應 API key。", "Update API key": "更新 API key", "Leave blank to keep the current API key.": "留空保留目前 API key。", "All domains": "全部網域", "Selected domains": "指定網域", "Domain list": "網域清單", "Verify and update connection": "驗證並更新連線",
  "Protocol configuration · mailbox revision {revision}": "協定設定 · 信箱版本 {revision}", "Network configuration": "網路設定", "Inherit connection template": "繼承連線範本", "Use independent network configuration": "使用獨立網路設定", "Disable protocol": "停用協定",
  "Authentication credential": "認證憑證", "Keep credential {id}": "保留憑證 {id}", "Enter a new password or application password": "輸入新密碼或應用程式密碼", "Password or application password": "密碼或應用程式密碼", "Verify and update protocol configuration": "驗證並更新協定設定",
});
Object.assign(ja, {
  "Server hostname": "サーバーの hostname", "Port": "ポート", "Use a configured CA bundle ID, or leave blank to use system certificates.": "設定済みの証明書番号を指定してください。空欄の場合はシステム証明書を使用します。",
  "Provider": "プロバイダー", "Manual connection": "手動接続", "Manual IMAP/SMTP": "手動 IMAP/SMTP", "Connection name": "接続名", "Migadu account email": "Migadu アカウントのメールアドレス",
  "Domain scope": "ドメインの対象範囲", "Leave blank for all domains; separate domains with commas or whitespace.": "空欄はすべてのドメインを対象にします。複数のドメインはカンマまたは空白で区切ってください。", "Save connection": "接続を保存", "Mailbox type": "メールボックスの種類",
  "{protocol} username": "{protocol} ユーザー名", "{protocol} password or application password": "{protocol} パスワードまたはアプリパスワード", "Sent copy": "送信済みコピー", "Saved by Mailhearth": "Mailhearth が保存", "Saved by mail server": "メールサーバーが保存", "Create member": "メンバーを作成",
  "Mail connections": "メール接続", "Add connection": "接続を追加", "Management check": "管理認証の検証", "Configuration revision": "設定リビジョン", "Edit connection configuration": "接続設定を編集", "Verify connection": "接続を検証",
  "Disable connection": "接続を無効化", "Enable connection": "接続を有効化", "Enter the full connection name to remove this empty connection.": "この空の接続を削除するには接続名を完全に入力してください。", "Remove empty connection": "空の接続を削除",
  "Add mail connection": "メール接続を追加", "Edit mail connection": "メール接続を編集", "Mailbox registered": "メールボックスを登録しました", "Select resources to import": "取り込むリソースを選択", "Resources imported": "リソースを取り込みました",
  "Changing the API username requires its API key.": "API ユーザー名の変更には対応する API key が必要です。", "Update API key": "API key を更新", "Leave blank to keep the current API key.": "空欄の場合は現在の API key を保持します。", "All domains": "すべてのドメイン", "Selected domains": "指定したドメイン", "Domain list": "ドメイン一覧", "Verify and update connection": "接続を検証して更新",
  "Protocol configuration · mailbox revision {revision}": "プロトコル設定 · メールボックスのリビジョン {revision}", "Network configuration": "ネットワーク設定", "Inherit connection template": "接続テンプレートを継承", "Use independent network configuration": "独立したネットワーク設定を使用", "Disable protocol": "プロトコルを無効化",
  "Authentication credential": "認証情報", "Keep credential {id}": "認証情報 {id} を保持", "Enter a new password or application password": "新しいパスワードまたはアプリパスワードを入力", "Password or application password": "パスワードまたはアプリパスワード", "Verify and update protocol configuration": "プロトコル設定を検証して更新",
});
Object.assign(es, {
  "Server hostname": "Hostname del servidor", "Port": "Puerto", "Use a configured CA bundle ID, or leave blank to use system certificates.": "Use un ID de certificados configurado o déjelo vacío para usar los certificados del sistema.",
  "Provider": "Proveedor", "Manual connection": "Conexión manual", "Manual IMAP/SMTP": "IMAP/SMTP manual", "Connection name": "Nombre de la conexión", "Migadu account email": "Correo de la cuenta de Migadu",
  "Domain scope": "Ámbito de dominios", "Leave blank for all domains; separate domains with commas or whitespace.": "Déjelo vacío para todos los dominios; sepárelos con comas o espacios.", "Save connection": "Guardar conexión", "Mailbox type": "Tipo de buzón",
  "{protocol} username": "Usuario de {protocol}", "{protocol} password or application password": "Contraseña o contraseña de aplicación de {protocol}", "Sent copy": "Copia de enviados", "Saved by Mailhearth": "Guardada por Mailhearth", "Saved by mail server": "Guardada por el servidor de correo", "Create member": "Crear miembro",
  "Mail connections": "Conexiones de correo", "Add connection": "Añadir conexión", "Management check": "Verificación de administración", "Configuration revision": "Revisión de configuración", "Edit connection configuration": "Editar configuración de conexión", "Verify connection": "Verificar conexión",
  "Disable connection": "Desactivar conexión", "Enable connection": "Activar conexión", "Enter the full connection name to remove this empty connection.": "Introduzca el nombre completo para eliminar esta conexión vacía.", "Remove empty connection": "Eliminar conexión vacía",
  "Add mail connection": "Añadir conexión de correo", "Edit mail connection": "Editar conexión de correo", "Mailbox registered": "Buzón registrado", "Select resources to import": "Seleccionar recursos para importar", "Resources imported": "Recursos importados",
  "Changing the API username requires its API key.": "Cambiar el usuario de API requiere su API key.", "Update API key": "Actualizar API key", "Leave blank to keep the current API key.": "Déjelo vacío para conservar la API key actual.", "All domains": "Todos los dominios", "Selected domains": "Dominios seleccionados", "Domain list": "Lista de dominios", "Verify and update connection": "Verificar y actualizar conexión",
  "Protocol configuration · mailbox revision {revision}": "Configuración de protocolos · revisión del buzón {revision}", "Network configuration": "Configuración de red", "Inherit connection template": "Heredar plantilla de conexión", "Use independent network configuration": "Usar configuración de red independiente", "Disable protocol": "Desactivar protocolo",
  "Authentication credential": "Credencial de autenticación", "Keep credential {id}": "Conservar credencial {id}", "Enter a new password or application password": "Introducir una nueva contraseña o contraseña de aplicación", "Password or application password": "Contraseña o contraseña de aplicación", "Verify and update protocol configuration": "Verificar y actualizar configuración de protocolos",
});

Object.assign(dicts["zh-CN"], { "DNS status unverified": "DNS 状态尚未验证", "configured a mail connection": "配置了邮件连接", "synced a mail connection": "同步了邮件连接" });
Object.assign(dicts["zh-TW"], { "DNS status unverified": "DNS 狀態尚未驗證", "configured a mail connection": "設定了郵件連線", "synced a mail connection": "同步了郵件連線" });
Object.assign(dicts.ja, { "DNS status unverified": "DNS 状態は未検証です", "configured a mail connection": "メール接続を設定しました", "synced a mail connection": "メール接続を同期しました" });
Object.assign(dicts.es, { "DNS status unverified": "Estado DNS sin verificar", "configured a mail connection": "configuró una conexión de correo", "synced a mail connection": "sincronizó una conexión de correo" });

Object.assign(zhCN, {
  "CA bundle ID": "CA 证书集合 ID", "Balance": "余额", "Not checked": "尚未检查", "Failed": "失败", "Check expired": "检查已过期", "Synchronized": "已同步", "Pending": "等待处理", "Error": "错误", "Awaiting confirmation": "等待确认", "Blocked": "已阻止", "Present": "存在", "Missing": "缺失", "Inaccessible": "无法访问", "Externally managed": "外部管理", "Queued": "等待执行", "Running": "正在执行", "Succeeded": "执行成功", "Action required": "需要处理", "Cancelled": "已取消", "Ready": "已就绪", "Not configured": "尚未配置", "Automatic": "自动处理", "Unsupported": "不支持", "Degraded": "部分功能不可用", "Mailbox forwarding": "邮箱转发", "Address rule": "地址规则", "External rule": "外部规则", "Identity": "发件身份",
  "Select the source mailbox and domain for forwarding. Delivery modes remain unverified until delivery is tested.": "导入转发时请选择来源邮箱和所属域名。完成投递测试之前，投递方式保持尚未验证。",
  "Delivery mode has not been verified by a delivery test.": "投递方式尚未通过投递测试验证。",
  "At least 10 characters": "至少 10 个字符", "No mailboxes.": "没有邮箱。",
  "from: to: subject: is:unread is:flagged has:attachment since:2026-01-31 larger:2M": "from: to: subject: is:unread is:flagged has:attachment since:2026-01-31 larger:2M",
});
Object.assign(zhTW, {
  "CA bundle ID": "CA 憑證集合 ID", "Balance": "餘額", "Not checked": "尚未檢查", "Failed": "失敗", "Check expired": "檢查已過期", "Synchronized": "已同步", "Pending": "等待處理", "Error": "錯誤", "Awaiting confirmation": "等待確認", "Blocked": "已阻擋", "Present": "存在", "Missing": "缺失", "Inaccessible": "無法存取", "Externally managed": "外部管理", "Queued": "等待執行", "Running": "正在執行", "Succeeded": "執行成功", "Action required": "需要處理", "Cancelled": "已取消", "Ready": "已就緒", "Not configured": "尚未設定", "Automatic": "自動處理", "Unsupported": "不支援", "Degraded": "部分功能無法使用", "Mailbox forwarding": "信箱轉寄", "Address rule": "地址規則", "External rule": "外部規則", "Identity": "寄件身分",
  "Select the source mailbox and domain for forwarding. Delivery modes remain unverified until delivery is tested.": "匯入轉寄時請選擇來源信箱和所屬網域。完成投遞測試之前，投遞方式保持尚未驗證。",
  "Delivery mode has not been verified by a delivery test.": "投遞方式尚未通過投遞測試驗證。",
  "At least 10 characters": "至少 10 個字元", "No mailboxes.": "沒有信箱。",
  "from: to: subject: is:unread is:flagged has:attachment since:2026-01-31 larger:2M": "from: to: subject: is:unread is:flagged has:attachment since:2026-01-31 larger:2M",
});
Object.assign(ja, {
  "CA bundle ID": "CA 証明書セット ID", "Balance": "残高", "Not checked": "未確認", "Failed": "失敗", "Check expired": "検証期限切れ", "Synchronized": "同期済み", "Pending": "処理待ち", "Error": "エラー", "Awaiting confirmation": "確認待ち", "Blocked": "ブロック済み", "Present": "存在", "Missing": "見つかりません", "Inaccessible": "アクセス不可", "Externally managed": "外部管理", "Queued": "実行待ち", "Running": "実行中", "Succeeded": "成功", "Action required": "対応が必要", "Cancelled": "キャンセル済み", "Ready": "準備完了", "Not configured": "未設定", "Automatic": "自動処理", "Unsupported": "非対応", "Degraded": "一部機能を利用できません", "Mailbox forwarding": "メールボックスの転送", "Address rule": "アドレスルール", "External rule": "外部ルール", "Identity": "送信者情報",
  "Select the source mailbox and domain for forwarding. Delivery modes remain unverified until delivery is tested.": "転送元のメールボックスと所属ドメインを選択してください。配信テストが完了するまで配信方式は未検証のままです。",
  "Delivery mode has not been verified by a delivery test.": "配信方式は配信テストで検証されていません。",
  "At least 10 characters": "10 文字以上", "No mailboxes.": "メールボックスがありません。",
  "from: to: subject: is:unread is:flagged has:attachment since:2026-01-31 larger:2M": "from: to: subject: is:unread is:flagged has:attachment since:2026-01-31 larger:2M",
});
Object.assign(es, {
  "CA bundle ID": "ID del conjunto de certificados CA", "Balance": "Saldo", "Not checked": "Sin comprobar", "Failed": "Fallido", "Check expired": "Comprobación caducada", "Synchronized": "Sincronizado", "Pending": "Pendiente", "Error": "Error", "Awaiting confirmation": "Pendiente de confirmación", "Blocked": "Bloqueado", "Present": "Presente", "Missing": "Ausente", "Inaccessible": "Inaccesible", "Externally managed": "Administrado externamente", "Queued": "En espera", "Running": "En ejecución", "Succeeded": "Completado", "Action required": "Requiere intervención", "Cancelled": "Cancelado", "Ready": "Listo", "Not configured": "Sin configurar", "Automatic": "Automático", "Unsupported": "No compatible", "Degraded": "Algunas funciones no están disponibles", "Mailbox forwarding": "Reenvío del buzón", "Address rule": "Regla de dirección", "External rule": "Regla externa", "Identity": "Identidad de envío",
  "Select the source mailbox and domain for forwarding. Delivery modes remain unverified until delivery is tested.": "Seleccione el buzón de origen y su dominio para importar el reenvío. Los modos de entrega permanecen sin verificar hasta completar una prueba de entrega.",
  "Delivery mode has not been verified by a delivery test.": "El modo de entrega no se ha verificado mediante una prueba de entrega.",
  "At least 10 characters": "Al menos 10 caracteres", "No mailboxes.": "No hay buzones.",
  "from: to: subject: is:unread is:flagged has:attachment since:2026-01-31 larger:2M": "from: to: subject: is:unread is:flagged has:attachment since:2026-01-31 larger:2M",
});

Object.assign(zhCN, { "API management": "API 管理", "Accepted": "已接受", "Not started": "尚未开始", "Sending": "正在发送", "Saving": "正在保存", "Saved": "已保存", "Skipped": "已跳过" });
Object.assign(zhTW, { "API management": "API 管理", "Accepted": "已接受", "Not started": "尚未開始", "Sending": "正在寄送", "Saving": "正在儲存", "Saved": "已儲存", "Skipped": "已略過" });
Object.assign(ja, { "API management": "API 管理", "Accepted": "受付済み", "Not started": "未開始", "Sending": "送信中", "Saving": "保存中", "Saved": "保存済み", "Skipped": "スキップ済み" });
Object.assign(es, { "API management": "Administración por API", "Accepted": "Aceptado", "Not started": "Sin iniciar", "Sending": "Enviando", "Saving": "Guardando", "Saved": "Guardado", "Skipped": "Omitido" });

Object.assign(zhCN, { "Protocol status": "协议状态" });
Object.assign(zhTW, { "Protocol status": "協定狀態" });
Object.assign(ja, { "Protocol status": "プロトコルの状態" });
Object.assign(es, { "Protocol status": "Estado de protocolos" });

Object.assign(zhCN, {
  "This address is already registered.": "该地址已经登记。", "This identity is already registered.": "该发件身份已经登记。", "Use the current connection-specific interface.": "请使用当前指定邮件连接的接口。", "This resource has dependencies.": "该资源存在关联依赖。", "The saved credential could not be decrypted.": "无法解密已保存的凭据。", "This protocol or connection is disabled.": "该协议或连接已停用。", "Configure this protocol before using it.": "请配置该协议后再使用。", "Complete the action in the provider console and report the result.": "请在服务商管理页面完成操作并报告结果。", "Configure the required special folder.": "请配置所需的特殊文件夹。", "You do not have permission for this action.": "您没有执行该操作的权限。", "This request ID was already used for different content.": "该 requestId 已用于其他请求内容。", "Check the request fields and current resource state.": "请检查请求字段及当前资源状态。", "The operation result is incomplete.": "操作结果缺少必要信息。", "An internal error occurred.": "发生内部错误。", "Archive this mailbox to retain its history.": "请归档该邮箱以保留历史记录。", "The requested resource was not found.": "未找到请求的资源。", "Another operation is using this resource.": "另一个操作正在使用该资源。", "This operation cannot be cancelled in its current state.": "当前状态无法取消该操作。", "This operation cannot be checked in its current state.": "当前状态无法核查该操作。", "This operation cannot be retried in its current state.": "当前状态无法重试该操作。", "Management authentication failed.": "管理认证失败。", "Protocol authentication failed.": "协议认证失败。", "The server does not support the required authentication mechanism.": "服务器不支持所需的认证机制。", "The remote result requires checking.": "远程结果需要核查。", "This action requires verification.": "该操作需要验证。", "The configuration changed. Refresh before continuing.": "配置已经变化，请刷新后继续。", "This secret has already been claimed.": "该秘密已经领取。", "The secret claim has expired.": "秘密领取期限已经到期。", "The server lacks a required Sieve extension.": "服务器缺少所需的 Sieve 扩展。", "Confirm takeover of the active Sieve script.": "请确认接管当前 active Sieve script。", "The staged message is unavailable.": "暂存邮件不可用。", "The draft location changed.": "草稿位置已经变化。", "The staged message content changed.": "暂存邮件内容已经变化。", "This sending request cannot be retried in its current state.": "当前状态无法重试该发送请求。", "The targets do not meet this provider's restrictions.": "目标不符合该服务商的限制。", "The authentication mode is unsupported.": "不支持该认证方式。", "This operation is unsupported.": "不支持该操作。", "The remote service request failed.": "远程服务请求失败。",
});
Object.assign(zhTW, {
  "This address is already registered.": "該地址已登記。", "This identity is already registered.": "該寄件身分已登記。", "Use the current connection-specific interface.": "請使用目前指定郵件連線的介面。", "This resource has dependencies.": "該資源存在關聯相依項目。", "The saved credential could not be decrypted.": "無法解密已保存的憑證。", "This protocol or connection is disabled.": "該協定或連線已停用。", "Configure this protocol before using it.": "請設定該協定後再使用。", "Complete the action in the provider console and report the result.": "請在供應商管理頁面完成操作並報告結果。", "Configure the required special folder.": "請設定所需的特殊資料夾。", "You do not have permission for this action.": "您沒有執行該操作的權限。", "This request ID was already used for different content.": "該 requestId 已用於其他請求內容。", "Check the request fields and current resource state.": "請檢查請求欄位及目前資源狀態。", "The operation result is incomplete.": "操作結果缺少必要資訊。", "An internal error occurred.": "發生內部錯誤。", "Archive this mailbox to retain its history.": "請封存該信箱以保留歷史記錄。", "The requested resource was not found.": "找不到請求的資源。", "Another operation is using this resource.": "另一個操作正在使用該資源。", "This operation cannot be cancelled in its current state.": "目前狀態無法取消該操作。", "This operation cannot be checked in its current state.": "目前狀態無法查核該操作。", "This operation cannot be retried in its current state.": "目前狀態無法重試該操作。", "Management authentication failed.": "管理驗證失敗。", "Protocol authentication failed.": "協定驗證失敗。", "The server does not support the required authentication mechanism.": "伺服器不支援所需的驗證機制。", "The remote result requires checking.": "遠端結果需要查核。", "This action requires verification.": "該操作需要驗證。", "The configuration changed. Refresh before continuing.": "設定已變更，請重新整理後繼續。", "This secret has already been claimed.": "該秘密已領取。", "The secret claim has expired.": "秘密領取期限已到期。", "The server lacks a required Sieve extension.": "伺服器缺少所需的 Sieve 擴充功能。", "Confirm takeover of the active Sieve script.": "請確認接管目前 active Sieve script。", "The staged message is unavailable.": "暫存郵件無法使用。", "The draft location changed.": "草稿位置已變更。", "The staged message content changed.": "暫存郵件內容已變更。", "This sending request cannot be retried in its current state.": "目前狀態無法重試該寄送請求。", "The targets do not meet this provider's restrictions.": "目標不符合該供應商的限制。", "The authentication mode is unsupported.": "不支援該驗證方式。", "This operation is unsupported.": "不支援該操作。", "The remote service request failed.": "遠端服務請求失敗。",
});
Object.assign(ja, {
  "This address is already registered.": "このアドレスは登録済みです。", "This identity is already registered.": "この送信者情報は登録済みです。", "Use the current connection-specific interface.": "対象接続を指定する現行インターフェースを使用してください。", "This resource has dependencies.": "このリソースには依存関係があります。", "The saved credential could not be decrypted.": "保存した認証情報を復号できません。", "This protocol or connection is disabled.": "このプロトコルまたは接続は無効です。", "Configure this protocol before using it.": "利用前にこのプロトコルを設定してください。", "Complete the action in the provider console and report the result.": "プロバイダーの管理画面で処理を完了し、結果を報告してください。", "Configure the required special folder.": "必要な特殊フォルダーを設定してください。", "You do not have permission for this action.": "この操作の権限がありません。", "This request ID was already used for different content.": "この requestId は別の内容に使用されています。", "Check the request fields and current resource state.": "入力フィールドと現在のリソース状態を確認してください。", "The operation result is incomplete.": "操作結果に必要な情報がありません。", "An internal error occurred.": "内部エラーが発生しました。", "Archive this mailbox to retain its history.": "履歴を保持するためメールボックスをアーカイブしてください。", "The requested resource was not found.": "対象リソースが見つかりません。", "Another operation is using this resource.": "別の操作がこのリソースを使用中です。", "This operation cannot be cancelled in its current state.": "現在の状態ではキャンセルできません。", "This operation cannot be checked in its current state.": "現在の状態では結果を確認できません。", "This operation cannot be retried in its current state.": "現在の状態では再試行できません。", "Management authentication failed.": "管理認証に失敗しました。", "Protocol authentication failed.": "プロトコル認証に失敗しました。", "The server does not support the required authentication mechanism.": "必要な認証方式にサーバーが対応していません。", "The remote result requires checking.": "リモート結果の照合が必要です。", "This action requires verification.": "この操作には検証が必要です。", "The configuration changed. Refresh before continuing.": "設定が変更されています。更新してから続行してください。", "This secret has already been claimed.": "この秘密は取得済みです。", "The secret claim has expired.": "秘密の取得期限が切れています。", "The server lacks a required Sieve extension.": "必要な Sieve 拡張機能がありません。", "Confirm takeover of the active Sieve script.": "有効な Sieve スクリプトの引き継ぎを確認してください。", "The staged message is unavailable.": "一時保存したメールを利用できません。", "The draft location changed.": "下書きの位置が変わりました。", "The staged message content changed.": "一時保存したメールの内容が変わりました。", "This sending request cannot be retried in its current state.": "現在の状態ではこの送信を再試行できません。", "The targets do not meet this provider's restrictions.": "宛先がプロバイダーの制約を満たしていません。", "The authentication mode is unsupported.": "この認証方式には対応していません。", "This operation is unsupported.": "この操作には対応していません。", "The remote service request failed.": "リモートサービスへのリクエストが失敗しました。",
});
Object.assign(es, {
  "This address is already registered.": "Esta dirección ya está registrada.", "This identity is already registered.": "Esta identidad ya está registrada.", "Use the current connection-specific interface.": "Use la interfaz actual que especifica la conexión.", "This resource has dependencies.": "Este recurso tiene dependencias.", "The saved credential could not be decrypted.": "No se pudo descifrar la credencial guardada.", "This protocol or connection is disabled.": "Este protocolo o conexión está deshabilitado.", "Configure this protocol before using it.": "Configure este protocolo antes de usarlo.", "Complete the action in the provider console and report the result.": "Complete la acción en la consola del proveedor e informe del resultado.", "Configure the required special folder.": "Configure la carpeta especial necesaria.", "You do not have permission for this action.": "No tiene permiso para esta acción.", "This request ID was already used for different content.": "Este requestId ya se utilizó para otro contenido.", "Check the request fields and current resource state.": "Compruebe los campos y el estado actual del recurso.", "The operation result is incomplete.": "El resultado de la operación está incompleto.", "An internal error occurred.": "Se produjo un error interno.", "Archive this mailbox to retain its history.": "Archive este buzón para conservar su historial.", "The requested resource was not found.": "No se encontró el recurso solicitado.", "Another operation is using this resource.": "Otra operación está utilizando este recurso.", "This operation cannot be cancelled in its current state.": "Esta operación no puede cancelarse en su estado actual.", "This operation cannot be checked in its current state.": "Esta operación no puede comprobarse en su estado actual.", "This operation cannot be retried in its current state.": "Esta operación no puede reintentarse en su estado actual.", "Management authentication failed.": "Falló la autenticación de administración.", "Protocol authentication failed.": "Falló la autenticación del protocolo.", "The server does not support the required authentication mechanism.": "El servidor no admite el mecanismo de autenticación necesario.", "The remote result requires checking.": "El resultado remoto requiere comprobación.", "This action requires verification.": "Esta acción requiere verificación.", "The configuration changed. Refresh before continuing.": "La configuración cambió. Actualice antes de continuar.", "This secret has already been claimed.": "Este secreto ya se ha reclamado.", "The secret claim has expired.": "El plazo para reclamar el secreto ha caducado.", "The server lacks a required Sieve extension.": "El servidor carece de una extensión Sieve necesaria.", "Confirm takeover of the active Sieve script.": "Confirme la sustitución del script Sieve activo.", "The staged message is unavailable.": "El mensaje temporal no está disponible.", "The draft location changed.": "Cambió la ubicación del borrador.", "The staged message content changed.": "Cambió el contenido del mensaje temporal.", "This sending request cannot be retried in its current state.": "Esta solicitud de envío no puede reintentarse en su estado actual.", "The targets do not meet this provider's restrictions.": "Los destinos no cumplen las restricciones del proveedor.", "The authentication mode is unsupported.": "El modo de autenticación no es compatible.", "This operation is unsupported.": "Esta operación no es compatible.", "The remote service request failed.": "Falló la solicitud al servicio remoto.",
});

export function t(key: string, vars?: Record<string, string | number>): string {
  let s = lang.value === "en" ? key : (dicts[lang.value][key] ?? key);
  if (vars) for (const [k, v] of Object.entries(vars)) s = s.replace(new RegExp(`\\{${k}\\}`, "g"), String(v));
  return s;
}

export function folderLabel(display: string, role: string): string {
  const m: Record<string, string> = { inbox: "Inbox", drafts: "Drafts", sent: "Sent", trash: "Trash", junk: "Junk", archive: "Archive" };
  return role && m[role] ? t(m[role]) : display;
}

const kindNames: Record<string, string> = { primary: "Primary", alias: "Alias", forward: "Forward", group: "Group", catchall: "Catch-all", prefix: "Prefix", member: "Member", shared: "Shared", external_rule: "External rule" };

// kindLabel translates an address/contact kind key.
export function kindLabel(kind: string): string {
  return t(kindNames[kind] ?? kind);
}
