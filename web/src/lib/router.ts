// Minimal client-side router. Paths are split into segments; components read
// them through the `route` signal and navigate with `go()`.
import { signal } from "@preact/signals";

export interface Route {
  path: string;
  segments: string[];
  query: URLSearchParams;
}

function parse(): Route {
  const path = location.pathname;
  const segments = path.split("/").filter(Boolean).map((s) => {
    try {
      return decodeURIComponent(s);
    } catch {
      return s;
    }
  });
  return { path, segments, query: new URLSearchParams(location.search) };
}

export const route = signal<Route>(parse());

export function go(path: string, replace = false) {
  if (path === location.pathname + location.search) return;
  if (replace) history.replaceState(null, "", path);
  else history.pushState(null, "", path);
  route.value = parse();
}

window.addEventListener("popstate", () => {
  route.value = parse();
});

// Intercept plain in-app anchor clicks so they do not reload the page.
document.addEventListener("click", (e) => {
  const a = (e.target as HTMLElement | null)?.closest?.("a");
  if (!a || a.target === "_blank" || a.hasAttribute("download") || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey) return;
  const href = a.getAttribute("href");
  if (!href || !href.startsWith("/") || href.startsWith("/api/")) return;
  e.preventDefault();
  go(href);
});

export function seg(i: number): string | undefined {
  return route.value.segments[i];
}

export function mailPath(mailboxId: number | string, folder = "INBOX", uid?: number | string): string {
  let p = `/mail/${mailboxId}/${encodeURIComponent(folder)}`;
  if (uid) p += `/${uid}`;
  return p;
}
