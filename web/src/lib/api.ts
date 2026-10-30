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
  // Some proxies drop DELETE bodies; send those as POST with a method override.
  if (method === "DELETE" && body !== undefined) {
    method = "POST";
    headers["X-HTTP-Method-Override"] = "DELETE";
  }
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
// Response types are generated from the Go structs (go run ./cmd/apitypes);
// the aliases keep the names the client code uses.
import type { Capability, ConnectionBillingView, ConnectionView, DiscoverySnapshotResource, MessageListResponse, MessageState, MeResponse, OperationView, Organization, SubmissionView, UploadInfo } from "./api-types";
export type * from "./api-types";
export type Org = Organization;
export type Me = MeResponse;
export type MailConnection = ConnectionView;
export type Operation = OperationView;
export type DiscoveryResource = DiscoverySnapshotResource;
export type ConnectionBilling = ConnectionBillingView;
export type Submission = SubmissionView;
export type TeamState = MessageState;
export type Page = MessageListResponse;
export type Upload = UploadInfo;

export const capabilityAvailable = (capability?: Capability) => capability?.support === "automatic" && capability.readiness === "ready" && capability.permissionAllowed;
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
