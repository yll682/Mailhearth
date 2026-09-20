import { useState } from "preact/hooks";
import { t } from "@/lib/i18n";
import { get, post, put, patch, del, type Overview, type Connection, type Discovery, type ImportResult, type AuditEntry, type Role, type Member } from "@/lib/api";
import { toast, errorToast, can, me, loadMe } from "@/lib/state";
import { fmtDate } from "@/lib/format";
import { Button, Field, Icon, useAsync, Spinner, ErrorBox, Modal, Badge, Confirm, Avatar, Info } from "@/ui";

export function PageHead({ title, children }: { title: string; children?: preact.ComponentChildren }) {
  return (
    <header class="page-head">
      <h1>{title}</h1>
      <div class="spacer" />
      {children}
    </header>
  );
}

export function OverviewPage() {
  const { data, error, loading, reload } = useAsync(() => get<Overview>("/api/admin/overview"), []);
  if (loading) return <Spinner />;
  if (error || !data) return <ErrorBox error={error} onRetry={reload} />;
  const o = data;
  const dnsBad = o.domains.filter((d) => d.dns && !(d.dns.mx && d.dns.spf && d.dns.dkim && d.dns.dmarc) && !d.isShared);
  const attention = o.unconnected.length + o.unassigned.length + dnsBad.length + (o.connection.lastError ? 1 : 0);
  return (
    <div class="page">
      <PageHead title={t("Overview")}>
        <span class="muted small">{o.org.name}</span>
      </PageHead>
      <div class="page-body">
        <div class="stat-grid">
          <a class="stat" href="/admin/members">
            <span class="stat-n">{(o.members.active ?? 0) + (o.members.invited ?? 0)}</span>
            <span class="stat-l">{t("People")}</span>
            <span class="stat-sub muted">{o.members.invited ? `${o.members.invited} ${t("invited")}` : ""}{o.members.departed ? ` · ${o.members.departed} ${t("departed")}` : ""}</span>
          </a>
          <a class="stat" href="/admin/mailboxes">
            <span class="stat-n">{Object.entries(o.mailboxes).filter(([k]) => k.endsWith(".active")).reduce((n, [, v]) => n + v, 0)}</span>
            <span class="stat-l">{t("Mailboxes")}</span>
            <span class="stat-sub muted">{o.mailboxes["shared.active"] ? `${o.mailboxes["shared.active"]} ${t("shared")}` : ""}</span>
          </a>
          <a class="stat" href="/admin/addresses">
            <span class="stat-n">{o.addresses}</span>
            <span class="stat-l">{t("Addresses")}</span>
          </a>
          <a class="stat" href="/admin/domains">
            <span class="stat-n">{o.domains.filter((d) => d.status === "active").length}</span>
            <span class="stat-l">{t("Domains")}</span>
            <span class="stat-sub muted">{dnsBad.length ? <span class="warn-text">{dnsBad.length} DNS</span> : t("Passing")}</span>
          </a>
          {can("billing.read") && o.connection.credit ? (
            <div class="stat">
              <span class="stat-n">${Number(o.connection.credit).toFixed(2)}</span>
              <span class="stat-l">{t("Purelymail credit")}</span>
              <span class="stat-sub muted">{o.connection.lastSyncAt ? `${t("Last sync")} ${fmtDate(o.connection.lastSyncAt)}` : ""}</span>
            </div>
          ) : null}
        </div>

        <section class="card">
          <h3>
            {t("Needs attention")} {attention ? <Badge tone="warn">{attention}</Badge> : <Badge tone="good">0</Badge>}
          </h3>
          {attention === 0 ? <p class="muted">{t("Everything looks good.")}</p> : null}
          {o.connection.lastError ? <div class="notice warn">{o.connection.lastError}</div> : null}
          {o.unconnected.length ? (
            <div class="attn">
              <b>{t("Mailboxes not connected")}</b>
              <ul>
                {o.unconnected.map((m) => (
                  <li key={m.id}>
                    <a href={`/admin/mailboxes/${m.id}`}>{m.address}</a>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          {o.unassigned.length ? (
            <div class="attn">
              <b>{t("Mailboxes without an owner")}</b>
              <ul>
                {o.unassigned.map((m) => (
                  <li key={m.id}>
                    <a href={`/admin/mailboxes/${m.id}`}>{m.address}</a>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          {dnsBad.length ? (
            <div class="attn">
              <b>DNS</b>
              <ul>
                {dnsBad.map((d) => (
                  <li key={d.id}>
                    <a href="/admin/domains">{d.name}</a> — {["mx", "spf", "dkim", "dmarc"].filter((k) => !(d.dns as unknown as Record<string, boolean>)[k]).map((k) => k.toUpperCase()).join(", ")}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
        </section>

        <section class="card">
          <h3>{t("Recent activity")}</h3>
          <AuditTable entries={o.recentAudit} />
          {can("audit.read") ? <a href="/admin/audit" class="linklike">{t("Audit log")} →</a> : null}
        </section>
        {o.pool ? (
          <p class="muted small">
            {t("Connections")}: {o.pool.open} {t("open")}, {o.pool.idle} {t("idle")}, {o.pool.watchers} {t("watchers")}
          </p>
        ) : null}
      </div>
    </div>
  );
}

const actionLabels: Record<string, string> = {
  "org.create": "created the organisation", "org.update": "renamed the organisation", "org.transfer": "transferred ownership", "connection.set": "connected Purelymail", "connection.sync": "synced with Purelymail",
  "member.create": "added a member", "member.update": "updated a member", "member.status": "changed member status", "member.invite": "created an invite", "member.password": "reset a password", "member.offboard": "offboarded a member", "member.delete": "deleted a member",
  "mailbox.create": "created a mailbox", "mailbox.bind": "connected a mailbox", "mailbox.credential": "connected a mailbox", "mailbox.rotate": "rotated a mailbox credential", "mailbox.password": "reset a mailbox password", "mailbox.suspend": "suspended a mailbox", "mailbox.reactivate": "reactivated a mailbox", "mailbox.update": "updated a mailbox", "mailbox.delete": "deleted a mailbox", "mailbox.grant": "granted mailbox access", "mailbox.revoke": "revoked mailbox access", "mailbox.forward": "set mailbox forwarding", "mailbox.forward.clear": "cleared mailbox forwarding", "mailbox.handover": "handed over a mailbox", "mailbox.toshared": "converted a mailbox to shared",
  "address.create": "created an address", "address.update": "updated an address", "address.delete": "deleted an address", "group.create": "created a group", "group.update": "updated a group", "group.delete": "deleted a group", "group.address": "set a group address", "group.address.remove": "removed a group address",
  "domain.add": "added a domain", "domain.update": "updated a domain", "domain.delete": "removed a domain", "role.create": "created a role", "role.update": "updated a role", "role.delete": "deleted a role",
};
const zhActions: Record<string, string> = {
  "created the organisation": "创建了组织", "renamed the organisation": "重命名了组织", "transferred ownership": "转让了所有权", "connected Purelymail": "连接了 Purelymail", "synced with Purelymail": "同步了 Purelymail",
  "added a member": "添加了成员", "updated a member": "更新了成员", "changed member status": "更改了成员状态", "created an invite": "创建了邀请", "reset a password": "重置了密码", "offboarded a member": "为成员办理了离职", "deleted a member": "删除了成员",
  "created a mailbox": "创建了邮箱", "connected a mailbox": "连接了邮箱", "rotated a mailbox credential": "轮换了邮箱凭据", "reset a mailbox password": "重置了邮箱密码", "suspended a mailbox": "挂起了邮箱", "reactivated a mailbox": "重新启用了邮箱", "updated a mailbox": "更新了邮箱", "deleted a mailbox": "删除了邮箱", "granted mailbox access": "授予了邮箱访问权限", "revoked mailbox access": "撤销了邮箱访问权限", "set mailbox forwarding": "设置了邮箱转发", "cleared mailbox forwarding": "取消了邮箱转发", "handed over a mailbox": "移交了邮箱", "converted a mailbox to shared": "将邮箱转为共享",
  "created an address": "创建了地址", "updated an address": "更新了地址", "deleted an address": "删除了地址", "created a group": "创建了群组", "updated a group": "更新了群组", "deleted a group": "删除了群组", "set a group address": "设置了群组地址", "removed a group address": "移除了群组地址",
  "added a domain": "添加了域名", "updated a domain": "更新了域名", "removed a domain": "移除了域名", "created a role": "创建了角色", "updated a role": "更新了角色", "deleted a role": "删除了角色",
};
function actionText(a: string): string {
  const en = actionLabels[a] ?? a;
  return t(en) === en && zhActions[en] && document.documentElement.lang === "zh-CN" ? zhActions[en] : en;
}

function detailText(d: unknown): string {
  if (!d || typeof d !== "object") return "";
  const o = d as Record<string, unknown>;
  const parts: string[] = [];
  for (const k of ["address", "name", "login", "kind", "level", "status", "targets", "newOwner", "role"]) {
    if (o[k] !== undefined && o[k] !== null && o[k] !== "" && o[k] !== 0) parts.push(Array.isArray(o[k]) ? (o[k] as unknown[]).join(", ") : String(o[k]));
  }
  return parts.join(" · ");
}

export function AuditTable({ entries }: { entries: AuditEntry[] }) {
  if (!entries.length) return <p class="muted">{t("Nothing here yet.")}</p>;
  return (
    <table class="table">
      <thead>
        <tr>
          <th>{t("Time")}</th>
          <th>{t("Who")}</th>
          <th>{t("What")}</th>
          <th>{t("Target")}</th>
        </tr>
      </thead>
      <tbody>
        {entries.map((e) => (
          <tr key={e.id}>
            <td class="nowrap muted">{fmtDate(e.createdAt, "full")}</td>
            <td class="nowrap">{e.actorName || t("System")}</td>
            <td>{actionText(e.action)}</td>
            <td class="muted">{detailText(e.detail)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

export function AuditPage() {
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [done, setDone] = useState(false);
  const { error, loading, reload } = useAsync(async () => {
    const list = await get<AuditEntry[]>("/api/admin/audit?limit=100");
    setEntries(list);
    setDone(list.length < 100);
    return list;
  }, []);
  const more = async () => {
    const before = entries[entries.length - 1]?.id ?? 0;
    const list = await get<AuditEntry[]>(`/api/admin/audit?limit=100&before=${before}`);
    setEntries([...entries, ...list]);
    setDone(list.length < 100);
  };
  return (
    <div class="page">
      <PageHead title={t("Audit log")} />
      <div class="page-body">
        {loading ? <Spinner /> : error ? <ErrorBox error={error} onRetry={reload} /> : <AuditTable entries={entries} />}
        {!done && !loading ? <Button onClick={more}>{t("Load older")}</Button> : null}
      </div>
    </div>
  );
}

export function ConnectionPage() {
  const { data, error, loading, reload } = useAsync(() => get<Connection>("/api/admin/connection"), []);
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [orgName, setOrgName] = useState(me.value?.org.name ?? "");
  const [transfer, setTransfer] = useState(false);
  if (loading) return <Spinner />;
  if (error || !data) return <ErrorBox error={error} onRetry={reload} />;
  return (
    <div class="page">
      <PageHead title={t("Connection")} />
      <div class="page-body narrow stack">
        <section class="card">
          <h3>{t("Organisation")}</h3>
          <Field label={t("Organisation name")}>
            <input value={orgName} onInput={(e) => setOrgName((e.target as HTMLInputElement).value)} />
          </Field>
          <div class="row gap">
            <Button
              kind="primary"
              size="sm"
              onClick={async () => {
                try {
                  await patch("/api/admin/org", { name: orgName });
                  await loadMe();
                  toast(t("Organisation renamed."), "success");
                } catch (e) {
                  errorToast(e);
                }
              }}
            >
              {t("Rename organisation")}
            </Button>
            {can("org.owner") ? (
              <Button size="sm" onClick={() => setTransfer(true)}>
                {t("Transfer ownership")}
              </Button>
            ) : null}
          </div>
        </section>
        <section class="card">
          <h3>Purelymail</h3>
          <dl class="kv">
            <dt>{t("API endpoint")}</dt>
            <dd class="mono">{data.apiUrl}</dd>
            <dt>{t("API token")}</dt>
            <dd class="mono">{data.tokenHint}</dd>
            {can("billing.read") ? (
              <>
                <dt>{t("Purelymail credit")}</dt>
                <dd>${Number(data.credit || 0).toFixed(2)}</dd>
              </>
            ) : null}
            <dt>{t("Last sync")}</dt>
            <dd>{data.lastSyncAt ? fmtDate(data.lastSyncAt, "full") : t("Never")}</dd>
          </dl>
          {data.lastError ? <div class="notice warn">{data.lastError}</div> : null}
          <p class="muted">
            {t("Imports changes made outside Mailhearth.")} <Info text={t("Re-reads domains, mailboxes and routing rules from Purelymail and updates the organisation model.")} />
          </p>
          <Button
            icon="refresh"
            busy={syncing}
            onClick={async () => {
              setSyncing(true);
              try {
                const r = await post<ImportResult>("/api/admin/connection/sync");
                toast(t("Sync finished: {n} new mailboxes, {a} new addresses.", { n: r.mailboxesNew, a: r.addressesNew }), "success");
                reload();
              } catch (e) {
                errorToast(e);
              } finally {
                setSyncing(false);
              }
            }}
          >
            {syncing ? t("Syncing…") : t("Sync now")}
          </Button>
        </section>
        <section class="card">
          <h3>{t("Replace token")}</h3>
          <Field label={t("Purelymail API token")} hint={t("The token is never shown again after saving.")}>
            <input value={token} onInput={(e) => setToken((e.target as HTMLInputElement).value)} />
          </Field>
          <Button
            kind="primary"
            busy={busy}
            disabled={!token.trim()}
            onClick={async () => {
              setBusy(true);
              try {
                await put<Discovery>("/api/admin/connection", { apiToken: token });
                setToken("");
                toast(t("Token updated."), "success");
                reload();
              } catch (e) {
                errorToast(e);
              } finally {
                setBusy(false);
              }
            }}
          >
            {t("Save")}
          </Button>
        </section>
      </div>
      {transfer ? <TransferModal onClose={() => setTransfer(false)} /> : null}
    </div>
  );
}

function TransferModal({ onClose }: { onClose: () => void }) {
  const { data } = useAsync(() => get<Member[]>("/api/admin/members"), []);
  const [to, setTo] = useState(0);
  const [busy, setBusy] = useState(false);
  const candidates = (data ?? []).filter((m) => m.status === "active" && m.id !== me.value?.member.id);
  return (
    <Modal
      title={t("Transfer ownership")}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button
            kind="danger"
            busy={busy}
            disabled={!to}
            onClick={async () => {
              setBusy(true);
              try {
                await post("/api/admin/org/transfer", { memberId: to });
                await loadMe();
                toast(t("Ownership transferred."), "success");
                onClose();
              } catch (e) {
                errorToast(e);
              } finally {
                setBusy(false);
              }
            }}
          >
            {t("Transfer ownership")}
          </Button>
        </>
      }
    >
      <Field label={t("Choose the new owner")} hint={t("You will become an administrator.")}>
        <select value={to} onChange={(e) => setTo(Number((e.target as HTMLSelectElement).value))}>
          <option value={0}>—</option>
          {candidates.map((m) => (
            <option key={m.id} value={m.id}>
              {m.displayName} ({m.loginEmail})
            </option>
          ))}
        </select>
      </Field>
    </Modal>
  );
}

const ALL_PERMS = ["org.manage", "domains.manage", "members.manage", "mailboxes.manage", "shared.manage", "addresses.manage", "groups.manage", "audit.read", "billing.read"];

export function RolesPage() {
  const { data, error, loading, reload } = useAsync(() => get<Role[]>("/api/admin/roles"), []);
  const [editing, setEditing] = useState<Role | null | "new">(null);
  const [deleting, setDeleting] = useState<Role | null>(null);
  return (
    <div class="page">
      <PageHead title={t("Roles")}>
        <Button kind="primary" icon="plus" size="sm" onClick={() => setEditing("new")}>
          {t("Add role")}
        </Button>
      </PageHead>
      <div class="page-body">
        {loading ? <Spinner /> : error ? <ErrorBox error={error} onRetry={reload} /> : null}
        <div class="card-list">
          {(data ?? []).map((r) => (
            <div key={r.id} class="card row gap">
              <div class="grow">
                <div>
                  <b>{t(r.name)}</b> {r.builtin ? <Badge>{t("Built-in")}</Badge> : <Badge tone="accent">{t("custom")}</Badge>} <span class="muted small">{r.memberCount} {t("members")}</span>
                </div>
                <div class="muted small">{r.description}</div>
                <div class="chips">
                  {r.permissions.map((p) => (
                    <span key={p} class="chip small">{t(p)}</span>
                  ))}
                </div>
              </div>
              {!r.builtin ? (
                <>
                  <Button size="sm" onClick={() => setEditing(r)}>{t("Edit")}</Button>
                  <Button size="sm" kind="ghost" onClick={() => setDeleting(r)} disabled={r.memberCount > 0}>{t("Delete")}</Button>
                </>
              ) : null}
            </div>
          ))}
        </div>
      </div>
      {editing ? <RoleEditor role={editing === "new" ? null : editing} onClose={() => setEditing(null)} onSaved={reload} /> : null}
      {deleting ? (
        <Confirm title={t("Delete")} text={deleting.name} danger onClose={() => setDeleting(null)} onConfirm={async () => { await del(`/api/admin/roles/${deleting.id}`); reload(); }} />
      ) : null}
    </div>
  );
}

function RoleEditor({ role, onClose, onSaved }: { role: Role | null; onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState(role?.name ?? "");
  const [desc, setDesc] = useState(role?.description ?? "");
  const [perms, setPerms] = useState<Set<string>>(new Set(role?.permissions ?? []));
  const [busy, setBusy] = useState(false);
  return (
    <Modal
      title={role ? t("Edit") : t("Add role")}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button kind="primary" busy={busy} onClick={async () => { setBusy(true); try { const body = { name, description: desc, permissions: [...perms] }; if (role) await patch(`/api/admin/roles/${role.id}`, body); else await post("/api/admin/roles", body); onSaved(); onClose(); } catch (e) { errorToast(e); } finally { setBusy(false); } }}>
            {t("Save")}
          </Button>
        </>
      }
    >
      <Field label={t("Name")}>
        <input value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
      </Field>
      <Field label={t("Description")}>
        <input value={desc} onInput={(e) => setDesc((e.target as HTMLInputElement).value)} />
      </Field>
      <div class="field-label">{t("Permissions")}</div>
      <div class="check-list">
        {ALL_PERMS.map((p) => (
          <label key={p} class="check-row">
            <input type="checkbox" checked={perms.has(p)} onChange={(e) => { const s = new Set(perms); (e.target as HTMLInputElement).checked ? s.add(p) : s.delete(p); setPerms(s); }} />
            <span>{t(p)}</span> <span class="muted small mono">{p}</span>
          </label>
        ))}
      </div>
    </Modal>
  );
}

export { Avatar, Icon };
