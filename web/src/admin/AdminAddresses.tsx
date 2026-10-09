import { useEffect, useState } from "preact/hooks";
import { t, kindLabel } from "@/lib/i18n";
import { statusLabel } from "@/lib/resourceLabels";
import { get, post, patch, del, waitOperation, type Operation, type ResourceOptions, type Address, type Mailbox, type Group, type DirectoryEntry } from "@/lib/api";
import { toast, errorToast, can } from "@/lib/state";
import { Button, Field, Icon, useAsync, Spinner, ErrorBox, Modal, Badge, Confirm } from "@/ui";
import { PageHead } from "./AdminOrg";
import { useOperationRequests } from "@/lib/useOperationRequests";

export function AddressesPage() {
  const requests = useOperationRequests();
  const { data, error, loading, reload } = useAsync(async () => {
    const [addresses, options, mailboxes] = await Promise.all([get<Address[]>("/api/admin/addresses"), get<ResourceOptions>("/api/admin/resource-options"), get<Mailbox[]>("/api/admin/mailboxes")]);
    return { addresses, options, mailboxes };
  }, []);
  const [editing, setEditing] = useState<Address | null | "new">(null);
  const [deleting, setDeleting] = useState<Address | null>(null);
  const [q, setQ] = useState("");
  const manage = can("addresses.manage");
  const list = (data?.addresses ?? []).filter((a) => !q || `${a.address} ${a.targets.join(" ")} ${a.note}`.toLowerCase().includes(q.toLowerCase()));
  const mailboxOf = (a: Address) => data?.mailboxes.find((m) => m.id === a.mailboxId && m.connectionId === a.connectionId);
  const deliversTo = (a: Address) => {
    const mb = mailboxOf(a);
    if (a.kind === "primary") return <span class="muted">{mb?.displayName || "—"}</span>;
    if (a.kind === "alias") return mb?.address || a.targets.join(", ");
    return a.targets.join(", ");
  };
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
                <td class="mono">{a.address}<div class="muted small">{data?.options.connections.find((connection) => connection.id === a.connectionId)?.label} · #{a.connectionId} · {statusLabel(a.syncState)}</div></td>
                <td><Badge tone={a.kind === "primary" ? "good" : a.kind === "catchall" || a.kind === "prefix" ? "warn" : a.kind === "group" ? "accent" : "neutral"}>{kindLabel(a.kind)}</Badge></td>
                <td class="small">{deliversTo(a)}</td>
                <td class="muted small">
                  {a.note}
                  {a.syncState !== "synced" ? <div>{t("Desired targets")}: {a.desiredTargets.join(", ")}<br />{t("Observed targets")}: {a.observedTargets.join(", ") || t("Unverified")}</div> : null}
                </td>
                <td class="nowrap">
                  {manage && a.kind !== "primary" && a.kind !== "group" && a.kind !== "external_rule" ? (
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
      {editing && data ? <AddressEditor address={editing === "new" ? null : editing} options={data.options} mailboxes={data.mailboxes.filter((m) => m.status === "active")} onClose={() => setEditing(null)} onSaved={reload} /> : null}
      {deleting ? <Confirm title={t("Delete")} text={`${t("Delete address {a}?", { a: deleting.address })} · #${deleting.connectionId}`} danger confirmLabel={t("Delete")} onClose={() => setDeleting(null)} onConfirm={async () => { await waitOperation(await del<Operation>(`/api/admin/addresses/${deleting.id}`, requests.prepare(`delete/${deleting.id}`, { expectedRevision: deleting.revision }))); reload(); }} /> : null}
    </div>
  );
}

// 显示各类地址的用途说明。
const kindHelp: Record<string, string> = {
  alias: "Another address for one mailbox.",
  forward: "Sends mail on to one or more addresses, inside or outside the organisation.",
  catchall: "Receives anything at this domain that has no mailbox of its own.",
  prefix: "Receives every address starting with this prefix.",
};

function AddressEditor({ address, options, mailboxes, onClose, onSaved }: { address: Address | null; options: ResourceOptions; mailboxes: Mailbox[]; onClose: () => void; onSaved: () => void }) {
  const requests = useOperationRequests();
  const [kind, setKind] = useState(address?.kind ?? "alias");
  const [bindingId, setBindingId] = useState(address?.domainBindingId ?? 0);
  const [externalConnectionId, setExternalConnectionId] = useState(address?.connectionId ?? 0);
  const [domainId, setDomainId] = useState(address?.domainId ?? 0);
  const [mode, setMode] = useState<"api" | "external">(address?.managementMode ?? "external");
  const [local, setLocal] = useState(address?.localPart ?? "");
  const [mailboxId, setMailboxId] = useState(address?.mailboxId ?? 0);
  const [targets, setTargets] = useState(address?.targets.join(", ") ?? "");
  const [note, setNote] = useState(address?.note ?? "");
  const [busy, setBusy] = useState(false);
  const binding = options.bindings.find((item) => item.id === bindingId);
  const dom = mode === "api" ? binding?.domainName ?? "" : options.domains.find((item) => item.id === domainId)?.name ?? address?.domain ?? "";
  const connectionId = address?.connectionId ?? (mode === "api" ? binding?.connectionId ?? 0 : externalConnectionId);
  return (
    <Modal title={address ? t("Edit") : t("Add address")} onClose={onClose} footer={<><Button onClick={onClose}>{t("Cancel")}</Button><Button kind="primary" busy={busy} disabled={!connectionId || (!address && (mode === "api" ? !binding : !domainId))} onClick={async () => { setBusy(true); try { const body = requests.prepare("save", { expectedRevision: address?.revision, connectionId, domainId, domainBindingId: address?.domainBindingId ?? (mode === "api" ? bindingId : 0), localPart: local, kind, mode, mailboxId: kind === "alias" ? mailboxId : 0, targets: kind === "alias" ? [] : targets.split(/[,\s;]+/).filter(Boolean), note }); await waitOperation(address ? await patch<Operation>(`/api/admin/addresses/${address.id}`, body) : await post<Operation>("/api/admin/addresses", body)); toast(address ? t("Address updated.") : t("Address created."), "success"); onSaved(); onClose(); } catch (e) { errorToast(e); } finally { setBusy(false); } }}>{t("Save")}</Button></>}>
      {!address ? <Field label={t("Management mode")}><select value={mode} onChange={(event) => { setMode(event.currentTarget.value as "api" | "external"); setMailboxId(0); }}><option value="api">{t("Manage remote rule")}</option><option value="external">{t("Register external configuration")}</option></select></Field> : null}
      {mode === "external" ? <Field label={t("Mail connection")}><select value={externalConnectionId} disabled={!!address} onChange={(event) => { setExternalConnectionId(Number(event.currentTarget.value)); setMailboxId(0); }}><option value={0}>—</option>{options.connections.filter((item) => item.enabled).map((item) => <option key={item.id} value={item.id}>{item.label} · #{item.id}</option>)}</select></Field> : null}
      {mode === "external" ? <p class="muted small">{t("External registration does not verify server delivery or sender authorization.")}</p> : null}
      {!address ? (
        <Field label={t("Kind")} info={t(kindHelp[kind] ?? "")}>
          <select value={kind} onChange={(e) => setKind((e.target as HTMLSelectElement).value as Address["kind"])}>
            <option value="alias">{t("Alias")}</option>
            <option value="forward">{t("Forward")}</option>
            <option value="catchall">{t("Catch-all")}</option>
            <option value="prefix">{t("Prefix")}</option>
          </select>
        </Field>
      ) : null}
      <div class="row gap addr-row">
        {kind !== "catchall" ? <input placeholder={kind === "prefix" ? "invoice" : t("Local part")} value={local} disabled={!!address} onInput={(e) => setLocal((e.target as HTMLInputElement).value.toLowerCase())} /> : <span class="mono">*</span>}
        {kind === "prefix" ? <span class="mono">*</span> : null}
        <span>@</span>
        <select value={mode === "api" ? bindingId : domainId} disabled={!!address} onChange={(e) => { const id = Number(e.currentTarget.value); if (mode === "api") setBindingId(id); else setDomainId(id); setMailboxId(0); }}>
          <option value={0}>—</option>
          {mode === "api" ? options.bindings.filter((item) => item.managementMode === "api" && item.remoteState === "present").map((item) => <option key={item.id} value={item.id}>{item.domainName} · {item.connectionLabel} · #{item.connectionId}</option>) : options.domains.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
        </select>
      </div>
      {kind === "alias" ? (
        <Field label={t("Delivers to mailbox")}>
          <select value={mailboxId} onChange={(e) => setMailboxId(Number((e.target as HTMLSelectElement).value))}>
            <option value={0}>—</option>
            {mailboxes.filter((mailbox) => mailbox.connectionId === connectionId).map((m) => <option key={m.id} value={m.id}>{m.address} · #{m.id}{m.displayName ? ` (${m.displayName})` : ""}</option>)}
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
  const requests = useOperationRequests();
  const { data, error, loading, reload } = useAsync(async () => {
    const [groups, dir, options] = await Promise.all([get<Group[]>("/api/admin/groups"), get<DirectoryEntry[]>("/api/admin/directory"), get<ResourceOptions>("/api/admin/resource-options")]);
    return { groups, dir, options };
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
                {g.address ? <div class="small mono"><Icon name="at" size={12} /> {g.address.address} · #{g.address.connectionId} · {statusLabel(g.address.syncState)}{g.address.targets.length === 0 ? <p>{t("No distribution targets")}</p> : null}{g.address.syncState !== "synced" ? <p>{t("Desired targets")}: {g.address.desiredTargets.join(", ")}<br />{t("Observed targets")}: {g.address.observedTargets.join(", ") || t("Unverified")}</p> : null}</div> : null}
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
      {editing && data ? <GroupEditor group={editing === "new" ? null : editing} dir={data.dir} options={data.options} onClose={() => setEditing(null)} onSaved={reload} /> : null}
      {deleting ? <Confirm title={t("Delete")} text={t("Delete group {g}?", { g: deleting.name })} danger confirmLabel={t("Delete")} onClose={() => setDeleting(null)} onConfirm={async () => { await waitOperation(await del<Operation>(`/api/admin/groups/${deleting.id}`, requests.prepare(`delete/${deleting.id}`, { expectedRevision: deleting.revision }))); reload(); }} /> : null}
    </div>
  );
}

function GroupEditor({ group, dir, options, onClose, onSaved }: { group: Group | null; dir: DirectoryEntry[]; options: ResourceOptions; onClose: () => void; onSaved: () => void }) {
  const requests = useOperationRequests();
  const [name, setName] = useState(group?.name ?? "");
  const [desc, setDesc] = useState(group?.description ?? "");
  const [members, setMembers] = useState<Set<number>>(new Set(group?.memberIds ?? []));
  const [hasAddr, setHasAddr] = useState(!!group?.address);
  const [bindingId, setBindingId] = useState(group?.address?.domainBindingId ?? 0);
  const [connectionId, setConnectionId] = useState(group?.address?.connectionId ?? 0);
  const [domainId, setDomainId] = useState(group?.address?.domainId ?? 0);
  const [mode, setMode] = useState<"api" | "external">(group?.address?.managementMode ?? "external");
  const [local, setLocal] = useState(group?.address?.localPart ?? "");
  const [busy, setBusy] = useState(false);
  return (
    <Modal title={group ? t("Edit") : t("Add group")} onClose={onClose} footer={<><Button onClick={onClose}>{t("Cancel")}</Button><Button kind="primary" busy={busy} disabled={hasAddr && (mode === "api" ? !bindingId : !connectionId || !domainId)} onClick={async () => { setBusy(true); try { const binding = options.bindings.find((item) => item.id === bindingId); const body = requests.prepare("save", { expectedRevision: group?.revision, name, description: desc, memberIds: [...members].sort((a, b) => a - b), connectionId: hasAddr ? (mode === "api" ? binding?.connectionId : connectionId) : 0, domainBindingId: hasAddr && mode === "api" ? bindingId : 0, addressDomainId: hasAddr && mode === "external" ? domainId : 0, addressLocal: hasAddr ? local : "", mode, removeAddress: !hasAddr && !!group?.address }); await waitOperation(group ? await patch<Operation>(`/api/admin/groups/${group.id}`, body) : await post<Operation>("/api/admin/groups", body)); toast(t("Group saved."), "success"); onSaved(); onClose(); } catch (e) { errorToast(e); } finally { setBusy(false); } }}>{t("Save")}</Button></>}>
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
        <p class="muted small">{t("Mail to this address reaches every member.")}</p>
        <label class="check-row"><input type="checkbox" checked={hasAddr} onChange={(e) => setHasAddr((e.target as HTMLInputElement).checked)} /> {t("Distribution address")}</label>
        {hasAddr ? (
          <>
          <Field label={t("Management mode")}><select value={mode} disabled={!!group?.address} onChange={(event) => setMode(event.currentTarget.value as "api" | "external")}><option value="api">{t("Manage remote rule")}</option><option value="external">{t("Register external configuration")}</option></select></Field>
          {mode === "external" ? <><p class="muted small">{t("External registration does not verify server delivery or sender authorization.")}</p><Field label={t("Mail connection")}><select value={connectionId} onChange={(event) => setConnectionId(Number(event.currentTarget.value))}><option value={0}>—</option>{options.connections.filter((item) => item.enabled).map((item) => <option key={item.id} value={item.id}>{item.label} · #{item.id}</option>)}</select></Field></> : null}
          <div class="row gap addr-row">
            <input value={local} onInput={(e) => setLocal((e.target as HTMLInputElement).value.toLowerCase())} placeholder="team" />
            <span>@</span>
            <select value={mode === "api" ? bindingId : domainId} onChange={(e) => { const id = Number(e.currentTarget.value); if (mode === "api") setBindingId(id); else setDomainId(id); }}>
              <option value={0}>—</option>
              {mode === "api" ? options.bindings.filter((item) => item.managementMode === "api" && item.remoteState === "present").map((item) => <option key={item.id} value={item.id}>{item.domainName} · {item.connectionLabel} · #{item.connectionId}</option>) : options.domains.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
            </select>
          </div>
          </>
        ) : null}
      </fieldset>
    </Modal>
  );
}

export { useEffect };
