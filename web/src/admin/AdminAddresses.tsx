import { useEffect, useState } from "preact/hooks";
import { t, kindLabel } from "@/lib/i18n";
import { get, post, patch, del, type Address, type Domain, type Mailbox, type Group, type DirectoryEntry } from "@/lib/api";
import { toast, errorToast, can } from "@/lib/state";
import { Button, Field, Icon, useAsync, Spinner, ErrorBox, Modal, Badge, Confirm } from "@/ui";
import { PageHead } from "./AdminOrg";

export function AddressesPage() {
  const { data, error, loading, reload } = useAsync(async () => {
    const [addresses, domains, mailboxes] = await Promise.all([get<Address[]>("/api/admin/addresses"), get<Domain[]>("/api/admin/domains"), get<Mailbox[]>("/api/admin/mailboxes")]);
    return { addresses, domains: domains.filter((d) => d.status === "active" && !d.isShared), mailboxes };
  }, []);
  const [editing, setEditing] = useState<Address | null | "new">(null);
  const [deleting, setDeleting] = useState<Address | null>(null);
  const [q, setQ] = useState("");
  const manage = can("addresses.manage");
  const list = (data?.addresses ?? []).filter((a) => !q || `${a.address} ${a.targets.join(" ")} ${a.note}`.toLowerCase().includes(q.toLowerCase()));
  const mbName = (id: number | null) => data?.mailboxes.find((m) => m.id === id)?.address ?? "";
  return (
    <div class="page">
      <PageHead title={t("Addresses")}>
        <div class="search compact"><Icon name="search" size={15} /><input placeholder={t("Search")} value={q} onInput={(e) => setQ((e.target as HTMLInputElement).value)} /></div>
        {manage ? <Button kind="primary" icon="plus" size="sm" onClick={() => setEditing("new")}>{t("Add address")}</Button> : null}
      </PageHead>
      <div class="page-body">
        {loading ? <Spinner /> : error ? <ErrorBox error={error} onRetry={reload} /> : null}
        <table class="table">
          <thead><tr><th>{t("Addresses")}</th><th>{t("Kind")}</th><th>{t("Delivers to")}</th><th>{t("Note")}</th><th></th></tr></thead>
          <tbody>
            {list.map((a) => (
              <tr key={a.id}>
                <td class="mono">{a.address}</td>
                <td><Badge tone={a.kind === "primary" ? "good" : a.kind === "catchall" || a.kind === "prefix" ? "warn" : a.kind === "group" ? "accent" : "neutral"}>{kindLabel(a.kind)}</Badge></td>
                <td class="small">{a.kind === "primary" ? <span class="muted">{t("Mailbox")}</span> : a.kind === "alias" ? mbName(a.mailboxId) || a.targets.join(", ") : a.targets.join(", ")}</td>
                <td class="muted small">{a.note}</td>
                <td class="nowrap">
                  {manage && a.kind !== "primary" && a.kind !== "group" ? (
                    <>
                      <button class="btn btn-icon" onClick={() => setEditing(a)} aria-label={t("Edit")}><Icon name="draft" size={15} /></button>
                      <button class="btn btn-icon" onClick={() => setDeleting(a)} aria-label={t("Delete")}><Icon name="trash" size={15} /></button>
                    </>
                  ) : null}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {editing && data ? <AddressEditor address={editing === "new" ? null : editing} domains={data.domains} mailboxes={data.mailboxes.filter((m) => m.status === "active")} onClose={() => setEditing(null)} onSaved={reload} /> : null}
      {deleting ? <Confirm title={t("Delete")} text={t("Delete address {a}?", { a: deleting.address })} danger confirmLabel={t("Delete")} onClose={() => setDeleting(null)} onConfirm={async () => { await del(`/api/admin/addresses/${deleting.id}`); reload(); }} /> : null}
    </div>
  );
}

function AddressEditor({ address, domains, mailboxes, onClose, onSaved }: { address: Address | null; domains: Domain[]; mailboxes: Mailbox[]; onClose: () => void; onSaved: () => void }) {
  const [kind, setKind] = useState(address?.kind ?? "alias");
  const [domainId, setDomainId] = useState(address?.domainId ?? domains[0]?.id ?? 0);
  const [local, setLocal] = useState(address?.localPart ?? "");
  const [mailboxId, setMailboxId] = useState(address?.mailboxId ?? 0);
  const [targets, setTargets] = useState(address?.targets.join(", ") ?? "");
  const [note, setNote] = useState(address?.note ?? "");
  const [busy, setBusy] = useState(false);
  const dom = domains.find((d) => d.id === domainId)?.name ?? "";
  return (
    <Modal title={address ? t("Edit") : t("Add address")} onClose={onClose} footer={<><Button onClick={onClose}>{t("Cancel")}</Button><Button kind="primary" busy={busy} onClick={async () => { setBusy(true); try { const body = { domainId, localPart: local, kind, mailboxId, targets: targets.split(/[,\s;]+/).filter(Boolean), note }; if (address) await patch(`/api/admin/addresses/${address.id}`, body); else await post("/api/admin/addresses", body); toast(address ? t("Address updated.") : t("Address created."), "success"); onSaved(); onClose(); } catch (e) { errorToast(e); } finally { setBusy(false); } }}>{t("Save")}</Button></>}>
      {!address ? (
        <Field label={t("Kind")}>
          <select value={kind} onChange={(e) => setKind((e.target as HTMLSelectElement).value as Address["kind"])}>
            <option value="alias">{t("Alias")} — {t("Delivers to mailbox")}</option>
            <option value="forward">{t("Forward")} — {t("Targets")}</option>
            <option value="catchall">{t("Catch-all")} — {t("Any address at this domain that has no mailbox")}</option>
            <option value="prefix">{t("Prefix")} — {t("Everything starting with this prefix")}</option>
          </select>
        </Field>
      ) : null}
      <div class="row gap addr-row">
        {kind !== "catchall" ? <input placeholder={kind === "prefix" ? "invoice" : t("Local part")} value={local} disabled={!!address} onInput={(e) => setLocal((e.target as HTMLInputElement).value.toLowerCase())} /> : <span class="mono">*</span>}
        {kind === "prefix" ? <span class="mono">*</span> : null}
        <span>@</span>
        <select value={domainId} disabled={!!address} onChange={(e) => setDomainId(Number((e.target as HTMLSelectElement).value))}>
          {domains.map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
        </select>
      </div>
      {kind === "alias" ? (
        <Field label={t("Delivers to mailbox")}>
          <select value={mailboxId} onChange={(e) => setMailboxId(Number((e.target as HTMLSelectElement).value))}>
            <option value={0}>—</option>
            {mailboxes.map((m) => <option key={m.id} value={m.id}>{m.address}{m.displayName ? ` (${m.displayName})` : ""}</option>)}
          </select>
        </Field>
      ) : (
        <Field label={t("Targets")} hint={t("Comma-separated addresses")}>
          <input value={targets} onInput={(e) => setTargets((e.target as HTMLInputElement).value)} placeholder={`alice@${dom}, bob@${dom}`} />
        </Field>
      )}
      <Field label={t("Note")}><input value={note} onInput={(e) => setNote((e.target as HTMLInputElement).value)} /></Field>
    </Modal>
  );
}

export function GroupsPage() {
  const { data, error, loading, reload } = useAsync(async () => {
    const [groups, dir, domains] = await Promise.all([get<Group[]>("/api/admin/groups"), get<DirectoryEntry[]>("/api/admin/directory"), get<Domain[]>("/api/admin/domains")]);
    return { groups, dir, domains: domains.filter((d) => d.status === "active" && !d.isShared) };
  }, []);
  const [editing, setEditing] = useState<Group | null | "new">(null);
  const [deleting, setDeleting] = useState<Group | null>(null);
  const manage = can("groups.manage");
  const nameOf = (id: number) => data?.dir.find((m) => m.id === id)?.displayName ?? `#${id}`;
  return (
    <div class="page">
      <PageHead title={t("Groups")}>
        {manage ? <Button kind="primary" icon="plus" size="sm" onClick={() => setEditing("new")}>{t("Add group")}</Button> : null}
      </PageHead>
      <div class="page-body">
        {loading ? <Spinner /> : error ? <ErrorBox error={error} onRetry={reload} /> : null}
        {data && data.groups.length === 0 ? <p class="muted">{t("Nothing here yet.")}</p> : null}
        <div class="card-list">
          {(data?.groups ?? []).map((g) => (
            <div key={g.id} class="card row gap">
              <div class="grow">
                <div><b>{g.name}</b> <span class="muted small">{g.memberIds.length} {t("members")}</span></div>
                {g.description ? <div class="muted small">{g.description}</div> : null}
                {g.address ? <div class="small mono"><Icon name="at" size={12} /> {g.address.address}</div> : null}
                <div class="chips">{g.memberIds.map((id) => <span key={id} class="chip small">{nameOf(id)}</span>)}</div>
              </div>
              {manage ? (
                <>
                  <Button size="sm" onClick={() => setEditing(g)}>{t("Edit")}</Button>
                  <button class="btn btn-icon" onClick={() => setDeleting(g)} aria-label={t("Delete")}><Icon name="trash" size={15} /></button>
                </>
              ) : null}
            </div>
          ))}
        </div>
      </div>
      {editing && data ? <GroupEditor group={editing === "new" ? null : editing} dir={data.dir} domains={data.domains} onClose={() => setEditing(null)} onSaved={reload} /> : null}
      {deleting ? <Confirm title={t("Delete")} text={t("Delete group {g}?", { g: deleting.name })} danger confirmLabel={t("Delete")} onClose={() => setDeleting(null)} onConfirm={async () => { await del(`/api/admin/groups/${deleting.id}`); reload(); }} /> : null}
    </div>
  );
}

function GroupEditor({ group, dir, domains, onClose, onSaved }: { group: Group | null; dir: DirectoryEntry[]; domains: Domain[]; onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState(group?.name ?? "");
  const [desc, setDesc] = useState(group?.description ?? "");
  const [members, setMembers] = useState<Set<number>>(new Set(group?.memberIds ?? []));
  const [hasAddr, setHasAddr] = useState(!!group?.address);
  const [domainId, setDomainId] = useState(group?.address?.domainId ?? domains[0]?.id ?? 0);
  const [local, setLocal] = useState(group?.address?.localPart ?? "");
  const [busy, setBusy] = useState(false);
  return (
    <Modal title={group ? t("Edit") : t("Add group")} onClose={onClose} footer={<><Button onClick={onClose}>{t("Cancel")}</Button><Button kind="primary" busy={busy} onClick={async () => { setBusy(true); try { const body = { name, description: desc, memberIds: [...members], addressDomainId: hasAddr ? domainId : 0, addressLocal: hasAddr ? local : "", removeAddress: !hasAddr && !!group?.address }; if (group) await patch(`/api/admin/groups/${group.id}`, body); else await post("/api/admin/groups", body); toast(t("Group saved."), "success"); onSaved(); onClose(); } catch (e) { errorToast(e); } finally { setBusy(false); } }}>{t("Save")}</Button></>}>
      <Field label={t("Name")}><input value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} autoFocus /></Field>
      <Field label={t("Description")}><input value={desc} onInput={(e) => setDesc((e.target as HTMLInputElement).value)} /></Field>
      <div class="field-label">{t("Members")}</div>
      <div class="chips">
        {dir.map((m) => (
          <label key={m.id} class={"chip select" + (members.has(m.id) ? " on" : "")}>
            <input type="checkbox" hidden checked={members.has(m.id)} onChange={(e) => { const s = new Set(members); (e.target as HTMLInputElement).checked ? s.add(m.id) : s.delete(m.id); setMembers(s); }} />
            {m.displayName}
          </label>
        ))}
      </div>
      <fieldset class="fieldset">
        <legend>{t("Distribution address")}</legend>
        <p class="muted small">{t("Mail sent to this address reaches every member.")}</p>
        <label class="check-row"><input type="checkbox" checked={hasAddr} onChange={(e) => setHasAddr((e.target as HTMLInputElement).checked)} /> {t("Distribution address")}</label>
        {hasAddr ? (
          <div class="row gap addr-row">
            <input value={local} onInput={(e) => setLocal((e.target as HTMLInputElement).value.toLowerCase())} placeholder="team" />
            <span>@</span>
            <select value={domainId} onChange={(e) => setDomainId(Number((e.target as HTMLSelectElement).value))}>
              {domains.map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
            </select>
          </div>
        ) : null}
      </fieldset>
    </Modal>
  );
}

export { useEffect };
