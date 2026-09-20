# Architecture

**English** · [简体中文](architecture.zh-CN.md)

## Goals and non-goals

Mailhearth wraps Purelymail's reliable, inexpensive mail infrastructure into
a product a small organisation can deploy, administer and use daily. It
deliberately does **not** run an SMTP server, store the primary copy of mail,
filter spam, or implement DLP / eDiscovery / MDM. It is also not a re-skin of
the Purelymail portal or another Roundcube: the unit of administration is a
person in an organisation, not a "user" on an account.

Constraints that shaped the design:

- **Tiny hosts.** Target deployments run on the cheapest VPS available. The
  server is a single static Go binary with SQLite, an in-process bounded IMAP
  connection pool, and a 51 KB (gzipped) Preact front end. No Redis, no
  Postgres, no Node at runtime, no background workers beyond a few goroutines.
- **Purelymail is the source of truth for mail.** Messages are never copied
  into Mailhearth's database. Everything the client shows is fetched over
  IMAP on demand; the database only holds the organisation model and small
  collaboration metadata keyed by `Message-ID`.
- **No secrets in the browser.** The API token and mailbox app passwords live
  encrypted in SQLite. The browser talks only to Mailhearth.

## Components

| Package | Responsibility |
|---|---|
| `cmd/mailhearth` | Entry point: config, master key, database, IMAP pool, HTTP server |
| `internal/config` | Environment configuration |
| `internal/db` | SQLite (modernc, no cgo) and embedded migrations |
| `internal/secrets` | AES-256-GCM box (HKDF from the master key), argon2id, tokens |
| `internal/purelymail` | Typed API client; `fake/` is an in-memory Purelymail |
| `internal/model` | Organisation types shared by the services and the API |
| `internal/core` | Services: setup/import, members, roles, domains, mailboxes, addresses, groups, offboarding, team state |
| `internal/mailproto/imappool` | Bounded IMAP connection pool and IDLE watchers |
| `internal/mailproto/mailops` | Folders, listing, rendering, actions, compose, SMTP |
| `internal/mailproto/mimeutil` | HTML sanitiser, text/HTML conversion, decoding |
| `internal/mailproto/sieve` | Rule model to Sieve compiler; ManageSieve client |
| `internal/httpapi` | JSON API, sessions, CSRF, uploads, SSE, sandboxed message view |
| `internal/web` | Embedded SPA with gzip and immutable caching |
| `internal/devstack` | Fake Purelymail, IMAP and SMTP for development and tests |
| `web/` | Preact + Vite single-page app (mail, admin, settings) |

## Organisation model

| Concept | Meaning | Backed by |
|---|---|---|
| **Organization** | The single tenant of an installation. | `organizations` |
| **Member** | A real person who signs in to Mailhearth. Has a role, status (invited/active/disabled/departed), title, department. | `members` |
| **Role** | Named set of permissions (`members.manage`, `shared.manage`, …). Built-in: owner, admin, member; custom roles allowed. | `roles` |
| **Domain** | A domain on the Purelymail account, with DNS health. | `domains` ↔ Purelymail domain |
| **Mailbox** | A login account that stores mail. `personal` (owned by one member) or `shared` (owned by the organisation, worked by several members). | `mailboxes` ↔ Purelymail user |
| **Address** | Something that receives mail: the mailbox's own address (`primary`), an `alias` to one mailbox, a `forward` to arbitrary targets, a `group` distribution address, a `catchall` or `prefix` rule. | `addresses` ↔ Purelymail routing rule |
| **Identity** | A From address + display name + signature a mailbox may send as. | `identities` |
| **Group** | A set of members, optionally with a distribution address whose targets follow membership. | `groups`, `group_members` |
| **Access grant** | Member → mailbox with level `full`/`send`/`read`. | `mailbox_access` |

A member can own several mailboxes; a mailbox can have several addresses; an
address can reach several members (via a forward or group). When people
change roles or leave, mailboxes and addresses stay with the organisation:
ownership is reassigned, never deleted implicitly.

## Mapping onto Purelymail

| Mailhearth action | Purelymail API calls |
|---|---|
| Connect | `checkAccountCredit` (validates token) |
| Import / sync | `listDomains`, `listUser`, `listRoutingRules` — read-only, idempotent |
| Create mailbox | `createUser` (random password, no welcome mail) + `createAppPassword` |
| Connect imported mailbox | `createAppPassword` (the existing password is never needed) |
| Rotate credential | `createAppPassword` then `deleteAppPassword` of the old one |
| Reset password for external clients | `modifyUser{newPassword}` + rotate |
| Suspend / offboard lock-out | `modifyUser{newPassword}` + `deleteAppPassword` |
| Alias / forward / catch-all / prefix / group address | `createRoutingRule` / `deleteRoutingRule` |
| Forwarding on a mailbox | routing rule on the mailbox's own address (Purelymail semantics: rule wins over delivery) |
| Domain add / DNS recheck / settings | `addDomain`, `updateDomainSettings`, `getOwnershipCode` |

Mailhearth holds exactly one app password per mailbox, named "Mailhearth".
Members never see it; the server uses it for IMAP, SMTP and ManageSieve on
their behalf, after checking that the member owns the mailbox or has a grant.
Admin permissions do not grant mail access: reading a shared mailbox always
needs an explicit grant.

## Mail path

1. `httpapi` resolves the mailbox for the signed-in member
   (`core.ResolveMailbox`) and gets a credential.
2. `imappool.Get` returns a pooled connection (max `MAILHEARTH_IMAP_MAX_CONNS`
   in total, 2 idle per credential, reaped after 90 s idle).
3. `mailops` runs the IMAP commands: `LIST` with `LIST-STATUS` for folders,
   sequence-range `FETCH` of envelope + flags + `BODYSTRUCTURE` for paging,
   `UID SEARCH` for queries, `BODY.PEEK[part]` for rendering, `MOVE`/`UIDPLUS`
   where available with fallbacks.
4. HTML bodies pass through `mimeutil.SanitizeHTML` (bluemonday allow-list,
   CSS scrubbing, `cid:` resolution, remote-image blocking) and are served in a
   separate document with `default-src 'none'` CSP, shown in a sandboxed iframe.
5. Sending builds RFC 5322 with go-message, submits via SMTP with the same
   credential, then appends to Sent and marks the original answered/forwarded.
6. IDLE watchers (one per mailbox+folder, shared by all open tabs) push
   changes to browsers over Server-Sent Events.

## Shared mailbox collaboration

Collaboration state is keyed by `mid:<message-id>` so it survives moves
between folders. `message_state` holds assignee and open/resolved status;
`mail_activity` is an append-only log (replied, forwarded, assigned, note …)
written both by explicit team actions and automatically when a member sends
from the shared mailbox. The message list decorates rows with "replied by",
assignee and status badges.

## Rules

Members edit rules as structured conditions/actions. `sieve.Compile` turns
them (plus the vacation auto-reply) into a Sieve script with the extensions
Purelymail advertises (`fileinto imap4flags copy body vacation`). The script is
uploaded as `mailhearth` over ManageSieve (`mailserver.purelymail.com:4190`,
STARTTLS) and activated. The structured form is kept in `mailboxes.settings_json`.

## Front end

Preact + `@preact/signals`, a 60-line history router, no UI framework. Routes:
`/mail/:mailbox/:folder/:uid`, `/admin/:section/:id`, `/settings/:tab`,
`/login`, `/invite/:token`, `/setup`. Strings are English keys with a zh-CN
dictionary. The layout is a three-pane mail client above 860 px and a
drawer + single pane below.
