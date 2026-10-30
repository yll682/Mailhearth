# System Architecture Guide

**English** · [简体中文](architecture.zh-CN.md) · [繁體中文](architecture.zh-TW.md) · [日本語](architecture.ja.md) · [Español](architecture.es.md)

## Overall Design & Subsystems

Mailhearth is architected as an efficient, self-contained monolith. The backend is written in Go, the frontend is built with Preact, and persistent storage is powered by embedded SQLite using the pure-Go `modernc.org/sqlite` driver (free of any cgo dependencies). All production web assets are statically embedded directly into the single binary via Go's `embed` mechanism.

System responsibilities are cleanly bifurcated:
- **Upstream Mail Servers**: Responsible for protocol-level network transport, message routing and delivery, physical directory structures, and storing full RFC 822 message bodies.
- **Mailhearth Core**: Operates as the management and application layer, persisting team structure, access controls, encrypted credentials, resource associations, asynchronous operations, message sending states, and team collaboration metadata.

Core subsystems across the repository:
- `cmd/mailhearth`: Main application entry point; handles configuration loading, master key derivation, database initialisation, IMAP connection pool initialization, background worker orchestration, and HTTP/SSE server binding.
- `internal/config`, `internal/secrets`: Environment configuration validation; AES-256-GCM encryption with HKDF key derivation; argon2id password hashing and session token lifecycle management.
- `internal/db`, `internal/model`: Embedded schema applied in one transaction, foreign key and tenant-scope constraints, core domain entities, and data persistence models.
- `cmd/apitypes`, `internal/apitypes`: Generates `web/src/lib/api-types.ts` from the Go response types (reflection over the `encoding/json` rules, with `tstype` tags for string unions). The web client imports only these generated types; `go test` and CI fail when the committed file is out of date.
- `internal/provider`: Unified interface abstractions for email providers, alongside dedicated adapters for Purelymail, Migadu, and generic manual IMAP/SMTP setups.
- `internal/purelymail`: Low-level API client and structured error mappings for the Purelymail management service.
- `internal/core`: Core domain services handling mail connections, resource discovery and ingestion, organizational permissions, mailbox lifecycle, persistent operations, and message submission tracking.
- `internal/mailproto/imappool`: Bounded IMAP connection pooling with strict quota enforcement, revision-triggered connection invalidation, and efficient IDLE listeners.
- `internal/mailproto/mailops`, `mimeutil`, `sieve`: Protocol-level IMAP/SMTP operations; MIME sanitization and attachment streaming; RFC-compliant Sieve script compilation and ManageSieve client operations.
- `internal/httpapi`, `internal/web`, `web/`: Session cookie authentication, RESTful JSON APIs, Server-Sent Events (SSE) streaming, embedded static assets, and the webmail/admin interfaces.

## Organizational Hierarchy & Resource Ownership

Every Mailhearth installation serves a single distinct `Organization`.

1. **Members & Roles**:
   - Team members maintain personal metadata (name, department), assigned roles, and an explicit lifecycle state (`invited`, `active`, `disabled`, `departed`).
   - Access control relies on granular named permissions (e.g., `mailboxes.manage`, `domains.manage`) grouped into roles.
   - **Security Invariant**: Administrative permissions never grant implicit access to mailbox contents. Ownership of personal mailboxes and explicit grants on shared mailboxes (`full`, `send`, `read`) are the sole authorization boundaries for reading email.

2. **Mail Connections & Domain Bindings**:
   - A `MailConnection` belongs to the organization, holding provider credentials, API tokens, allowed domain scopes, and default connection templates for the three mail protocols.
   - A `Domain` represents a logical email domain. `DomainBinding` links it to a connection, tracking provider settings and live DNS health checks (MX, SPF, DKIM, DMARC).
   - Mailbox addresses are unique strictly within their assigned connection. The same email address configured under different connections remains completely isolated.

3. **Address Topology & Sending Identities**:
   - Addresses support explicit types: `primary`, `alias`, `forward`, `group`, `catchall`, `prefix`, and externally managed `external_rule`.
   - Forwarding rules are tracked separately in `mailbox_forwardings`, preserving parent mailbox ownership upon import.
   - Identity presentation and SMTP sending rights are decoupled: having an inbound alias does not authorize sending as that address (`From:`); explicit authorization must be granted.
   - Group distribution addresses dynamically resolve all eligible personal mailboxes, validating connection and domain constraints while deduplicating targets prior to delivery.

4. **Remote Resource Mapping (`ProviderResource`)**:
   - Maps each upstream resource reference (remote mailbox, alias, rule ID) to a corresponding local object and intended purpose.
   - Governed by revision checks and relational integrity constraints to prevent cross-connection boundary leaks and preserve historical audit trails.

## Protocol Configuration & Credential Isolation

Each mailbox has independent protocol endpoints for IMAP, SMTP, and ManageSieve:
- **Network Mode**: Configurable as `inherit` (uses connection defaults), `override` (custom host/port/TLS), or `disabled`.
- **Credential Mode**: Supports `managed` (credentials generated and rotated by Mailhearth) or `entered` (manually supplied passwords).
- **Atomic Verification**: Prior to committing endpoint updates, Mailhearth executes live authentication handshakes across all enabled protocols. The configuration is committed in a single local transaction only if all handshakes succeed.
- **Connection Invalidation**: Any mutation to endpoint settings, credentials, or access rights immediately closes pooled connections, forcing subsequent requests to re-authenticate cleanly.
- **Provider Defaults**:
  - Purelymail: IMAP (`imap.purelymail.com:993` TLS), SMTP (`smtp.purelymail.com:465` TLS), ManageSieve (`mailserver.purelymail.com:4190` STARTTLS).
  - Migadu: IMAP (`imap.migadu.com:993` TLS), SMTP (`smtp.migadu.com:465` TLS), ManageSieve disabled.
  - Manual: Endpoints default to disabled until explicitly configured.
  - TLS Validation: Validates server hostnames (SNI) against host CA trust stores or explicitly configured private CA bundles.

## Resource Discovery, Import & Persistent Operations

1. **Discovery & Selective Import**:
   - Discovery scans the remote provider to produce an expiring snapshot tied to the connection's current revision.
   - Importing executes inside a single transaction, validating ownership, revision bounds, and relational dependencies. Imported personal mailboxes start without an owner or local login credential until assigned.
   - Sync safely refreshes observed states for registered resources while leaving newly found items pending. Existing owners, access grants, signatures, and entered credentials are strictly preserved.
   - Forwarding import captures upstream targets, remote references, and confirmation states (`active`, `pending_confirmation`, `blocked`, `unknown`). Unverified delivery modes remain flagged as `unverified`. Migadu forwarding requires strict delivery validation.

2. **Persistent Operation Engine**:
   - Long-running or external administrative workflows are executed as persistent `Operation` entities, tracking a unique `requestId`, content digest, encrypted payloads, resource exclusion locks, and step-by-step progress.
   - **Strict Idempotency**: Resubmitting with an identical `requestId` safely returns the existing operation without duplicate execution. Outdated operations are aborted if configuration revisions change or permissions are revoked.
   - **Ambiguity Containment**: Network timeouts or indeterminate remote responses leave the operation in an explicit verification state; verified steps are never re-executed blindly.
   - **External Operator Reports**: When steps must be completed manually in a third-party console, Mailhearth accepts administrator completion reports recorded with `systemVerified=false`. If credential creation drops without returning a remote ID, an explicit cleanup report is mandatory.
   - **Guided Offboarding**: Tracks mailbox reassignment or retention, group membership revocation, session invalidation, and external credential revocation as persistent sub-tasks.

## Mail Processing & Secure Rendering Pipeline

1. **Authorization Resolution & Connection Allocation**:
   - Incoming HTTP requests resolve caller identity, session validity, and mailbox access grants to select the active endpoint.
   - IMAP connection pooling applies three-tier limits: default 24 globally, 8 per connection, 3 per mailbox. When background IDLE watchers run, the pool guarantees 4 global and 2 per-connection slots for interactive user requests.

2. **Mailbox Sync & Special Folders**:
   - The `mailops` package executes IMAP commands for folder enumeration, paginated listing, server-side search, and MIME structure resolution.
   - Special folders are identified through a strict priority cascade: Explicit user mappings > RFC 6154 `SPECIAL-USE` flags > unique canonical folder names.

3. **Content Sandboxing**:
   - Message HTML is thoroughly sanitized server-side by `mimeutil`, stripping script tags, dangerous styles, and tracking beacons. Inlined attachments are resolved to internal `cid:` endpoints.
   - Emails render inside an isolated, scriptless sandboxed iframe enforced by a strict Content Security Policy (CSP). Remote tracking images are blocked by default until explicitly authorized.

4. **Reliable Submission Pipeline**:
   - Sending requests persist with a stable `Message-ID`, content digest, and encrypted envelope. Message bodies stage in the server's Drafts folder.
   - **Decoupled Steps**: SMTP acceptance and Sent-copy archival are tracked independently. Unknown delivery results are never automatically retried to avoid duplicate delivery. Retrying a failed Sent copy only re-executes IMAP copy/append, never re-triggering SMTP. Staged drafts are cleaned via precise UID EXPUNGE.

5. **Real-Time Collaboration**:
   - Shared mailboxes utilize IMAP IDLE watchers to broadcast updates via Server-Sent Events (SSE).
   - Collaboration metadata is anchored to `mid:<message-id>`. The `message_state` table tracks assignees and resolution statuses, while `mail_activity` logs replies, notes, and reassignment history across folder movements.

## Mail Rules & Frontend Architecture

1. **Sieve Filtering & Vacation Responders**:
   - Structured filter rules are dynamically compiled into standard Sieve scripts based on server capabilities declared via ManageSieve.
   - Before uploading new rules, Mailhearth inspects the active script's hash. Taking over an existing active script requires explicit confirmation, ensuring no unexpected script overwrites occur.

2. **Modern Single-Page Application (SPA)**:
   - Built with Preact and `@preact/signals` for reactive state, navigated via a lightweight History Router across `/mail`, `/admin`, `/settings`, `/login`, `/invite/:token`, and `/setup`.
   - Responsive Layout: Displays a productive three-pane layout on screens wider than 860px (folders, message list, reader), collapsing gracefully to a single pane with drawer navigation on mobile and compact displays.
   - **Internationalization**: Canonical English keys are mirrored across Simplified Chinese (zh-CN), Traditional Chinese (zh-TW), Japanese (ja), and Spanish (es). Automated tests parse the TypeScript AST to guarantee complete key coverage and template variable parity.
