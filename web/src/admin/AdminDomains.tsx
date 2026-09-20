import { useState } from "preact/hooks";
import { t } from "@/lib/i18n";
import { get, post, patch, del, type Domain, type DNSGuide } from "@/lib/api";
import { toast, errorToast } from "@/lib/state";
import { fmtDate, copyText } from "@/lib/format";
import { Button, Field, Icon, useAsync, Spinner, ErrorBox, Modal, Badge, Confirm, Toggle } from "@/ui";
import { PageHead } from "./AdminOrg";

function DnsPill({ ok, label }: { ok: boolean | undefined; label: string }) {
  return <span class={"pill " + (ok === undefined ? "" : ok ? "good" : "bad")}>{ok === undefined ? null : <Icon name={ok ? "check" : "x"} size={11} />} {label}</span>;
}

export function DomainsPage() {
  const { data, error, loading, reload } = useAsync(() => get<Domain[]>("/api/admin/domains"), []);
  const [adding, setAdding] = useState(false);
  const [guideFor, setGuideFor] = useState<string | null>(null);
  const [removing, setRemoving] = useState<Domain | null>(null);
  const [busyId, setBusyId] = useState(0);
  const act = async (id: number, fn: () => Promise<unknown>) => {
    setBusyId(id);
    try {
      await fn();
      reload();
    } catch (e) {
      errorToast(e);
    } finally {
      setBusyId(0);
    }
  };
  return (
    <div class="page">
      <PageHead title={t("Domains")}>
        <Button kind="primary" icon="plus" size="sm" onClick={() => setAdding(true)}>{t("Add domain")}</Button>
      </PageHead>
      <div class="page-body">
        {loading ? <Spinner /> : error ? <ErrorBox error={error} onRetry={reload} /> : null}
        <div class="card-list">
          {(data ?? []).map((d) => (
            <div key={d.id} class={"card" + (d.status !== "active" ? " muted" : "")}>
              <div class="row gap wrap">
                <div class="grow">
                  <div class="h3 mono">{d.name} {d.isShared ? <Badge>{t("Shared Purelymail domain")}</Badge> : null} {d.status !== "active" ? <Badge tone="bad">{d.status}</Badge> : null}</div>
                  <div class="muted small">{d.mailboxCount} {t("mailboxes")} · {d.addressCount} {t("addresses")}{d.dnsCheckedAt ? ` · ${t("Checked")} ${fmtDate(d.dnsCheckedAt)}` : ""}</div>
                </div>
                {!d.isShared ? (
                  <>
                    <Button size="sm" icon="refresh" busy={busyId === d.id} onClick={() => act(d.id, () => post(`/api/admin/domains/${d.id}/recheck`))}>{t("Recheck DNS")}</Button>
                    <Button size="sm" icon="list" onClick={() => setGuideFor(d.name)}>{t("DNS records")}</Button>
                    <button class="btn btn-icon" onClick={() => setRemoving(d)} aria-label={t("Remove domain")}><Icon name="trash" size={15} /></button>
                  </>
                ) : null}
              </div>
              {!d.isShared ? (
                <>
                  <div class="row gap wrap dns-row">
                    <DnsPill ok={d.dns?.mx} label="MX" />
                    <DnsPill ok={d.dns?.spf} label="SPF" />
                    <DnsPill ok={d.dns?.dkim} label="DKIM" />
                    <DnsPill ok={d.dns?.dmarc} label="DMARC" />
                  </div>
                  <div class="row gap wrap">
                    <Toggle checked={d.allowAccountReset} label={t("Account password reset")} onChange={(v) => act(d.id, () => patch(`/api/admin/domains/${d.id}`, { allowAccountReset: v }))} />
                    <Toggle checked={d.symbolicSubaddressing} label={t("Symbolic subaddressing")} onChange={(v) => act(d.id, () => patch(`/api/admin/domains/${d.id}`, { symbolicSubaddressing: v }))} />
                  </div>
                </>
              ) : null}
            </div>
          ))}
        </div>
      </div>
      {adding ? <AddDomainModal onClose={() => setAdding(false)} onDone={reload} /> : null}
      {guideFor ? <DnsGuideModal domain={guideFor} onClose={() => setGuideFor(null)} /> : null}
      {removing ? <Confirm title={t("Remove domain")} text={t("Deletes every mailbox and rule on this domain.")} danger requireText={removing.name} confirmLabel={t("Remove domain")} onClose={() => setRemoving(null)} onConfirm={async () => { await del(`/api/admin/domains/${removing.id}`, { confirm: removing.name }); reload(); }} /> : null}
    </div>
  );
}

function DnsTable({ guide }: { guide: DNSGuide }) {
  return (
    <table class="table dns">
      <thead><tr><th>{t("Type")}</th><th>{t("Host")}</th><th>{t("Value")}</th><th>{t("Purpose")}</th></tr></thead>
      <tbody>
        {guide.records.map((r, i) => (
          <tr key={i}>
            <td class="mono">{r.type}{r.priority ? ` (${r.priority})` : ""}</td>
            <td class="mono">{r.host}</td>
            <td class="mono small">{r.value} <button class="btn btn-icon" onClick={() => { copyText(r.value); toast(t("Copied"), "success"); }} aria-label={t("Copy")}><Icon name="copy" size={12} /></button></td>
            <td>{r.purpose.toUpperCase()}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function DnsGuideModal({ domain, onClose }: { domain: string; onClose: () => void }) {
  const { data, error, loading } = useAsync(() => get<DNSGuide>(`/api/admin/domains/dns-guide?domain=${encodeURIComponent(domain)}`), [domain]);
  return (
    <Modal title={`${t("DNS records")} · ${domain}`} wide onClose={onClose}>
      {loading ? <Spinner /> : error ? <ErrorBox error={error} /> : data ? <DnsTable guide={data} /> : null}
    </Modal>
  );
}

function AddDomainModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const guide = useAsync(() => get<DNSGuide>("/api/admin/domains/dns-guide"), []);
  return (
    <Modal title={t("Add domain")} wide onClose={onClose} footer={<><Button onClick={onClose}>{t("Cancel")}</Button><Button kind="primary" busy={busy} disabled={!name.trim()} onClick={async () => { setBusy(true); try { await post("/api/admin/domains", { name }); toast(t("Domain added."), "success"); onDone(); onClose(); } catch (e) { errorToast(e); } finally { setBusy(false); } }}>{t("Add domain")}</Button></>}>
      <Field label={t("Domain")}><input value={name} onInput={(e) => setName((e.target as HTMLInputElement).value.toLowerCase())} placeholder="example.com" autoFocus /></Field>
      <p class="muted small">{t("Add these records at your DNS provider, then add the domain.")}</p>
      {guide.loading ? <Spinner /> : guide.error ? <ErrorBox error={guide.error} /> : guide.data ? <DnsTable guide={guide.data} /> : null}
    </Modal>
  );
}
