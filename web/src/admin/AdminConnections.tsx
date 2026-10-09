import { useState } from "preact/hooks";
import { get, post, patch, del, waitOperation, type MailConnection, type DomainBinding, type ProviderKind, type ProtocolTemplate, type ProtocolTemplates, type Operation, type DiscoveryResource } from "@/lib/api";
import { t } from "@/lib/i18n";
import { Button, Field, Modal, Spinner, ErrorBox, useAsync, Badge } from "@/ui";
import { errorToast, toast } from "@/lib/state";
import { PageHead } from "./AdminOrg";
import { useOperationRequests } from "@/lib/useOperationRequests";
import { canImportResource, resourceLabel, statusLabel } from "@/lib/resourceLabels";

const protocolNames = ["imap", "smtp", "managesieve"] as const;
function presets(kind: ProviderKind): ProtocolTemplates {
  if (kind === "manual") return { imap: { enabled: false }, smtp: { enabled: false }, managesieve: { enabled: false } };
  return {
    imap: { enabled: true, host: `imap.${kind}.com`, port: 993, tlsMode: "tls", caBundleId: null },
    smtp: { enabled: true, host: `smtp.${kind}.com`, port: 465, tlsMode: "tls", caBundleId: null },
    managesieve: kind === "purelymail" ? { enabled: true, host: "mailserver.purelymail.com", port: 4190, tlsMode: "starttls", caBundleId: null } : { enabled: false },
  };
}

export function ProtocolEditor({ name, value, onChange }: { name: string; value: ProtocolTemplate; onChange: (value: ProtocolTemplate) => void }) {
  const edit = (fields: Partial<ProtocolTemplate>) => onChange({ ...value, ...fields });
  return <section class="card">
    <label class="check-row"><input type="checkbox" checked={value.enabled} onChange={(event) => onChange(event.currentTarget.checked ? { enabled: true, host: "", port: name === "imap" ? 993 : name === "smtp" ? 465 : 4190, tlsMode: "tls", caBundleId: null } : { enabled: false })} />{name.toUpperCase()}</label>
    {value.enabled && <>
      <Field label={t("Server hostname")}><input required value={value.host ?? ""} onInput={(event) => edit({ host: event.currentTarget.value })} /></Field>
      <Field label={t("Port")}><input type="number" required min={1} max={65535} value={value.port ?? 0} onInput={(event) => edit({ port: Number(event.currentTarget.value) })} /></Field>
      <Field label="TLS"><select value={value.tlsMode ?? "tls"} onChange={(event) => edit({ tlsMode: event.currentTarget.value as "tls" | "starttls" })}><option value="tls">TLS</option><option value="starttls">STARTTLS</option></select></Field>
      <Field label={t("CA bundle ID")} hint={t("Use a configured CA bundle ID, or leave blank to use system certificates.")}><input type="number" min={1} value={value.caBundleId ?? ""} onInput={(event) => edit({ caBundleId: event.currentTarget.value ? Number(event.currentTarget.value) : null })} /></Field>
    </>}
  </section>;
}

export function ConnectionCreateForm({ onSaved }: { onSaved: (connection: MailConnection) => void }) {
  const [kind, setKind] = useState<ProviderKind>("purelymail");
  const [label, setLabel] = useState("Purelymail");
  const [username, setUsername] = useState("");
  const [apiKey, setAPIKey] = useState("");
  const [domains, setDomains] = useState("");
  const [protocols, setProtocols] = useState(presets("purelymail"));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  return <form onSubmit={async (event) => {
    event.preventDefault(); setBusy(true); setError("");
    try {
      const connection = await post<MailConnection>("/api/admin/connections", {
        providerKind: kind, label, apiAuth: kind === "manual" ? null : kind === "migadu" ? { username, apiKey } : { apiKey },
        domainScope: kind !== "manual" && domains.trim() ? { mode: "selected", domains: domains.split(/[,\s]+/).filter(Boolean) } : { mode: "all" },
        protocolDefaults: protocols,
      });
      setAPIKey(""); onSaved(connection);
    } catch (error) { setError((error as Error).message); } finally { setBusy(false); }
  }}>
    <Field label={t("Provider")}><select value={kind} onChange={(event) => { const kind = event.currentTarget.value as ProviderKind; setKind(kind); setLabel(kind === "manual" ? t("Manual connection") : kind === "migadu" ? "Migadu" : "Purelymail"); setProtocols(presets(kind)); }}><option value="purelymail">Purelymail</option><option value="migadu">Migadu</option><option value="manual">{t("Manual IMAP/SMTP")}</option></select></Field>
    <Field label={t("Connection name")}><input required maxLength={100} value={label} onInput={(event) => setLabel(event.currentTarget.value)} /></Field>
    {kind === "migadu" && <Field label={t("Migadu account email")}><input type="email" required value={username} onInput={(event) => setUsername(event.currentTarget.value)} /></Field>}
    {kind !== "manual" && <>
      <Field label="API key"><input type="password" autocomplete="new-password" required value={apiKey} onInput={(event) => setAPIKey(event.currentTarget.value)} /></Field>
      <Field label={t("Domain scope")} hint={t("Leave blank for all domains; separate domains with commas or whitespace.")}><input value={domains} onInput={(event) => setDomains(event.currentTarget.value)} /></Field>
    </>}
    {protocolNames.map((name) => <ProtocolEditor key={name} name={name} value={protocols[name]} onChange={(value) => setProtocols({ ...protocols, [name]: value })} />)}
    {error && <div class="form-error">{error}</div>}
    <Button type="submit" kind="primary" busy={busy}>{t("Save connection")}</Button>
  </form>;
}

export function MailboxAttachForm({ connection, onSaved, ownerMemberId = null, initialKind = "personal", mode = "attach", bindings = [], onPrepared, fixedKind = false }: { connection: Pick<MailConnection, "id" | "label" | "providerKind" | "protocolDefaults">; onSaved: () => void; ownerMemberId?: number | null; initialKind?: "personal" | "shared"; mode?: "attach" | "create"; bindings?: Pick<DomainBinding, "id" | "domainName" | "connectionId" | "managementMode" | "remoteState">[]; onPrepared?: (payload: Record<string, unknown>) => Promise<void>; fixedKind?: boolean }) {
  const requests = useOperationRequests();
  const [address, setAddress] = useState("");
  const [displayName, setDisplayName] = useState("");
	const [bindingId, setBindingId] = useState(0);
	const [local, setLocal] = useState("");
	const [credentialMode, setCredentialMode] = useState<"managed" | "entered">(mode === "create" ? "managed" : "entered");
  const [kind, setKind] = useState<"personal" | "shared">(initialKind);
  const [protocols, setProtocols] = useState(connection.protocolDefaults);
  const [users, setUsers] = useState({ imap: "", smtp: "", managesieve: "" });
  const [passwords, setPasswords] = useState({ imap: "", smtp: "", managesieve: "" });
  const [sentCopyMode, setSentCopyMode] = useState("append");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  return <form onSubmit={async (event) => {
    event.preventDefault(); setBusy(true); setError("");
    try {
      const endpoints = Object.fromEntries(protocolNames.map((name) => [name, protocols[name].enabled ? { networkMode: "override", network: protocols[name], authMode: "password", username: users[name], credential: { clientKey: name } } : { networkMode: "disabled" }]));
      const payload = requests.prepare("mailbox", { mode, connectionId: connection.id, kind, displayName,
        ...(mode === "create" ? { domainBindingId: bindingId, localPart: local } : { address }),
        ownerMemberId: kind === "personal" ? ownerMemberId : null, credentialMode,
        ...(credentialMode === "entered" ? { endpoints,
        credentials: protocolNames.filter((name) => protocols[name].enabled).map((name) => ({ clientKey: name, secret: passwords[name] })), sentCopyMode,
        folderMapping: { sent: null, drafts: null, trash: null, junk: null, archive: null },
        } : { sentCopyMode }),
      });
      if (onPrepared) await onPrepared(payload); else await waitOperation(await post<Operation>("/api/admin/mailboxes", payload));
      setPasswords({ imap: "", smtp: "", managesieve: "" }); onSaved();
    } catch (error) { setError((error as Error).message); } finally { setBusy(false); }
  }}>
    <p>{t("Mail connection")}: {connection.label} · ID {connection.id}</p>
    {mode === "attach" ? <Field label={t("Mailbox address")}><input type="email" required value={address} onInput={(event) => setAddress(event.currentTarget.value)} /></Field> : <>
      <Field label={t("Domain binding")}><select required value={bindingId} onChange={(event) => setBindingId(Number(event.currentTarget.value))}><option value={0}>—</option>{bindings.filter((item) => item.connectionId === connection.id && item.managementMode === "api" && item.remoteState === "present").map((item) => <option key={item.id} value={item.id}>{item.domainName} · #{item.id}</option>)}</select></Field>
      <Field label={t("Local part")}><input required value={local} onInput={(event) => setLocal(event.currentTarget.value)} /></Field>
      <Field label={t("Credential mode")}><select value={credentialMode} onChange={(event) => setCredentialMode(event.currentTarget.value as "managed" | "entered")}><option value="managed" disabled={connection.providerKind !== "purelymail"}>{t("Managed credentials")}</option><option value="entered">{t("Entered credentials")}</option></select></Field>
    </>}
    <Field label={t("Display name")}><input value={displayName} onInput={(event) => setDisplayName(event.currentTarget.value)} /></Field>
    <Field label={t("Mailbox type")}><select value={kind} disabled={fixedKind} onChange={(event) => setKind(event.currentTarget.value as "personal" | "shared")}><option value="personal">{t("Personal mailbox")}</option><option value="shared">{t("Shared mailbox")}</option></select></Field>
    {credentialMode === "entered" ? protocolNames.map((name) => <div key={name}>
      <ProtocolEditor name={name} value={protocols[name]} onChange={(value) => setProtocols({ ...protocols, [name]: value })} />
      {protocols[name].enabled && <>
        <Field label={t("{protocol} username", { protocol: name.toUpperCase() })}><input required value={users[name]} onInput={(event) => setUsers({ ...users, [name]: event.currentTarget.value })} /></Field>
        <Field label={t("{protocol} password or application password", { protocol: name.toUpperCase() })}><input type="password" required autocomplete="new-password" value={passwords[name]} onInput={(event) => setPasswords({ ...passwords, [name]: event.currentTarget.value })} /></Field>
      </>}
    </div>) : <p class="muted small">{t("Managed credentials use the enabled connection templates.")}</p>}
    <Field label={t("Sent copy")}><select value={sentCopyMode} onChange={(event) => setSentCopyMode(event.currentTarget.value)}><option value="append">{t("Saved by Mailhearth")}</option><option value="server">{t("Saved by mail server")}</option></select></Field>
    {error && <div class="form-error">{error}</div>}
    <Button type="submit" kind="primary" busy={busy} disabled={mode === "create" && (!bindingId || (credentialMode === "managed" && connection.providerKind !== "purelymail"))}>{t(onPrepared ? "Create member" : mode === "create" ? "Create and verify mailbox" : "Verify and register mailbox")}</Button>
  </form>;
}

export function ConnectionsPage() {
  const requests = useOperationRequests();
  const { data, loading, error, reload } = useAsync(() => get<MailConnection[]>("/api/admin/connections"), []);
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<MailConnection | null>(null);
  const [attaching, setAttaching] = useState<MailConnection | null>(null);
  const [discovery, setDiscovery] = useState<{ connection: MailConnection; snapshotId: number; resources: DiscoveryResource[] } | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState<number | null>(null);
  const run = async (id: number, action: () => Promise<void>) => { setBusy(id); try { await action(); } catch (error) { errorToast(error); } finally { setBusy(null); } };
  return <div class="page"><PageHead title={t("Mail connections")}><Button kind="primary" onClick={() => setCreating(true)}>{t("Add connection")}</Button></PageHead><div class="page-body">
    {loading ? <Spinner /> : error ? <ErrorBox error={error} onRetry={reload} /> : null}
    {(data ?? []).map((connection) => <section class="card" key={connection.id}>
      <h3>{connection.label} <Badge>{connection.providerKind} · ID {connection.id}</Badge></h3>
      <p>{t("Management check")}: {statusLabel(connection.lastApiCheckStatus)} · {t("Configuration revision")}: {connection.revision}</p>
      {connection.apiConfigured && <p>API key：{connection.apiHint}</p>}
      {connection.lastApiErrorCode && <div class="notice warn">{connection.lastApiErrorCode}</div>}
      <div class="row gap wrap">
        <Button onClick={() => setEditing(connection)}>{t("Edit connection configuration")}</Button>
        <Button disabled={!connection.enabled} busy={busy === connection.id} onClick={() => run(connection.id, async () => { await post(`/api/admin/connections/${connection.id}/test`); reload(); })}>{t("Verify connection")}</Button>
        <Button disabled={!connection.enabled} onClick={() => setAttaching(connection)}>{t("Register existing mailbox")}</Button>
        {connection.providerKind !== "manual" && <Button disabled={!connection.enabled} busy={busy === connection.id} onClick={() => run(connection.id, async () => {
          const action = `discover/${connection.id}`;
          const operation = await waitOperation(await post<Operation>(`/api/admin/connections/${connection.id}/discover`, requests.prepare(action, {})));
          if (!operation.result.snapshotId || !operation.result.resources) throw new Error(t("The discovery response is incomplete."));
          requests.complete(action);
          setSelected(new Set()); setDiscovery({ connection, snapshotId: operation.result.snapshotId!, resources: operation.result.resources! });
        })}>{t("Discover resources")}</Button>}
        {connection.providerKind !== "manual" && <Button disabled={!connection.enabled} busy={busy === connection.id} onClick={() => run(connection.id, async () => {
          const action = `sync/${connection.id}`;
          const operation = await waitOperation(await post<Operation>(`/api/admin/connections/${connection.id}/sync`, requests.prepare(action, {})));
          requests.complete(action); reload();
          if (operation.result.snapshotId && operation.result.resources?.length) { setSelected(new Set()); setDiscovery({ connection, snapshotId: operation.result.snapshotId, resources: operation.result.resources }); }
        })}>{t("Sync now")}</Button>}
        <Button onClick={() => run(connection.id, async () => { await patch(`/api/admin/connections/${connection.id}`, { expectedRevision: connection.revision, enabled: !connection.enabled }); reload(); })}>{t(connection.enabled ? "Disable connection" : "Enable connection")}</Button>
        <Button kind="danger" onClick={() => run(connection.id, async () => { const confirmLabel = prompt(t("Enter the full connection name to remove this empty connection.")); if (confirmLabel === null) return; await del(`/api/admin/connections/${connection.id}`, { expectedRevision: connection.revision, confirmLabel }); reload(); })}>{t("Remove empty connection")}</Button>
      </div>
    </section>)}
  </div>
    {creating && <Modal title={t("Add mail connection")} onClose={() => setCreating(false)}><ConnectionCreateForm onSaved={() => { setCreating(false); reload(); }} /></Modal>}
    {editing && <Modal title={t("Edit mail connection")} onClose={() => setEditing(null)}><ConnectionEditForm connection={editing} onSaved={() => { setEditing(null); reload(); }} /></Modal>}
    {attaching && <Modal title={t("Register existing mailbox")} onClose={() => setAttaching(null)}><MailboxAttachForm connection={attaching} onSaved={() => { setAttaching(null); toast(t("Mailbox registered"), "success"); reload(); }} /></Modal>}
    {discovery && <Modal title={t("Select resources to import")} onClose={() => setDiscovery(null)}>
      {discovery.resources.map((item) => { const key = `${item.resource.resourceType}/${item.resource.remoteKey}`; return <label class="check-row" key={key}>
        <input type="checkbox" checked={selected.has(key)} disabled={busy !== null || !canImportResource(item)} onChange={(event) => { const next = new Set(selected); event.currentTarget.checked ? next.add(key) : next.delete(key); setSelected(next); }} />
        {resourceLabel(item)} · {item.summary.mailboxAddress ? `${item.summary.mailboxAddress} → ` : ""}{item.summary.address ?? item.summary.name ?? item.resource.remoteKey}{item.summary.status ? ` · ${statusLabel(item.summary.status)}` : ""}{item.forwarding ? ` · ${statusLabel(item.forwarding.deliveryMode)}` : ""}
      </label>; })}
      <p>{t("Select each mailbox and its domain. Configure login credentials after import.")}</p>
      <p>{t("Select the source mailbox and domain for forwarding. Delivery modes remain unverified until delivery is tested.")}</p>
      <Button kind="primary" disabled={!selected.size} busy={busy === discovery.connection.id} onClick={() => run(discovery.connection.id, async () => {
        await waitOperation(await post<Operation>(`/api/admin/connections/${discovery.connection.id}/import`, requests.prepare(`import/${discovery.connection.id}`, { snapshotId: discovery.snapshotId,
          selectedResources: discovery.resources.filter((item) => selected.has(`${item.resource.resourceType}/${item.resource.remoteKey}`)).map((item) => ({ resourceType: item.resource.resourceType, remoteKey: item.resource.remoteKey })),
        }))); setDiscovery(null); reload(); toast(t("Resources imported"), "success");
      })}>{t("Import selected resources")}</Button>
    </Modal>}
  </div>;
}

function ConnectionEditForm({ connection, onSaved }: { connection: MailConnection; onSaved: () => void }) {
  const requests = useOperationRequests();
  const [label, setLabel] = useState(connection.label);
  const [username, setUsername] = useState(connection.apiUsername ?? "");
  const [apiKey, setAPIKey] = useState("");
  const [scopeMode, setScopeMode] = useState(connection.domainScope.mode);
  const [domains, setDomains] = useState(connection.domainScope.domains?.join(", ") ?? "");
  const [protocols, setProtocols] = useState(connection.protocolDefaults);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  return <form onSubmit={async (event) => {
    event.preventDefault(); setBusy(true); setError("");
    try {
      const scope = connection.providerKind === "manual" || scopeMode === "all" ? { mode: "all" as const } : { mode: "selected" as const, domains: Array.from(new Set(domains.split(/[,\s]+/).filter(Boolean))).sort() };
      const protocolsChanged = JSON.stringify(protocols) !== JSON.stringify(connection.protocolDefaults);
      const scopeChanged = JSON.stringify(scope) !== JSON.stringify(connection.domainScope);
      if (username !== (connection.apiUsername ?? "") && apiKey === "") throw new Error(t("Changing the API username requires its API key."));
      const operation = await patch<Operation | MailConnection>(`/api/admin/connections/${connection.id}`, requests.prepare("connection", {
        expectedRevision: connection.revision, label,
        ...(apiKey !== "" ? { apiAuth: connection.providerKind === "migadu" ? { username, apiKey } : { apiKey } } : {}),
        ...(scopeChanged ? { domainScope: scope } : {}),
        ...(protocolsChanged ? { protocolDefaults: protocols } : {}),
      }));
      if ("operationId" in operation) await waitOperation(operation); setAPIKey(""); onSaved();
    } catch (error) { setError((error as Error).message); } finally { setBusy(false); }
  }}>
    <p>{connection.providerKind} · ID {connection.id} · {t("Configuration revision")} {connection.revision}</p>
    <Field label={t("Connection name")}><input required maxLength={100} value={label} onInput={(event) => setLabel(event.currentTarget.value)} /></Field>
    {connection.providerKind !== "manual" && <>
      {connection.providerKind === "migadu" && <Field label={t("Migadu account email")}><input type="email" required value={username} onInput={(event) => setUsername(event.currentTarget.value)} /></Field>}
      <Field label={t("Update API key")} hint={t("Leave blank to keep the current API key.")}><input type="password" autocomplete="new-password" value={apiKey} onInput={(event) => setAPIKey(event.currentTarget.value)} /></Field>
      <Field label={t("Domain scope")}><select value={scopeMode} onChange={(event) => setScopeMode(event.currentTarget.value as "all" | "selected")}><option value="all">{t("All domains")}</option><option value="selected">{t("Selected domains")}</option></select></Field>
      {scopeMode === "selected" && <Field label={t("Domain list")}><input required value={domains} onInput={(event) => setDomains(event.currentTarget.value)} /></Field>}
    </>}
    {protocolNames.map((name) => <ProtocolEditor key={name} name={name} value={protocols[name]} onChange={(value) => setProtocols({ ...protocols, [name]: value })} />)}
    {error && <div class="form-error">{error}</div>}
    <Button type="submit" kind="primary" busy={busy}>{t("Verify and update connection")}</Button>
  </form>;
}
