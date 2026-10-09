import { useState } from "preact/hooks";
import { get, post, type SetupStatus, type MailConnection, type Mailbox } from "@/lib/api";
import { Button, Field, Icon, useAsync, Spinner, ErrorBox } from "@/ui";
import { LangSwitch } from "./Login";
import { go } from "@/lib/router";
import { me } from "@/lib/state";
import { ConnectionCreateForm, MailboxAttachForm } from "@/admin/AdminConnections";
import { ResourceImport } from "@/admin/ResourceImport";
import { EndpointSettings } from "@/admin/EndpointEditor";
import { t } from "@/lib/i18n";

export function Setup({ status, onDone }: { status: SetupStatus; onDone: () => Promise<void> }) {
  const [step, setStep] = useState(status.step);
  const [org, setOrg] = useState({ orgName: status.orgName ?? "", adminName: "", adminEmail: "", password: "" });
  const [connection, setConnection] = useState<MailConnection | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [bindMailboxId, setBindMailboxId] = useState<number | null>(null);
  const [attaching, setAttaching] = useState(false);
  const connections = useAsync(() => step === "org" ? Promise.resolve([]) : get<MailConnection[]>("/api/admin/connections"), [step]);
  const mailboxes = useAsync(() => step === "mailboxes" ? get<Mailbox[]>("/api/admin/mailboxes") : Promise.resolve([]), [step]);
  const current = connection;
  return <div class="auth-page"><div class="auth-card setup-card">
    <div class="brand"><img src="/favicon.svg" alt="" width={36} height={36} /><span>Mailhearth</span></div>
    <h1>{t("Set up Mailhearth")}</h1>
    <ol class="steps">{[{ key: "org", label: t("Organisation") }, { key: "connection", label: t("Mail connection") }, { key: "mailboxes", label: t("Mailboxes") }].map((item, index) => <li key={item.key} class={step === item.key ? "current" : ""}><span class="step-num">{index + 1}</span>{item.label}</li>)}</ol>
    {step === "org" && <form onSubmit={async (event) => {
      event.preventDefault(); setBusy(true); setError("");
      try { await post("/api/setup/init", org); await onDone(); setStep("connection"); } catch (error) { setError((error as Error).message); } finally { setBusy(false); }
    }}>
      <Field label={t("Organisation name")}><input required value={org.orgName} onInput={(event) => setOrg({ ...org, orgName: event.currentTarget.value })} /></Field>
      <Field label={t("Administrator name")}><input required value={org.adminName} onInput={(event) => setOrg({ ...org, adminName: event.currentTarget.value })} /></Field>
      <Field label={t("Administrator email")}><input type="email" required value={org.adminEmail} onInput={(event) => setOrg({ ...org, adminEmail: event.currentTarget.value })} /></Field>
      <Field label={t("Password")} hint={t("At least 10 characters")}><input type="password" minLength={10} required autocomplete="new-password" value={org.password} onInput={(event) => setOrg({ ...org, password: event.currentTarget.value })} /></Field>
      <Button kind="primary" type="submit" busy={busy}>{t("Create organisation")}</Button>
    </form>}
    {step === "connection" && <ConnectionCreateForm onSaved={(connection) => { setConnection(connection); setStep("mailboxes"); }} />}
    {step === "mailboxes" && <>
      {connections.loading && <Spinner />}
      {connections.error && <ErrorBox error={connections.error} onRetry={connections.reload} />}
      <Field label={t("Select a connection")}><select value={current?.id ?? ""} onChange={(event) => { setConnection(connections.data?.find((item) => item.id === Number(event.currentTarget.value)) ?? null); setBindMailboxId(null); setAttaching(false); }}>
        <option value="">{t("Select a connection")}</option>{(connections.data ?? []).filter((item) => item.enabled).map((item) => <option key={item.id} value={item.id}>{item.label} · {item.providerKind} · ID {item.id}</option>)}
      </select></Field>
      {current && <>
        {current.providerKind !== "manual" ? <ResourceImport key={current.id} connection={current} onSaved={mailboxes.reload} /> : null}
        <Button onClick={() => setAttaching(!attaching)}>{t("Register existing mailbox")}</Button>
        {attaching && <MailboxAttachForm connection={current} ownerMemberId={me.value?.member.id ?? null} onSaved={() => { setAttaching(false); mailboxes.reload(); }} />}
        <Field label={t("Administrator mailbox")}><select value={bindMailboxId ?? ""} onChange={(event) => setBindMailboxId(event.currentTarget.value ? Number(event.currentTarget.value) : null)}>
          <option value="">{t("Do not bind a mailbox")}</option>{(mailboxes.data ?? []).filter((item) => item.connectionId === current.id && item.kind === "personal").map((item) => <option key={item.id} value={item.id}>{item.address} · ID {item.id}</option>)}
        </select></Field>
        {bindMailboxId ? <EndpointSettings key={bindMailboxId} mailboxId={bindMailboxId} onSaved={mailboxes.reload} /> : null}
        <Button kind="primary" busy={busy} onClick={async () => {
          setBusy(true); setError(""); try { await post("/api/setup/complete", { connectionId: current.id, bindMailboxId }); await onDone(); setStep("done"); } catch (error) { setError((error as Error).message); } finally { setBusy(false); }
        }}>{t("Complete setup")}</Button>
      </>}
    </>}
    {step === "done" && <div><Icon name="check" size={28} /><p>{t("Setup complete")}</p><div class="row gap"><Button kind="primary" onClick={() => go("/mail")}>{t("Open mail")}</Button><Button onClick={() => go("/admin/connections")}>{t("Manage mail connections")}</Button></div></div>}
    {error && <div class="form-error">{error}</div>}
    <LangSwitch />
  </div></div>;
}
