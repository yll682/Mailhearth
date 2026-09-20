// HTTP client and API types (mirrors the Go JSON payloads).

export class ApiError extends Error {
  status: number;
  code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

export let onUnauthorized: () => void = () => {};
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

export async function api<T = unknown>(method: string, path: string, body?: unknown, init?: { raw?: boolean; signal?: AbortSignal }): Promise<T> {
  const headers: Record<string, string> = { "X-Requested-With": "Mailhearth" };
  let payload: BodyInit | undefined;
  if (body instanceof FormData) payload = body;
  else if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    payload = JSON.stringify(body);
  }
  const res = await fetch(path, { method, headers, body: payload, credentials: "same-origin", signal: init?.signal });
  if (res.status === 401 && !path.startsWith("/api/auth/login") && !path.startsWith("/api/setup/")) onUnauthorized();
  if (!res.ok) {
    let msg = res.statusText, code = "error";
    try {
      const j = await res.json();
      msg = j.error || msg;
      code = j.code || code;
    } catch {}
    throw new ApiError(res.status, code, msg);
  }
  if (init?.raw) return (await res.text()) as unknown as T;
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export const get = <T,>(p: string, signal?: AbortSignal) => api<T>("GET", p, undefined, { signal });
export const post = <T,>(p: string, b?: unknown) => api<T>("POST", p, b ?? {});
export const put = <T,>(p: string, b?: unknown) => api<T>("PUT", p, b ?? {});
export const patch = <T,>(p: string, b?: unknown) => api<T>("PATCH", p, b ?? {});
export const del = <T,>(p: string, b?: unknown) => api<T>("DELETE", p, b);

// --- Types ---

export interface Member {
  id: number; displayName: string; loginEmail: string; roleId: number; roleKey: string; roleName: string; title: string; department: string;
  status: "invited" | "active" | "disabled" | "departed"; hasPassword: boolean; createdAt: string; updatedAt: string; lastLoginAt: string | null; departedAt: string | null;
}
export interface Org { id: number; name: string; createdAt: string }
export interface Role { id: number; key: string; name: string; description: string; permissions: string[]; builtin: boolean; memberCount: number }
export interface Identity { id: number; mailboxId: number; address: string; displayName: string; replyTo: string; signatureHtml: string; isDefault: boolean }
export interface SieveCondition { field: string; header?: string; op: string; value: string }
export interface SieveAction { type: string; folder?: string; address?: string; flag?: string }
export interface SieveRule { id: string; name: string; enabled: boolean; match: "all" | "any"; conditions: SieveCondition[]; actions: SieveAction[] }
export interface Vacation { enabled: boolean; subject: string; body: string; days: number }
export interface MailboxSettings { sieveRules: SieveRule[]; vacation?: Vacation | null; sieveSync?: string }
export interface Mailbox {
  id: number; kind: "personal" | "shared"; address: string; domainId: number | null; displayName: string; ownerMemberId: number | null; ownerName: string;
  hasCredential: boolean; credentialAt: string | null; status: "active" | "suspended" | "archived"; imported: boolean; settings: MailboxSettings; accessCount: number; createdAt: string; updatedAt: string;
}
export interface AccessibleMailbox extends Mailbox { level: "full" | "send" | "read"; identities: Identity[] }
export interface MailboxAccess { id: number; mailboxId: number; memberId: number; memberName: string; level: string; grantedAt: string }
export interface DNSSummary { mx: boolean; spf: boolean; dkim: boolean; dmarc: boolean }
export interface Domain {
  id: number; name: string; isShared: boolean; allowAccountReset: boolean; symbolicSubaddressing: boolean; dns: DNSSummary | null; dnsCheckedAt: string | null;
  status: string; mailboxCount: number; addressCount: number; createdAt: string;
}
export interface Address {
  id: number; domainId: number; domain: string; localPart: string; address: string; kind: "primary" | "alias" | "forward" | "group" | "catchall" | "prefix";
  mailboxId: number | null; groupId: number | null; targets: string[]; pmRuleId: number | null; isPrefix: boolean; isCatchall: boolean; note: string; createdAt: string;
}
export interface Group { id: number; name: string; description: string; memberIds: number[]; address: Address | null; createdAt: string }
export interface AuditEntry { id: number; actorId: number | null; actorName: string; action: string; targetType: string; targetId: string; detail: unknown; createdAt: string }
export interface SetupStatus { needsSetup: boolean; step: "org" | "connect" | "import" | "done"; orgName?: string; devStack: boolean }
export interface Me { member: Member; org: Org; roleKey: string; permissions: string[]; mailboxes: AccessibleMailbox[]; setup: SetupStatus }
export interface Connection { connected: boolean; tokenHint: string; credit: string; lastSyncAt: string | null; lastError: string; apiUrl: string }
export interface Discovery {
  credit: string; domains: { name: string; isShared: boolean; dnsSummary: { passesMx: boolean; passesSpf: boolean; passesDkim: boolean; passesDmarc: boolean } }[];
  users: string[]; rules: { id: number; domainName: string; prefix: boolean; matchUser: string; targetAddresses: string[]; catchall: boolean }[];
  existing: { mailboxes: number; addresses: number };
}
export interface ImportResult { domains: number; mailboxesNew: number; mailboxesKept: number; mailboxesGone: number; addressesNew: number; addressesUpdated: number; addressesGone: number; warnings: string[] }
export interface Overview {
  org: Org; connection: Connection; members: Record<string, number>; mailboxes: Record<string, number>; domains: Domain[]; addresses: number; groups: number;
  unconnected: Mailbox[]; unassigned: Mailbox[]; recentAudit: AuditEntry[]; pool: Record<string, number>;
}
export interface DirectoryEntry { id: number; displayName: string; title: string; department: string; status: string }

export interface Folder { name: string; display: string; delim: string; role: string; total: number; unseen: number; noSelect: boolean; hasChildren: boolean; depth: number }
export interface Addr { name: string; address: string }
export interface Summary {
  uid: number; subject: string; from: Addr[]; to: Addr[]; date: string; size: number; flags: string[]; seen: boolean; flagged: boolean; answered: boolean; draft: boolean;
  forwarded: boolean; hasAttachment: boolean; messageId: string; inReplyTo: string[] | null;
}
export interface Activity { id: number; memberId: number | null; memberName: string; action: string; detail: string; createdAt: string }
export interface TeamState { key: string; assigneeId: number | null; assigneeName: string; status: string; updatedAt: string; activity: Activity[] }
export interface Page { folder: string; uidValidity: number; total: number; page: number; pageSize: number; query: string; messages: Summary[]; team: Record<string, TeamState> }
export interface Part { path: string; mime: string; filename: string; size: number; contentId?: string; disposition: string; isInline: boolean }
export interface Message extends Summary {
  cc: Addr[]; bcc: Addr[]; replyTo: Addr[]; html: string; text: string; hasRemote: boolean; attachments: Part[]; inline: Part[]; headers: Record<string, string>; truncated: boolean; references: string[];
}
export interface MessageResponse { message: Message; viewToken: string; viewUrl: string; team: TeamState; key: string }
export interface Contact { name: string; address: string; kind: string }
export interface Upload { id: string; filename: string; mime: string; size: number }
export interface DNSGuide { ownershipCode: string; records: { type: string; host: string; value: string; priority?: number; purpose: string }[] }
