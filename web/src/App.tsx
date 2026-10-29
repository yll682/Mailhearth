import { useEffect } from "preact/hooks";
import { route, go } from "@/lib/router";
import { me, booted, loadMe, isAdmin, loadError } from "@/lib/state";
import { lang, t } from "@/lib/i18n";
import { get, type SetupStatus } from "@/lib/api";
import { Button, Spinner, Toasts } from "@/ui";
import { Login, Invite } from "@/pages/Login";
import { Setup } from "@/pages/Setup";
import { MailApp } from "@/mail/MailApp";
import { AdminApp } from "@/admin/AdminApp";
import { SettingsPage } from "@/settings/Settings";
import { signal } from "@preact/signals";

const setup = signal<SetupStatus | null>(null);

async function boot() {
  document.documentElement.lang = lang.value;
  try {
    setup.value = await get<SetupStatus>("/api/setup/status");
  } catch (e) {
    setup.value = null;
    loadError.value = e instanceof Error ? e.message : String(e);
    booted.value = true;
    return;
  }
  await loadMe();
  booted.value = true;
}

export function App() {
  useEffect(() => {
    boot();
  }, []);
  // React to language changes for the document lang attribute.
  useEffect(() => {
    document.documentElement.lang = lang.value;
  }, [lang.value]);

  if (!booted.value) {
    return (
      <div class="boot">
        <Spinner size={28} />
      </div>
    );
  }
  // A network or server failure must not look like being signed out.
  if (loadError.value && !me.value) {
    return (
      <div class="boot" role="alert">
        <p>{t("Something went wrong.")}</p>
        <p class="muted small">{loadError.value}</p>
        <Button kind="primary" onClick={() => { booted.value = false; boot(); }}>
          {t("Retry")}
        </Button>
      </div>
    );
  }
  const seg = route.value.segments;
  const first = seg[0] ?? "";
  const st = setup.value;
  const user = me.value;

  // Setup flow takes precedence until finished.
  if (st?.needsSetup && first !== "invite") {
    if (st.step !== "org" && !user) {
      // Org exists but nobody is signed in: must sign in as owner to continue.
      if (first !== "login") go("/login", true);
      return (
        <>
          <Login onDone={boot} />
          <Toasts />
        </>
      );
    }
    if (first !== "setup") go("/setup", true);
    return (
      <>
        <Setup status={st} onDone={boot} />
        <Toasts />
      </>
    );
  }

  let page;
  if (first === "invite") page = <Invite token={seg[1] ?? ""} onDone={boot} />;
  else if (!user) {
    if (first !== "login") go("/login", true);
    page = <Login onDone={boot} />;
  } else if (first === "login" || first === "setup" || first === "") {
    go(isAdmin.value && user.mailboxes.length === 0 ? "/admin" : "/mail", true);
    page = null;
  } else if (first === "admin") page = <AdminApp />;
  else if (first === "settings") page = <SettingsPage />;
  else page = <MailApp />;

  return (
    <>
      {page}
      <Toasts />
    </>
  );
}
