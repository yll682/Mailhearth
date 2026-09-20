import { signal, computed } from "@preact/signals";
import { get, post, setUnauthorizedHandler, type Me, type AccessibleMailbox } from "./api";
import { go } from "./router";

export const me = signal<Me | null>(null);
export const booted = signal(false);
export const mailboxes = computed(() => me.value?.mailboxes ?? []);

export function can(perm: string): boolean {
  const p = me.value?.permissions ?? [];
  return p.includes("org.owner") || p.includes(perm);
}
export const isAdmin = computed(() => {
  const p = me.value?.permissions ?? [];
  return p.length > 0;
});

export async function loadMe(): Promise<Me | null> {
  try {
    const m = await get<Me>("/api/auth/me");
    me.value = m;
    return m;
  } catch {
    me.value = null;
    return null;
  }
}

export async function refreshMailboxes() {
  if (!me.value) return;
  const list = await get<AccessibleMailbox[]>("/api/mail/mailboxes");
  me.value = { ...me.value, mailboxes: list };
}

export async function logout() {
  try {
    await post("/api/auth/logout");
  } catch {}
  me.value = null;
  go("/login");
}

setUnauthorizedHandler(() => {
  if (me.value) {
    me.value = null;
    go("/login");
  }
});

// --- toasts ---
export interface Toast { id: number; kind: "info" | "success" | "error"; text: string; action?: { label: string; run: () => void } }
export const toasts = signal<Toast[]>([]);
let toastSeq = 1;
export function toast(text: string, kind: Toast["kind"] = "info", action?: Toast["action"], ttl = 4500) {
  const id = toastSeq++;
  toasts.value = [...toasts.value, { id, kind, text, action }];
  setTimeout(() => dismissToast(id), ttl);
  return id;
}
export function dismissToast(id: number) {
  toasts.value = toasts.value.filter((x) => x.id !== id);
}
export function errorToast(e: unknown) {
  const msg = e instanceof Error ? e.message : String(e);
  toast(msg, "error", undefined, 7000);
}
