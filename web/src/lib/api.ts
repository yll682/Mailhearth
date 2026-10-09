// HTTP client and API types (mirrors the Go JSON payloads).
import { errorLabel } from "./errorLabels";

export class ApiError extends Error {
  status: number;
  code: string;
  operationId: string | null;
  details: unknown;
  serverMessage: string;
  constructor(status: number, code: string, message: string, operationId: string | null = null, details: unknown = null) {
    super(`${errorLabel(code, message)}${operationId ? ` · ${operationId}` : ""}`);
    this.serverMessage = message;
    this.status = status;
    this.code = code;
    this.operationId = operationId;
    this.details = details;
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
    let msg = res.statusText, code = "error", operationId: string | null = null, details: unknown = null;
    try {
      const j = await res.json();
      msg = j.error || msg;
      code = j.code || code;
      operationId = typeof j.operationId === "string" ? j.operationId : null;
      details = j.details ?? null;
    } catch {}
    throw new ApiError(res.status, code, msg, operationId, details);
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
	 revision: number;
  id: number; displayName: string; loginEmail: string; roleId: number; roleKey: string; roleName: string; title: string; department: string;
  status: "invited" | "active" | "disabled" | "departed"; hasPassword: boolean; createdAt: string; updatedAt: string; lastLoginAt: string | null; departedAt: string | null;
}
export interface Org { id: number; name: string; createdAt: string }
export interface Role { id: number; key: string; name: string; description: string; permissions: string[]; builtin: boolean; memberCount: number }
export interface Identity { id: number; mailboxId: number; address: string; displayName: string; replyTo: string; signatureHtml: string; isDefault: boolean; authorizationSource: "admin" | "provider"; authorizationStatus: "allowed" | "unverified" | "denied"; revision: number }
export interface SieveCondition { field: string; header?: string; op: string; value: string }
export interface SieveAction { type: string; folder?: string; address?: string; flag?: string }
export interface SieveRule { id: string; name: string; enabled: boolean; match: "all" | "any"; conditions: SieveCondition[]; actions: SieveAction[] }
export interface Vacation { enabled: boolean; subject: string; body: string; days: number }
export interface MailboxSettings { sieveRules: SieveRule[]; vacation?: Vacation | null; sieveSync?: string }
export interface FolderMapping { sent: string | null; drafts: string | null; trash: string | null; junk: string | null; archive: string | null }
export interface FolderMappingView { mailboxId: number; revision: number; mapping: FolderMapping; sentCopyMode: "append" | "server" }
export interface Mailbox {
	protocols: { imap: ProtocolStatus | null; smtp: ProtocolStatus | null; managesieve: ProtocolStatus | null };
  connectionId: number; connectionLabel: string; addressKey: string; managementMode: "api" | "external"; remoteState: string;
  revision: number; accessRevision: number; sentCopyMode: "append" | "server"; folderMapping: FolderMapping;
  id: number; kind: "personal" | "shared"; address: string; domainId: number | null; displayName: string; ownerMemberId: number | null; ownerName: string;
  status: "active" | "suspended" | "archived"; imported: boolean; settings: MailboxSettings; accessCount: number; createdAt: string; updatedAt: string;
}
export interface ProtocolStatus { readiness: "ready" | "unconfigured" | "unverified" | "disabled"; checkStatus: "never" | "passed" | "failed" | "stale" }
export interface AccessibleMailbox extends Mailbox { level: "full" | "send" | "read"; identities: Identity[] }
export interface Submission {
  submissionId: string; mailboxId: number; memberId: number; messageId: string;
  status: "preparing" | "queued" | "running" | "sent" | "sent_copy_failed" | "failed" | "unknown";
  smtpStatus: "not_started" | "submitting" | "accepted" | "rejected" | "unknown";
  sentStatus: "not_started" | "saving" | "saved" | "server_managed" | "failed" | "unknown";
  errorCode: string | null; cleanupErrorCode: string | null;
  sentLocator: { folder: string; uidValidity: number; uid: number } | null;
  createdAt: string; updatedAt: string;
}
export interface MailboxAccess { id: number; mailboxId: number; memberId: number; memberName: string; level: string; grantedAt: string }
export interface DNSSummary { mx: boolean; spf: boolean; dkim: boolean; dmarc: boolean }
export interface Domain {
  id: number; name: string; isShared: boolean; allowAccountReset: boolean; symbolicSubaddressing: boolean; dns: DNSSummary | null; dnsCheckedAt: string | null;
  status: string; mailboxCount: number; addressCount: number; createdAt: string;
}
export interface Address {
	connectionId: number; domainBindingId?: number; revision: number; managementMode: "api" | "external"; syncState: string; desiredTargets: string[]; observedTargets: string[];
  id: number; domainId: number; domain: string; localPart: string; address: string; kind: "primary" | "alias" | "forward" | "group" | "catchall" | "prefix" | "external_rule";
  mailboxId: number | null; groupId: number | null; targets: string[]; pmRuleId: number | null; isPrefix: boolean; isCatchall: boolean; note: string; createdAt: string;
}
export interface Group { id: number; name: string; description: string; memberIds: number[]; address: Address | null; revision: number; createdAt: string }
export interface ResourceOptions {
	 domains: { id: number; name: string }[];
  connections: Pick<MailConnection, "id" | "providerKind" | "label" | "enabled" | "revision" | "protocolDefaults" | "capabilities">[];
  bindings: Pick<DomainBinding, "id" | "domainId" | "domainName" | "connectionId" | "connectionLabel" | "managementMode" | "remoteState" | "revision">[];
}
export interface MailboxForwarding { id: number; mailboxId: number; revision: number; targets: string[]; deliveryMode: "redirect" | "copy" | "unverified"; syncState: string; remoteStatus: { desiredTargets?: string[]; observedTargets?: string[]; statusByTarget?: Record<string, string>; remoteStates?: Record<string, string>; systemVerified?: boolean; verificationSource?: string } }
export interface AuditEntry { id: number; actorId: number | null; actorName: string; action: string; targetType: string; targetId: string; detail: unknown; createdAt: string }
export interface SetupStatus { needsSetup: boolean; step: "org" | "connection" | "mailboxes" | "done"; orgName?: string; devStack: boolean }

export type ProviderKind = "purelymail" | "migadu" | "manual";
export interface ProtocolTemplate { enabled: boolean; host?: string; port?: number; tlsMode?: "tls" | "starttls"; caBundleId?: number | null }
export interface ProtocolTemplates { imap: ProtocolTemplate; smtp: ProtocolTemplate; managesieve: ProtocolTemplate }
export interface Capability { key: string; support: "automatic" | "unsupported" | "external" | "unverified"; readiness: string; permissionAllowed: boolean; reasonCode?: string; constraints?: Record<string, unknown> }
export interface DomainBinding { id: number; domainId: number; domainName: string; connectionId: number; connectionLabel: string; managementMode: "api" | "external"; remoteState: string; providerSettings: { allowAccountReset?: boolean; symbolicSubaddressing?: boolean }; dnsStatus: { mx?: string; spf?: string; dkim?: string; dmarc?: string }; dnsCheckedAt: string | null; revision: number; createdAt: string; updatedAt: string }
export const capabilityAvailable = (capability?: Capability) => capability?.support === "automatic" && capability.readiness === "ready" && capability.permissionAllowed;
export interface MailConnection {
  id: number; providerKind: ProviderKind; label: string; enabled: boolean; revision: number;
  apiBaseUrl: string | null; apiUsername: string | null; apiConfigured: boolean; apiHint: string;
  domainScope: { mode: "all" | "selected"; domains?: string[] }; protocolDefaults: ProtocolTemplates;
  lastApiCheckAt: string | null; lastApiCheckStatus: "never" | "passed" | "failed"; lastApiErrorCode: string | null;
  lastSyncAt: string | null; createdAt: string; updatedAt: string;
  capabilities?: Record<string, Capability>;
}
export interface Operation {
  operationId: string; kind: string; status: "queued" | "running" | "succeeded" | "failed" | "unknown" | "needs_action" | "cancelled";
  result: { snapshotId?: number; resources?: DiscoveryResource[]; id?: number; memberId?: number; inviteLink?: string; mailboxIds?: number[]; mailboxId?: number; domainBindingId?: number; errorDetails?: unknown }; errorCode: string | null;
  steps: { operationId: string; stepKey: string; sequence: number; status: string; resultJson: string; remoteRefJson: string; errorCode?: string; startedAt?: string; finishedAt?: string }[];
  createdAt: string; updatedAt: string;
}
export interface DiscoveryResource {
  resource: { resourceType: string; remoteKey: string; purpose: "domain" | "mailbox" | "routing" | "forwarding" | "sender_identity" | "login_credential"; remoteLocator?: Record<string, string> };
  summary: Record<string, string>;
  addressRule?: { remoteKey: string; remoteLocator: Record<string, string>; domain: string; localPart: string; prefix: boolean; catchall: boolean; targets: string[]; pattern?: string; name?: string };
  identity?: { domain: string; mailboxLocalPart: string; localPart: string; address: string; displayName?: string; passwordUse?: string; maySend?: boolean };
  domain?: { name: string; isShared: boolean; allowAccountReset?: boolean; symbolicSubaddressing?: boolean; dns: { mx: string; spf: string; dkim: string; dmarc: string } };
  forwarding?: { domain: string; localPart: string; targets: string[]; deliveryMode: string; statusByTarget: Record<string, string> };
}
export async function waitOperation(initial: Operation, signal?: AbortSignal): Promise<Operation> {
  let operation = initial;
  while (operation.status === "queued" || operation.status === "running") {
    await new Promise<void>((resolve, reject) => {
      if (signal?.aborted) { reject(signal.reason); return; }
      const abort = () => { clearTimeout(timer); reject(signal?.reason); };
      const timer = setTimeout(() => { signal?.removeEventListener("abort", abort); resolve(); }, 1000);
      signal?.addEventListener("abort", abort, { once: true });
    });
    operation = await get<Operation>(`/api/admin/operations/${operation.operationId}`, signal);
  }
  if (operation.status !== "succeeded") throw new ApiError(409, operation.errorCode ?? operation.status, `${operation.operationId}: ${operation.errorCode ?? operation.status}`, operation.operationId, operation.result.errorDetails);
  return operation;
}
export interface Me { member: Member; org: Org; roleKey: string; permissions: string[]; mailboxes: AccessibleMailbox[]; setup: SetupStatus }
export interface Overview {
  org: Org; connections: { id: number; providerKind: ProviderKind; label: string; enabled: boolean; lastSyncAt: string | null; lastApiCheckStatus?: string; lastApiErrorCode?: string }[]; members: Record<string, number>; mailboxes: Record<string, number>; domains: Domain[]; domainBindings: DomainBinding[]; addresses: number; groups: number;
  unconnected: Mailbox[]; unassigned: Mailbox[]; recentAudit: AuditEntry[]; pool: Record<string, number>;
}
export interface ConnectionBilling {
  connectionId: number; label: string; providerKind: ProviderKind;
  balanceSupport: "automatic" | "unsupported" | "external";
  usageSupport: "automatic" | "unsupported" | "external";
  values: { key: string; value: string; unit: string; domain?: string; period?: string }[];
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
