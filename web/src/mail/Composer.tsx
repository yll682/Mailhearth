import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import { t, kindLabel } from "@/lib/i18n";
import { get, post, del, type AccessibleMailbox, type Message, type Addr, type Contact, type Upload, type Identity } from "@/lib/api";
import { toast, errorToast, me } from "@/lib/state";
import { escapeHtml, fmtDate, fmtSize, stripSubjectPrefix } from "@/lib/format";
import { Button, Icon, Confirm } from "@/ui";

export interface ComposeInit {
  mailboxId: number;
  mode?: "new" | "reply" | "replyAll" | "forward" | "forwardAttach" | "draft";
  original?: Message;
  folder?: string;
  to?: Addr[];
}

interface Recipient {
  name: string;
  address: string;
}

function quoteHeader(m: Message): string {
  const from = m.from[0] ? (m.from[0].name ? `${m.from[0].name} <${m.from[0].address}>` : m.from[0].address) : "";
  return `${fmtDate(m.date, "full")} ${escapeHtml(from)} ${t("wrote:")}`;
}

function forwardHeader(m: Message): string {
  const f = (a: Addr) => (a.name ? `${a.name} <${a.address}>` : a.address);
  return `<div class="mh-fwd"><b>---------- ${t("Forwarded message")} ----------</b><br>${t("From")}: ${escapeHtml(m.from.map(f).join(", "))}<br>${t("Date")}: ${escapeHtml(fmtDate(m.date, "full"))}<br>${t("Subject")}: ${escapeHtml(m.subject)}<br>${t("To")}: ${escapeHtml(m.to.map(f).join(", "))}${m.cc.length ? `<br>${t("Cc")}: ${escapeHtml(m.cc.map(f).join(", "))}` : ""}</div><br>`;
}

function bodyHtmlOf(m: Message): string {
  return m.html || `<div style="white-space:pre-wrap">${escapeHtml(m.text)}</div>`;
}

export function Composer({ init, mailbox, onClose, onSent }: { init: ComposeInit; mailbox: AccessibleMailbox; onClose: () => void; onSent: () => void }) {
  const identities = mailbox.identities;
  const myAddrs = useMemo(() => new Set(identities.map((i) => i.address.toLowerCase())), [identities]);
  const orig = init.original;
  const mode = init.mode ?? "new";

  const initial = useMemo(() => {
    let to: Recipient[] = init.to ?? [];
    let cc: Recipient[] = [];
    let subject = "";
    let html = "";
    let identity: Identity | undefined = identities.find((i) => i.isDefault) ?? identities[0];
    if (orig) {
      // Prefer the identity the original was addressed to.
      const hit = [...orig.to, ...orig.cc].find((a) => myAddrs.has(a.address.toLowerCase()));
      if (hit) identity = identities.find((i) => i.address.toLowerCase() === hit.address.toLowerCase()) ?? identity;
      if (mode === "reply" || mode === "replyAll") {
        const replyTo = orig.replyTo.length ? orig.replyTo : orig.from;
        to = replyTo.map((a) => ({ name: a.name, address: a.address }));
        if (mode === "replyAll") {
          const others = [...orig.to, ...orig.cc].filter((a) => !myAddrs.has(a.address.toLowerCase()) && !to.some((x) => x.address.toLowerCase() === a.address.toLowerCase()));
          cc = others.map((a) => ({ name: a.name, address: a.address }));
        }
        subject = t("Re: ") + stripSubjectPrefix(orig.subject);
        html = `<br><br><div class="mh-quote-head">${quoteHeader(orig)}</div><blockquote style="margin:0 0 0 .8ex;border-left:2px solid #ccc;padding-left:1ex">${bodyHtmlOf(orig)}</blockquote>`;
      } else if (mode === "forward") {
        subject = t("Fwd: ") + stripSubjectPrefix(orig.subject);
        html = `<br><br>${forwardHeader(orig)}${bodyHtmlOf(orig)}`;
      } else if (mode === "forwardAttach") {
        subject = t("Fwd: ") + stripSubjectPrefix(orig.subject);
      } else if (mode === "draft") {
        to = orig.to.map((a) => ({ name: a.name, address: a.address }));
        cc = orig.cc.map((a) => ({ name: a.name, address: a.address }));
        subject = orig.subject;
        html = bodyHtmlOf(orig);
        const from = orig.from[0];
        if (from) identity = identities.find((i) => i.address.toLowerCase() === from.address.toLowerCase()) ?? identity;
      }
    }
    if (mode !== "draft" && identity?.signatureHtml) html = `<br><br><div class="mh-sig">${identity.signatureHtml}</div>` + html;
    return { to, cc, subject, html, identity };
  }, []);

  const [to, setTo] = useState<Recipient[]>(initial.to);
  const [cc, setCc] = useState<Recipient[]>(initial.cc);
  const [bcc, setBcc] = useState<Recipient[]>([]);
  const [showCc, setShowCc] = useState(initial.cc.length > 0);
  const [showBcc, setShowBcc] = useState(false);
  const [subject, setSubject] = useState(initial.subject);
  const [identityId, setIdentityId] = useState(initial.identity?.id ?? 0);
  const [uploads, setUploads] = useState<Upload[]>([]);
  const [sourceParts, setSourceParts] = useState(mode === "forward" || mode === "draft" ? (orig?.attachments ?? []).map((a) => ({ folder: init.folder!, uid: orig!.uid, path: a.path, filename: a.filename, size: a.size })) : []);
  const [uploading, setUploading] = useState(0);
  const [busy, setBusy] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [priority, setPriority] = useState("");
  const [minimized, setMinimized] = useState(false);
  const [confirmDiscard, setConfirmDiscard] = useState(false);
  const [draftRef, setDraftRef] = useState<{ folder: string; uid: number } | null>(mode === "draft" && orig ? { folder: init.folder!, uid: orig.uid } : null);
  const editor = useRef<HTMLDivElement>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const base = `/api/mail/mailboxes/${mailbox.id}`;

  useEffect(() => {
    if (editor.current) editor.current.innerHTML = initial.html;
    const target = to.length === 0 ? (document.querySelector(".composer .rcpt input") as HTMLInputElement | null) : editor.current;
    setTimeout(() => target?.focus(), 50);
  }, []);

  const payload = () => ({
    identityId,
    to,
    cc,
    bcc,
    subject,
    html: editor.current?.innerHTML ?? "",
    text: editor.current?.innerText ?? "",
    priority,
    attachmentIds: uploads.map((u) => u.id),
    sourceParts: sourceParts.map((s) => ({ folder: s.folder, uid: s.uid, path: s.path })),
    inReplyTo: orig && mode !== "draft" && mode !== "new" ? { folder: init.folder, uid: orig.uid, messageId: orig.messageId, references: orig.references, forward: mode === "forward" || mode === "forwardAttach", asAttachment: mode === "forwardAttach" } : undefined,
    draft: draftRef ?? undefined,
  });

  const saveDraft = async (silent = false) => {
    try {
      const r = await post<{ folder: string; uid: number }>(`${base}/drafts`, payload());
      setDraftRef(r);
      setDirty(false);
      if (!silent) toast(t("Draft saved."), "success");
      onSent();
    } catch (e) {
      if (!silent) errorToast(e);
    }
  };

  // Autosave every 30s while dirty.
  useEffect(() => {
    const id = setInterval(() => {
      if (dirty && !busy) saveDraft(true);
    }, 30000);
    return () => clearInterval(id);
  }, [dirty, busy, to, cc, bcc, subject, uploads, sourceParts, identityId, draftRef]);

  const send = async () => {
    if (to.length + cc.length + bcc.length === 0) {
      toast(t("Add at least one recipient."), "error");
      return;
    }
    setBusy(true);
    try {
      await post(`${base}/send`, payload());
      toast(t("Sent."), "success");
      onSent();
      onClose();
    } catch (e) {
      errorToast(e);
    } finally {
      setBusy(false);
    }
  };

  const upload = async (files: FileList | File[]) => {
    const fd = new FormData();
    for (const f of Array.from(files)) fd.append("file", f, f.name);
    setUploading((n) => n + 1);
    try {
      const res = await post<Upload[]>("/api/mail/uploads", fd);
      setUploads((u) => [...u, ...res]);
      setDirty(true);
    } catch (e) {
      errorToast(e);
    } finally {
      setUploading((n) => n - 1);
    }
  };

  const exec = (cmd: string, value?: string) => {
    editor.current?.focus();
    document.execCommand(cmd, false, value);
    setDirty(true);
  };

  const close = () => {
    if (dirty && !busy) setConfirmDiscard(true);
    else onClose();
  };

  const totalSize = uploads.reduce((n, u) => n + u.size, 0) + sourceParts.reduce((n, s) => n + s.size, 0);

  return (
    <div class={"composer" + (minimized ? " minimized" : "")} onDragOver={(e) => e.preventDefault()} onDrop={(e) => { e.preventDefault(); if (e.dataTransfer?.files.length) upload(e.dataTransfer.files); }}>
      <header class="composer-head" onDblClick={() => setMinimized(!minimized)}>
        <span class="composer-title">{subject || (mode === "new" ? t("New message") : t("Reply"))}</span>
        <div class="spacer" />
        <button class="btn btn-icon" onClick={() => setMinimized(!minimized)} aria-label={minimized ? t("Open") : t("Close")}>
          <Icon name={minimized ? "chevron" : "down"} size={16} class={minimized ? "rot-up" : ""} />
        </button>
        <button class="btn btn-icon" onClick={close} aria-label={t("Close")}>
          <Icon name="x" size={16} />
        </button>
      </header>
      {!minimized ? (
        <>
          <div class="composer-fields">
            <div class="crow">
              <label>{t("From")}</label>
              <select value={identityId} onChange={(e) => { const id = Number((e.target as HTMLSelectElement).value); setIdentityId(id); setDirty(true); swapSignature(editor.current, identities.find((i) => i.id === id)?.signatureHtml ?? ""); }}>
                {identities.map((i) => (
                  <option key={i.id} value={i.id}>
                    {i.displayName ? `${i.displayName} <${i.address}>` : i.address}
                  </option>
                ))}
              </select>
            </div>
            <div class="crow">
              <label>{t("To")}</label>
              <RecipientInput value={to} onChange={(v) => { setTo(v); setDirty(true); }} />
              <div class="cc-toggles">
                {!showCc ? <button class="linklike" onClick={() => setShowCc(true)}>{t("Cc")}</button> : null}
                {!showBcc ? <button class="linklike" onClick={() => setShowBcc(true)}>{t("Bcc")}</button> : null}
              </div>
            </div>
            {showCc ? (
              <div class="crow">
                <label>{t("Cc")}</label>
                <RecipientInput value={cc} onChange={(v) => { setCc(v); setDirty(true); }} />
              </div>
            ) : null}
            {showBcc ? (
              <div class="crow">
                <label>{t("Bcc")}</label>
                <RecipientInput value={bcc} onChange={(v) => { setBcc(v); setDirty(true); }} />
              </div>
            ) : null}
            <div class="crow">
              <label>{t("Subject")}</label>
              <input class="subject" value={subject} onInput={(e) => { setSubject((e.target as HTMLInputElement).value); setDirty(true); }} />
              <select class="priority" value={priority} onChange={(e) => setPriority((e.target as HTMLSelectElement).value)} title={t("Priority")}>
                <option value="">{t("Normal")}</option>
                <option value="high">{t("High")}</option>
                <option value="low">{t("Low")}</option>
              </select>
            </div>
          </div>
          <div class="toolbar">
            <button title={t("Bold")} onClick={() => exec("bold")}><Icon name="bold" size={15} /></button>
            <button title={t("Italic")} onClick={() => exec("italic")}><Icon name="italic" size={15} /></button>
            <button title={t("Underline")} onClick={() => exec("underline")}><Icon name="underline" size={15} /></button>
            <span class="sep" />
            <button title={t("Bulleted list")} onClick={() => exec("insertUnorderedList")}><Icon name="ul" size={15} /></button>
            <button title={t("Numbered list")} onClick={() => exec("insertOrderedList")}><Icon name="ol" size={15} /></button>
            <button title={t("Quote")} onClick={() => exec("formatBlock", "blockquote")}><Icon name="quote" size={15} /></button>
            <button title={t("Link")} onClick={() => { const u = prompt(t("Link URL"), "https://"); if (u) exec("createLink", u); }}><Icon name="link" size={15} /></button>
            <button title={t("Remove formatting")} onClick={() => exec("removeFormat")}><Icon name="eraser" size={15} /></button>
            <span class="sep" />
            <button title={t("Attach files")} onClick={() => fileInput.current?.click()}><Icon name="paperclip" size={15} /></button>
            <input ref={fileInput} type="file" multiple hidden onChange={(e) => { const f = (e.target as HTMLInputElement).files; if (f?.length) upload(f); (e.target as HTMLInputElement).value = ""; }} />
          </div>
          <div ref={editor} class="editor" contentEditable onInput={() => setDirty(true)} data-placeholder={t("Write your message…")} />
          {uploads.length || sourceParts.length || uploading ? (
            <div class="composer-att">
              {sourceParts.map((s) => (
                <span key={s.path} class="chip">
                  <Icon name="paperclip" size={12} /> {s.filename} <span class="muted">{fmtSize(s.size)}</span>
                  <button onClick={() => { setSourceParts(sourceParts.filter((x) => x !== s)); setDirty(true); }} aria-label={t("Remove")}><Icon name="x" size={12} /></button>
                </span>
              ))}
              {uploads.map((u) => (
                <span key={u.id} class="chip">
                  <Icon name="paperclip" size={12} /> {u.filename} <span class="muted">{fmtSize(u.size)}</span>
                  <button onClick={() => { del(`/api/mail/uploads/${u.id}`).catch(() => {}); setUploads(uploads.filter((x) => x.id !== u.id)); }} aria-label={t("Remove")}><Icon name="x" size={12} /></button>
                </span>
              ))}
              {uploading ? <span class="chip muted">{t("Uploading…")}</span> : null}
              {totalSize ? <span class="muted small">{fmtSize(totalSize)}</span> : null}
            </div>
          ) : null}
          <footer class="composer-foot">
            <Button kind="primary" icon="send" busy={busy} onClick={send} disabled={uploading > 0}>
              {busy ? t("Sending…") : t("Send")}
            </Button>
            <Button onClick={() => saveDraft()} disabled={busy}>
              {t("Save draft")}
            </Button>
            <div class="spacer" />
            {draftRef && !dirty ? <span class="muted small">{t("Draft saved")}</span> : null}
            <button class="btn btn-icon" title={t("Discard")} onClick={() => setConfirmDiscard(true)}>
              <Icon name="trash" size={16} />
            </button>
          </footer>
        </>
      ) : null}
      {confirmDiscard ? (
        <Confirm
          title={t("Discard")}
          text={t("Discard this message?")}
          danger
          confirmLabel={t("Discard")}
          onClose={() => setConfirmDiscard(false)}
          onConfirm={async () => {
            for (const u of uploads) del(`/api/mail/uploads/${u.id}`).catch(() => {});
            if (draftRef) post(`${base}/messages/delete`, { folder: draftRef.folder, uids: [draftRef.uid], permanent: true }).then(onSent).catch(() => {});
            onClose();
          }}
        />
      ) : null}
    </div>
  );
}

function swapSignature(editor: HTMLDivElement | null, sig: string) {
  if (!editor) return;
  const old = editor.querySelector(".mh-sig");
  if (old) {
    if (sig) old.innerHTML = sig;
    else old.remove();
  } else if (sig) {
    editor.insertAdjacentHTML("beforeend", `<br><br><div class="mh-sig">${sig}</div>`);
  }
}

function RecipientInput({ value, onChange }: { value: Recipient[]; onChange: (v: Recipient[]) => void }) {
  const [text, setText] = useState("");
  const [sugg, setSugg] = useState<Contact[]>([]);
  const [hi, setHi] = useState(0);
  const timer = useRef<number>(0);
  useEffect(() => {
    clearTimeout(timer.current);
    const q = text.trim();
    if (q.length < 1) {
      setSugg([]);
      return;
    }
    timer.current = window.setTimeout(() => {
      get<Contact[]>(`/api/mail/contacts?q=${encodeURIComponent(q)}`).then((c) => { setSugg(c.filter((x) => !value.some((v) => v.address === x.address))); setHi(0); }, () => {});
    }, 150);
  }, [text]);
  const commit = (r?: Recipient) => {
    const raw = text.trim().replace(/[,;]$/, "");
    if (r) onChange([...value, r]);
    else if (raw) {
      const m = raw.match(/^(.*?)\s*<([^>]+)>$/);
      onChange([...value, m ? { name: m[1].replace(/^"|"$/g, ""), address: m[2] } : { name: "", address: raw }]);
    }
    setText("");
    setSugg([]);
  };
  return (
    <div class="rcpt">
      {value.map((r, i) => (
        <span key={i} class={"chip" + (/@.+\..+/.test(r.address) ? "" : " bad")} title={r.address}>
          {r.name || r.address}
          <button onClick={() => onChange(value.filter((_, j) => j !== i))} aria-label={t("Remove")}><Icon name="x" size={12} /></button>
        </span>
      ))}
      <input
        value={text}
        onInput={(e) => setText((e.target as HTMLInputElement).value)}
        onKeyDown={(e) => {
          if (e.key === "ArrowDown") { setHi((h) => Math.min(h + 1, sugg.length - 1)); e.preventDefault(); }
          else if (e.key === "ArrowUp") { setHi((h) => Math.max(h - 1, 0)); e.preventDefault(); }
          else if (e.key === "Enter" || e.key === "," || e.key === ";" || e.key === "Tab") {
            if (text.trim() || sugg[hi]) { e.preventDefault(); commit(sugg.length && text.trim() ? sugg[hi] : undefined); }
          } else if (e.key === "Backspace" && !text && value.length) onChange(value.slice(0, -1));
          else if (e.key === "Escape") setSugg([]);
        }}
        onBlur={() => setTimeout(() => commit(), 120)}
        placeholder={value.length ? "" : t("Add recipients")}
      />
      {sugg.length ? (
        <ul class="suggest">
          {sugg.map((c, i) => (
            <li key={c.address} class={i === hi ? "hi" : ""} onMouseDown={(e) => { e.preventDefault(); commit({ name: c.name, address: c.address }); }}>
              <b>{c.name}</b> <span class="muted">{c.address}</span> <span class="pill">{kindLabel(c.kind)}</span>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

export { me };
