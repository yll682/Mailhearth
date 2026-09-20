import { useEffect, useRef, useState } from "preact/hooks";
import type { ComponentChildren, JSX } from "preact";
import { t } from "@/lib/i18n";
import { toasts, dismissToast } from "@/lib/state";
import { hueFor, initials } from "@/lib/format";

// --- Icons (inline SVG, stroke-based) ---
const paths: Record<string, string> = {
  inbox: "M22 12h-6l-2 3h-4l-2-3H2M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z",
  send: "m22 2-7 20-4-9-9-4zM22 2 11 13",
  draft: "M12 20h9M16.5 3.5a2.1 2.1 0 1 1 3 3L7 19l-4 1 1-4z",
  trash: "M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2m3 0v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6h14zM10 11v6M14 11v6",
  junk: "M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0zM12 9v4M12 17h.01",
  archive: "M21 8v13H3V8M1 3h22v5H1zM10 12h4",
  folder: "M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z",
  compose: "M12 20h9M16.5 3.5a2.1 2.1 0 1 1 3 3L7 19l-4 1 1-4z",
  search: "M11 19a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM21 21l-4.35-4.35",
  reply: "M9 17 4 12l5-5M20 18v-2a4 4 0 0 0-4-4H4",
  replyall: "M7 17 2 12l5-5M12 17l-5-5 5-5M22 18v-2a4 4 0 0 0-4-4H7",
  forward: "m15 17 5-5-5-5M4 18v-2a4 4 0 0 1 4-4h12",
  star: "m12 2 3.09 6.26L22 9.27l-5 4.87 1.18 6.88L12 17.77l-6.18 3.25L7 14.14 2 9.27l6.91-1.01z",
  paperclip: "m21.44 11.05-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48",
  more: "M12 13a1 1 0 1 0 0-2 1 1 0 0 0 0 2zM19 13a1 1 0 1 0 0-2 1 1 0 0 0 0 2zM5 13a1 1 0 1 0 0-2 1 1 0 0 0 0 2z",
  x: "M18 6 6 18M6 6l12 12",
  check: "M20 6 9 17l-5-5",
  chevron: "m9 18 6-6-6-6",
  down: "m6 9 6 6 6-6",
  menu: "M3 12h18M3 6h18M3 18h18",
  settings: "M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.6 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.6a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z",
  shield: "M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z",
  users: "M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM23 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75",
  user: "M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2M12 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8z",
  mail: "M4 4h16a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2zM22 6l-10 7L2 6",
  at: "M12 16a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM16 8v5a3 3 0 0 0 6 0v-1a10 10 0 1 0-3.92 7.94",
  globe: "M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20zM2 12h20M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z",
  key: "m21 2-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0 3 3L22 7l-3-3m-3.5 3.5L19 4",
  list: "M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01",
  plus: "M12 5v14M5 12h14",
  refresh: "M23 4v6h-6M1 20v-6h6M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15",
  alert: "M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20zM12 8v4M12 16h.01",
  info: "M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20zM12 16v-4M12 8h.01",
  download: "M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3",
  eye: "M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8zM12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z",
  filter: "M22 3H2l8 9.46V19l4 2v-8.54z",
  tag: "M20.59 13.41l-7.17 7.17a2 2 0 0 1-2.83 0L2 12V2h10l8.59 8.59a2 2 0 0 1 0 2.82zM7 7h.01",
  unread: "M22 12h-6l-2 3h-4l-2-3H2M22 12V6a2 2 0 0 0-2-2H4a2 2 0 0 0-2 2v6M2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6",
  external: "M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6M15 3h6v6M10 14 21 3",
  home: "m3 9 9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2zM9 22V12h6v10",
  bolt: "M13 2 3 14h9l-1 8 10-12h-9l1-8z",
  bold: "M6 4h8a4 4 0 0 1 4 4 4 4 0 0 1-4 4H6zM6 12h9a4 4 0 0 1 4 4 4 4 0 0 1-4 4H6z",
  italic: "M19 4h-9M14 20H5M15 4 9 20",
  underline: "M6 3v7a6 6 0 0 0 6 6 6 6 0 0 0 6-6V3M4 21h16",
  ul: "M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01",
  ol: "M10 6h11M10 12h11M10 18h11M4 6h1v4M4 10h2M6 18H4c0-1 2-2 2-3s-1-1.5-2-1",
  link: "M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71",
  quote: "M3 21c3 0 7-1 7-8V5c0-1.25-.756-2.017-2-2H4c-1.25 0-2 .75-2 1.972V11c0 1.25.75 2 2 2 1 0 1 0 1 1v1c0 1-1 2-2 2s-1 .008-1 1.031V20c0 1 0 1 1 1zM15 21c3 0 7-1 7-8V5c0-1.25-.757-2.017-2-2h-4c-1.25 0-2 .75-2 1.972V11c0 1.25.75 2 2 2h.75c0 2.25.25 4-2.75 4v3c0 1 0 1 1 1z",
  eraser: "m7 21-4.3-4.3c-1-1-1-2.5 0-3.4l9.6-9.6c1-1 2.5-1 3.4 0l5.6 5.6c1 1 1 2.5 0 3.4L13 21M22 21H7M5 11l9 9",
  clock: "M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20zM12 6v6l4 2",
  lock: "M19 11H5a2 2 0 0 0-2 2v7a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7a2 2 0 0 0-2-2zM7 11V7a5 5 0 0 1 10 0v4",
  logout: "M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9",
  layers: "m12 2 10 5-10 5L2 7l10-5zM2 17l10 5 10-5M2 12l10 5 10-5",
  activity: "M22 12h-4l-3 9L9 3l-3 9H2",
};

export function Icon({ name, size = 18, class: cls }: { name: string; size?: number; class?: string }) {
  return (
    <svg class={"icon " + (cls ?? "")} width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d={paths[name] ?? paths.info} />
    </svg>
  );
}

// --- Buttons ---
type BtnProps = JSX.HTMLAttributes<HTMLButtonElement> & { kind?: "primary" | "secondary" | "ghost" | "danger" | "icon"; busy?: boolean; icon?: string; size?: "sm" | "md"; disabled?: boolean; type?: "button" | "submit" };
export function Button({ kind = "secondary", busy, icon, size = "md", children, class: cls, disabled, type = "button", ...rest }: BtnProps) {
  return (
    <button type={type} {...rest} disabled={disabled || busy} class={`btn btn-${kind} btn-${size} ${cls ?? ""}`}>
      {busy ? <Spinner size={14} /> : icon ? <Icon name={icon} size={size === "sm" ? 15 : 17} /> : null}
      {children ? <span>{children}</span> : null}
    </button>
  );
}

export function Spinner({ size = 18 }: { size?: number }) {
  return <span class="spinner" style={{ width: size, height: size }} aria-label={t("Loading…")} />;
}

// Info is an ⓘ affordance: the short label stays visible, the long
// explanation lives in a popover so copy never overflows its container.
//
// The popover is positioned fixed and measured on open, because modals and
// scrollable panes would otherwise clip an absolutely-positioned child.
export function Info({ text }: { text: string }) {
  const [box, setBox] = useState<{ top: number; left: number; below: boolean } | null>(null);
  const [pinned, setPinned] = useState(false);
  const ref = useRef<HTMLSpanElement>(null);

  const place = () => {
    const r = ref.current?.getBoundingClientRect();
    if (!r) return;
    // The popover shrinks to its content, so centre it with translateX(-50%)
    // rather than guessing a width here. Clamp against the widest it can get
    // so it stays on screen near the viewport edges.
    const half = Math.min(260, window.innerWidth - 24) / 2;
    const below = r.top < 130; // not enough room above: flip under the icon
    setBox({
      top: below ? r.bottom + 6 : r.top - 6,
      left: Math.min(Math.max(r.left + r.width / 2, half + 12), window.innerWidth - half - 12),
      below,
    });
  };
  const hide = () => {
    if (!pinned) setBox(null);
  };

  useEffect(() => {
    if (!pinned) return;
    const close = (e: Event) => {
      if (!ref.current?.contains(e.target as Node)) {
        setPinned(false);
        setBox(null);
      }
    };
    document.addEventListener("mousedown", close);
    window.addEventListener("scroll", close, true);
    return () => {
      document.removeEventListener("mousedown", close);
      window.removeEventListener("scroll", close, true);
    };
  }, [pinned]);

  return (
    <span class="info" ref={ref} onMouseEnter={place} onMouseLeave={hide}>
      <button
        type="button"
        class="info-btn"
        aria-label={text}
        onClick={(e) => {
          e.preventDefault();
          e.stopPropagation();
          if (pinned) {
            setPinned(false);
            setBox(null);
          } else {
            place();
            setPinned(true);
          }
        }}
      >
        <Icon name="info" size={14} />
      </button>
      {box ? (
        <span
          class={"info-pop" + (box.below ? " below" : "")}
          role="tooltip"
          style={{ top: box.top, left: box.left, maxWidth: Math.min(260, window.innerWidth - 24) }}
        >
          {text}
        </span>
      ) : null}
    </span>
  );
}

// --- Forms ---
export function Field({ label, hint, info, error, children, inline }: { label?: string; hint?: string; info?: string; error?: string; children: ComponentChildren; inline?: boolean }) {
  return (
    <label class={"field" + (inline ? " field-inline" : "")}>
      {label ? (
        <span class="field-label">
          {label}
          {info ? <Info text={info} /> : null}
        </span>
      ) : null}
      {children}
      {error ? <span class="field-error">{error}</span> : hint ? <span class="field-hint">{hint}</span> : null}
    </label>
  );
}

export function Toggle({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label?: string }) {
  return (
    <label class="toggle">
      <input type="checkbox" checked={checked} onChange={(e) => onChange((e.target as HTMLInputElement).checked)} />
      <span class="toggle-track" />
      {label ? <span>{label}</span> : null}
    </label>
  );
}

// --- Avatar & badges ---
export function Avatar({ name, size = 32 }: { name: string; size?: number }) {
  const h = hueFor(name);
  return (
    <span class="avatar" style={{ width: size, height: size, fontSize: size * 0.38, background: `hsl(${h} 55% 88%)`, color: `hsl(${h} 45% 30%)` }}>
      {initials(name)}
    </span>
  );
}

export function Badge({ children, tone = "neutral" }: { children: ComponentChildren; tone?: "neutral" | "good" | "warn" | "bad" | "accent" }) {
  return <span class={`badge badge-${tone}`}>{children}</span>;
}

export function StatusBadge({ status }: { status: string }) {
  const tone = status === "active" ? "good" : status === "invited" ? "accent" : status === "disabled" || status === "suspended" ? "warn" : status === "departed" || status === "archived" ? "bad" : "neutral";
  const label: Record<string, string> = { active: "Active", invited: "Invited", disabled: "Disabled", departed: "Departed", suspended: "Suspended", archived: "Archived" };
  return <Badge tone={tone}>{t(label[status] ?? status)}</Badge>;
}

// --- Modal ---
export function Modal({ title, children, onClose, footer, wide }: { title: string; children: ComponentChildren; onClose: () => void; footer?: ComponentChildren; wide?: boolean }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    document.body.classList.add("modal-open");
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.classList.remove("modal-open");
    };
  }, [onClose]);
  return (
    <div class="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div class={"modal" + (wide ? " modal-wide" : "")} role="dialog" aria-modal="true" aria-label={title}>
        <header class="modal-head">
          <h2>{title}</h2>
          <button class="btn btn-icon" onClick={onClose} aria-label={t("Close")}>
            <Icon name="x" />
          </button>
        </header>
        <div class="modal-body">{children}</div>
        {footer ? <footer class="modal-foot">{footer}</footer> : null}
      </div>
    </div>
  );
}

export function Confirm({ title, text, confirmLabel, danger, onConfirm, onClose, requireText }: { title: string; text?: ComponentChildren; confirmLabel?: string; danger?: boolean; onConfirm: () => Promise<void> | void; onClose: () => void; requireText?: string }) {
  const [busy, setBusy] = useState(false);
  const [typed, setTyped] = useState("");
  const ok = !requireText || typed.trim().toLowerCase() === requireText.toLowerCase();
  return (
    <Modal
      title={title}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button
            kind={danger ? "danger" : "primary"}
            busy={busy}
            disabled={!ok}
            onClick={async () => {
              setBusy(true);
              try {
                await onConfirm();
                onClose();
              } finally {
                setBusy(false);
              }
            }}
          >
            {confirmLabel ?? t("Confirm")}
          </Button>
        </>
      }
    >
      {text ? <p>{text}</p> : null}
      {requireText ? (
        <Field label={`${t("Confirm")}: ${requireText}`}>
          <input value={typed} onInput={(e) => setTyped((e.target as HTMLInputElement).value)} placeholder={requireText} />
        </Field>
      ) : null}
    </Modal>
  );
}

// --- Toasts ---
export function Toasts() {
  const list = toasts.value;
  if (list.length === 0) return null;
  return (
    <div class="toasts" aria-live="polite">
      {list.map((x) => (
        <div key={x.id} class={`toast toast-${x.kind}`}>
          <span>{x.text}</span>
          {x.action ? (
            <button
              class="toast-action"
              onClick={() => {
                x.action!.run();
                dismissToast(x.id);
              }}
            >
              {x.action.label}
            </button>
          ) : null}
          <button class="toast-close" onClick={() => dismissToast(x.id)} aria-label={t("Close")}>
            <Icon name="x" size={14} />
          </button>
        </div>
      ))}
    </div>
  );
}

// --- Misc ---
export function Empty({ icon = "inbox", title, text, children }: { icon?: string; title: string; text?: string; children?: ComponentChildren }) {
  return (
    <div class="empty">
      <Icon name={icon} size={36} />
      <h3>{title}</h3>
      {text ? <p>{text}</p> : null}
      {children}
    </div>
  );
}

export function Tabs({ tabs, value, onChange }: { tabs: { key: string; label: string }[]; value: string; onChange: (k: string) => void }) {
  return (
    <div class="tabs" role="tablist">
      {tabs.map((x) => (
        <button key={x.key} role="tab" class={"tab" + (x.key === value ? " active" : "")} aria-selected={x.key === value} onClick={() => onChange(x.key)}>
          {x.label}
        </button>
      ))}
    </div>
  );
}

export function Menu({ button, items }: { button: ComponentChildren; items: { label: string; icon?: string; onClick: () => void; danger?: boolean; disabled?: boolean }[] }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, [open]);
  return (
    <div class="menu" ref={ref}>
      <div onClick={() => setOpen(!open)}>{button}</div>
      {open ? (
        <div class="menu-list" role="menu">
          {items.map((it, i) => (
            <button
              key={i}
              role="menuitem"
              class={"menu-item" + (it.danger ? " danger" : "")}
              disabled={it.disabled}
              onClick={() => {
                setOpen(false);
                it.onClick();
              }}
            >
              {it.icon ? <Icon name={it.icon} size={15} /> : null}
              {it.label}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}

export function useAsync<T>(fn: () => Promise<T>, deps: unknown[]): { data: T | null; error: string; loading: boolean; reload: () => void } {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    let alive = true;
    setLoading(true);
    setError("");
    fn().then(
      (d) => {
        if (alive) {
          setData(d);
          setLoading(false);
        }
      },
      (e) => {
        if (alive) {
          setError(e instanceof Error ? e.message : String(e));
          setLoading(false);
        }
      },
    );
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick]);
  return { data, error, loading, reload: () => setTick((x) => x + 1) };
}

export function ErrorBox({ error, onRetry }: { error: string; onRetry?: () => void }) {
  return (
    <div class="errorbox">
      <Icon name="alert" />
      <span>{error}</span>
      {onRetry ? (
        <Button size="sm" onClick={onRetry}>
          {t("Retry")}
        </Button>
      ) : null}
    </div>
  );
}
