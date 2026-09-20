import { useState } from "preact/hooks";
import { t } from "@/lib/i18n";
import { post, type SetupStatus, type Discovery, type ImportResult } from "@/lib/api";
import { Button, Field, Icon } from "@/ui";
import { LangSwitch } from "./Login";
import { go } from "@/lib/router";
import { me } from "@/lib/state";

export function Setup({ status, onDone }: { status: SetupStatus; onDone: () => Promise<void> }) {
  const [step, setStep] = useState<"org" | "connect" | "import" | "finished">(status.step === "done" ? "finished" : status.step);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [org, setOrg] = useState({ orgName: status.orgName ?? "", adminName: "", adminEmail: "", password: "" });
  const [token, setToken] = useState("");
  const [disc, setDisc] = useState<Discovery | null>(null);
  const [bind, setBind] = useState("");
  const [result, setResult] = useState<ImportResult | null>(null);

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError("");
    try {
      await fn();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const steps = [
    { key: "org", label: t("Your organisation") },
    { key: "connect", label: t("Connect Purelymail") },
    { key: "import", label: t("Import") },
  ];
  const idx = step === "finished" ? 3 : steps.findIndex((s) => s.key === step);

  return (
    <div class="auth-page">
      <div class="auth-card setup-card">
        <div class="brand">
          <img src="/favicon.svg" alt="" width={36} height={36} />
          <span>Mailhearth</span>
        </div>
        <h1>{t("Set up Mailhearth")}</h1>
        {status.devStack ? (
          <div class="notice">
            <Icon name="bolt" size={16} /> {t("Development stack is active: this instance talks to a fake Purelymail and an in-memory mail server.")}
          </div>
        ) : null}
        <ol class="steps">
          {steps.map((s, i) => (
            <li key={s.key} class={i < idx ? "done" : i === idx ? "current" : ""}>
              <span class="step-num">{i < idx ? <Icon name="check" size={14} /> : i + 1}</span>
              {s.label}
            </li>
          ))}
        </ol>

        {step === "org" ? (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              run(async () => {
                await post("/api/setup/init", org);
                await onDone();
                setStep("connect");
              });
            }}
          >
            <Field label={t("Organisation name")}>
              <input required value={org.orgName} onInput={(e) => { const v = (e.target as HTMLInputElement).value; setOrg((o) => ({ ...o, orgName: v })); }} autoFocus />
            </Field>
            <Field label={t("Administrator name")}>
              <input required value={org.adminName} onInput={(e) => { const v = (e.target as HTMLInputElement).value; setOrg((o) => ({ ...o, adminName: v })); }} />
            </Field>
            <Field label={t("Administrator email")} hint={t("This is your sign-in address; it can be an address on your domain.")}>
              <input type="email" required value={org.adminEmail} onInput={(e) => { const v = (e.target as HTMLInputElement).value; setOrg((o) => ({ ...o, adminEmail: v })); }} />
            </Field>
            <Field label={t("Password")} hint={t("At least 10 characters.")}>
              <input type="password" required minLength={10} autocomplete="new-password" value={org.password} onInput={(e) => { const v = (e.target as HTMLInputElement).value; setOrg((o) => ({ ...o, password: v })); }} />
            </Field>
            {error ? <div class="form-error">{error}</div> : null}
            <Button kind="primary" busy={busy} type="submit">
              {t("Create organisation")}
            </Button>
          </form>
        ) : null}

        {step === "connect" ? (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              run(async () => {
                const d = await post<Discovery>("/api/setup/connect", { apiToken: token });
                setDisc(d);
                setStep("import");
              });
            }}
          >
            <Field label={t("Purelymail API token")} hint={t("Create an API token in the Purelymail account portal (Account → API). It is stored encrypted on this server and never sent to browsers.")}>
              <input required value={token} onInput={(e) => setToken((e.target as HTMLInputElement).value)} autoFocus placeholder={status.devStack ? "dev-token" : ""} />
            </Field>
            {error ? <div class="form-error">{error}</div> : null}
            <Button kind="primary" busy={busy} type="submit">
              {busy ? t("Checking…") : t("Next")}
            </Button>
          </form>
        ) : null}

        {step === "import" ? (
          <div>
            {disc ? (
              <div class="discovery">
                <h3>{t("Found on your Purelymail account")}</h3>
                <ul class="stat-row">
                  <li>
                    <b>{disc.domains.length}</b> {t("domains")}
                  </li>
                  <li>
                    <b>{disc.users.length}</b> {t("mailboxes")}
                  </li>
                  <li>
                    <b>{disc.rules.length}</b> {t("routing rules")}
                  </li>
                  <li>
                    <b>${Number(disc.credit).toFixed(2)}</b> {t("credit")}
                  </li>
                </ul>
                <div class="chips">
                  {disc.domains.map((d) => (
                    <span key={d.name} class="chip">
                      <Icon name="globe" size={13} /> {d.name}
                    </span>
                  ))}
                </div>
                <p class="muted">{t("Importing builds the organisation model from what already exists. Nothing on Purelymail is changed.")}</p>
                <Field label={t("Connect my own mailbox")} hint={t("Choose the mailbox you use so you can read mail right away.")}>
                  <select value={bind} onChange={(e) => setBind((e.target as HTMLSelectElement).value)}>
                    <option value="">{t("None for now")}</option>
                    {disc.users.map((u) => (
                      <option key={u} value={u}>
                        {u}
                      </option>
                    ))}
                  </select>
                </Field>
              </div>
            ) : (
              <Button
                onClick={() =>
                  run(async () => {
                    const { get } = await import("@/lib/api");
                    setDisc(await get<Discovery>("/api/setup/discover"));
                  })
                }
                busy={busy}
              >
                {t("Refresh")}
              </Button>
            )}
            {error ? <div class="form-error">{error}</div> : null}
            <Button
              kind="primary"
              busy={busy}
              onClick={() =>
                run(async () => {
                  const r = await post<ImportResult>("/api/setup/complete", { bindMailbox: bind });
                  setResult(r);
                  await onDone();
                  setStep("finished");
                })
              }
            >
              {busy ? t("Importing…") : t("Import and finish")}
            </Button>
          </div>
        ) : null}

        {step === "finished" ? (
          <div>
            <div class="success">
              <Icon name="check" size={28} />
              <p>{result ? t("Imported {d} domains, {m} mailboxes and {a} addresses.", { d: result.domains, m: result.mailboxesNew + result.mailboxesKept, a: result.addressesNew + result.addressesUpdated }) : t("Done")}</p>
            </div>
            {result && result.warnings.length ? (
              <div class="notice warn">
                <b>{t("Warnings")}</b>
                <ul>
                  {result.warnings.map((w, i) => (
                    <li key={i}>{w}</li>
                  ))}
                </ul>
              </div>
            ) : null}
            <div class="row gap">
              {me.value && me.value.mailboxes.length > 0 ? (
                <Button kind="primary" onClick={() => go("/mail")}>
                  {t("Go to your mail")}
                </Button>
              ) : null}
              <Button onClick={() => go("/admin")}>{t("Open the admin console")}</Button>
            </div>
          </div>
        ) : null}
        <LangSwitch />
      </div>
    </div>
  );
}
