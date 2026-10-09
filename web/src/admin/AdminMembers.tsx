import { useEffect, useRef, useState } from "preact/hooks";
import { useOperationRequests } from "@/lib/useOperationRequests";
import { t } from "@/lib/i18n";
import { get, post, patch, del, type ResourceOptions, type Operation, type Member, type Role, type Mailbox, type Group, type AccessibleMailbox } from "@/lib/api";
import { toast, errorToast, can, me, refreshMailboxes } from "@/lib/state";
import { fmtDate, copyText } from "@/lib/format";
import { Button, Field, Icon, useAsync, Spinner, ErrorBox, Modal, StatusBadge, Avatar, Confirm, Menu, Toggle, Badge } from "@/ui";
import { PageHead } from "./AdminOrg";
import { go } from "@/lib/router";
import { MailboxAttachForm } from "./AdminConnections";

export function MembersPage() {
  const { data, error, loading, reload } = useAsync(() => get<Member[]>("/api/admin/members"), []);
  const [adding, setAdding] = useState(false);
  const [q, setQ] = useState("");
  const list = (data ?? []).filter((m) => !q || `${m.displayName} ${m.loginEmail} ${m.title} ${m.department}`.toLowerCase().includes(q.toLowerCase()));
  return (
    <div class="page">
      <PageHead title={t("Members")}>
        <div class="search compact">
          <Icon name="search" size={15} />
          <input placeholder={t("Search")} value={q} onInput={(e) => setQ((e.target as HTMLInputElement).value)} />
        </div>
        <Button kind="primary" icon="plus" size="sm" onClick={() => setAdding(true)}>
          {t("Add member")}
        </Button>
      </PageHead>
      <div class="page-body">
        {loading ? <Spinner /> : error ? <ErrorBox error={error} onRetry={reload} /> : null}
        <table class="table clickable">
          <thead>
            <tr>
              <th>{t("Name")}</th>
              <th>{t("Login email")}</th>
              <th>{t("Role")}</th>
              <th>{t("Department")}</th>
              <th>{t("Status")}</th>
              <th>{t("Last sign-in")}</th>
            </tr>
          </thead>
          <tbody>
            {list.map((m) => (
              <tr key={m.id} onClick={() => go(`/admin/members/${m.id}`)}>
                <td>
                  <div class="row gap-sm">
                    <Avatar name={m.displayName} size={28} />
                    <div>
                      <div>{m.displayName}</div>
                      {m.title ? <div class="muted small">{m.title}</div> : null}
                    </div>
                  </div>
                </td>
                <td class="mono small">{m.loginEmail}</td>
                <td>{t(m.roleName)}</td>
                <td>{m.department}</td>
                <td><StatusBadge status={m.status} /></td>
                <td class="muted small">{m.lastLoginAt ? fmtDate(m.lastLoginAt, "full") : t("Never")}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {adding ? <AddMemberModal onClose={() => setAdding(false)} onDone={reload} /> : null}
    </div>
  );
}

function useRefData() {
  return useAsync(async () => {
    const [roles, options, mailboxes, groups] = await Promise.all([get<Role[]>("/api/admin/roles"), get<ResourceOptions>("/api/admin/resource-options"), get<Mailbox[]>("/api/admin/mailboxes"), get<Group[]>("/api/admin/groups")]);
    return { roles, options, mailboxes, groups };
  }, []);
}

function AddMemberModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const ref = useRefData();
  const [f, setF] = useState({ displayName: "", loginEmail: "", roleId: 0, title: "", department: "" });
  const [mbMode, setMbMode] = useState<"create" | "attach" | "bind" | "none">("none");
  const [connectionId, setConnectionId] = useState(0);
  const [bindId, setBindId] = useState(0);
  const [shared, setShared] = useState<Set<number>>(new Set());
  const [groups, setGroups] = useState<Set<number>>(new Set());
  const [auth, setAuth] = useState<"invite" | "password">("invite");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const request = useRef<{ body: string; id: string } | null>(null);
  useEffect(() => {
    if (ref.data) {
      if (!f.roleId) setF((x) => ({ ...x, roleId: ref.data!.roles.find((r) => r.key === "member")?.id ?? 0 }));
    }
  }, [ref.data]);
  const unassigned = (ref.data?.mailboxes ?? []).filter((m) => m.kind === "personal" && !m.ownerMemberId && m.status === "active");
  const sharedBoxes = (ref.data?.mailboxes ?? []).filter((m) => m.kind === "shared" && m.status === "active");
  const canMailbox = can("mailboxes.manage");
  const connection = ref.data?.options.connections.find((item) => item.id === connectionId);
  const saveMember = async (mailbox?: Record<string, unknown>) => {
    const body: Record<string, unknown> = { ...f, mailboxAction: canMailbox ? mbMode : "none", sendInvite: auth === "invite", password: auth === "password" ? password : "", groupIds: [...groups].sort((a, b) => a - b), sharedMailboxes: [...shared].sort((a, b) => a - b), groupExpectedRevisions: Object.fromEntries((ref.data?.groups ?? []).filter((item) => groups.has(item.id)).map((item) => [item.id, item.revision])), sharedExpectedRevisions: Object.fromEntries(sharedBoxes.filter((item) => shared.has(item.id)).map((item) => [item.id, item.revision])) };
    if (mbMode === "bind" && canMailbox) { body.mailboxId = bindId; body.mailboxExpectedRevision = unassigned.find((item) => item.id === bindId)?.revision; }
    if (mailbox && (mbMode === "create" || mbMode === "attach")) { const { requestId, ...configuration } = mailbox; body[mbMode] = configuration; }
    const serialized = JSON.stringify(body); if (request.current?.body !== serialized) request.current = { body: serialized, id: crypto.randomUUID() };
    const operation = await post<Operation>("/api/admin/members", { ...body, requestId: request.current!.id });
    onDone(); onClose(); go(`/admin/operations/${operation.operationId}`);
  };
  return (
    <Modal
      title={t("Onboard a new member")}
      wide
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button
            kind="primary"
            busy={busy}
            disabled={mbMode === "create" || mbMode === "attach" || ref.loading || !!ref.error}
            onClick={async () => {
              setBusy(true);
              try {
                await saveMember();
              } catch (e) {
                errorToast(e);
              } finally {
                setBusy(false);
              }
            }}
          >
            {t("Create")}
          </Button>
        </>
      }
    >
      {ref.loading ? <Spinner /> : null}
      {ref.error ? <ErrorBox error={ref.error} onRetry={ref.reload} /> : null}
      <div class="grid2">
        <Field label={t("Name")}>
          <input value={f.displayName} onInput={(e) => setF({ ...f, displayName: (e.target as HTMLInputElement).value })} autoFocus />
        </Field>
        <Field label={t("Role")}>
          <select value={f.roleId} onChange={(e) => setF({ ...f, roleId: Number((e.target as HTMLSelectElement).value) })}>
            {(ref.data?.roles ?? []).filter((r) => r.key !== "owner").map((r) => <option key={r.id} value={r.id}>{t(r.name)}</option>)}
          </select>
        </Field>
        <Field label={t("Title")}>
          <input value={f.title} onInput={(e) => setF({ ...f, title: (e.target as HTMLInputElement).value })} />
        </Field>
        <Field label={t("Department")}>
          <input value={f.department} onInput={(e) => setF({ ...f, department: (e.target as HTMLInputElement).value })} />
        </Field>
      </div>
      {canMailbox ? (
        <fieldset class="fieldset">
          <legend>{t("Mailbox")}</legend>
          <div class="radio-row">
            <label><input type="radio" checked={mbMode === "create"} onChange={() => setMbMode("create")} /> {t("Create a new mailbox")}</label>
            <label><input type="radio" checked={mbMode === "attach"} onChange={() => setMbMode("attach")} /> {t("Register existing mailbox")}</label>
            <label><input type="radio" checked={mbMode === "bind"} onChange={() => setMbMode("bind")} disabled={!unassigned.length} /> {t("Use an existing mailbox")}</label>
            <label><input type="radio" checked={mbMode === "none"} onChange={() => setMbMode("none")} /> {t("No mailbox for now")}</label>
          </div>
          {mbMode === "create" || mbMode === "attach" ? <Field label={t("Mail connection")}><select value={connectionId} onChange={(event) => setConnectionId(Number(event.currentTarget.value))}><option value={0}>—</option>{(ref.data?.options.connections ?? []).filter((item) => item.enabled && (mbMode !== "create" || item.providerKind !== "manual")).map((item) => <option key={item.id} value={item.id}>{item.label} · #{item.id}</option>)}</select></Field> : null}
          {mbMode === "bind" ? (
            <select value={bindId} onChange={(e) => setBindId(Number((e.target as HTMLSelectElement).value))}>
              <option value={0}>—</option>
              {unassigned.map((m) => <option key={m.id} value={m.id}>{m.address} · {m.connectionLabel} · #{m.id}</option>)}
            </select>
          ) : null}
        </fieldset>
      ) : null}
      <Field label={t("Login email")} hint={t("Same as the mailbox address if left blank.")}>
        <input type="email" value={f.loginEmail} onInput={(e) => setF({ ...f, loginEmail: (e.target as HTMLInputElement).value })} />
      </Field>
      {sharedBoxes.length ? (
        <div>
          <div class="field-label">{t("Access to shared mailboxes")}</div>
          <div class="chips">
            {sharedBoxes.map((m) => (
              <label key={m.id} class={"chip select" + (shared.has(m.id) ? " on" : "")}>
                <input type="checkbox" hidden checked={shared.has(m.id)} onChange={(e) => { const s = new Set(shared); (e.target as HTMLInputElement).checked ? s.add(m.id) : s.delete(m.id); setShared(s); }} />
                {m.displayName || m.address}
              </label>
            ))}
          </div>
        </div>
      ) : null}
      {ref.data?.groups.length ? (
        <div>
          <div class="field-label">{t("Add to groups")}</div>
          <div class="chips">
            {ref.data.groups.map((g) => (
              <label key={g.id} class={"chip select" + (groups.has(g.id) ? " on" : "")}>
                <input type="checkbox" hidden checked={groups.has(g.id)} onChange={(e) => { const s = new Set(groups); (e.target as HTMLInputElement).checked ? s.add(g.id) : s.delete(g.id); setGroups(s); }} />
                {g.name}
              </label>
            ))}
          </div>
        </div>
      ) : null}
      <fieldset class="fieldset">
        <legend>{t("How will they sign in?")}</legend>
        <div class="radio-row">
          <label><input type="radio" checked={auth === "invite"} onChange={() => setAuth("invite")} /> {t("Send an invite link")}</label>
          <label><input type="radio" checked={auth === "password"} onChange={() => setAuth("password")} /> {t("Set a password now")}</label>
        </div>
        {auth === "password" ? (
          <Field label={t("Password")} hint={t("At least 10 characters.")}>
            <input type="text" value={password} onInput={(e) => setPassword((e.target as HTMLInputElement).value)} />
          </Field>
        ) : null}
      </fieldset>
      {connection && (mbMode === "create" || mbMode === "attach") ? <MailboxAttachForm key={`${connection.id}/${mbMode}`} connection={connection} mode={mbMode} bindings={ref.data?.options.bindings} initialKind="personal" fixedKind onPrepared={saveMember} onSaved={() => {}} /> : null}
    </Modal>
  );
}

export function InviteLinkBox({ link, name }: { link: string; name: string }) {
  const full = link.startsWith("http") ? link : location.origin + link;
  return (
    <div class="invite-box">
      <div class="field-label">{t("Invite link")}</div>
      <div class="row gap">
        <input readOnly value={full} onFocus={(e) => (e.target as HTMLInputElement).select()} />
        <Button size="sm" icon="copy" onClick={() => { copyText(full); toast(t("Copied"), "success"); }}>{t("Copy")}</Button>
      </div>
      <p class="muted small">{t("Share this link with {name}. It expires in {days} days.", { name, days: 7 })}</p>
    </div>
  );
}

export function MemberDetail({ id }: { id: number }) {
  const requests = useOperationRequests();
  const { data, error, loading, reload } = useAsync(() => get<{ member: Member; mailboxes: AccessibleMailbox[]; groupIds: number[] }>(`/api/admin/members/${id}`), [id]);
  const roles = useAsync(() => get<Role[]>("/api/admin/roles"), []);
  const [edit, setEdit] = useState(false);
  const [invite, setInvite] = useState("");
  const [pw, setPw] = useState<null | string>(null);
  const [offboard, setOffboard] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  if (loading) return <Spinner />;
  if (error || !data) return <ErrorBox error={error} onRetry={reload} />;
  const m = data.member;
  const isSelf = me.value?.member.id === m.id;
  const act = async (fn: () => Promise<unknown>) => {
    try {
      await fn();
      reload();
    } catch (e) {
      errorToast(e);
    }
  };
  return (
    <div class="page">
      <PageHead title={m.displayName}>
        <StatusBadge status={m.status} />
        <Menu
          button={<Button icon="more" size="sm">{t("Actions")}</Button>}
          items={[
            { label: t("Edit"), icon: "draft", onClick: () => setEdit(true) },
            { label: t("New invite link"), icon: "link", onClick: () => act(async () => setInvite((await post<{ inviteLink: string }>(`/api/admin/members/${m.id}/invite`)).inviteLink)), disabled: m.status === "departed" },
            { label: t("Reset password"), icon: "key", onClick: () => setPw(""), disabled: m.status === "departed" },
            m.status === "disabled"
              ? { label: t("Enable"), icon: "check", onClick: () => act(async () => { const operation = await post<Operation>(`/api/admin/members/${m.id}/status`, requests.prepare(`status/${m.id}`, { expectedRevision: m.revision, enabled: true })); go(`/admin/operations/${operation.operationId}`); }) }
              : { label: t("Disable"), icon: "lock", onClick: () => act(async () => { const operation = await post<Operation>(`/api/admin/members/${m.id}/status`, requests.prepare(`status/${m.id}`, { expectedRevision: m.revision, enabled: false })); go(`/admin/operations/${operation.operationId}`); }), disabled: isSelf || m.roleKey === "owner" || m.status === "departed" },
            { label: t("Offboard"), icon: "logout", onClick: () => setOffboard(true), danger: true, disabled: isSelf || m.roleKey === "owner" || m.status === "departed" },
            { label: t("Delete member"), icon: "trash", onClick: () => setConfirmDelete(true), danger: true, disabled: isSelf || m.roleKey === "owner" || (m.status !== "invited" && m.status !== "departed") || data.mailboxes.some((b) => b.ownerMemberId === m.id) },
          ]}
        />
      </PageHead>
      <div class="page-body">
        {invite ? <InviteLinkBox link={invite} name={m.displayName} /> : null}
        <div class="grid2">
          <section class="card">
            <div class="row gap">
              <Avatar name={m.displayName} size={48} />
              <div>
                <div class="h3">{m.displayName}</div>
                <div class="muted">{m.title}{m.title && m.department ? " · " : ""}{m.department}</div>
              </div>
            </div>
            <dl class="kv">
              <dt>{t("Login email")}</dt><dd class="mono">{m.loginEmail}</dd>
              <dt>{t("Role")}</dt><dd>{t(m.roleName)}</dd>
              <dt>{t("Last sign-in")}</dt><dd>{m.lastLoginAt ? fmtDate(m.lastLoginAt, "full") : t("Never")}</dd>
              {m.departedAt ? <><dt>{t("Departed")}</dt><dd>{fmtDate(m.departedAt, "full")}</dd></> : null}
            </dl>
          </section>
          <section class="card">
            <h3>{t("Mailboxes")}</h3>
            {data.mailboxes.length === 0 ? <p class="muted">{t("No mailboxes")}</p> : null}
            <ul class="plain-list">
              {data.mailboxes.map((b) => (
                <li key={b.id} class="row gap">
                  <Icon name={b.kind === "shared" ? "users" : "mail"} size={15} class="muted" />
                  <a href={`/admin/mailboxes/${b.id}`} class="grow">{b.displayName || b.address} <span class="muted small">{b.address}</span></a>
                  <Badge tone={b.ownerMemberId === m.id ? "good" : "neutral"}>{b.ownerMemberId === m.id ? t("Owner") : t(b.level)}</Badge>
                  {b.protocols.imap?.readiness !== "ready" ? <Badge tone="warn">IMAP · {b.protocols.imap?.readiness ?? "unconfigured"}</Badge> : null}
                </li>
              ))}
            </ul>
          </section>
        </div>
      </div>
      {edit ? <EditMemberModal member={m} roles={roles.data ?? []} onClose={() => setEdit(false)} onSaved={reload} /> : null}
      {pw !== null ? (
        <Modal title={t("Reset password")} onClose={() => setPw(null)} footer={<><Button onClick={() => setPw(null)}>{t("Cancel")}</Button><Button kind="primary" onClick={() => act(async () => { await post(`/api/admin/members/${m.id}/password`, { password: pw }); toast(t("Password changed."), "success"); setPw(null); })}>{t("Save")}</Button></>}>
          <p>{t("Set a temporary password for {name}. They will be signed out everywhere.", { name: m.displayName })}</p>
          <Field label={t("Temporary password")}><input value={pw} onInput={(e) => setPw((e.target as HTMLInputElement).value)} /></Field>
        </Modal>
      ) : null}
      {offboard ? <OffboardModal member={m} mailboxes={data.mailboxes.filter((b) => b.ownerMemberId === m.id)} onClose={() => setOffboard(false)} onDone={reload} /> : null}
      {confirmDelete ? <Confirm title={t("Delete member")} text={m.displayName} danger onClose={() => setConfirmDelete(false)} onConfirm={async () => { await del(`/api/admin/members/${m.id}`, { expectedRevision: m.revision }); go("/admin/members"); }} /> : null}
    </div>
  );
}

function EditMemberModal({ member, roles, onClose, onSaved }: { member: Member; roles: Role[]; onClose: () => void; onSaved: () => void }) {
  const [f, setF] = useState({ expectedRevision: member.revision, displayName: member.displayName, loginEmail: member.loginEmail, roleId: member.roleId, title: member.title, department: member.department });
  const [busy, setBusy] = useState(false);
  const isSelf = me.value?.member.id === member.id;
  return (
    <Modal title={t("Edit")} onClose={onClose} footer={<><Button onClick={onClose}>{t("Cancel")}</Button><Button kind="primary" busy={busy} onClick={async () => { setBusy(true); try { await patch(`/api/admin/members/${member.id}`, f); onSaved(); onClose(); } catch (e) { errorToast(e); } finally { setBusy(false); } }}>{t("Save")}</Button></>}>
      <Field label={t("Name")}><input value={f.displayName} onInput={(e) => setF({ ...f, displayName: (e.target as HTMLInputElement).value })} /></Field>
      <Field label={t("Login email")}><input type="email" value={f.loginEmail} onInput={(e) => setF({ ...f, loginEmail: (e.target as HTMLInputElement).value })} /></Field>
      <Field label={t("Role")}>
        <select value={f.roleId} disabled={isSelf || member.roleKey === "owner"} onChange={(e) => setF({ ...f, roleId: Number((e.target as HTMLSelectElement).value) })}>
          {roles.filter((r) => r.key !== "owner" || member.roleKey === "owner").map((r) => <option key={r.id} value={r.id}>{t(r.name)}</option>)}
        </select>
      </Field>
      <div class="grid2">
        <Field label={t("Title")}><input value={f.title} onInput={(e) => setF({ ...f, title: (e.target as HTMLInputElement).value })} /></Field>
        <Field label={t("Department")}><input value={f.department} onInput={(e) => setF({ ...f, department: (e.target as HTMLInputElement).value })} /></Field>
      </div>
    </Modal>
  );
}

interface Plan { mailboxId: number; expectedRevision: number; action: "handover" | "shared" | "keep" | "suspend"; newOwnerId: number; grantMemberIds: number[]; forwardTo: string }

function OffboardModal({ member, mailboxes, onClose, onDone }: { member: Member; mailboxes: AccessibleMailbox[]; onClose: () => void; onDone: () => void }) {
  const requests = useOperationRequests();
  const members = useAsync(() => get<Member[]>("/api/admin/members"), []);
  const others = (members.data ?? []).filter((m) => m.id !== member.id && m.status === "active");
  const [plans, setPlans] = useState<Plan[]>(mailboxes.map((b) => ({ mailboxId: b.id, expectedRevision: b.revision, action: "handover", newOwnerId: 0, grantMemberIds: [], forwardTo: "" })));
  const [removeGroups, setRemoveGroups] = useState(true);
  const [busy, setBusy] = useState(false);
  const upd = (i: number, p: Partial<Plan>) => setPlans(plans.map((x, j) => (j === i ? { ...x, ...p } : x)));
  return (
    <Modal title={t("Offboard {name}", { name: member.displayName })} wide onClose={onClose} footer={<><Button onClick={onClose}>{t("Cancel")}</Button><Button kind="danger" busy={busy} onClick={async () => { setBusy(true); try { const operation = await post<Operation>(`/api/admin/members/${member.id}/offboard`, requests.prepare("offboard", { expectedRevision: member.revision, plans: plans.map((p) => ({ ...p, forwardTo: p.forwardTo.split(/[,\s;]+/).filter(Boolean) })), removeFromGroups: removeGroups })); refreshMailboxes(); onDone(); onClose(); go(`/admin/operations/${operation.operationId}`); } catch (e) { errorToast(e); } finally { setBusy(false); } }}>{t("Complete offboarding")}</Button></>}>
      <p class="muted">{t("Offboarding immediately revokes local sessions and mailbox access. Handover and remote access actions remain visible in the operation.")}</p>
      {mailboxes.length === 0 ? <p class="muted">{t("No mailboxes")}</p> : null}
      {mailboxes.map((b, i) => {
        const p = plans[i];
        return (
          <section key={b.id} class="card">
            <b>{b.address}</b>
            <div class="radio-row wrap">
              <label><input type="radio" checked={p.action === "handover"} onChange={() => upd(i, { action: "handover" })} /> {t("Hand over to")}</label>
              <label><input type="radio" checked={p.action === "shared"} onChange={() => upd(i, { action: "shared" })} /> {t("Convert to a shared mailbox")}</label>
              <label><input type="radio" checked={p.action === "keep"} onChange={() => upd(i, { action: "keep" })} /> {t("Keep, unassigned")}</label>
              <label><input type="radio" checked={p.action === "suspend"} onChange={() => upd(i, { action: "suspend" })} /> {t("Suspend (lock)")}</label>
            </div>
            {p.action === "handover" ? (
              <select value={p.newOwnerId} onChange={(e) => upd(i, { newOwnerId: Number((e.target as HTMLSelectElement).value) })}>
                <option value={0}>—</option>
                {others.map((m) => <option key={m.id} value={m.id}>{m.displayName}</option>)}
              </select>
            ) : null}
            {p.action === "shared" ? (
              <div>
                <div class="field-label">{t("Grant access to")}</div>
                <div class="chips">
                  {others.map((m) => (
                    <label key={m.id} class={"chip select" + (p.grantMemberIds.includes(m.id) ? " on" : "")}>
                      <input type="checkbox" hidden checked={p.grantMemberIds.includes(m.id)} onChange={(e) => upd(i, { grantMemberIds: (e.target as HTMLInputElement).checked ? [...p.grantMemberIds, m.id] : p.grantMemberIds.filter((x) => x !== m.id) })} />
                      {m.displayName}
                    </label>
                  ))}
                </div>
              </div>
            ) : null}
            <Field label={t("Forward new mail to")} hint={t("Comma-separated addresses")}>
              <input value={p.forwardTo} onInput={(e) => upd(i, { forwardTo: (e.target as HTMLInputElement).value })} placeholder={t("Optional")} />
            </Field>
          </section>
        );
      })}
      <Toggle checked={removeGroups} onChange={setRemoveGroups} label={t("Remove from groups")} />
      <p class="muted small">{t("All shared mailbox access is revoked during offboarding.")}</p>
    </Modal>
  );
}
