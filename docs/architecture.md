# Architecture

**English** · [简体中文](architecture.zh-CN.md) · [繁體中文](architecture.zh-TW.md) · [日本語](architecture.ja.md) · [Español](architecture.es.md)

## Deployment and components

Mailhearth runs as one Go binary with embedded Preact assets and SQLite (modernc, without cgo). Mail servers retain message bodies and provide delivery and filtering. SQLite stores organisation data, encrypted credentials, resource associations, operations, sending requests and collaboration metadata. Node is used during the frontend build.

- `cmd/mailhearth`: configuration, master key, database, IMAP pool, workers and HTTP server.
- `internal/config`, `internal/secrets`: environment configuration, AES-256-GCM/HKDF, argon2id and tokens.
- `internal/db`, `internal/model`: embedded migrations, association checks and persisted models.
- `internal/provider`: common management interface and Purelymail, Migadu and manual adapters.
- `internal/purelymail`: Purelymail API transport used by its adapter.
- `internal/core`: connections, discovery/import, organisation, resource lifecycle, Operation and Submission.
- `internal/mailproto/imappool`: bounded pooling, endpoint version invalidation and IDLE watchers.
- `internal/mailproto/mailops`, `mimeutil`, `sieve`: IMAP/SMTP, MIME sanitisation, Sieve compilation and ManageSieve.
- `internal/httpapi`, `internal/web`, `web/`: permission-checked JSON/SSE, embedded assets and the mail/admin/settings UI.

## Organisation and resource ownership

Each installation has one Organization. Members have roles, departments and states `invited`, `active`, `disabled`, `departed`. Roles contain named permissions. Personal Mailbox ownership and explicit shared Mailbox grants (`full`, `send`, `read`) control mail access; administrative permissions do not grant mail access.

A MailConnection belongs to the organisation and carries provider kind, label, management API authentication, domain scope and three protocol defaults. Domain is a logical name; DomainBinding associates it with a connection and its provider settings/DNS state. Mailbox addresses are unique within a connection, so identical addresses in different connections remain independent.

Addresses represent `primary`, `alias`, `forward`, `group`, `catchall`, `prefix` and externally managed `external_rule`. Mailbox forwarding has its own `mailbox_forwardings` row; importing it preserves the primary address. Identity display settings and SMTP sender authorization are independent. Receiving an alias does not authorize its use as From. Groups calculate all eligible personal mailboxes and validate connection/domain restrictions before deduplicating targets.

ProviderResource associates each remote reference with one local object and its purpose. It records ownership, remote state and safe typed observations. References and revision checks protect connection/organisation boundaries and historical records.

## Protocol configuration

Each Mailbox has independent IMAP, SMTP and ManageSieve endpoints. Network mode is `inherit`, `override` or `disabled`; enabled endpoints specify username and an encrypted Credential independently. Credentials are `managed` or explicitly entered. Candidates must authenticate on every enabled protocol before atomic configuration commit. Endpoint, connection, credential and access versions invalidate old connections.

Purelymail defaults: IMAP `imap.purelymail.com:993` TLS, SMTP `smtp.purelymail.com:465` TLS, ManageSieve `mailserver.purelymail.com:4190` STARTTLS. Migadu defaults: IMAP `imap.migadu.com:993` TLS, SMTP `smtp.migadu.com:465` TLS, ManageSieve disabled. Manual defaults are disabled until explicitly configured. TLS validates the hostname with system certificates or an explicit private CA bundle.

## Discovery, import and operations

Discovery reads the entire selected scope into a complete, expiring snapshot tied to a connection revision. Import checks ownership, revision, expiry and dependencies in one local transaction. Imported personal mailboxes have no owner or login credential. Sync updates registered observations and leaves new resources pending selection. It preserves owners, access grants, collaboration, signatures and entered credentials.

Forwarding imports retain the source mailbox, selected targets, remote references and `active`/`pending_confirmation`/`blocked`/`unknown` confirmation states. Targets and delivery modes are observed separately from desired settings. Unverified modes remain `unverified`; API presence or an administrator report does not prove delivery. Migadu forwarding writes remain gated by V03 verification.

Operation persists requestId, content digest, encrypted payload, resource locks and individual steps. Duplicate submissions return the same operation; revision changes and permission loss prevent stale execution. Unknown remote writes require reconciliation, and confirmed steps are not repeated. External actions record administrator reports with `systemVerified=false`. Credential responses lost without a remote ID require an explicit cleanup report. Suspension immediately revokes local access; archiving preserves history. Offboarding tracks transfer, groups and credential revocation separately.

## Mail and sending path

1. HTTP resolves current ownership/grants and the selected protocol endpoint.
2. IMAP pooling defaults to 24 global, 8 per connection and 3 per mailbox, reserving 4 global and 2 per connection slots for ordinary requests while watchers are active.
3. `mailops` lists folders, pages messages, searches and reads MIME parts using IMAP. Explicit folder mapping, unique SPECIAL-USE and unique names determine special folders.
4. `mimeutil` sanitises HTML/CSS, resolves `cid:` and blocks remote images. A dedicated document with restrictive CSP is rendered in a script-free sandboxed iframe.
5. Submission retains requestId, stable Message-ID, digest and encrypted envelope; message bodies remain in server Drafts. It records SMTP acceptance and Sent-copy state independently. Unknown delivery is not automatically retried. Sent-copy retries retain SMTP acceptance; draft cleanup requires UID EXPUNGE.
6. Shared IDLE watchers deliver updates through SSE. Collaboration uses `mid:<message-id>`; `message_state` stores assignments/status and `mail_activity` records replies, forwarding, assignment and notes across folder moves.

## Rules and frontend

Rules are structured and compiled to Sieve using advertised extensions. ManageSieve uses the mailbox's endpoint and go-managesieve. Activation checks the current script hash, requires confirmation for takeover, reads back a separate candidate and verifies activation before updating local settings. Existing scripts are retained. Rule and vacation capabilities depend on enabled endpoints and required extensions.

Preact, `@preact/signals` and the history router serve `/mail`, `/admin`, `/settings`, `/login`, `/invite/:token`, `/setup`. Layout uses three panes above 860 px and a drawer with one pane below. English source keys have zh-CN, zh-TW, Japanese and Spanish dictionaries; translation coverage and interpolation are checked with the TypeScript AST. Production builds preserve previous hashed assets for already opened clients.

Local storage/HTTP checks and builds are documented in [integration testing](integration-testing.md). Real-provider acceptance remains pending.
