import { useEffect, useState } from "preact/hooks";
import { ApiError, get, post, type Operation } from "@/lib/api";
import { errorToast } from "@/lib/state";
import { t } from "@/lib/i18n";
import { statusLabel } from "@/lib/resourceLabels";
import { errorLabel } from "@/lib/errorLabels";
import { Button, Badge, Spinner, Confirm, Modal, Field } from "@/ui";

export function OperationsPage({ id }: { id?: string }) {
  const [items, setItems] = useState<Operation[]>([]);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [refresh, setRefresh] = useState(0);
  const [action, setAction] = useState<{ operation: Operation; name: "cancel" | "retry" | "reconcile" } | null>(null);
  const [conflict, setConflict] = useState<string | null>(null);
  const [external, setExternal] = useState<{ operation: Operation; stepKey: string } | null>(null);
  const [note, setNote] = useState("");
  const [cleanup, setCleanup] = useState<{ operation: Operation; stepKey: string } | null>(null);
  const [cleanupAddress, setCleanupAddress] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    setLoading(true); setItems([]);
    const load = async () => {
      try {
        const result = id ? [await get<Operation>(`/api/admin/operations/${encodeURIComponent(id)}`, controller.signal)] : await get<Operation[]>("/api/admin/operations", controller.signal);
        if (controller.signal.aborted) return;
        setItems(result);
        if (result.some((item) => item.status === "queued" || item.status === "running")) timer = setTimeout(load, 2000);
      } catch (error) { if (!controller.signal.aborted) errorToast(error); }
      finally { if (!controller.signal.aborted) setLoading(false); }
    };
    void load();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [id, refresh]);
  const labels = { cancel: "Cancel operation", retry: "Retry operation", reconcile: "Check operation result" };
  return <div class="page">
    <header class="page-head"><h1>{t("Operations")}</h1><div class="spacer" />{id ? <a href="/admin/operations">{t("All operations")}</a> : null}<Button disabled={busy || loading} onClick={() => setRefresh((value) => value + 1)}>{t("Refresh")}</Button></header>
    <div class="page-body stack">
      <p class="muted">{t("Retries preserve the original request and operation ID. Unknown results require checking.")}</p>
      {conflict ? <a href={`/admin/operations/${conflict}`}>{t("View the active operation")}</a> : null}
      {loading ? <Spinner /> : null}
      {!loading && !items.length ? <p class="muted">{t("No operations.")}</p> : null}
      {items.map((item) => <section key={item.operationId} class="card">
        <h3>{item.kind} <Badge>{statusLabel(item.status)}</Badge></h3><a class="small" href={`/admin/operations/${item.operationId}`}>{item.operationId}</a>
        <p class="muted small">{item.createdAt} · {item.updatedAt}</p>
        {item.errorCode ? <p class="form-error">{errorLabel(item.errorCode)}</p> : null}
        {item.result.memberId ? <p><a href={`/admin/members/${item.result.memberId}`}>{t("Member")} · #{item.result.memberId}</a></p> : null}
        {item.status === "succeeded" && item.result.inviteLink ? <Field label={t("Invite link")}><input readOnly value={item.result.inviteLink.startsWith("http") ? item.result.inviteLink : location.origin + item.result.inviteLink} /></Field> : null}
        <ol>{item.steps.map((step) => <li key={step.stepKey}>
          {step.stepKey} · {step.status === "external_reported" ? t("Administrator reported completion") : statusLabel(step.status)}{step.errorCode ? ` · ${errorLabel(step.errorCode)}` : ""}{step.finishedAt ? ` · ${step.finishedAt}` : ""}
          {step.resultJson && step.resultJson !== "{}" ? <pre class="small">{JSON.stringify(JSON.parse(step.resultJson), null, 2)}</pre> : null}
          {item.status === "needs_action" && step.status === "pending" && step.errorCode === "external_action_required" && step.stepKey.endsWith(".external") ? <Button disabled={busy} onClick={() => { setExternal({ operation: item, stepKey: step.stepKey }); setNote(""); }}>{t("Report external completion")}</Button> : null}
          {item.status === "unknown" && step.status === "unknown" && step.stepKey.endsWith("credential.create") && !JSON.parse(step.resultJson || "{}").credentialId ? <Button disabled={busy} onClick={() => { setCleanup({ operation: item, stepKey: step.stepKey }); setCleanupAddress(""); setNote(""); }}>{t("Report lost credential cleanup")}</Button> : null}
        </li>)}</ol>
        <div class="row gap wrap">
          {item.status === "queued" || item.status === "failed" || item.status === "needs_action" ? <Button disabled={busy} onClick={() => setAction({ operation: item, name: "cancel" })}>{t(labels.cancel)}</Button> : null}
          {item.status === "failed" || item.status === "needs_action" ? <Button disabled={busy} onClick={() => setAction({ operation: item, name: "retry" })}>{t(labels.retry)}</Button> : null}
          {item.status === "unknown" ? <Button disabled={busy} onClick={() => setAction({ operation: item, name: "reconcile" })}>{t(labels.reconcile)}</Button> : null}
        </div>
      </section>)}
    </div>
    {action ? <Confirm title={t(labels[action.name])} text={`${action.operation.kind} · ${action.operation.operationId}`} onClose={() => setAction(null)} onConfirm={async () => {
      setBusy(true); setConflict(null);
      try { await post<Operation>(`/api/admin/operations/${action.operation.operationId}/${action.name}`); setAction(null); setRefresh((value) => value + 1); }
      catch (error) { errorToast(error); if (error instanceof ApiError) setConflict(error.operationId); }
      finally { setBusy(false); }
    }} /> : null}
    {cleanup ? <Modal title={t("Report lost credential cleanup")} onClose={() => setCleanup(null)} footer={<><Button disabled={busy} onClick={() => setCleanup(null)}>{t("Cancel")}</Button><Button kind="primary" busy={busy} disabled={!cleanupAddress || !note.trim()} onClick={async () => {
      setBusy(true);
      try { await post<Operation>(`/api/admin/operations/${cleanup.operation.operationId}/report-credential-cleanup`, { stepKey: cleanup.stepKey, confirmAddress: cleanupAddress, note }); setCleanup(null); setRefresh((value) => value + 1); }
      catch (error) { errorToast(error); }
      finally { setBusy(false); }
    }}>{t("Report completion")}</Button></>}>
      <p>{t("Remove the unknown application credential in the provider console before reporting. This cancels the operation and retains already created resources.")}</p>
      <p>{t("This records the administrator's report. Mailhearth has not verified remote access revocation.")}</p><p>{cleanup.stepKey}</p>
      <Field label={t("Full mailbox address")}><input value={cleanupAddress} onInput={(event) => setCleanupAddress(event.currentTarget.value)} /></Field>
      <Field label={t("Completion note")}><textarea value={note} maxLength={2000} onInput={(event) => setNote(event.currentTarget.value)} /></Field>
    </Modal> : null}
    {external ? <Modal title={t("Report external completion")} onClose={() => setExternal(null)} footer={<><Button onClick={() => setExternal(null)}>{t("Cancel")}</Button><Button kind="primary" busy={busy} disabled={!note.trim()} onClick={async () => {
      setBusy(true);
      try { await post<Operation>(`/api/admin/operations/${external.operation.operationId}/confirm-external`, { itemId: external.stepKey, note }); setExternal(null); setRefresh((value) => value + 1); }
      catch (error) { errorToast(error); }
      finally { setBusy(false); }
    }}>{t("Report completion")}</Button></>}>
      <p>{t("This records the administrator's report. Mailhearth has not verified remote access revocation.")}</p>
      <p>{external.stepKey}</p>
      <Field label={t("Completion note")}><textarea value={note} maxLength={2000} onInput={(event) => setNote(event.currentTarget.value)} /></Field>
    </Modal> : null}
  </div>;
}
