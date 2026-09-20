import { useEffect, useState } from "preact/hooks";
import { t, lang, setLang } from "@/lib/i18n";
import { post, get, ApiError } from "@/lib/api";
import { Button, Field, Icon, Spinner } from "@/ui";
import { go } from "@/lib/router";

function Brand() {
  return (
    <div class="brand">
      <img src="/favicon.svg" alt="" width={36} height={36} />
      <span>Mailhearth</span>
    </div>
  );
}

export function LangSwitch() {
  return (
    <div class="lang-switch">
      <button class={lang.value === "zh-CN" ? "active" : ""} onClick={() => setLang("zh-CN")}>
        中文
      </button>
      <button class={lang.value === "en" ? "active" : ""} onClick={() => setLang("en")}>
        EN
      </button>
    </div>
  );
}

export function Login({ onDone }: { onDone: () => Promise<void> }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const submit = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await post("/api/auth/login", { email, password });
      await onDone();
      go("/mail", true);
    } catch (err) {
      setError(err instanceof ApiError && err.status === 401 ? t("Incorrect email or password") : String((err as Error).message));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div class="auth-page">
      <form class="auth-card" onSubmit={submit}>
        <Brand />
        <h1>{t("Welcome back")}</h1>
        <Field label={t("Email or mailbox address")}>
          <input type="email" autocomplete="username" required value={email} onInput={(e) => setEmail((e.target as HTMLInputElement).value)} autoFocus />
        </Field>
        <Field label={t("Password")}>
          <input type="password" autocomplete="current-password" required value={password} onInput={(e) => setPassword((e.target as HTMLInputElement).value)} />
        </Field>
        {error ? <div class="form-error">{error}</div> : null}
        <Button kind="primary" busy={busy} type="submit">
          {busy ? t("Signing in…") : t("Sign in")}
        </Button>
        <LangSwitch />
      </form>
    </div>
  );
}

export function Invite({ token, onDone }: { token: string; onDone: () => Promise<void> }) {
  const [info, setInfo] = useState<{ memberName: string; loginEmail: string; orgName: string } | null>(null);
  const [error, setError] = useState("");
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    get<{ memberName: string; loginEmail: string; orgName: string }>("/api/auth/invite/" + encodeURIComponent(token)).then(
      (i) => {
        setInfo(i);
        setName(i.memberName);
      },
      (e) => setError(e instanceof ApiError && e.status === 404 ? t("This invite link is invalid or has expired.") : (e as Error).message),
    );
  }, [token]);
  const submit = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await post("/api/auth/invite/" + encodeURIComponent(token) + "/accept", { password, displayName: name });
      await onDone();
      go("/mail", true);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div class="auth-page">
      <form class="auth-card" onSubmit={submit}>
        <Brand />
        {!info && !error ? <Spinner /> : null}
        {error && !info ? <div class="form-error">{error}</div> : null}
        {info ? (
          <>
            <h1>{t("You have been invited")}</h1>
            <p class="muted">
              {info.orgName} · {info.loginEmail}
            </p>
            <p>{t("Set a password to activate your account.")}</p>
            <Field label={t("Your name")}>
              <input value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
            </Field>
            <Field label={t("Choose a password")} hint={t("At least 10 characters.")}>
              <input type="password" autocomplete="new-password" minLength={10} required value={password} onInput={(e) => setPassword((e.target as HTMLInputElement).value)} />
            </Field>
            {error ? <div class="form-error">{error}</div> : null}
            <Button kind="primary" busy={busy} type="submit">
              {t("Activate account")}
            </Button>
          </>
        ) : null}
        <LangSwitch />
      </form>
      <Icon name="shield" class="hidden" />
    </div>
  );
}
