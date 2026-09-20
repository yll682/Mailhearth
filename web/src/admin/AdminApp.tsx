import { route, go } from "@/lib/router";
import { me, can, logout } from "@/lib/state";
import { t } from "@/lib/i18n";
import { Icon, Avatar, Menu } from "@/ui";
import { OverviewPage, ConnectionPage, AuditPage, RolesPage } from "./AdminOrg";
import { MembersPage, MemberDetail } from "./AdminMembers";
import { MailboxesPage, MailboxDetail } from "./AdminMailboxes";
import { AddressesPage, GroupsPage } from "./AdminAddresses";
import { DomainsPage } from "./AdminDomains";
import { useState } from "preact/hooks";

const NAV = [
  { key: "overview", label: "Overview", icon: "home", perm: "members.manage" },
  { key: "members", label: "Members", icon: "users", perm: "members.manage" },
  { key: "mailboxes", label: "Mailboxes", icon: "mail", perm: "members.manage" },
  { key: "addresses", label: "Addresses", icon: "at", perm: "members.manage" },
  { key: "groups", label: "Groups", icon: "layers", perm: "members.manage" },
  { key: "domains", label: "Domains", icon: "globe", perm: "domains.manage" },
  { key: "roles", label: "Roles", icon: "key", perm: "org.manage" },
  { key: "audit", label: "Audit log", icon: "activity", perm: "audit.read" },
  { key: "connection", label: "Connection", icon: "bolt", perm: "org.manage" },
];

export function AdminApp() {
  const segs = route.value.segments; // admin / section / id
  const section = segs[1] ?? "overview";
  const id = segs[2];
  const [drawer, setDrawer] = useState(false);
  const nav = NAV.filter((n) => can(n.perm));
  if (nav.length === 0) {
    go("/mail", true);
    return null;
  }
  if (!nav.some((n) => n.key === section)) {
    go("/admin/" + nav[0].key, true);
    return null;
  }
  let page;
  switch (section) {
    case "members":
      page = id ? <MemberDetail id={Number(id)} /> : <MembersPage />;
      break;
    case "mailboxes":
      page = id ? <MailboxDetail id={Number(id)} /> : <MailboxesPage />;
      break;
    case "addresses":
      page = <AddressesPage />;
      break;
    case "groups":
      page = <GroupsPage />;
      break;
    case "domains":
      page = <DomainsPage />;
      break;
    case "roles":
      page = <RolesPage />;
      break;
    case "audit":
      page = <AuditPage />;
      break;
    case "connection":
      page = <ConnectionPage />;
      break;
    default:
      page = <OverviewPage />;
  }
  return (
    <div class={"app-shell admin-shell" + (drawer ? " drawer-open" : "")}>
      <aside class="sidebar">
        <div class="sidebar-top brand-row">
          <a href="/mail" class="brand small">
            <img src="/favicon.svg" alt="" width={26} height={26} />
            <span>{me.value?.org.name}</span>
          </a>
          <button class="btn btn-icon hide-desktop" onClick={() => setDrawer(false)} aria-label={t("Close")}>
            <Icon name="x" />
          </button>
        </div>
        <nav class="folders">
          {nav.map((n) => (
            <a key={n.key} href={"/admin/" + n.key} class={"folder" + (section === n.key ? " active" : "")} onClick={() => setDrawer(false)}>
              <Icon name={n.icon} size={16} />
              <span class="folder-name">{t(n.label)}</span>
            </a>
          ))}
          <div class="section-label">{t("Mail")}</div>
          <a href="/mail" class="folder">
            <Icon name="inbox" size={16} />
            <span class="folder-name">{t("Go to your mail")}</span>
          </a>
        </nav>
        <div class="sidebar-bottom">
          <Menu
            button={
              <button class="user-btn">
                <Avatar name={me.value!.member.displayName} size={28} />
                <span class="user-name">{me.value!.member.displayName}</span>
                <Icon name="down" size={14} />
              </button>
            }
            items={[
              { label: t("Settings"), icon: "settings", onClick: () => go("/settings") },
              { label: t("Sign out"), icon: "logout", onClick: logout },
            ]}
          />
        </div>
      </aside>
      <div class="drawer-backdrop" onClick={() => setDrawer(false)} />
      <main class="admin-main">
        <button class="btn btn-icon hide-desktop drawer-btn" onClick={() => setDrawer(true)} aria-label={t("Admin")}>
          <Icon name="menu" />
        </button>
        {page}
      </main>
    </div>
  );
}
