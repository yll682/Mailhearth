import { useState } from "preact/hooks";
import { t } from "@/lib/i18n";
import { post, waitOperation, type Operation, type MailConnection, type DiscoveryResource } from "@/lib/api";
import { Button } from "@/ui";
import { useOperationRequests } from "@/lib/useOperationRequests";
import { canImportResource, resourceDescription, resourceLabel, statusLabel } from "@/lib/resourceLabels";

export function ResourceImport({ connection, onSaved }: { connection: MailConnection; onSaved: () => void }) {
  const requests = useOperationRequests();
  const [snapshot, setSnapshot] = useState<{ id: number; resources: DiscoveryResource[] } | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const discover = async () => {
    setBusy(true); setError("");
    try {
      const action = `discover/${connection.id}`;
      const operation = await waitOperation(await post<Operation>(`/api/admin/connections/${connection.id}/discover`, requests.prepare(action, {})));
      if (!operation.result.snapshotId || !operation.result.resources) throw new Error(t("The discovery response is incomplete."));
      requests.complete(action);
      setSnapshot({ id: operation.result.snapshotId, resources: operation.result.resources }); setSelected(new Set());
    } catch (error) { setError((error as Error).message); }
    finally { setBusy(false); }
  };
  const importSelected = async () => {
    if (!snapshot) return;
    setBusy(true); setError("");
    try {
      await waitOperation(await post<Operation>(`/api/admin/connections/${connection.id}/import`, requests.prepare(`import/${connection.id}`, { snapshotId: snapshot.id, selectedResources: snapshot.resources.filter((item) => selected.has(`${item.resource.resourceType}/${item.resource.remoteKey}`)).map((item) => ({ resourceType: item.resource.resourceType, remoteKey: item.resource.remoteKey })) })));
      setSnapshot(null); setSelected(new Set()); onSaved();
    } catch (error) { setError((error as Error).message); }
    finally { setBusy(false); }
  };
  return <section class="card">
    <h3>{connection.label} · {t("Discover resources")}</h3>
    <Button busy={busy} disabled={!connection.enabled || connection.providerKind === "manual"} onClick={discover}>{t("Discover resources")}</Button>
    {snapshot ? <>
      <p>{t("Select each mailbox and its domain. Configure login credentials after import.")}</p>
      <p>{t("Select the source mailbox and domain for forwarding. Delivery modes remain unverified until delivery is tested.")}</p>
      {snapshot.resources.map((item) => { const key = `${item.resource.resourceType}/${item.resource.remoteKey}`; return <label key={key} class="check-row"><input type="checkbox" disabled={busy || !canImportResource(item)} checked={selected.has(key)} onChange={(event) => { const next = new Set(selected); if (event.currentTarget.checked) next.add(key); else next.delete(key); setSelected(next); }} />{resourceLabel(item)} · {resourceDescription(item)}{item.summary.status ? ` · ${statusLabel(item.summary.status)}` : ""}{item.forwarding ? ` · ${statusLabel(item.forwarding.deliveryMode)}` : ""}</label>; })}
      <Button kind="primary" busy={busy} disabled={!selected.size} onClick={importSelected}>{t("Import selected resources")}</Button>
    </> : null}
    {error ? <div class="form-error">{error}</div> : null}
  </section>;
}
