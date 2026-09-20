import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import { signal } from "@preact/signals";
import { route, go, mailPath } from "@/lib/router";
import { me, mailboxes, toast, errorToast, logout, isAdmin, refreshMailboxes } from "@/lib/state";
import { t, folderLabel } from "@/lib/i18n";
import { get, post, del, patch, type Folder, type Page, type Summary, type AccessibleMailbox, type TeamState } from "@/lib/api";
import { Button, Icon, Avatar, Empty, Spinner, Modal, Field, Confirm, Menu, ErrorBox } from "@/ui";
import { MessageList } from "./MessageList";
import { MessageView } from "./MessageView";
import { Composer, type ComposeInit } from "./Composer";

export const composeState = signal<ComposeInit | null>(null);

export function openCompose(init: ComposeInit) {
  composeState.value = init;
}

export function MailApp() {
  const segs = route.value.segments; // mail / mailboxId / folder / uid
  const boxes = mailboxes.value;
  const mailboxId = Number(segs[1]) || boxes[0]?.id || 0;
  const mailbox = boxes.find((b) => b.id === mailboxId) ?? boxes[0];
  const folder = segs[2] ?? "INBOX";
  const uid = Number(segs[3]) || 0;
  const [folders, setFolders] = useState<Folder[]>([]);
  const [foldersErr, setFoldersErr] = useState("");
  const [query, setQuery] = useState(route.value.query.get("q") ?? "");
  const [page, setPage] = useState<Page | null>(null);
  const [pages, setPages] = useState<Summary[]>([]);
  const [team, setTeam] = useState<Record<string, TeamState>>({});
  const [loading, setLoading] = useState(false);
  const [listErr, setListErr] = useState("");
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [drawer, setDrawer] = useState(false);
  const [folderModal, setFolderModal] = useState<null | { mode: "new" | "rename"; name: string; parent?: string }>(null);
  const [deleteFolder, setDeleteFolder] = useState<string | null>(null);
  const listAbort = useRef<AbortController | null>(null);
  const listVersion = useRef(0);

  useEffect(() => {
    if (!segs[1] && mailbox) go(mailPath(mailbox.id, "INBOX"), true);
  }, [segs[1], mailbox?.id]);

  const base = mailbox ? `/api/mail/mailboxes/${mailbox.id}` : "";

  const loadFolders = async () => {
    if (!mailbox) return;
    try {
      setFolders(await get<Folder[]>(`${base}/folders`));
      setFoldersErr("");
    } catch (e) {
      setFoldersErr((e as Error).message);
    }
  };

  const loadList = async (pageNo: number, append: boolean) => {
    if (!mailbox) return;
    listAbort.current?.abort();
    const ac = new AbortController();
    listAbort.current = ac;
    const v = ++listVersion.current;
    setLoading(true);
    setListErr("");
    try {
      const q = query.trim();
      const p = await get<Page>(`${base}/messages?folder=${encodeURIComponent(folder)}&page=${pageNo}&size=50${q ? "&q=" + encodeURIComponent(q) : ""}`, ac.signal);
      if (v !== listVersion.current) return;
      setPage(p);
      setPages(append ? [...pages, ...p.messages] : p.messages);
      setTeam(append ? { ...team, ...p.team } : p.team);
      if (!append) setSelected(new Set());
    } catch (e) {
      if ((e as Error).name === "AbortError") return;
      setListErr((e as Error).message);
    } finally {
      if (v === listVersion.current) setLoading(false);
    }
  };

  useEffect(() => {
    loadFolders();
  }, [mailbox?.id]);
  useEffect(() => {
    setPages([]);
    setPage(null);
    loadList(0, false);
  }, [mailbox?.id, folder, query]);

  // Live updates for the current folder.
  useEffect(() => {
    if (!mailbox) return;
    let es: EventSource | null = null;
    let retry = 2000;
    let stopped = false;
    let lastCount = -1;
    const connect = () => {
      if (stopped) return;
      es = new EventSource(`${base}/events?folder=${encodeURIComponent(folder)}`);
      es.addEventListener("change", (ev) => {
        try {
          const data = JSON.parse((ev as MessageEvent).data) as { numMessages: number; expunged: boolean; err?: string };
          if (data.err === "auth") return;
          if (!data.expunged && lastCount >= 0 && data.numMessages > lastCount && folder === "INBOX") notifyNewMail(mailbox, data.numMessages - lastCount);
          if (data.numMessages) lastCount = data.numMessages;
          loadFolders();
          if (!query) loadList(0, false);
        } catch {}
      });
      es.addEventListener("ready", () => {
        retry = 2000;
      });
      es.addEventListener("reconnect", () => {
        es?.close();
        setTimeout(connect, 500);
      });
      es.onerror = () => {
        es?.close();
        setTimeout(connect, retry);
        retry = Math.min(retry * 2, 60000);
      };
    };
    connect();
    return () => {
      stopped = true;
      es?.close();
    };
  }, [mailbox?.id, folder]);

  // Document title with unread count.
  useEffect(() => {
    const inbox = folders.find((f) => f.role === "inbox");
    document.title = (inbox && inbox.unseen ? `(${inbox.unseen}) ` : "") + (mailbox ? mailbox.displayName || mailbox.address : "Mailhearth") + " · Mailhearth";
  }, [folders, mailbox?.id]);

  // Keyboard shortcuts.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement)?.tagName;
      if (tag === "INPUT" || tag === "TEXTAREA" || (e.target as HTMLElement)?.isContentEditable || composeState.value) return;
      if (e.key === "c") {
        e.preventDefault();
        openCompose({ mailboxId: mailbox!.id });
      } else if (e.key === "/") {
        e.preventDefault();
        (document.querySelector(".search input") as HTMLInputElement | null)?.focus();
      } else if (e.key === "j" || e.key === "k") {
        const i = pages.findIndex((m) => m.uid === uid);
        const next = pages[e.key === "j" ? i + 1 : i - 1];
        if (next) go(mailPath(mailbox!.id, folder, next.uid));
      } else if (e.key === "Escape" && uid) {
        go(mailPath(mailbox!.id, folder));
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [pages, uid, folder, mailbox?.id]);

  if (!mailbox) {
    return (
      <div class="app-shell">
        <Empty icon="mail" title={t("No mailboxes")} text={isAdmin.value ? undefined : t("Nothing here yet.")}>
          {isAdmin.value ? (
            <Button kind="primary" onClick={() => go("/admin")}>
              {t("Open the admin console")}
            </Button>
          ) : null}
        </Empty>
      </div>
    );
  }

  const special = Object.fromEntries(folders.filter((f) => f.role).map((f) => [f.role, f.name]));
  const canWrite = mailbox.level !== "read";
  const canDelete = mailbox.level === "full";

  const act = async (fn: () => Promise<void>, refresh = true) => {
    try {
      await fn();
      if (refresh) {
        await loadList(0, false);
        loadFolders();
      }
    } catch (e) {
      errorToast(e);
    }
  };

  const onFlags = (uids: number[], add: string[], remove: string[]) =>
    act(async () => {
      await post(`${base}/messages/flags`, { folder, uids, add, remove });
      setPages((ps) => ps.map((m) => (uids.includes(m.uid) ? applyFlags(m, add, remove) : m)));
    }, false).then(loadFolders);
  const onMove = (uids: number[], to: string) =>
    act(async () => {
      await post(`${base}/messages/move`, { folder, uids, to });
      toast(t("Moved to {f}", { f: to }), "success");
      if (uids.includes(uid)) go(mailPath(mailbox.id, folder), true);
    });
  const onDelete = (uids: number[], permanent = false) =>
    act(async () => {
      await post(`${base}/messages/delete`, { folder, uids, permanent: permanent || folder === special.trash });
      toast(t("Deleted"), "success");
      if (uids.includes(uid)) {
        const i = pages.findIndex((m) => m.uid === uid);
        const next = pages[i + 1] ?? pages[i - 1];
        go(next ? mailPath(mailbox.id, folder, next.uid) : mailPath(mailbox.id, folder), true);
      }
    });
  const onArchive = (uids: number[]) => onMove(uids, special.archive ?? "Archive");

  const folderTree = useMemo(() => folders.filter((f) => !f.noSelect || f.hasChildren), [folders]);

  return (
    <div class={"app-shell mail-shell" + (uid ? " has-uid" : "") + (drawer ? " drawer-open" : "")}>
      <aside class="sidebar">
        <div class="sidebar-top">
          <button class="btn btn-icon hide-desktop" onClick={() => setDrawer(false)} aria-label={t("Close")}>
            <Icon name="x" />
          </button>
          <Button kind="primary" icon="compose" class="compose-btn" onClick={() => openCompose({ mailboxId: mailbox.id })} disabled={!canWrite}>
            {t("Compose")}
          </Button>
        </div>
        <MailboxPicker boxes={boxes} current={mailbox} />
        <nav class="folders">
          {foldersErr ? <ErrorBox error={foldersErr} onRetry={loadFolders} /> : null}
          {folderTree.map((f) => (
            <a
              key={f.name}
              href={mailPath(mailbox.id, f.name)}
              class={"folder" + (f.name === folder ? " active" : "") + (f.noSelect ? " noselect" : "")}
              style={{ paddingLeft: 12 + f.depth * 14 }}
              onClick={() => setDrawer(false)}
            >
              <Icon name={folderIcon(f.role)} size={16} />
              <span class="folder-name">{folderLabel(f.display, f.role)}</span>
              {f.unseen > 0 ? <span class="count">{f.unseen}</span> : f.role === "drafts" && f.total > 0 ? <span class="count muted">{f.total}</span> : null}
            </a>
          ))}
          {canDelete ? (
            <button class="folder add" onClick={() => setFolderModal({ mode: "new", name: "" })}>
              <Icon name="plus" size={16} />
              <span>{t("New folder")}</span>
            </button>
          ) : null}
        </nav>
        <div class="sidebar-bottom">
          <Menu
            button={
              <button class="user-btn">
                <Avatar name={me.value!.member.displayName} size={28} />
                <span class="user-name">{me.value!.member.displayName}</span>
                <Icon name="down" size={14} />
              </button>
            }
            items={[
              { label: t("Settings"), icon: "settings", onClick: () => go("/settings") },
              ...(isAdmin.value ? [{ label: t("Admin"), icon: "shield", onClick: () => go("/admin") }] : []),
              { label: t("Sign out"), icon: "logout", onClick: logout },
            ]}
          />
        </div>
      </aside>
      <div class="drawer-backdrop" onClick={() => setDrawer(false)} />

      <section class="list-pane">
        <header class="pane-head">
          <button class="btn btn-icon hide-desktop" onClick={() => setDrawer(true)} aria-label={t("Folders")}>
            <Icon name="menu" />
          </button>
          <div class="search">
            <Icon name="search" size={16} />
            <input
              placeholder={t("Search mail… try from:, subject:, is:unread, has:attachment")}
              value={query}
              onInput={(e) => {
                const v = (e.target as HTMLInputElement).value;
                setQuery(v);
              }}
              onKeyDown={(e) => {
                if (e.key === "Escape") setQuery("");
              }}
            />
            {query ? (
              <button class="btn btn-icon" onClick={() => setQuery("")} aria-label={t("Clear")}>
                <Icon name="x" size={14} />
              </button>
            ) : null}
          </div>
          <button class="btn btn-icon" onClick={() => loadList(0, false)} aria-label={t("Refresh")}>
            <Icon name="refresh" size={16} />
          </button>
          {canDelete && folder !== "INBOX" && !special || (canDelete && folder !== "INBOX" && !Object.values(special).includes(folder)) ? (
            <Menu
              button={
                <button class="btn btn-icon" aria-label={t("Actions")}>
                  <Icon name="more" size={16} />
                </button>
              }
              items={[
                { label: t("Rename folder"), icon: "draft", onClick: () => setFolderModal({ mode: "rename", name: folder }) },
                { label: t("Delete folder"), icon: "trash", danger: true, onClick: () => setDeleteFolder(folder) },
              ]}
            />
          ) : null}
        </header>
        <MessageList
          mailbox={mailbox}
          folder={folder}
          messages={pages}
          team={team}
          total={page?.total ?? 0}
          loading={loading}
          error={listErr}
          query={query}
          activeUid={uid}
          selected={selected}
          onSelect={setSelected}
          onOpen={(m) => go(mailPath(mailbox.id, folder, m.uid))}
          onLoadMore={() => page && loadList(page.page + 1, true)}
          onFlags={canWrite ? onFlags : undefined}
          onMove={canDelete ? onMove : undefined}
          onDelete={canDelete ? onDelete : undefined}
          onArchive={canDelete && folder !== special.archive ? onArchive : undefined}
          folders={folderTree}
          special={special}
          onRetry={() => loadList(0, false)}
        />
      </section>

      <section class="reader-pane">
        {uid ? (
          <MessageView
            key={`${mailbox.id}/${folder}/${uid}`}
            mailbox={mailbox}
            folder={folder}
            uid={uid}
            folders={folderTree}
            special={special}
            onFlags={canWrite ? onFlags : undefined}
            onMove={canDelete ? onMove : undefined}
            onDelete={canDelete ? onDelete : undefined}
            onArchive={canDelete && folder !== special.archive ? onArchive : undefined}
            onBack={() => go(mailPath(mailbox.id, folder))}
            onChanged={(u) => setPages((ps) => ps.map((m) => (m.uid === u.uid ? { ...m, ...u } : m)))}
            onTeam={(key, st) => setTeam((tm) => ({ ...tm, [key]: st }))}
          />
        ) : (
          <div class="reader-empty">
            <Empty icon="mail" title={t("Select a message to read")} />
          </div>
        )}
      </section>

      {composeState.value ? (
        <Composer
          init={composeState.value}
          mailbox={boxes.find((b) => b.id === composeState.value!.mailboxId) ?? mailbox}
          onClose={() => (composeState.value = null)}
          onSent={() => {
            loadFolders();
            if (folder === special.sent || folder === special.drafts) loadList(0, false);
          }}
        />
      ) : null}

      {folderModal ? (
        <FolderModal
          mode={folderModal.mode}
          initial={folderModal.mode === "rename" ? folder : ""}
          folders={folderTree}
          onClose={() => setFolderModal(null)}
          onSave={async (name, parent) => {
            if (folderModal.mode === "new") await post(`${base}/folders`, { name, parent });
            else {
              const r = await patch<{ name: string }>(`${base}/folders`, { name: folder, newName: name });
              go(mailPath(mailbox.id, r.name), true);
            }
            await loadFolders();
          }}
        />
      ) : null}
      {deleteFolder ? (
        <Confirm
          title={t("Delete folder")}
          text={t("Delete folder “{name}” and all its messages?", { name: deleteFolder })}
          danger
          confirmLabel={t("Delete")}
          onClose={() => setDeleteFolder(null)}
          onConfirm={async () => {
            await del(`${base}/folders?name=${encodeURIComponent(deleteFolder)}`);
            go(mailPath(mailbox.id, "INBOX"), true);
            await loadFolders();
          }}
        />
      ) : null}
    </div>
  );
}

function applyFlags(m: Summary, add: string[], remove: string[]): Summary {
  const flags = new Set(m.flags);
  add.forEach((f) => flags.add(f));
  remove.forEach((f) => flags.delete(f));
  return { ...m, flags: [...flags], seen: flags.has("\\Seen"), flagged: flags.has("\\Flagged"), answered: flags.has("\\Answered"), forwarded: flags.has("$Forwarded") };
}

function folderIcon(role: string): string {
  return ({ inbox: "inbox", sent: "send", drafts: "draft", trash: "trash", junk: "junk", archive: "archive" } as Record<string, string>)[role] ?? "folder";
}

function MailboxPicker({ boxes, current }: { boxes: AccessibleMailbox[]; current: AccessibleMailbox }) {
  const personal = boxes.filter((b) => b.kind === "personal");
  const shared = boxes.filter((b) => b.kind === "shared");
  if (boxes.length === 1) {
    return (
      <div class="mailbox-current single">
        <Avatar name={current.displayName || current.address} size={26} />
        <div>
          <div class="mb-name">{current.displayName || current.address}</div>
          <div class="mb-addr">{current.address}</div>
        </div>
      </div>
    );
  }
  return (
    <div class="mailbox-list">
      {personal.length ? <div class="section-label">{t("Mailboxes")}</div> : null}
      {personal.map((b) => (
        <MailboxLink key={b.id} b={b} active={b.id === current.id} />
      ))}
      {shared.length ? <div class="section-label">{t("Shared mailboxes")}</div> : null}
      {shared.map((b) => (
        <MailboxLink key={b.id} b={b} active={b.id === current.id} />
      ))}
    </div>
  );
}

function MailboxLink({ b, active }: { b: AccessibleMailbox; active: boolean }) {
  return (
    <a href={mailPath(b.id, "INBOX")} class={"mailbox-link" + (active ? " active" : "")} title={b.address}>
      <Avatar name={b.displayName || b.address} size={22} />
      <span class="mb-name">{b.displayName || b.address}</span>
      {b.kind === "shared" ? <Icon name="users" size={13} class="muted" /> : null}
    </a>
  );
}

function FolderModal({ mode, initial, folders, onClose, onSave }: { mode: "new" | "rename"; initial: string; folders: Folder[]; onClose: () => void; onSave: (name: string, parent: string) => Promise<void> }) {
  const [name, setName] = useState(mode === "rename" ? initial.split("/").pop() ?? initial : "");
  const [parent, setParent] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  return (
    <Modal
      title={mode === "new" ? t("New folder") : t("Rename folder")}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button
            kind="primary"
            busy={busy}
            onClick={async () => {
              setBusy(true);
              setErr("");
              try {
                await onSave(mode === "rename" && initial.includes("/") ? initial.slice(0, initial.lastIndexOf("/") + 1) + name.trim() : name.trim(), parent);
                onClose();
              } catch (e) {
                setErr((e as Error).message);
              } finally {
                setBusy(false);
              }
            }}
          >
            {t("Save")}
          </Button>
        </>
      }
    >
      <Field label={t("Folder name")} error={err}>
        <input value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} autoFocus />
      </Field>
      {mode === "new" ? (
        <Field label={t("Folders")}>
          <select value={parent} onChange={(e) => setParent((e.target as HTMLSelectElement).value)}>
            <option value="">—</option>
            {folders
              .filter((f) => !f.noSelect && f.role !== "inbox")
              .map((f) => (
                <option key={f.name} value={f.name}>
                  {f.name}
                </option>
              ))}
          </select>
        </Field>
      ) : null}
    </Modal>
  );
}

function notifyNewMail(mailbox: AccessibleMailbox, n: number) {
  if (localStorage.getItem("mh.notify") !== "1" || typeof Notification === "undefined" || Notification.permission !== "granted" || document.hasFocus()) return;
  try {
    const note = new Notification(mailbox.displayName || mailbox.address, { body: t("{n} new messages", { n }), tag: "mh-" + mailbox.id });
    note.onclick = () => {
      window.focus();
      note.close();
    };
  } catch {}
}

export function refreshAfterAdminChange() {
  refreshMailboxes().catch(() => {});
}

export { Spinner };
