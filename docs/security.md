# Security

**English** · [简体中文](https://github.com/yll682/Mailhearth/blob/main/docs/security.zh-CN.md) · [繁體中文](https://github.com/yll682/Mailhearth/blob/main/docs/security.zh-TW.md) · [日本語](https://github.com/yll682/Mailhearth/blob/main/docs/security.ja.md) · [Español](https://github.com/yll682/Mailhearth/blob/main/docs/security.es.md)

## Threat model

Mailhearth sits between staff browsers and Purelymail, Migadu or manual mail servers. Management credentials can affect account resources; protocol credentials access individual mailboxes. The assets, in order of sensitivity:

1. Each connection's management API credentials.
2. Independent mailbox passwords for IMAP/SMTP/ManageSieve.
3. Mail content and the organisation directory.
4. Member sessions.

Adversaries considered: an attacker on the internet, a malicious or phishing email, a departed employee, and a member who tries to read a mailbox they were not granted. A compromised host is out of scope beyond limiting blast radius (secrets are encrypted at rest, so a database copy alone is useless).

## Controls

**Secrets at rest.** `secrets.Box` seals values with AES-256-GCM using a key derived (HKDF-SHA256) from the installation master key. The master key comes from `MAILHEARTH_MASTER_KEY` or `<data>/master.key` (mode 0600, generated on first start). Different purposes use different derived keys. Saved API and protocol credentials are returned only as masked metadata. A newly generated external-client password can be claimed explicitly once by an authorized administrator before expiry; the claim response uses `Cache-Control: no-store` and removes the claimable secret.

**Authentication.** Member passwords are argon2id (19 MiB, t=2). Sessions are random 256-bit tokens stored hashed (SHA-256); cookies are `HttpOnly`, `SameSite=Lax`, `Secure` when the base URL is HTTPS, 30-day sliding expiry. Login and invite acceptance are rate-limited per IP. Disabling or offboarding a member revokes all their sessions immediately.

**Authorisation.** Every admin endpoint checks a permission from the member's role; owner-only actions (transfer) require `org.owner`. Mail endpoints resolve the mailbox through `core.ResolveMailbox`, which requires ownership or an explicit access grant and enforces the grant level (`read` cannot flag or send; `send` cannot delete or move). Administrative rights never imply mail access.

**CSRF.** State-changing `/api` requests must carry `X-Requested-With: Mailhearth` (which browsers cannot add cross-origin without CORS) and, when an `Origin` header is present, it must match the host.

**Untrusted mail content.**
- HTML is sanitised server-side with an allow-list (bluemonday) plus a DOM pass that strips dangerous CSS (`expression`, `url()`, `position:fixed`), rewrites `cid:` images to authenticated part URLs, blocks remote images unless requested, and forces `target=_blank rel=noopener noreferrer` on links.
- The sanitised document is served from its own URL with `Content-Security-Policy: default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; script-src 'none'; form-action 'none'` and displayed in an `<iframe sandbox="allow-same-origin allow-popups …">` without `allow-scripts`. `allow-same-origin` is kept only so the parent can measure the height; the CSP guarantees no script can run inside regardless.
- The iframe is authenticated with a short-lived HMAC view token bound to the member, mailbox, folder and UID, so it needs no cookies.
- Attachments are served with `Content-Disposition: attachment` unless the type is a safe inline type (raster images, PDF, audio/video, text/plain); HTML, SVG and XML are never rendered inline. `X-Content-Type-Options: nosniff` everywhere.
- Composed HTML and signatures pass through the same sanitiser before being sent, so a compromised browser session cannot inject scripts into mail.
- Message and attachment sizes are capped (`MAILHEARTH_MAX_UPLOAD_MB`, `MAILHEARTH_MAX_MESSAGE_MB`); text parts larger than 2 MB are truncated for display.

**Application headers.** `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, a CSP for the SPA (`script-src 'self'`), immutable caching only for hashed assets, `no-store` for API responses.

**Connections and credentials.** Management authentication is separate from protocol authentication. Candidate settings authenticate all enabled endpoints before commit. Connection, endpoint, credential and access revisions invalidate old pooled connections and requests. Suspension revokes local access immediately and preserves saved credentials. Managed rotation records creation, validation, commit and revocation separately. Entered credentials and external clients require explicit provider-side handling. A remote revocation is confirmed only by the relevant verification; administrator reports use `systemVerified=false`. Losing a credential-creation response without a remote ID requires a cleanup report and does not trigger automatic creation of another credential.

**Association and concurrency.** Database constraints validate organisation, connection, mailbox and credential ownership. requestId, resource locks and expectedRevision protect operations; permissions are checked at queueing, execution and control. Historical mailboxes remain archived when deletion would remove sending or collaboration records. Domain scope controls management and discovery, while mail access retains separate checks.

**TLS and authorization.** Every enabled protocol validates hostname and certificates, using system trust or an explicitly configured private CA. No protocol has an insecure certificate option. Identity display settings and sender authorization are independent; an alias, import or forwarding registration does not authorize SMTP From.

**Sending and remote observations.** Submission encrypts its envelope and retains stable identifiers. SMTP and Sent results are separate; unknown delivery requires investigation and a new explicit request for resending. Forwarding import preserves confirmation states and unverified delivery modes. API presence and external reports do not verify delivery.

**Sieve.** Rules are compiled from a structured model with validation of header names, sizes, addresses and flags; users cannot submit raw Sieve. ManageSieve requires advertised extensions, verifies the active-script hash, reads back a separate candidate and checks activation. Taking over an active script requires confirmation; existing scripts are retained. Ambiguous authentication refusal remains unverified.

**Audit.** Every administrative change is recorded with actor, target and detail in `audit_log`.

## Deployment recommendations

- Terminate TLS at a reverse proxy and set `MAILHEARTH_BASE_URL` to the HTTPS origin so cookies are `Secure`. Set `MAILHEARTH_TRUST_PROXY=true` only when the proxy sets `X-Forwarded-For`.
- Back up `master.key` separately from the database; store it in your password manager. Without it, stored credentials must be re-created (the appropriate entered or managed flow must be completed and verified).
- Use management API credentials dedicated to Mailhearth so they can be revoked independently.
- Enable two-factor authentication on the provider account itself; the API token bypasses it, which is why it is the top asset above.
