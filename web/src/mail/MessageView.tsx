import { useEffect, useRef, useState } from "preact/hooks";
import { t } from "@/lib/i18n";
import { get, type AccessibleMailbox, type Folder, type MessageResponse, type Summary, type TeamState } from "@/lib/api";
import { fmtDate, fmtSize, addrName } from "@/lib/format";
import { Button, Icon, Spinner, Avatar, Menu, ErrorBox } from "@/ui";
import { openCompose } from "./MailApp";
import { TeamPanel } from "./TeamPanel";

interface Props {
  mailbox: AccessibleMailbox;
  folder: string;
  uid: number;
  folders: Folder[];
  special: Record<string, string>;
  onFlags?: (uids: number[], add: string[], remove: string[]) => void;
  onMove?: (uids: number[], to: string) => void;
  onDelete?: (uids: number[], permanent?: boolean) => void;
  onArchive?: (uids: number[]) => void;
  onBack: () => void;
  onChanged: (u: Partial<Summary> & { uid: number }) => void;
  onTeam: (key: string, st: TeamState) => void;
}

export function MessageView(p: Props) {
  const [data, setData] = useState<MessageResponse | null>(null);
  const [error, setError] = useState("");
  const [remote, setRemote] = useState(false);
  const [details, setDetails] = useState(false);
  const [frameH, setFrameH] = useState(200);
  const frame = useRef<HTMLIFrameElement>(null);
  const base = `/api/mail/mailboxes/${p.mailbox.id}/messages/${p.uid}`;
  const canWrite = p.mailbox.level !== "read";

  const load = () => {
    setError("");
    setData(null);
    get<MessageResponse>(`${base}?folder=${encodeURIComponent(p.folder)}${remote ? "&remote=1" : ""}${canWrite ? "" : "&markRead=0"}`).then(
      (d) => {
        setData(d);
        if (!d.message.seen && canWrite) p.onChanged({ uid: p.uid, seen: true });
        else if (d.message.seen) p.onChanged({ uid: p.uid, seen: true });
      },
      (e) => setError(e.status === 404 ? t("Message not found. It may have been moved or deleted.") : e.message),
    );
  };
  useEffect(load, [p.uid, p.folder, p.mailbox.id, remote]);

  // Size the sandboxed frame to its content (no scripts inside; same-origin lets
  // us measure). Measure <body>, not <html>: the root element stretches to the
  // frame's current height, so measuring it would never let the frame shrink.
  const fit = () => {
    try {
      const doc = frame.current?.contentDocument;
      const body = doc?.body;
      if (!body) return;
      const h = Math.max(body.scrollHeight, body.getBoundingClientRect().height);
      setFrameH(Math.min(Math.max(Math.ceil(h) + 2, 80), 20000));
    } catch {}
  };
  useEffect(() => {
    // Images and web fonts settle after load, so re-measure a few times early
    // and then keep a slow interval for late-loading remote images.
    const timers = [60, 250, 700, 1500].map((d) => setTimeout(fit, d));
    const id = setInterval(fit, 2000);
    return () => {
      timers.forEach(clearTimeout);
      clearInterval(id);
    };
  }, [p.uid, remote]);

  if (error) {
    return (
      <div class="reader">
        <div class="reader-toolbar">
          <button class="btn btn-icon hide-desktop" onClick={p.onBack}>
            <Icon name="chevron" class="flip" />
          </button>
        </div>
        <ErrorBox error={error} onRetry={load} />
      </div>
    );
  }
  if (!data) {
    return (
      <div class="reader loading">
        <Spinner size={24} />
      </div>
    );
  }
  const m = data.message;
  const viewUrl = data.viewUrl + (remote ? "&remote=1" : "");
  const inTrash = p.folder === p.special.trash;
  const isDraft = p.folder === p.special.drafts || m.draft;

  const reply = (all: boolean) =>
    openCompose({
      mailboxId: p.mailbox.id,
      mode: all ? "replyAll" : "reply",
      original: m,
      folder: p.folder,
    });
  const forward = (asAttachment: boolean) => openCompose({ mailboxId: p.mailbox.id, mode: asAttachment ? "forwardAttach" : "forward", original: m, folder: p.folder });
  const editDraft = () => openCompose({ mailboxId: p.mailbox.id, mode: "draft", original: m, folder: p.folder });

  return (
    <div class="reader">
      <div class="reader-toolbar">
        <button class="btn btn-icon hide-desktop" onClick={p.onBack} aria-label={t("Back")}>
          <Icon name="chevron" class="flip" />
        </button>
        {isDraft && canWrite ? (
          <Button kind="primary" icon="draft" size="sm" onClick={editDraft}>
            {t("Edit")}
          </Button>
        ) : (
          <>
            <Button icon="reply" size="sm" onClick={() => reply(false)} disabled={!canWrite}>
              {t("Reply")}
            </Button>
            <Button icon="replyall" size="sm" onClick={() => reply(true)} disabled={!canWrite} class="hide-narrow">
              {t("Reply all")}
            </Button>
            <Button icon="forward" size="sm" onClick={() => forward(false)} disabled={!canWrite}>
              {t("Forward")}
            </Button>
          </>
        )}
        <div class="spacer" />
        {p.onArchive && !inTrash ? (
          <button class="btn btn-icon" title={t("Archive message")} onClick={() => p.onArchive!([p.uid])}>
            <Icon name="archive" size={17} />
          </button>
        ) : null}
        {p.onDelete ? (
          <button class="btn btn-icon" title={inTrash ? t("Delete permanently") : t("Delete")} onClick={() => p.onDelete!([p.uid], inTrash)}>
            <Icon name="trash" size={17} />
          </button>
        ) : null}
        {p.onFlags ? (
          <button
            class={"btn btn-icon" + (m.flagged ? " flag" : "")}
            title={m.flagged ? t("Unflag") : t("Flag")}
            onClick={() => {
              p.onFlags!([p.uid], m.flagged ? [] : ["\\Flagged"], m.flagged ? ["\\Flagged"] : []);
              setData({ ...data, message: { ...m, flagged: !m.flagged } });
              p.onChanged({ uid: p.uid, flagged: !m.flagged });
            }}
          >
            <Icon name="star" size={17} />
          </button>
        ) : null}
        <Menu
          button={
            <button class="btn btn-icon" aria-label={t("Actions")}>
              <Icon name="more" size={17} />
            </button>
          }
          items={[
            ...(p.onFlags ? [{ label: t("Mark as unread"), icon: "unread", onClick: () => { p.onFlags!([p.uid], [], ["\\Seen"]); p.onChanged({ uid: p.uid, seen: false }); p.onBack(); } }] : []),
            ...(p.onMove ? p.folders.filter((f) => !f.noSelect && f.name !== p.folder).slice(0, 12).map((f) => ({ label: `${t("Move to")} ${f.name}`, icon: "folder", onClick: () => p.onMove!([p.uid], f.name) })) : []),
            { label: t("Forward as attachment"), icon: "paperclip", onClick: () => forward(true), disabled: !canWrite },
            { label: t("Download original (.eml)"), icon: "download", onClick: () => window.open(`${base}/raw?folder=${encodeURIComponent(p.folder)}&t=${data.viewToken}`, "_blank") },
            { label: t("Print"), icon: "eye", onClick: () => frame.current?.contentWindow?.print() },
          ]}
        />
      </div>

      <div class="reader-scroll">
        <h1 class="reader-subject">{m.subject || t("(no subject)")}</h1>
        <div class="reader-head">
          <Avatar name={addrName(m.from[0]) || "?"} size={40} />
          <div class="reader-from">
            <div class="from-line">
              <b>{m.from[0]?.name || m.from[0]?.address}</b>
              {m.from[0]?.name ? <span class="muted raw-addr"> &lt;{m.from[0].address}&gt;</span> : null}
            </div>
            <div class="to-line muted">
              {t("To")}: {m.to.map(addrName).join(", ") || "—"}
              {m.cc.length ? ` · ${t("Cc")}: ${m.cc.map(addrName).join(", ")}` : ""}
              <button class="linklike" onClick={() => setDetails(!details)}>
                {details ? t("Hide details") : t("Show details")}
              </button>
            </div>
            {details ? (
              <dl class="details">
                <dt>{t("From")}</dt>
                <dd>{m.from.map((a) => `${a.name} <${a.address}>`).join(", ")}</dd>
                <dt>{t("To")}</dt>
                <dd>{m.to.map((a) => (a.name ? `${a.name} <${a.address}>` : a.address)).join(", ")}</dd>
                {m.cc.length ? (
                  <>
                    <dt>{t("Cc")}</dt>
                    <dd>{m.cc.map((a) => (a.name ? `${a.name} <${a.address}>` : a.address)).join(", ")}</dd>
                  </>
                ) : null}
                {m.replyTo.length ? (
                  <>
                    <dt>{t("Reply-To")}</dt>
                    <dd>{m.replyTo.map((a) => a.address).join(", ")}</dd>
                  </>
                ) : null}
                <dt>{t("Date")}</dt>
                <dd>{fmtDate(m.date, "full")}</dd>
                <dt>Message-ID</dt>
                <dd class="mono">{m.messageId}</dd>
                {m.headers["List-Unsubscribe"] ? (
                  <>
                    <dt>List-Unsubscribe</dt>
                    <dd class="mono">{m.headers["List-Unsubscribe"]}</dd>
                  </>
                ) : null}
              </dl>
            ) : null}
          </div>
          <div class="reader-date muted">{fmtDate(m.date, "full")}</div>
        </div>

        {m.hasRemote && !remote ? (
          <div class="notice inline">
            <Icon name="eye" size={15} /> {t("This message has remote images.")}
            <button class="linklike" onClick={() => setRemote(true)}>
              {t("Show images")}
            </button>
          </div>
        ) : null}
        {m.truncated ? (
          <div class="notice inline warn">
            <Icon name="alert" size={15} /> {t("Message is too large to show completely.")}
          </div>
        ) : null}

        <iframe
          ref={frame}
          class="msg-frame"
          title={m.subject}
          src={viewUrl}
          sandbox="allow-same-origin allow-popups allow-popups-to-escape-sandbox"
          referrerpolicy="no-referrer"
          style={{ height: frameH }}
          onLoad={fit}
        />

        {m.attachments.length ? (
          <div class="attachments">
            <div class="section-label">
              {t("Attachments")} · {m.attachments.length}
            </div>
            <div class="att-grid">
              {m.attachments.map((a) => (
                <a key={a.path} class="att" href={`${base}/parts/${a.path}?folder=${encodeURIComponent(p.folder)}&t=${data.viewToken}`} download={a.filename}>
                  <span class="att-icon">
                    <Icon name={a.mime.startsWith("image/") ? "eye" : "paperclip"} size={16} />
                  </span>
                  <span class="att-name">{a.filename || a.path}</span>
                  <span class="att-size muted">{fmtSize(a.size)}</span>
                </a>
              ))}
            </div>
          </div>
        ) : null}

        {p.mailbox.kind === "shared" ? (
          <TeamPanel mailbox={p.mailbox} msgKey={data.key} state={data.team} onChange={(st) => { setData({ ...data, team: st }); p.onTeam(data.key, st); }} />
        ) : null}
      </div>
    </div>
  );
}
