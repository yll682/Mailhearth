import { useEffect, useState } from "preact/hooks";
import { t } from "@/lib/i18n";
import { get, post, patch, del, type Member, type Role, type Domain, type Mailbox, type Group, type AccessibleMailbox } from "@/lib/api";
import { toast, errorToast, can, me, refreshMailboxes } from "@/lib/state";
import { fmtDate, copyText } from "@/lib/format";
import { Button, Field, Icon, useAsync, Spinner, ErrorBox, Modal, StatusBadge, Avatar, Confirm, Menu, Toggle, Badge } from "@/ui";
import { PageHead } from "./AdminOrg";
import { go } from "@/lib/router";

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
    const [roles, domains, mailboxes, groups] = await Promise.all([get<Role[]>("/api/admin/roles"), get<Domain[]>("/api/admin/domains"), get<Mailbox[]>("/api/admin/mailboxes"), get<Group[]>("/api/admin/groups")]);
    return { roles, domains: domains.filter((d) => d.status === "active"), mailboxes, groups };
  }, []);
}

function AddMemberModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const ref = useRefData();
  const [f, setF] = useState({ displayName: "", loginEmail: "", roleId: 0, title: "", department: "" });
  const [mbMode, setMbMode] = useState<"new" | "bind" | "none">("new");
  const [domainId, setDomainId] = useState(0);
  const [local, setLocal] = useState("");
  const [bindId, setBindId] = useState(0);
  const [shared, setShared] = useState<Set<number>>(new Set());
  const [groups, setGroups] = useState<Set<number>>(new Set());
  const [auth, setAuth] = useState<"invite" | "password">("invite");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ inviteLink?: string; warnings?: string[]; member: Member } | null>(null);
  useEffect(() => {
    if (ref.data) {
      if (!domainId && ref.data.domains[0]) setDomainId(ref.data.domains[0].id);
      if (!f.roleId) setF((x) => ({ ...x, roleId: ref.data!.roles.find((r) => r.key === "member")?.id ?? 0 }));
    }
  }, [ref.data]);
  const unassigned = (ref.data?.mailboxes ?? []).filter((m) => m.kind === "personal" && !m.ownerMemberId && m.status === "active");
  const sharedBoxes = (ref.data?.mailboxes ?? []).filter((m) => m.kind === "shared" && m.status === "active");
  const canMailbox = can("mailboxes.manage");
  if (result) {
    return (
      <Modal title={t("Member created.")} onClose={() => { onDone(); onClose(); }} footer={<Button kind="primary" onClick={() => { onDone(); onClose(); go(`/admin/members/${result.member.id}`); }}>{t("Done")}</Button>}>
        {result.inviteLink ? <InviteLinkBox link={result.inviteLink} name={result.member.displayName} /> : null}
        {result.warnings?.length ? (
          <div class="notice warn">
            <ul>{result.warnings.map((w, i) => <li key={i}>{w}</li>)}</ul>
          </div>
        ) : null}
      </Modal>
    );
  }
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
            onClick={async () => {
              setBusy(true);
              try {
                const body: Record<string, unknown> = { ...f, sendInvite: auth === "invite", password: auth === "password" ? password : "", groupIds: [...groups], sharedMailboxes: [...shared] };
                if (mbMode === "new" && canMailbox) body.newMailbox = { domainId, localPart: local };
                if (mbMode === "bind" && canMailbox) body.bindMailboxId = bindId;
                setResult(await post("/api/admin/members", body));
                refreshMailboxes();
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
            <label><input type="radio" checked={mbMode === "new"} onChange={() => setMbMode("new")} /> {t("Create a new mailbox")}</label>
            <label><input type="radio" checked={mbMode === "bind"} onChange={() => setMbMode("bind")} disabled={!unassigned.length} /> {t("Use an existing mailbox")}</label>
            <label><input type="radio" checked={mbMode === "none"} onChange={() => setMbMode("none")} /> {t("No mailbox for now")}</label>
          </div>
          {mbMode === "new" ? (
            <div class="row gap addr-row">
              <input placeholder={t("Mailbox name")} value={local} onInput={(e) => setLocal((e.target as HTMLInputElement).value.toLowerCase())} />
              <span>@</span>
              <select value={domainId} onChange={(e) => setDomainId(Number((e.target as HTMLSelectElement).value))}>
                {(ref.data?.domains ?? []).map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
              </select>
            </div>
          ) : null}
          {mbMode === "bind" ? (
            <select value={bindId} onChange={(e) => setBindId(Number((e.target as HTMLSelectElement).value))}>
              <option value={0}>—</option>
              {unassigned.map((m) => <option key={m.id} value={m.id}>{m.address}</option>)}
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
              ? { label: t("Enable"), icon: "check", onClick: () => act(() => post(`/api/admin/members/${m.id}/status`, { enabled: true })) }
              : { label: t("Disable"), icon: "lock", onClick: () => act(() => post(`/api/admin/members/${m.id}/status`, { enabled: false })), disabled: isSelf || m.roleKey === "owner" || m.status === "departed" },
            { label: t("Offboard"), icon: "logout", onClick: () => setOffboard(true), danger: true, disabled: isSelf || m.roleKey === "owner" || m.status === "departed" },
            { label: t("Delete member"), icon: "trash", onClick: () => setConfirmDelete(true), danger: true, disabled: isSelf || m.roleKey === "owner" || data.mailboxes.some((b) => b.ownerMemberId === m.id) },
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
                  {!b.hasCredential ? <Badge tone="warn">{t("Not connected")}</Badge> : null}
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
      {confirmDelete ? <Confirm title={t("Delete member")} text={m.displayName} danger onClose={() => setConfirmDelete(false)} onConfirm={async () => { await del(`/api/admin/members/${m.id}`); go("/admin/members"); }} /> : null}
    </div>
  );
}

function EditMemberModal({ member, roles, onClose, onSaved }: { member: Member; roles: Role[]; onClose: () => void; onSaved: () => void }) {
  const [f, setF] = useState({ displayName: member.displayName, loginEmail: member.loginEmail, roleId: member.roleId, title: member.title, department: member.department });
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

interface Plan { mailboxId: number; action: "handover" | "shared" | "keep" | "suspend"; newOwnerId: number; grantMemberIds: number[]; forwardTo: string }

function OffboardModal({ member, mailboxes, onClose, onDone }: { member: Member; mailboxes: AccessibleMailbox[]; onClose: () => void; onDone: () => void }) {
  const members = useAsync(() => get<Member[]>("/api/admin/members"), []);
  const others = (members.data ?? []).filter((m) => m.id !== member.id && m.status === "active");
  const [plans, setPlans] = useState<Plan[]>(mailboxes.map((b) => ({ mailboxId: b.id, action: "handover", newOwnerId: 0, grantMemberIds: [], forwardTo: "" })));
  const [removeGroups, setRemoveGroups] = useState(true);
  const [revokeShared, setRevokeShared] = useState(true);
  const [busy, setBusy] = useState(false);
  const upd = (i: number, p: Partial<Plan>) => setPlans(plans.map((x, j) => (j === i ? { ...x, ...p } : x)));
  return (
    <Modal title={t("Offboard {name}", { name: member.displayName })} wide onClose={onClose} footer={<><Button onClick={onClose}>{t("Cancel")}</Button><Button kind="danger" busy={busy} onClick={async () => { setBusy(true); try { const r = await post<{ warnings: string[] }>(`/api/admin/members/${member.id}/offboard`, { plans: plans.map((p) => ({ ...p, forwardTo: p.forwardTo.split(/[,\s;]+/).filter(Boolean) })), removeFromGroups: removeGroups, revokeShared }); toast(t("Offboarding complete."), "success"); r.warnings?.forEach((w) => toast(w, "error", undefined, 9000)); refreshMailboxes(); onDone(); onClose(); } catch (e) { errorToast(e); } finally { setBusy(false); } }}>{t("Complete offboarding")}</Button></>}>
      <p class="muted">{t("Decide what happens to each mailbox. Access for {name} ends immediately and all credentials are rotated.", { name: member.displayName })}</p>
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
      <Toggle checked={revokeShared} onChange={setRevokeShared} label={t("Revoke shared mailbox access")} />
    </Modal>
  );
}
