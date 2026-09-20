import { lang, t } from "./i18n";
import type { Addr } from "./api";

export function fmtDate(iso: string, style: "list" | "full" = "list"): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  const loc = lang.value === "zh-CN" ? "zh-CN" : "en-GB";
  if (style === "full") return d.toLocaleString(loc, { dateStyle: "medium", timeStyle: "short" });
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  if (sameDay) return d.toLocaleTimeString(loc, { hour: "2-digit", minute: "2-digit" });
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  if (d.toDateString() === yesterday.toDateString()) return t("Yesterday");
  if (d.getFullYear() === now.getFullYear()) return d.toLocaleDateString(loc, { month: "short", day: "numeric" });
  return d.toLocaleDateString(loc, { year: "numeric", month: "short", day: "numeric" });
}

export function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10240 ? 1 : 0)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1048576).toFixed(1)} MB`;
  return `${(n / 1073741824).toFixed(2)} GB`;
}

export function addrName(a: Addr | undefined): string {
  if (!a) return "";
  return a.name || a.address;
}

export function addrList(list: Addr[] | undefined, max = 3): string {
  if (!list || list.length === 0) return "";
  const names = list.map(addrName);
  if (names.length <= max) return names.join(", ");
  return names.slice(0, max).join(", ") + ` +${names.length - max}`;
}

export function initials(name: string): string {
  const s = name.trim();
  if (!s) return "?";
  if (/[一-鿿]/.test(s)) return s.slice(-2);
  const parts = s.split(/[\s@._-]+/).filter(Boolean);
  return (parts[0]?.[0] ?? "") + (parts[1]?.[0] ?? "");
}

const hues = [14, 32, 46, 160, 190, 210, 260, 300, 340];
export function hueFor(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0;
  return hues[h % hues.length];
}

export function stripSubjectPrefix(s: string): string {
  return s.replace(/^\s*((re|fwd?|aw|wg|回复|转发)\s*[:：]\s*)+/i, "").trim();
}

export function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);
}

export function copyText(text: string) {
  navigator.clipboard?.writeText(text).catch(() => {});
}
