import { useState } from "preact/hooks";
import { get, put, waitOperation, type Operation, type ProtocolTemplate } from "@/lib/api";
import { Button, Field, Spinner, ErrorBox, useAsync } from "@/ui";
import { ProtocolEditor } from "./AdminConnections";
import { t } from "@/lib/i18n";
import { statusLabel } from "@/lib/resourceLabels";
import { errorLabel } from "@/lib/errorLabels";
import { useOperationRequests } from "@/lib/useOperationRequests";

interface EndpointView {
  protocol: "imap" | "smtp" | "managesieve";
  networkMode: "inherit" | "override" | "disabled";
  effectiveNetwork: ProtocolTemplate;
  username: string | null;
  credential: { id: number; configured: boolean; hint: string; generation: number; state: string } | null;
  revision: number;
  checkStatus: string;
  lastErrorCode: string | null;
}
interface EndpointsView { mailboxId: number; revision: number; connectionId: number; endpoints: EndpointView[] }

export function EndpointSettings({ mailboxId, onSaved }: { mailboxId: number; onSaved: () => void }) {
  const { data, error, loading, reload } = useAsync(() => get<EndpointsView>(`/api/admin/mailboxes/${mailboxId}/endpoints`), [mailboxId]);
  if (loading) return <Spinner />;
  if (error || !data) return <ErrorBox error={error} onRetry={reload} />;
  return <EndpointForm key={`${data.mailboxId}/${data.revision}`} value={data} onSaved={() => { reload(); onSaved(); }} />;
}

function EndpointForm({ value, onSaved }: { value: EndpointsView; onSaved: () => void }) {
  const requests = useOperationRequests();
  const [endpoints, setEndpoints] = useState(value.endpoints.map((endpoint) => ({
    ...endpoint, network: endpoint.effectiveNetwork, newCredential: !endpoint.credential?.configured, password: "",
  })));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const edit = (index: number, fields: Partial<(typeof endpoints)[number]>) => setEndpoints(endpoints.map((item, position) => position === index ? { ...item, ...fields } : item));
  return <form onSubmit={async (event) => {
    event.preventDefault(); setBusy(true); setError("");
    try {
      const operation = await put<Operation>(`/api/admin/mailboxes/${value.mailboxId}/endpoints`, requests.prepare("endpoints", {
        expectedRevision: value.revision,
        credentials: endpoints.filter((item) => item.networkMode !== "disabled" && item.newCredential).map((item) => ({ clientKey: item.protocol, secret: item.password })),
        endpoints: Object.fromEntries(endpoints.map((item) => [item.protocol, item.networkMode === "disabled" ? { networkMode: "disabled" } : {
          networkMode: item.networkMode, ...(item.networkMode === "override" ? { network: item.network } : {}),
          authMode: "password", username: item.username,
          credential: item.newCredential ? { clientKey: item.protocol } : { credentialId: item.credential?.id },
        }])),
      }));
      await waitOperation(operation); onSaved();
    } catch (error) { setError((error as Error).message); } finally { setBusy(false); }
  }}>
    <h3>{t("Protocol configuration · mailbox revision {revision}", { revision: value.revision })}</h3>
    {endpoints.map((item, index) => <section class="card" key={item.protocol}>
      <h4>{item.protocol.toUpperCase()} · {statusLabel(item.checkStatus)}</h4>
      {item.lastErrorCode && <p class="warn-text">{errorLabel(item.lastErrorCode)}</p>}
      <Field label={t("Network configuration")}><select value={item.networkMode} onChange={(event) => edit(index, { networkMode: event.currentTarget.value as EndpointView["networkMode"] })}>
        <option value="inherit">{t("Inherit connection template")}</option><option value="override">{t("Use independent network configuration")}</option><option value="disabled">{t("Disable protocol")}</option>
      </select></Field>
      {item.networkMode !== "disabled" && <>
        {item.networkMode === "override" && <ProtocolEditor name={item.protocol} value={item.network} onChange={(network) => edit(index, { network })} />}
        <Field label={t("Username")}><input required value={item.username ?? ""} onInput={(event) => edit(index, { username: event.currentTarget.value })} /></Field>
        <Field label={t("Authentication credential")}><select value={item.newCredential ? "new" : "existing"} onChange={(event) => edit(index, { newCredential: event.currentTarget.value === "new" })}>
          {item.credential?.configured && <option value="existing">{t("Keep credential {id}", { id: item.credential.id })} · {item.credential.hint}</option>}
          <option value="new">{t("Enter a new password or application password")}</option>
        </select></Field>
        {item.newCredential && <Field label={t("Password or application password")}><input type="password" required autocomplete="new-password" value={item.password} onInput={(event) => edit(index, { password: event.currentTarget.value })} /></Field>}
      </>}
    </section>)}
    {error && <div class="form-error">{error}</div>}
    <Button type="submit" kind="primary" busy={busy}>{t("Verify and update protocol configuration")}</Button>
  </form>;
}
