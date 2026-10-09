import { useEffect, useState } from "preact/hooks";
import { get, post, type Submission } from "@/lib/api";
import { mailboxes, errorToast } from "@/lib/state";
import { t } from "@/lib/i18n";
import { statusLabel } from "@/lib/resourceLabels";
import { Button, Field, Spinner, Confirm } from "@/ui";

export function SubmissionsTab() {
  const [id, setId] = useState(mailboxes.value[0]?.id ?? 0);
  const [items, setItems] = useState<Submission[]>([]);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [retry, setRetry] = useState<Submission | null>(null);
  const [refresh, setRefresh] = useState(0);
  const box = mailboxes.value.find((item) => item.id === id);
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    setItems([]);
    if (!id) return;
    setLoading(true);
    const load = async () => {
      try {
        const result = await get<Submission[]>(`/api/mail/mailboxes/${id}/submissions`, controller.signal);
        if (controller.signal.aborted) return;
        setItems(result);
        if (result.some((item) => ["preparing", "queued", "running"].includes(item.status))) timer = setTimeout(load, 2000);
      } catch (error) { if (!controller.signal.aborted) errorToast(error); }
      finally { if (!controller.signal.aborted) setLoading(false); }
    };
    void load();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [id, refresh]);
  const status = (item: Submission) => {
    if (item.status === "sent") return t("Sent.");
    if (item.status === "sent_copy_failed") return t("Sent, but saving the copy failed.");
    if (item.status === "unknown") return t("Delivery result unknown. Sending again may deliver a duplicate.");
    if (item.status === "failed") return t("Submission failed.");
    return t("Submission queued.");
  };
  return <div class="stack">
    <Field label={t("Mailbox")}><select disabled={busy} value={id} onChange={(event) => setId(Number(event.currentTarget.value))}>{mailboxes.value.map((item) => <option key={item.id} value={item.id}>{item.connectionLabel} · {item.address}</option>)}</select></Field>
    <div class="row gap"><p class="muted grow">{t("Recent sending requests remain available after closing the composer.")}</p><Button disabled={busy || loading} onClick={() => setRefresh((value) => value + 1)}>{t("Refresh")}</Button></div>
    {loading ? <Spinner /> : null}
    {!loading && !items.length ? <p class="muted">{t("No sending requests.")}</p> : null}
    {items.map((item) => <section key={item.submissionId} class="card">
      <p><b>{status(item)}</b></p>
      <p class="small">{item.messageId}</p><p class="muted small">{item.createdAt} · {item.submissionId}</p>
      <p class="small">SMTP: {statusLabel(item.smtpStatus)} · {t("Sent")}: {statusLabel(item.sentStatus)}{item.errorCode ? ` · ${item.errorCode}` : ""}{item.cleanupErrorCode ? ` · ${item.cleanupErrorCode}` : ""}</p>
      {item.status === "unknown" ? <p class="muted">{t("Check the mail server using the Message-ID and creation time before sending again.")}</p> : null}
      {item.status === "sent_copy_failed" && box?.level !== "read" ? <Button disabled={busy} onClick={() => setRetry(item)}>{t("Retry saving the sent copy")}</Button> : null}
    </section>)}
    {retry ? <Confirm title={t("Retry saving the sent copy")} text={t("Only the Sent copy is retried. SMTP acceptance is retained.")} onClose={() => setRetry(null)} onConfirm={async () => {
      setBusy(true);
      try { await post<Submission>(`/api/mail/mailboxes/${id}/submissions/${retry.submissionId}/retry-sent-copy`); setRetry(null); setRefresh((value) => value + 1); }
      catch (error) { errorToast(error); } finally { setBusy(false); }
    }} /> : null}
  </div>;
}
