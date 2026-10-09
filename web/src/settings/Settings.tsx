import { useEffect, useState } from "preact/hooks";
import { route, go } from "@/lib/router";
import { me, mailboxes, toast, errorToast, refreshMailboxes, logout, isAdmin } from "@/lib/state";
import { t, lang, setLang, LANGS } from "@/lib/i18n";
import { get, post, put, patch, waitOperation, ApiError, type Operation, type Capability, type AccessibleMailbox, type Identity, type SieveRule, type Vacation, type Folder, type FolderMappingView } from "@/lib/api";
import { Button, Field, Icon, Tabs, Toggle, Avatar, Spinner, Modal, Info } from "@/ui";
import { SubmissionsTab } from "./SubmissionsTab";
import { useOperationRequests } from "@/lib/useOperationRequests";

export function SettingsPage() {
  const tab = route.value.segments[1] ?? "account";
  const tabs = [
    { key: "account", label: t("Account") },
    { key: "identities", label: t("Identities & signatures") },
    { key: "rules", label: t("Mail rules") },
    { key: "folders", label: t("Special folders") },
    { key: "submissions", label: t("Sending requests") },
  ];
  return (
    <div class="page settings-page">
      <header class="page-head">
        <a href="/mail" class="btn btn-ghost btn-sm">
          <Icon name="chevron" size={16} class="flip" /> {t("Mail")}
        </a>
        <h1>{t("Settings")}</h1>
        <div class="spacer" />
        {isAdmin.value ? (
          <a href="/admin" class="btn btn-secondary btn-sm">
            <Icon name="shield" size={15} /> {t("Admin")}
          </a>
        ) : null}
      </header>
      <Tabs tabs={tabs} value={tab} onChange={(k) => go("/settings/" + k)} />
      <div class="page-body narrow">
        {tab === "account" ? <AccountTab /> : tab === "identities" ? <IdentitiesTab /> : tab === "folders" ? <FoldersTab /> : tab === "submissions" ? <SubmissionsTab /> : <RulesTab />}
      </div>
    </div>
  );
}

function FoldersTab() {
  const boxes = mailboxes.value.filter((box) => box.level === "full");
  const [id, setId] = useState(boxes[0]?.id ?? 0);
  const [view, setView] = useState<FolderMappingView | null>(null);
  const [folders, setFolders] = useState<Folder[]>([]);
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState(false);
  const [reload, setReload] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setView(null); setFolders([]); setFailed(false);
    if (!id) return;
    setLoading(true);
    Promise.all([get<FolderMappingView>(`/api/mail/mailboxes/${id}/folder-mapping`, controller.signal), get<Folder[]>(`/api/mail/mailboxes/${id}/folders`, controller.signal)])
      .then(([mapping, list]) => { if (!controller.signal.aborted) { setView(mapping); setFolders(list.filter((folder) => !folder.noSelect)); } })
      .catch((error) => { if (!controller.signal.aborted) { setFailed(true); errorToast(error); } })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [id, reload]);
  const save = async () => {
    if (!view) return;
    setBusy(true);
    try {
      const saved = await put<FolderMappingView>(`/api/mail/mailboxes/${id}/folder-mapping`, { expectedRevision: view.revision, mapping: view.mapping, sentCopyMode: view.sentCopyMode });
      setView(saved); await refreshMailboxes(); toast(t("Folder mapping saved."), "success");
    } catch (error) { errorToast(error); } finally { setBusy(false); }
  };
  if (!boxes.length) return <p class="muted">{t("No mailboxes.")}</p>;
  const labels = { sent: "Sent", drafts: "Drafts", trash: "Trash", junk: "Junk", archive: "Archive" };
  return <div class="stack">
    <Field label={t("Mailbox")}><select value={id} disabled={busy} onChange={(event) => setId(Number(event.currentTarget.value))}>{boxes.map((box) => <option key={box.id} value={box.id}>{box.connectionLabel} · {box.address}</option>)}</select></Field>
    <p class="muted">{t("Choose existing folders. Automatic selection requires a unique match.")}</p>
    {loading ? <Spinner /> : null}
    {failed ? <Button onClick={() => setReload((value) => value + 1)}>{t("Retry")}</Button> : null}
    {view ? <section class="card"><form onSubmit={(event) => { event.preventDefault(); void save(); }}>
      {(Object.keys(labels) as (keyof typeof labels)[]).map((role) => <Field key={role} label={t(labels[role])}>
        <select value={view.mapping[role] ?? ""} disabled={busy} onChange={(event) => setView({ ...view, mapping: { ...view.mapping, [role]: event.currentTarget.value || null } })}>
          <option value="">{t("Automatic selection")}</option>
          {view.mapping[role] && !folders.some((folder) => folder.name === view.mapping[role]) ? <option value={view.mapping[role]!} disabled>{view.mapping[role]} · {t("Folder unavailable")}</option> : null}
          {folders.map((folder) => <option key={folder.name} value={folder.name}>{folder.name}</option>)}
        </select>
      </Field>)}
      <Field label={t("Sent copy handling")}><select disabled={busy} value={view.sentCopyMode} onChange={(event) => setView({ ...view, sentCopyMode: event.currentTarget.value as "append" | "server" })}>
        <option value="append">{t("Mailhearth saves the Sent copy")}</option><option value="server">{t("Mail server saves the Sent copy")}</option>
      </select></Field>
      <Button type="submit" kind="primary" busy={busy}>{t("Save")}</Button>
    </form></section> : null}
  </div>;
}

function AccountTab() {
  const m = me.value!.member;
  const [cur, setCur] = useState("");
  const [nw, setNw] = useState("");
  const [busy, setBusy] = useState(false);
  const [notify, setNotify] = useState(localStorage.getItem("mh.notify") === "1");
  return (
    <div class="stack">
      <section class="card">
        <div class="row gap">
          <Avatar name={m.displayName} size={48} />
          <div>
            <div class="h3">{m.displayName}</div>
            <div class="muted">
              {m.loginEmail} · {m.roleName}
              {m.title ? ` · ${m.title}` : ""}
            </div>
          </div>
        </div>
      </section>
      <section class="card">
        <h3>{t("Language")}</h3>
        <div class="row gap wrap">
          {LANGS.map((l) => (
            <Button key={l.id} kind={lang.value === l.id ? "primary" : "secondary"} size="sm" onClick={() => setLang(l.id)}>
              {l.label}
            </Button>
          ))}
        </div>
      </section>
      <section class="card">
        <h3>{t("Notifications")}</h3>
        <Toggle
          checked={notify}
          label={t("Enable desktop notifications")}
          onChange={async (v) => {
            if (v && typeof Notification !== "undefined") {
              const perm = await Notification.requestPermission();
              if (perm !== "granted") {
                toast(t("Notifications are blocked in your browser."), "error");
                return;
              }
            }
            setNotify(v);
            localStorage.setItem("mh.notify", v ? "1" : "0");
          }}
        />
      </section>
      <section class="card">
        <h3>{t("Change password")}</h3>
        <form
          onSubmit={async (e) => {
            e.preventDefault();
            setBusy(true);
            try {
              await post("/api/auth/password", { current: cur, new: nw });
              toast(t("Password changed."), "success");
              setCur("");
              setNw("");
            } catch (err) {
              errorToast(err);
            } finally {
              setBusy(false);
            }
          }}
        >
          <Field label={t("Current password")}>
            <input type="password" autocomplete="current-password" required value={cur} onInput={(e) => setCur((e.target as HTMLInputElement).value)} />
          </Field>
          <Field label={t("New password")} hint={t("At least 10 characters.")}>
            <input type="password" autocomplete="new-password" required minLength={10} value={nw} onInput={(e) => setNw((e.target as HTMLInputElement).value)} />
          </Field>
          <Button kind="primary" busy={busy} type="submit">
            {t("Change password")}
          </Button>
        </form>
      </section>
      <section class="card">
        <h3>{t("Keyboard shortcuts")}</h3>
        <dl class="kbd-list">
          <dt><kbd>c</kbd></dt><dd>{t("Compose")}</dd>
          <dt><kbd>/</kbd></dt><dd>{t("Search")}</dd>
          <dt><kbd>j</kbd> / <kbd>k</kbd></dt><dd>{t("Next")} / {t("Back")}</dd>
          <dt><kbd>Esc</kbd></dt><dd>{t("Close")}</dd>
        </dl>
      </section>
      <Button kind="ghost" icon="logout" onClick={logout}>
        {t("Sign out")}
      </Button>
    </div>
  );
}

function MailboxSelect({ value, onChange }: { value: number; onChange: (id: number) => void }) {
  const boxes = mailboxes.value.filter((b) => b.level === "full");
  if (boxes.length <= 1) return null;
  return (
    <Field label={t("Mailbox")} inline>
      <select value={value} onChange={(e) => onChange(Number((e.target as HTMLSelectElement).value))}>
        {boxes.map((b) => (
          <option key={b.id} value={b.id}>
            {b.displayName || b.address} ({b.address})
          </option>
        ))}
      </select>
    </Field>
  );
}

function IdentitiesTab() {
  const boxes = mailboxes.value.filter((b) => b.level === "full");
  const [mbId, setMbId] = useState(boxes[0]?.id ?? 0);
  const mb = boxes.find((b) => b.id === mbId);
  const [editing, setEditing] = useState<Identity | null>(null);
  if (!mb) return <p class="muted">{t("No mailboxes")}</p>;
  return (
    <div class="stack">
      <MailboxSelect value={mbId} onChange={setMbId} />
      <div class="card-list">
        {mb.identities.map((i) => (
          <div key={i.id} class="card row gap">
            <div class="grow">
              <div>
                <b>{i.displayName || i.address}</b> {i.isDefault ? <span class="pill good">{t("Default")}</span> : null}
              </div>
              <div class="muted small">{i.address}{i.replyTo ? ` · ${t("Reply-To")}: ${i.replyTo}` : ""}</div>
              {i.signatureHtml ? <div class="sig-preview" dangerouslySetInnerHTML={{ __html: i.signatureHtml }} /> : null}
            </div>
            <Button size="sm" icon="draft" onClick={() => setEditing(i)}>
              {t("Edit")}
            </Button>
          </div>
        ))}
      </div>
      {editing ? <IdentityEditor mailbox={mb} identity={editing} onClose={() => setEditing(null)} /> : null}
    </div>
  );
}

export function IdentityEditor({ mailbox, identity, onClose, admin }: { mailbox: AccessibleMailbox | { id: number; address: string }; identity: Identity; onClose: () => void; admin?: boolean }) {
  const [name, setName] = useState(identity.displayName);
  const [replyTo, setReplyTo] = useState(identity.replyTo);
  const [def, setDef] = useState(identity.isDefault);
  const [sig, setSig] = useState(identity.signatureHtml);
  const [busy, setBusy] = useState(false);
  const save = async () => {
    setBusy(true);
    try {
      const body = { expectedRevision: identity.revision, displayName: name, replyTo, signatureHtml: sig, isDefault: def };
      if (admin) await patch(`/api/admin/mailboxes/${mailbox.id}/identities/${identity.id}`, { ...body, address: identity.address });
      else await put(`/api/mail/mailboxes/${mailbox.id}/identities/${identity.id}`, body);
      await refreshMailboxes();
      toast(t("Sender identity saved."), "success");
      onClose();
    } catch (e) {
      errorToast(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      title={identity.address}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button kind="primary" busy={busy} onClick={save}>
            {t("Save")}
          </Button>
        </>
      }
    >
      <Field label={t("Display name")}>
        <input value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
      </Field>
      <Field label={t("Reply-To")} hint={t("Optional")}>
        <input type="email" value={replyTo} onInput={(e) => setReplyTo((e.target as HTMLInputElement).value)} />
      </Field>
      <Field label={t("Signature")}>
        <div class="editor small" contentEditable dangerouslySetInnerHTML={{ __html: sig }} onInput={(e) => setSig((e.target as HTMLDivElement).innerHTML)} />
      </Field>
      <Toggle checked={def} onChange={setDef} label={t("Make default")} />
    </Modal>
  );
}

const FIELDS = ["from", "to", "subject", "body", "header", "size"];
const OPS: Record<string, string[]> = { size: ["over", "under"], default: ["contains", "not_contains", "is", "matches"] };
const ACTIONS = ["move", "copy", "flag", "markread", "forward", "redirect", "discard", "stop"];

function RulesTab() {
  const requests = useOperationRequests();
  const boxes = mailboxes.value.filter((b) => b.level === "full");
  const [mbId, setMbId] = useState(boxes[0]?.id ?? 0);
  const [rules, setRules] = useState<SieveRule[]>([]);
  const [vacation, setVacation] = useState<Vacation>({ enabled: false, subject: "", body: "", days: 7 });
  const [folders, setFolders] = useState<Folder[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [available, setAvailable] = useState(true);
  const [revision, setRevision] = useState(0);
  const [reason, setReason] = useState("");
  const [vacationAvailable, setVacationAvailable] = useState(false);
  const [vacationReason, setVacationReason] = useState("");
  const [takeover, setTakeover] = useState<{ active: string; activeScriptHash: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [script, setScript] = useState("");
  const [showScript, setShowScript] = useState(false);
  useEffect(() => {
    if (!mbId) return;
    setLoaded(false);
    setTakeover(null);
    Promise.all([get<{ rules: SieveRule[]; vacation: Vacation | null; script: string; available: boolean; revision: number; capability: Capability; vacationCapability: Capability }>(`/api/mail/mailboxes/${mbId}/rules`), get<Folder[]>(`/api/mail/mailboxes/${mbId}/folders`)]).then(([r, f]) => {
      setRules(r.rules ?? []);
      setVacation(r.vacation ?? { enabled: false, subject: "", body: "", days: 7 });
      setScript(r.script);
      setAvailable(r.available);
      setRevision(r.revision);
      setReason(r.capability.reasonCode ?? "");
      setVacationAvailable(r.vacationCapability.support === "automatic" && r.vacationCapability.readiness === "ready" && r.vacationCapability.permissionAllowed);
      setVacationReason(r.vacationCapability.reasonCode ?? "");
      setFolders(f);
      setLoaded(true);
    }, errorToast);
  }, [mbId]);
  if (!mbId) return <p class="muted">{t("No mailboxes")}</p>;
  if (!loaded) return <Spinner />;
  const upd = (i: number, r: SieveRule) => setRules(rules.map((x, j) => (j === i ? r : x)));
  const save = async (confirmTakeover = false) => {
    setBusy(true);
    try {
      const action = `rules/${mbId}`;
      await waitOperation(await put<Operation>(`/api/mail/mailboxes/${mbId}/rules`, requests.prepare(action, { expectedRevision: revision, rules, vacation, takeover: confirmTakeover, expectedActiveScriptHash: confirmTakeover ? takeover?.activeScriptHash : "" })));
      requests.complete(action);
      const r = await get<{ rules: SieveRule[]; script: string; revision: number }>(`/api/mail/mailboxes/${mbId}/rules`);
      setRules(r.rules);
      setScript(r.script);
      setRevision(r.revision);
      setTakeover(null);
      toast(t("Rules saved and installed on the server."), "success");
    } catch (e) {
      if (e instanceof ApiError && e.code === "sieve_takeover_required" && e.details && typeof e.details === "object" && "active" in e.details && "activeScriptHash" in e.details && typeof e.details.active === "string" && typeof e.details.activeScriptHash === "string") setTakeover({ active: e.details.active, activeScriptHash: e.details.activeScriptHash });
      else errorToast(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div class="stack">
      <MailboxSelect value={mbId} onChange={setMbId} />
      {!available ? <div class="notice warn">{t("Rule management is not available for this server.")} {reason}</div> : null}
      {takeover ? <section class="card"><h3>{t("Confirm active script takeover")}</h3><p>{t("The existing script will be retained.")}</p><p class="mono">{takeover.active}<br />{takeover.activeScriptHash}</p><Button busy={busy} onClick={() => save(true)}>{t("Confirm takeover")}</Button><Button disabled={busy} onClick={() => setTakeover(null)}>{t("Cancel")}</Button></section> : null}
      <p class="muted">
        {t("Rules run on the mail server, before mail reaches your inbox.")} <Info text={t("The active script is verified before the new rules are activated.")} />
      </p>

      <section class="card">
        <h3>{t("Auto-reply")}</h3>
        {!vacationAvailable ? <div class="notice warn">{t("Auto-reply is not available for this server.")} {vacationReason}</div> : null}
        <Toggle checked={vacation.enabled} disabled={!vacationAvailable || busy} onChange={(v) => setVacation({ ...vacation, enabled: v })} label={t("Enabled")} />
        {vacation.enabled ? (
          <>
            <Field label={t("Auto-reply subject")}>
              <input disabled={!vacationAvailable || busy} value={vacation.subject} onInput={(e) => setVacation({ ...vacation, subject: (e.target as HTMLInputElement).value })} />
            </Field>
            <Field label={t("Auto-reply message")}>
              <textarea disabled={!vacationAvailable || busy} rows={4} value={vacation.body} onInput={(e) => setVacation({ ...vacation, body: (e.target as HTMLTextAreaElement).value })} />
            </Field>
            <Field label={t("Repeat interval")} info={t("Reply at most once every N days to the same sender.")}>
              <input disabled={!vacationAvailable || busy} type="number" min={1} max={30} value={vacation.days} onInput={(e) => setVacation({ ...vacation, days: Number((e.target as HTMLInputElement).value) })} />
            </Field>
          </>
        ) : null}
      </section>

      <div class="row gap">
        <h3 class="grow">{t("Rules")}</h3>
        <Button size="sm" icon="plus" disabled={busy} onClick={() => setRules([...rules, { id: crypto.randomUUID(), name: "", enabled: true, match: "all", conditions: [{ field: "from", op: "contains", value: "" }], actions: [{ type: "move", folder: "" }] }])}>
          {t("Add rule")}
        </Button>
      </div>
      {rules.length === 0 ? <p class="muted">{t("Nothing here yet.")}</p> : null}
      {rules.map((r, i) => (
        <section key={i} class={"card rule" + (r.enabled ? "" : " disabled")}>
          <div class="row gap">
            <input class="grow" placeholder={t("Rule name")} value={r.name} onInput={(e) => upd(i, { ...r, name: (e.target as HTMLInputElement).value })} />
            <Toggle checked={r.enabled} onChange={(v) => upd(i, { ...r, enabled: v })} />
            <button class="btn btn-icon" onClick={() => setRules(rules.filter((_, j) => j !== i))} aria-label={t("Delete")}>
              <Icon name="trash" size={16} />
            </button>
          </div>
          <div class="rule-when">
            <span>{t("When")}</span>
            <select value={r.match} onChange={(e) => upd(i, { ...r, match: (e.target as HTMLSelectElement).value as "all" | "any" })}>
              <option value="all">{t("all")}</option>
              <option value="any">{t("any")}</option>
            </select>
            <span>{t("of these match")}</span>
          </div>
          {r.conditions.map((c, ci) => (
            <div key={ci} class="rule-line">
              <select value={c.field} onChange={(e) => { const field = (e.target as HTMLSelectElement).value; upd(i, { ...r, conditions: r.conditions.map((x, k) => (k === ci ? { ...x, field, op: field === "size" ? "over" : "contains" } : x)) }); }}>
                {FIELDS.map((f) => <option key={f} value={f}>{t(f)}</option>)}
              </select>
              {c.field === "header" ? <input placeholder="X-Header" value={c.header ?? ""} onInput={(e) => upd(i, { ...r, conditions: r.conditions.map((x, k) => (k === ci ? { ...x, header: (e.target as HTMLInputElement).value } : x)) })} /> : null}
              <select value={c.op} onChange={(e) => upd(i, { ...r, conditions: r.conditions.map((x, k) => (k === ci ? { ...x, op: (e.target as HTMLSelectElement).value } : x)) })}>
                {(OPS[c.field] ?? OPS.default).map((o) => <option key={o} value={o}>{t(o)}</option>)}
              </select>
              <input class="grow" placeholder={c.field === "size" ? "2M" : ""} value={c.value} onInput={(e) => upd(i, { ...r, conditions: r.conditions.map((x, k) => (k === ci ? { ...x, value: (e.target as HTMLInputElement).value } : x)) })} />
              <button class="btn btn-icon" onClick={() => upd(i, { ...r, conditions: r.conditions.filter((_, k) => k !== ci) })} aria-label={t("Remove")}><Icon name="x" size={14} /></button>
            </div>
          ))}
          <button class="linklike" onClick={() => upd(i, { ...r, conditions: [...r.conditions, { field: "subject", op: "contains", value: "" }] })}>+ {t("Add condition")}</button>
          <div class="rule-when"><span>{t("Then")}</span></div>
          {r.actions.map((a, ai) => (
            <div key={ai} class="rule-line">
              <select value={a.type} onChange={(e) => upd(i, { ...r, actions: r.actions.map((x, k) => (k === ai ? { type: (e.target as HTMLSelectElement).value } : x)) })}>
                {ACTIONS.map((x) => <option key={x} value={x}>{t(x)}</option>)}
              </select>
              {a.type === "move" || a.type === "copy" ? (
                <select class="grow" value={a.folder ?? ""} onChange={(e) => upd(i, { ...r, actions: r.actions.map((x, k) => (k === ai ? { ...x, folder: (e.target as HTMLSelectElement).value } : x)) })}>
                  <option value="">—</option>
                  {folders.filter((f) => !f.noSelect).map((f) => <option key={f.name} value={f.name}>{f.name}</option>)}
                </select>
              ) : null}
              {a.type === "forward" || a.type === "redirect" ? <input class="grow" type="email" placeholder="name@example.com" value={a.address ?? ""} onInput={(e) => upd(i, { ...r, actions: r.actions.map((x, k) => (k === ai ? { ...x, address: (e.target as HTMLInputElement).value } : x)) })} /> : null}
              {a.type === "flag" ? <input class="grow" placeholder="\\Flagged" value={a.flag ?? ""} onInput={(e) => upd(i, { ...r, actions: r.actions.map((x, k) => (k === ai ? { ...x, flag: (e.target as HTMLInputElement).value } : x)) })} /> : null}
              <button class="btn btn-icon" onClick={() => upd(i, { ...r, actions: r.actions.filter((_, k) => k !== ai) })} aria-label={t("Remove")}><Icon name="x" size={14} /></button>
            </div>
          ))}
          <button class="linklike" onClick={() => upd(i, { ...r, actions: [...r.actions, { type: "stop" }] })}>+ {t("Add action")}</button>
        </section>
      ))}
      <div class="row gap">
        <Button kind="primary" busy={busy} onClick={() => save()} disabled={!available}>
          {t("Save")}
        </Button>
        <button class="linklike" onClick={() => setShowScript(!showScript)}>{t("Preview Sieve script")}</button>
      </div>
      {showScript ? <pre class="code">{script}</pre> : null}
    </div>
  );
}
