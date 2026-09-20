import { useEffect } from "preact/hooks";
import { route, go } from "@/lib/router";
import { me, booted, loadMe, isAdmin } from "@/lib/state";
import { lang } from "@/lib/i18n";
import { get, type SetupStatus } from "@/lib/api";
import { Spinner, Toasts } from "@/ui";
import { Login, Invite } from "@/pages/Login";
import { Setup } from "@/pages/Setup";
import { MailApp } from "@/mail/MailApp";
import { AdminApp } from "@/admin/AdminApp";
import { SettingsPage } from "@/settings/Settings";
import { signal } from "@preact/signals";

const setup = signal<SetupStatus | null>(null);

async function boot() {
  document.documentElement.lang = lang.value;
  const st = await get<SetupStatus>("/api/setup/status").catch(() => null);
  setup.value = st;
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
