// Tiny i18n: source strings are English; zh-CN translations are looked up
// by the English text. Unknown strings fall back to English.
import { signal } from "@preact/signals";

export type Lang = "en" | "zh-CN";

function detect(): Lang {
  const saved = localStorage.getItem("mh.lang") as Lang | null;
  if (saved === "en" || saved === "zh-CN") return saved;
  return /^zh/i.test(navigator.language) ? "zh-CN" : "en";
}

export const lang = signal<Lang>(detect());

export function setLang(l: Lang) {
  lang.value = l;
  localStorage.setItem("mh.lang", l);
  document.documentElement.lang = l;
}

const zh: Record<string, string> = {
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
};

export function t(key: string, vars?: Record<string, string | number>): string {
  let s = lang.value === "zh-CN" ? (zh[key] ?? key) : key;
  if (vars) for (const [k, v] of Object.entries(vars)) s = s.replace(new RegExp(`\\{${k}\\}`, "g"), String(v));
  return s;
}

export function folderLabel(display: string, role: string): string {
  const m: Record<string, string> = { inbox: "Inbox", drafts: "Drafts", sent: "Sent", trash: "Trash", junk: "Junk", archive: "Archive" };
  return role && m[role] ? t(m[role]) : display;
}

const kindNames: Record<string, string> = { primary: "Primary", alias: "Alias", forward: "Forward", group: "Group", catchall: "Catch-all", prefix: "Prefix", member: "Member", shared: "Shared" };

// kindLabel translates an address/contact kind key.
export function kindLabel(kind: string): string {
  return t(kindNames[kind] ?? kind);
}
