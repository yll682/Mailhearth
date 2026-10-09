import { useState } from "preact/hooks";
import { t } from "@/lib/i18n";
import { statusLabel } from "@/lib/resourceLabels";
import { get, post, patch, del, waitOperation, capabilityAvailable, type Operation, type MailConnection, type DomainBinding } from "@/lib/api";
import { errorToast } from "@/lib/state";
import { fmtDate } from "@/lib/format";
import { Button, Field, useAsync, Spinner, ErrorBox, Modal, Badge, Confirm, Toggle } from "@/ui";
import { PageHead } from "./AdminOrg";
import { useOperationRequests } from "@/lib/useOperationRequests";

export function DomainsPage() {
  const requests = useOperationRequests();
  const bindings = useAsync(() => get<DomainBinding[]>("/api/admin/domain-bindings"), []);
  const connections = useAsync(() => get<MailConnection[]>("/api/admin/connections"), []);
  const [adding, setAdding] = useState(false);
  const [dns, setDns] = useState<DomainBinding | null>(null);
  const [remove, setRemove] = useState<{ binding: DomainBinding; remote: boolean } | null>(null);
  const [busy, setBusy] = useState(0);
  const operate = async (binding: DomainBinding, action: string, settings?: DomainBinding["providerSettings"]) => {
    setBusy(binding.id);
    try {
      const body = requests.prepare(`binding/${binding.id}/${action}`, { expectedRevision: binding.revision, ...(settings ? { providerSettings: settings } : {}), ...(action === "delete-remote" ? { confirmDomain: binding.domainName } : {}) });
      const operation = settings ? await patch<Operation>(`/api/admin/domain-bindings/${binding.id}`, body) : await post<Operation>(`/api/admin/domain-bindings/${binding.id}/${action}`, body);
      await waitOperation(operation);
      bindings.reload();
    } catch (error) { errorToast(error); }
    finally { setBusy(0); }
  };
  return <div class="page">
    <PageHead title={t("Domains")}><Button kind="primary" icon="plus" size="sm" onClick={() => setAdding(true)}>{t("Add domain")}</Button></PageHead>
    <div class="page-body">
      {bindings.loading || connections.loading ? <Spinner /> : null}
      {bindings.error ? <ErrorBox error={bindings.error} onRetry={bindings.reload} /> : null}
      {connections.error ? <ErrorBox error={connections.error} onRetry={connections.reload} /> : null}
      <div class="card-list">{(bindings.data ?? []).map((binding) => {
        const connection = connections.data?.find((item) => item.id === binding.connectionId);
        const managed = binding.managementMode === "api" && binding.remoteState === "present";
        const enabled = (key: string) => managed && capabilityAvailable(connection?.capabilities?.[key]);
        return <section key={binding.id} class="card">
          <div class="row gap wrap"><div class="grow"><h3 class="mono">{binding.domainName}</h3><p class="muted small">{binding.connectionLabel} · ID {binding.connectionId} · {statusLabel(binding.managementMode)} · <Badge>{statusLabel(binding.remoteState)}</Badge></p></div>
            <Button size="sm" busy={busy === binding.id} disabled={!enabled("domain.checkDNS")} onClick={() => operate(binding, "recheck")}>{t("Recheck DNS")}</Button>
            <Button size="sm" disabled={!enabled("domain.dnsRecords")} onClick={() => setDns(binding)}>{t("DNS records")}</Button>
            <Button size="sm" disabled={!managed || !connection?.enabled || !connection.capabilities?.["domain.create"]?.permissionAllowed} onClick={() => operate(binding, "activate")}>{t("Activate domain")}</Button>
            <Button size="sm" onClick={() => setRemove({ binding, remote: false })}>{t("Unregister domain binding")}</Button>
            <Button size="sm" disabled={!enabled("domain.delete")} onClick={() => setRemove({ binding, remote: true })}>{t("Delete remote domain")}</Button>
          </div>
          {binding.dnsCheckedAt ? <p class="muted small">{t("Checked")} {fmtDate(binding.dnsCheckedAt)}</p> : null}
          <div class="row gap wrap">{(["mx", "spf", "dkim", "dmarc"] as const).map((key) => <Badge key={key} tone={binding.dnsStatus[key] === "pass" ? "good" : "warn"}>{key.toUpperCase()} · {binding.dnsStatus[key] === "pass" ? t("Passed") : binding.dnsStatus[key] === "fail" ? t("Failed") : t("Unverified")}</Badge>)}</div>
          {connection?.providerKind === "purelymail" && managed ? <div class="row gap wrap">
            <Toggle checked={binding.providerSettings.allowAccountReset ?? false} label={t("Account password reset")} disabled={busy === binding.id || !enabled("domain.create")} onChange={(value) => operate(binding, "", { allowAccountReset: value })} />
            <Toggle checked={binding.providerSettings.symbolicSubaddressing ?? false} label={t("Symbolic subaddressing")} disabled={busy === binding.id || !enabled("domain.create")} onChange={(value) => operate(binding, "", { symbolicSubaddressing: value })} />
          </div> : null}
          {!managed ? <p class="muted">{t("Manage this domain in the provider console.")}</p> : null}
        </section>;
      })}</div>
    </div>
    {adding ? <AddBinding connections={connections.data ?? []} onClose={() => setAdding(false)} onSaved={bindings.reload} /> : null}
    {dns ? <BindingDNS binding={dns} onClose={() => setDns(null)} /> : null}
    {remove ? <Confirm title={t(remove.remote ? "Delete remote domain" : "Unregister domain binding")} text={`${remove.binding.domainName} · ${remove.binding.connectionLabel}. ${t("Mailboxes and address rules must be removed first.")}`} requireText={remove.binding.domainName} danger onClose={() => setRemove(null)} onConfirm={async () => {
      if (remove.remote) await operate(remove.binding, "delete-remote");
      else { await del(`/api/admin/domain-bindings/${remove.binding.id}`, { expectedRevision: remove.binding.revision }); bindings.reload(); }
    }} /> : null}
  </div>;
}

function AddBinding({ connections, onClose, onSaved }: { connections: MailConnection[]; onClose: () => void; onSaved: () => void }) {
  const requests = useOperationRequests();
  const [connectionId, setConnectionId] = useState(0);
  const [domainName, setDomainName] = useState("");
  const [mode, setMode] = useState("register");
  const [busy, setBusy] = useState(false);
  const connection = connections.find((item) => item.id === connectionId);
  const createAllowed = capabilityAvailable(connection?.capabilities?.["domain.create"]);
  const save = async () => {
    setBusy(true);
    try { await waitOperation(await post<Operation>("/api/admin/domain-bindings", requests.prepare("create", { connectionId, domainName, mode }))); onSaved(); onClose(); }
    catch (error) { errorToast(error); }
    finally { setBusy(false); }
  };
  return <Modal title={t("Add domain")} onClose={onClose} footer={<><Button onClick={onClose}>{t("Cancel")}</Button><Button kind="primary" busy={busy} disabled={!connection || !domainName.trim() || (mode === "create" && !createAllowed)} onClick={save}>{t("Save")}</Button></>}>
    <Field label={t("Mail connection")}><select value={connectionId} onChange={(event) => { setConnectionId(Number(event.currentTarget.value)); setMode("register"); }}><option value={0}>{t("Select a connection")}</option>{connections.filter((item) => item.enabled).map((item) => <option value={item.id} key={item.id}>{item.label} · {item.providerKind} · ID {item.id}</option>)}</select></Field>
    <Field label={t("Domain")}><input value={domainName} onInput={(event) => setDomainName(event.currentTarget.value)} placeholder="example.org" /></Field>
    <Field label={t("Action")}><select value={mode} onChange={(event) => setMode(event.currentTarget.value)}><option value="register">{t("Register existing domain")}</option><option value="create" disabled={!createAllowed}>{t("Create remote domain")}</option></select></Field>
  </Modal>;
}

function BindingDNS({ binding, onClose }: { binding: DomainBinding; onClose: () => void }) {
  const records = useAsync(() => get<{ records: { type: string; host: string; value: string; priority?: number }[] }>(`/api/admin/domain-bindings/${binding.id}/dns-records`), [binding.id]);
  return <Modal title={`${t("DNS records")} · ${binding.domainName} · ${binding.connectionLabel}`} wide onClose={onClose}>
    {records.loading ? <Spinner /> : records.error ? <ErrorBox error={records.error} onRetry={records.reload} /> : <table class="table"><thead><tr><th>{t("Type")}</th><th>{t("Host")}</th><th>{t("Value")}</th></tr></thead><tbody>{records.data?.records.map((item, index) => <tr key={index}><td>{item.type} {item.priority}</td><td class="mono">{item.host}</td><td class="mono">{item.value}</td></tr>)}</tbody></table>}
  </Modal>;
}
