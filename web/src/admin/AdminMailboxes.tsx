import { useEffect, useState } from "preact/hooks";
import { t, kindLabel } from "@/lib/i18n";
import { get, post, patch, del, type Mailbox, type Domain, type MailboxAccess, type Identity, type Address, type DirectoryEntry } from "@/lib/api";
import { toast, errorToast, can, refreshMailboxes } from "@/lib/state";
import { fmtDate, copyText } from "@/lib/format";
import { Button, Field, Icon, useAsync, Spinner, ErrorBox, Modal, StatusBadge, Badge, Confirm, Menu, Tabs } from "@/ui";
import { PageHead } from "./AdminOrg";
import { IdentityEditor } from "@/settings/Settings";
import { go } from "@/lib/router";

export function MailboxesPage() {
  const { data, error, loading, reload } = useAsync(() => get<Mailbox[]>("/api/admin/mailboxes"), []);
  const [adding, setAdding] = useState<null | "personal" | "shared">(null);
  const [tab, setTab] = useState("all");
  const list = (data ?? []).filter((m) => tab === "all" || (tab === "shared" ? m.kind === "shared" : tab === "personal" ? m.kind === "personal" : m.status !== "active"));
  return (
    <div class="page">
      <PageHead title={t("Mailboxes")}>
        {can("mailboxes.manage") ? <Button kind="primary" icon="plus" size="sm" onClick={() => setAdding("personal")}>{t("Personal mailbox")}</Button> : null}
        {can("shared.manage") ? <Button icon="users" size="sm" onClick={() => setAdding("shared")}>{t("Shared mailbox")}</Button> : null}
      </PageHead>
      <Tabs tabs={[{ key: "all", label: t("Mailboxes") }, { key: "personal", label: t("Personal") }, { key: "shared", label: t("Shared") }, { key: "inactive", label: t("Suspended") }]} value={tab} onChange={setTab} />
      <div class="page-body">
        {loading ? <Spinner /> : error ? <ErrorBox error={error} onRetry={reload} /> : null}
        <table class="table clickable">
          <thead><tr><th>{t("Mailbox")}</th><th>{t("Kind")}</th><th>{t("Owner")}</th><th>{t("Access")}</th><th>{t("Credential")}</th><th>{t("Status")}</th></tr></thead>
          <tbody>
            {list.map((m) => (
              <tr key={m.id} onClick={() => go(`/admin/mailboxes/${m.id}`)}>
                <td><div>{m.displayName || m.address}</div><div class="muted small mono">{m.address}</div></td>
                <td>{m.kind === "shared" ? <Badge tone="accent">{t("Shared")}</Badge> : <Badge>{t("Personal")}</Badge>}</td>
                <td>{m.ownerName || (m.kind === "personal" ? <span class="warn-text">—</span> : "")}</td>
                <td class="muted">{m.accessCount || ""}</td>
                <td>{m.hasCredential ? <Badge tone="good">{t("Connected")}</Badge> : <Badge tone="warn">{t("Not connected")}</Badge>}</td>
                <td><StatusBadge status={m.status} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {adding ? <AddMailboxModal kind={adding} onClose={() => setAdding(null)} onDone={reload} /> : null}
    </div>
  );
}

function AddMailboxModal({ kind, onClose, onDone }: { kind: "personal" | "shared"; onClose: () => void; onDone: () => void }) {
  const domains = useAsync(() => get<Domain[]>("/api/admin/domains"), []);
  const dir = useAsync(() => get<DirectoryEntry[]>("/api/admin/directory"), []);
  const [domainId, setDomainId] = useState(0);
  const [local, setLocal] = useState("");
  const [name, setName] = useState("");
  const [owner, setOwner] = useState(0);
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (domains.data?.[0] && !domainId) setDomainId(domains.data.find((d) => d.status === "active")?.id ?? 0); }, [domains.data]);
  return (
    <Modal title={kind === "shared" ? t("Shared mailbox") : t("Personal mailbox")} onClose={onClose} footer={<><Button onClick={onClose}>{t("Cancel")}</Button><Button kind="primary" busy={busy} onClick={async () => { setBusy(true); try { const mb = await post<Mailbox>("/api/admin/mailboxes", { kind, domainId, localPart: local, displayName: name, ownerMemberId: owner }); refreshMailboxes(); onDone(); onClose(); go(`/admin/mailboxes/${mb.id}`); } catch (e) { errorToast(e); } finally { setBusy(false); } }}>{t("Create")}</Button></>}>
      <div class="row gap addr-row">
        <input placeholder={t("Mailbox name")} value={local} onInput={(e) => setLocal((e.target as HTMLInputElement).value.toLowerCase())} autoFocus />
        <span>@</span>
        <select value={domainId} onChange={(e) => setDomainId(Number((e.target as HTMLSelectElement).value))}>
          {(domains.data ?? []).filter((d) => d.status === "active").map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
        </select>
      </div>
      <Field label={t("Display name")}><input value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} placeholder={kind === "shared" ? "Support" : ""} /></Field>
      {kind === "personal" ? (
        <Field label={t("Owner")}>
          <select value={owner} onChange={(e) => setOwner(Number((e.target as HTMLSelectElement).value))}>
            <option value={0}>—</option>
            {(dir.data ?? []).map((m) => <option key={m.id} value={m.id}>{m.displayName}</option>)}
          </select>
        </Field>
      ) : null}
    </Modal>
  );
}

export function MailboxDetail({ id }: { id: number }) {
  const { data, error, loading, reload } = useAsync(() => get<{ mailbox: Mailbox; access: MailboxAccess[]; identities: Identity[]; addresses: Address[] }>(`/api/admin/mailboxes/${id}`), [id]);
  const dir = useAsync(() => get<DirectoryEntry[]>("/api/admin/directory"), []);
  const [pw, setPw] = useState<null | { password: string; imapHost: string; smtpHost: string; username: string }>(null);
  const [confirm, setConfirm] = useState<null | "suspend" | "delete" | "rotate">(null);
  const [grant, setGrant] = useState({ memberId: 0, level: "full" });
  const [fwd, setFwd] = useState("");
  const [editIdentity, setEditIdentity] = useState<Identity | null>(null);
  const [edit, setEdit] = useState(false);
  if (loading) return <Spinner />;
  if (error || !data) return <ErrorBox error={error} onRetry={reload} />;
  const mb = data.mailbox;
  const perm = mb.kind === "shared" ? "shared.manage" : "mailboxes.manage";
  const manage = can(perm);
  const base = `/api/admin/mailboxes/${mb.id}`;
  const act = async (fn: () => Promise<unknown>, msg?: string) => {
    try {
      await fn();
      if (msg) toast(msg, "success");
      reload();
      refreshMailboxes();
    } catch (e) {
      errorToast(e);
    }
  };
  const primary = data.addresses.find((a) => a.address === mb.address);
  const forwarding = primary?.kind === "forward" ? primary.targets : [];
  return (
    <div class="page">
      <PageHead title={mb.displayName || mb.address}>
        {mb.kind === "shared" ? <Badge tone="accent">{t("Shared")}</Badge> : <Badge>{t("Personal")}</Badge>}
        <StatusBadge status={mb.status} />
        {manage ? (
          <Menu
            button={<Button icon="more" size="sm">{t("Actions")}</Button>}
            items={[
              { label: t("Edit"), icon: "draft", onClick: () => setEdit(true) },
              ...(!mb.hasCredential && mb.status === "active" ? [{ label: t("Connect"), icon: "bolt", onClick: () => act(() => post(`${base}/connect`), t("Connected")) }] : []),
              { label: t("Rotate credential"), icon: "refresh", onClick: () => setConfirm("rotate"), disabled: mb.status !== "active" },
              { label: t("Reset mailbox password"), icon: "key", onClick: () => act(async () => setPw(await post(`${base}/reset-password`))), disabled: mb.status !== "active" },
              mb.status === "suspended"
                ? { label: t("Reactivate"), icon: "check", onClick: () => act(() => post(`${base}/reactivate`)) }
                : { label: t("Suspend mailbox"), icon: "lock", onClick: () => setConfirm("suspend"), disabled: mb.status !== "active" },
              { label: t("Delete mailbox"), icon: "trash", danger: true, onClick: () => setConfirm("delete") },
            ]}
          />
        ) : null}
      </PageHead>
      <div class="page-body">
        <div class="grid2">
          <section class="card">
            <dl class="kv">
              <dt>{t("Mailbox")}</dt><dd class="mono">{mb.address}</dd>
              <dt>{t("Owner")}</dt><dd>{mb.ownerMemberId ? <a href={`/admin/members/${mb.ownerMemberId}`}>{mb.ownerName}</a> : <span class="muted">—</span>}</dd>
              <dt>{t("Credential")}</dt><dd>{mb.hasCredential ? <>{t("Connected")} <span class="muted small">{mb.credentialAt ? fmtDate(mb.credentialAt, "full") : ""}</span></> : <span class="warn-text">{t("Not connected")}</span>}</dd>
              <dt>{t("Forwarding")}</dt><dd>{forwarding.length ? forwarding.join(", ") : <span class="muted">—</span>}</dd>
            </dl>
            <p class="muted small">{t("Mailhearth opens this mailbox with its own app password. Rotate it if you suspect it leaked.")}</p>
          </section>

          <section class="card">
            <h3>{t("Access")}</h3>
            {data.access.length === 0 ? <p class="muted">{t("Nobody else has access.")}</p> : null}
            <ul class="plain-list">
              {data.access.map((a) => (
                <li key={a.id} class="row gap">
                  <a href={`/admin/members/${a.memberId}`} class="grow">{a.memberName}</a>
                  <Badge>{t(a.level)}</Badge>
                  {manage ? <button class="btn btn-icon" onClick={() => act(() => del(`${base}/access/${a.memberId}`))} aria-label={t("Revoke")}><Icon name="x" size={14} /></button> : null}
                </li>
              ))}
            </ul>
            {manage ? (
              <div class="row gap wrap">
                <select value={grant.memberId} onChange={(e) => setGrant({ ...grant, memberId: Number((e.target as HTMLSelectElement).value) })}>
                  <option value={0}>—</option>
                  {(dir.data ?? []).filter((m) => m.id !== mb.ownerMemberId && !data.access.some((a) => a.memberId === m.id)).map((m) => <option key={m.id} value={m.id}>{m.displayName}</option>)}
                </select>
                <select value={grant.level} onChange={(e) => setGrant({ ...grant, level: (e.target as HTMLSelectElement).value })}>
                  <option value="full">{t("full")}</option>
                  <option value="send">{t("send")}</option>
                  <option value="read">{t("read")}</option>
                </select>
                <Button size="sm" disabled={!grant.memberId} onClick={() => act(() => post(`${base}/access`, grant))}>{t("Grant access")}</Button>
              </div>
            ) : null}
          </section>

          <section class="card">
            <h3>{t("Identities & signatures")}</h3>
            <ul class="plain-list">
              {data.identities.map((i) => (
                <li key={i.id} class="row gap">
                  <div class="grow"><b>{i.displayName || i.address}</b> {i.isDefault ? <span class="pill good">{t("Default")}</span> : null}<div class="muted small mono">{i.address}</div></div>
                  {manage ? <Button size="sm" onClick={() => setEditIdentity(i)}>{t("Edit")}</Button> : null}
                </li>
              ))}
            </ul>
          </section>

          <section class="card">
            <h3>{t("Related addresses")}</h3>
            <ul class="plain-list">
              {data.addresses.map((a) => (
                <li key={a.id} class="row gap">
                  <span class="mono grow">{a.address}</span>
                  <Badge tone={a.kind === "primary" ? "good" : "neutral"}>{kindLabel(a.kind)}</Badge>
                </li>
              ))}
            </ul>
            <a href="/admin/addresses" class="linklike">{t("Addresses")} →</a>
          </section>

          {manage ? (
            <section class="card">
              <h3>{t("Forwarding")}</h3>
              <p class="muted small">{t("While forwarding is on, new mail skips this mailbox.")}</p>
              <Field label={t("Forward incoming mail to")} hint={t("Comma-separated addresses")}>
                <input value={fwd} placeholder={forwarding.join(", ")} onInput={(e) => setFwd((e.target as HTMLInputElement).value)} />
              </Field>
              <div class="row gap">
                <Button size="sm" kind="primary" disabled={!fwd.trim()} onClick={() => act(() => post(`${base}/forwarding`, { targets: fwd.split(/[,\s;]+/).filter(Boolean) }), t("Address updated."))}>{t("Save")}</Button>
                {forwarding.length ? <Button size="sm" onClick={() => act(() => post(`${base}/forwarding`, { targets: [] }))}>{t("Stop forwarding")}</Button> : null}
              </div>
            </section>
          ) : null}
        </div>
      </div>

      {edit ? <EditMailboxModal mailbox={mb} dir={dir.data ?? []} onClose={() => setEdit(false)} onSaved={reload} /> : null}
      {editIdentity ? <IdentityEditor mailbox={mb} identity={editIdentity} admin onClose={() => { setEditIdentity(null); reload(); }} /> : null}
      {pw ? (
        <Modal title={t("Password for external clients")} onClose={() => setPw(null)} footer={<Button kind="primary" onClick={() => setPw(null)}>{t("Done")}</Button>}>
          <p class="muted">{t("This sets a new Purelymail password for use in other mail apps. It is shown once.")}</p>
          <dl class="kv">
            <dt>{t("Username")}</dt><dd class="mono">{pw.username}</dd>
            <dt>{t("Password")}</dt><dd class="mono row gap">{pw.password} <button class="btn btn-icon" onClick={() => { copyText(pw.password); toast(t("Copied"), "success"); }}><Icon name="copy" size={14} /></button></dd>
            <dt>{t("IMAP server")}</dt><dd class="mono">{pw.imapHost}</dd>
            <dt>{t("SMTP server")}</dt><dd class="mono">{pw.smtpHost}</dd>
          </dl>
        </Modal>
      ) : null}
      {confirm === "rotate" ? <Confirm title={t("Rotate credential")} text={mb.address} onClose={() => setConfirm(null)} onConfirm={() => act(() => post(`${base}/rotate`))} /> : null}
      {confirm === "suspend" ? <Confirm title={t("Suspend mailbox")} text={t("Suspending locks everyone out. Mail keeps arriving and is kept.")} danger onClose={() => setConfirm(null)} onConfirm={() => act(() => post(`${base}/suspend`))} /> : null}
      {confirm === "delete" ? <Confirm title={t("Delete mailbox")} text={t("Deleting removes the mailbox and all its mail from Purelymail. This cannot be undone.")} danger requireText={mb.address} confirmLabel={t("Delete")} onClose={() => setConfirm(null)} onConfirm={async () => { await del(base, { confirm: mb.address }); refreshMailboxes(); go("/admin/mailboxes"); }} /> : null}
    </div>
  );
}

function EditMailboxModal({ mailbox, dir, onClose, onSaved }: { mailbox: Mailbox; dir: DirectoryEntry[]; onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState(mailbox.displayName);
  const [kind, setKind] = useState(mailbox.kind);
  const [owner, setOwner] = useState(mailbox.ownerMemberId ?? 0);
  const [busy, setBusy] = useState(false);
  return (
    <Modal title={t("Edit")} onClose={onClose} footer={<><Button onClick={onClose}>{t("Cancel")}</Button><Button kind="primary" busy={busy} onClick={async () => { setBusy(true); try { await patch(`/api/admin/mailboxes/${mailbox.id}`, { displayName: name, kind, ownerMemberId: kind === "shared" ? 0 : owner }); refreshMailboxes(); onSaved(); onClose(); } catch (e) { errorToast(e); } finally { setBusy(false); } }}>{t("Save")}</Button></>}>
      <Field label={t("Display name")}><input value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} /></Field>
      <Field label={t("Kind")}>
        <select value={kind} onChange={(e) => setKind((e.target as HTMLSelectElement).value as "personal" | "shared")} disabled={!(can("mailboxes.manage") && can("shared.manage"))}>
          <option value="personal">{t("Personal")}</option>
          <option value="shared">{t("Shared")}</option>
        </select>
      </Field>
      {kind === "personal" ? (
        <Field label={t("Owner")}>
          <select value={owner} onChange={(e) => setOwner(Number((e.target as HTMLSelectElement).value))}>
            <option value={0}>—</option>
            {dir.map((m) => <option key={m.id} value={m.id}>{m.displayName}</option>)}
          </select>
        </Field>
      ) : null}
    </Modal>
  );
}
