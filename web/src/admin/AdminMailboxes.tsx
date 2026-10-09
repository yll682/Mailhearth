import { useEffect, useState } from "preact/hooks";
import { t, kindLabel } from "@/lib/i18n";
import { get, post, put, patch, del, waitOperation, capabilityAvailable, type Capability, type Operation, type Mailbox, type MailboxForwarding, type ResourceOptions, type MailboxAccess, type Identity, type Address, type DirectoryEntry } from "@/lib/api";
import { toast, errorToast, can, refreshMailboxes } from "@/lib/state";
import { fmtDate, copyText } from "@/lib/format";
import { Button, Field, Icon, useAsync, Spinner, ErrorBox, Modal, StatusBadge, Badge, Confirm, Menu, Tabs, Info } from "@/ui";
import { PageHead } from "./AdminOrg";
import { IdentityEditor } from "@/settings/Settings";
import { go } from "@/lib/router";
import { EndpointSettings } from "./EndpointEditor";
import { MailboxAttachForm } from "./AdminConnections";
import { useOperationRequests } from "@/lib/useOperationRequests";
import { statusLabel } from "@/lib/resourceLabels";
import { errorLabel } from "@/lib/errorLabels";

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
                <td><div>{m.displayName || m.address}</div><div class="muted small mono">{m.address}</div><div class="muted small">{m.connectionLabel} · ID {m.connectionId}</div></td>
                <td>{m.kind === "shared" ? <Badge tone="accent">{t("Shared")}</Badge> : <Badge>{t("Personal")}</Badge>}</td>
                <td>{m.ownerName || (m.kind === "personal" ? <span class="warn-text">—</span> : "")}</td>
                <td class="muted">{m.accessCount || "—"}</td>
                <td><Badge tone={m.protocols.imap?.readiness === "ready" ? "good" : "warn"}>IMAP · {statusLabel(m.protocols.imap?.readiness ?? "unconfigured")}</Badge></td>
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
  const connections = useAsync(() => get<ResourceOptions>("/api/admin/resource-options"), []);
  const dir = useAsync(() => get<DirectoryEntry[]>("/api/admin/directory"), []);
  const [connectionId, setConnectionId] = useState(0);
  const [owner, setOwner] = useState(0);
  const [mode, setMode] = useState<"attach" | "create">("attach");
  const connection = connections.data?.connections.find((item) => item.id === connectionId);
  return (
    <Modal title={kind === "shared" ? t("Shared mailbox") : t("Personal mailbox")} onClose={onClose}>
      {connections.loading && <Spinner />}
      {connections.error && <ErrorBox error={connections.error} onRetry={connections.reload} />}
      <Field label={t("Mailbox action")}><select value={mode} onChange={(event) => setMode(event.currentTarget.value as "attach" | "create")}><option value="attach">{t("Register existing mailbox")}</option><option value="create" disabled={!connection || connection.providerKind === "manual"}>{t("Create remote mailbox")}</option></select></Field>
      <Field label={t("Mail connection")}><select value={connectionId} onChange={(event) => { setConnectionId(Number(event.currentTarget.value)); setMode("attach"); }}>
        <option value={0}>—</option>{(connections.data?.connections ?? []).filter((item) => item.enabled).map((item) => <option key={item.id} value={item.id}>{item.label} · {item.providerKind} · ID {item.id}</option>)}
      </select></Field>
      {kind === "personal" ? (
        <Field label={t("Owner")}>
          <select value={owner} onChange={(e) => setOwner(Number((e.target as HTMLSelectElement).value))}>
            <option value={0}>—</option>
            {(dir.data ?? []).map((m) => <option key={m.id} value={m.id}>{m.displayName}</option>)}
          </select>
        </Field>
      ) : null}
      {connection && <MailboxAttachForm key={`${connection.id}/${mode}`} connection={connection} mode={mode} bindings={connections.data?.bindings} initialKind={kind} ownerMemberId={owner || null} onSaved={() => { refreshMailboxes(); onDone(); onClose(); }} />}
    </Modal>
  );
}

export function MailboxDetail({ id }: { id: number }) {
  const requests = useOperationRequests();
  const { data, error, loading, reload } = useAsync(() => get<{ mailbox: Mailbox; access: MailboxAccess[]; identities: Identity[]; addresses: Address[]; forwarding: MailboxForwarding | null }>(`/api/admin/mailboxes/${id}`), [id]);
  const dir = useAsync(() => get<DirectoryEntry[]>("/api/admin/directory"), []);
  const capabilities = useAsync(() => get<Record<string, Capability>>(`/api/admin/mailboxes/${id}/capabilities`), [id, data?.mailbox.revision]);
  const [pw, setPw] = useState<null | { password: string }>(null);
  const [confirm, setConfirm] = useState<null | "suspend" | "delete" | "rotate" | "remote" | "revoke">(null);
  const [grant, setGrant] = useState({ memberId: 0, level: "full" });
  const [fwd, setFwd] = useState("");
  const [deliveryMode, setDeliveryMode] = useState<"redirect" | "copy">("redirect");
  const [editIdentity, setEditIdentity] = useState<Identity | null>(null);
  const [edit, setEdit] = useState(false);
  if (loading) return <Spinner />;
  if (error || !data) return <ErrorBox error={error} onRetry={reload} />;
  const mb = data.mailbox;
  const perm = mb.kind === "shared" ? "shared.manage" : "mailboxes.manage";
  const manage = can(perm);
  const base = `/api/admin/mailboxes/${mb.id}`;
  const remoteAction = async (action: string) => waitOperation(await post<Operation>(`${base}/${action}`, requests.prepare(`${base}/${action}`, { expectedRevision: mb.revision, ...(action === "connect" ? { credentialMode: "managed" } : {}), ...(action === "delete-remote" ? { confirmAddress: mb.address } : {}) })));
  const act = async (fn: () => Promise<unknown>, msg?: string) => {
    try {
      await fn();
      if (msg) toast(msg, "success");
      reload();
      capabilities.reload();
      refreshMailboxes();
    } catch (e) {
      errorToast(e);
    }
  };
  const forwarding = data.forwarding?.targets ?? [];
  const forwardingCapability = capabilities.data?.["mailbox.forwarding"];
  const forwardingAvailable = capabilityAvailable(forwardingCapability) || (forwardingCapability?.support === "external" && forwardingCapability.permissionAllowed && forwardingCapability.readiness !== "disabled");
  const forwardingAction = async (remove: boolean) => {
    const operation = remove ? await del<Operation>(`${base}/forwarding`, requests.prepare(`${base}/forwarding/delete`, { expectedRevision: data.forwarding?.revision })) : await put<Operation>(`${base}/forwarding`, requests.prepare(`${base}/forwarding/put`, { expectedRevision: data.forwarding?.revision ?? mb.revision, targets: fwd.split(/[,\s;]+/).filter(Boolean), deliveryMode }));
    if (forwardingCapability?.support === "external") { go(`/admin/operations/${operation.operationId}`); return; }
    await waitOperation(operation);
  };
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
              { label: t("Rotate credential"), icon: "refresh", onClick: () => setConfirm("rotate"), disabled: !capabilityAvailable(capabilities.data?.["credential.rotate"]) },
              { label: t("Reset mailbox password"), icon: "key", onClick: () => act(async () => { const op = await remoteAction("reset-password"); setPw(await post<{ password: string }>(`/api/admin/operations/${op.operationId}/claim-secret`)); }), disabled: !capabilityAvailable(capabilities.data?.["mailbox.resetPassword"]) },
              mb.status === "suspended"
                ? { label: t("Reactivate"), icon: "check", onClick: () => act(async () => remoteAction("reactivate")) }
                : { label: t("Suspend mailbox"), icon: "lock", onClick: () => setConfirm("suspend"), disabled: mb.status !== "active" },
              { label: t("Archive mailbox"), icon: "archive", onClick: () => act(async () => { const operation = await post<Operation>(`${base}/archive`, requests.prepare(`${base}/archive`, { expectedRevision: mb.revision })); go(`/admin/operations/${operation.operationId}`); }), disabled: mb.status === "archived" },
              { label: t("Unregister mailbox"), icon: "trash", danger: true, onClick: () => setConfirm("delete") },
              { label: t("Delete remote mailbox"), icon: "trash", danger: true, onClick: () => setConfirm("remote"), disabled: !capabilityAvailable(capabilities.data?.["mailbox.delete"]) },
              { label: t("Revoke all remote access"), icon: "lock", danger: true, onClick: () => setConfirm("revoke"), disabled: !capabilities.data?.["remoteAccess.revokeAll"].permissionAllowed },
            ]}
          />
        ) : null}
      </PageHead>
      <div class="page-body">
        {capabilities.error ? <ErrorBox error={capabilities.error} onRetry={capabilities.reload} /> : null}
        {capabilities.data ? <section class="card"><dl class="kv">{["credential.rotate", "mailbox.resetPassword", "mailbox.delete"].map((key) => { const cap = capabilities.data![key]; return <><dt>{key}</dt><dd>{statusLabel(cap.support)} · {statusLabel(cap.readiness)}{cap.reasonCode ? ` · ${cap.reasonCode}` : ""}</dd></>; })}</dl></section> : null}
        <div class="grid2">
          <section class="card">
            <dl class="kv">
              <dt>{t("Mailbox")}</dt><dd class="mono">{mb.address}</dd>
              <dt>{t("Mail connection")}</dt><dd>{mb.connectionLabel} · ID {mb.connectionId}</dd>
              <dt>{t("Owner")}</dt><dd>{mb.ownerMemberId ? <a href={`/admin/members/${mb.ownerMemberId}`}>{mb.ownerName}</a> : <span class="muted">—</span>}</dd>
              <dt>{t("Protocol status")}</dt><dd>{["imap", "smtp", "managesieve"].map((name) => <div key={name}>{name.toUpperCase()} · {statusLabel(mb.protocols[name as keyof typeof mb.protocols]?.readiness ?? "unconfigured")}</div>)}</dd>
              <dt>{t("Forwarding")}</dt><dd>{forwarding.length ? forwarding.join(", ") : <span class="muted">—</span>}</dd>
            </dl>
            {manage && <EndpointSettings mailboxId={mb.id} onSaved={reload} />}
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
                  <Badge>{i.authorizationStatus}</Badge>
                  {manage ? <Button size="sm" onClick={() => act(() => post(`${base}/identities/${i.id}/authorization`, { expectedRevision: i.revision, allowed: i.authorizationStatus !== "allowed" }))}>{i.authorizationStatus === "allowed" ? t("Revoke sending authorization") : t("Authorize sending")}</Button> : null}
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

          {manage && can("addresses.manage") ? (
            <section class="card">
              <h3>{t("Forwarding")}</h3>
              {forwardingCapability?.support === "external" ? <p class="muted small">{t("External registration does not verify server delivery or sender authorization.")}</p> : null}
              {data.forwarding ? <><p class="muted small">{statusLabel(data.forwarding.deliveryMode)} · {statusLabel(data.forwarding.syncState)}</p><p>{t("Observed targets")}: {data.forwarding.remoteStatus.observedTargets?.join(", ") ?? "—"}</p>{Object.entries(data.forwarding.remoteStatus.statusByTarget ?? {}).map(([target, status]) => <p key={target}>{target} · {statusLabel(status)}</p>)}{data.forwarding.deliveryMode === "unverified" ? <p class="muted small">{t("Delivery mode has not been verified by a delivery test.")}</p> : null}</> : null}
              {!forwardingAvailable ? <p class="muted small">{forwardingCapability?.reasonCode ? errorLabel(forwardingCapability.reasonCode) : t("Unverified")}</p> : null}
              <Field label={t("Delivery mode")}><select value={deliveryMode} onChange={(event) => setDeliveryMode(event.currentTarget.value as "redirect" | "copy")}><option value="redirect">{t("Redirect")}</option><option value="copy" disabled={forwardingCapability?.support !== "external" && !(forwardingCapability?.constraints?.allowedDeliveryModes as string[] | undefined)?.includes("copy")}>{t("Keep a copy and forward")}</option></select></Field>
              <Field label={t("Forward incoming mail to")} hint={t("Comma-separated addresses")}>
                <input value={fwd} placeholder={forwarding.join(", ")} onInput={(e) => setFwd((e.target as HTMLInputElement).value)} />
              </Field>
              <div class="row gap">
                <Button size="sm" kind="primary" disabled={!fwd.trim() || !forwardingAvailable} onClick={() => act(() => forwardingAction(false))}>{t("Save")}</Button>
                {data.forwarding ? <Button size="sm" disabled={!forwardingAvailable} onClick={() => act(() => forwardingAction(true))}>{t("Stop forwarding")}</Button> : null}
              </div>
            </section>
          ) : null}
        </div>
      </div>

      {edit ? <EditMailboxModal mailbox={mb} dir={dir.data ?? []} onClose={() => setEdit(false)} onSaved={reload} /> : null}
      {editIdentity ? <IdentityEditor mailbox={mb} identity={editIdentity} admin onClose={() => { setEditIdentity(null); reload(); }} /> : null}
      {pw ? (
        <Modal title={t("Password for external clients")} onClose={() => setPw(null)} footer={<Button kind="primary" onClick={() => setPw(null)}>{t("Done")}</Button>}>
          <p class="muted">{t("For use in other mail apps. Shown once.")}</p>
          <dl class="kv">
            <dt>{t("Mailbox")}</dt><dd class="mono">{mb.address} · {mb.connectionLabel}</dd>
            <dt>{t("Password")}</dt><dd class="mono row gap">{pw.password} <button class="btn btn-icon" onClick={() => { copyText(pw.password); toast(t("Copied"), "success"); }}><Icon name="copy" size={14} /></button></dd>
          </dl>
        </Modal>
      ) : null}
      {confirm === "rotate" ? <Confirm title={t("Rotate credential")} text={`${mb.address} · ${mb.connectionLabel}`} onClose={() => setConfirm(null)} onConfirm={() => act(() => remoteAction("rotate"))} /> : null}
      {confirm === "remote" ? <Confirm title={t("Delete remote mailbox")} text={`${mb.address} · ${mb.connectionLabel}. ${t("The remote mailbox and its messages will be deleted.")}`} requireText={mb.address} danger onClose={() => setConfirm(null)} onConfirm={() => act(() => remoteAction("delete-remote"))} /> : null}
      {confirm === "revoke" ? <Confirm title={t("Revoke all remote access")} text={`${mb.address} · ${mb.connectionLabel}. ${t("Remote access methods must be reviewed individually. Completion is recorded as an administrator report.")}`} danger onClose={() => setConfirm(null)} onConfirm={() => act(async () => { const operation = await post<Operation>(`${base}/revoke-remote-access`, requests.prepare(`${base}/revoke-remote-access`, { expectedRevision: mb.revision })); go(`/admin/operations/${operation.operationId}`); })} /> : null}
      {confirm === "suspend" ? <Confirm title={t("Suspend mailbox")} text={t("Everyone loses access. Mail keeps arriving and is kept.")} danger onClose={() => setConfirm(null)} onConfirm={() => act(async () => { const operation = await post<Operation>(`${base}/suspend`, requests.prepare(`${base}/suspend`, { expectedRevision: mb.revision })); go(`/admin/operations/${operation.operationId}`); })} /> : null}
      {confirm === "delete" ? <MailboxRetirementConfirm mailbox={mb} onClose={() => setConfirm(null)} onConfirm={async () => { await del(base, { confirmAddress: mb.address, expectedRevision: mb.revision }); await refreshMailboxes(); go("/admin/mailboxes"); }} /> : null}
    </div>
  );
}

function MailboxRetirementConfirm({ mailbox, onClose, onConfirm }: { mailbox: Mailbox; onClose: () => void; onConfirm: () => Promise<void> }) {
  const { data, error } = useAsync<{ credentialId: number; source: string; state: string; externalRevocationRequired: boolean }[]>(() => get(`/api/admin/mailboxes/${mailbox.id}/retirement-credentials`), [mailbox.id, mailbox.revision]);
  if (error) return <Modal title={t("Unregister mailbox")} onClose={onClose}><ErrorBox error={error} /></Modal>;
  if (!data) return <Modal title={t("Unregister mailbox")} onClose={onClose}><Spinner /></Modal>;
  return <Confirm title={t("Unregister mailbox")} danger requireText={mailbox.address} onClose={onClose} onConfirm={onConfirm} text={<>
    {t("Removes local registration. Remote mail remains on the server.")}
    <p>{t("Revoke these credentials in the external mail service:")}</p>
    <ul>{data.filter((credential) => credential.externalRevocationRequired).map((credential) => <li key={credential.credentialId}>{credential.credentialId} · {credential.source} · {credential.state}</li>)}</ul>
  </>} />;
}

function EditMailboxModal({ mailbox, dir, onClose, onSaved }: { mailbox: Mailbox; dir: DirectoryEntry[]; onClose: () => void; onSaved: () => void }) {
  const requests = useOperationRequests();
  const [name, setName] = useState(mailbox.displayName);
  const [kind, setKind] = useState(mailbox.kind);
  const [owner, setOwner] = useState(mailbox.ownerMemberId ?? 0);
  const [busy, setBusy] = useState(false);
  return (
    <Modal title={t("Edit")} onClose={onClose} footer={<><Button onClick={onClose}>{t("Cancel")}</Button><Button kind="primary" busy={busy} onClick={async () => { setBusy(true); try { const operation = await patch<Operation>(`/api/admin/mailboxes/${mailbox.id}`, requests.prepare("edit", { expectedRevision: mailbox.revision, displayName: name, kind, ownerMemberId: kind === "shared" ? 0 : owner })); refreshMailboxes(); onSaved(); onClose(); go(`/admin/operations/${operation.operationId}`); } catch (e) { errorToast(e); } finally { setBusy(false); } }}>{t("Save")}</Button></>}>
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
