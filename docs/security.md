# Security

**English** · [简体中文](security.zh-CN.md)

## Threat model

Mailhearth sits between staff browsers and Purelymail with an API token that
can create, delete and reset every mailbox on the account. The assets, in
order of sensitivity:

1. The Purelymail API token.
2. Mailbox app passwords held for IMAP/SMTP/Sieve access.
3. Mail content and the organisation directory.
4. Member sessions.

Adversaries considered: an attacker on the internet, a malicious or phishing
email, a departed employee, and a member who tries to read a mailbox they
were not granted. A compromised host is out of scope beyond limiting blast
radius (secrets are encrypted at rest, so a database copy alone is useless).

## Controls

**Secrets at rest.** `secrets.Box` seals values with AES-256-GCM using a key
derived (HKDF-SHA256) from the installation master key. The master key comes
from `MAILHEARTH_MASTER_KEY` or `<data>/master.key` (mode 0600, generated on
first start). Different purposes use different derived keys. The API token is
shown only as a masked hint after saving; app passwords are never shown.

**Authentication.** Member passwords are argon2id (19 MiB, t=2). Sessions are
random 256-bit tokens stored hashed (SHA-256); cookies are `HttpOnly`,
`SameSite=Lax`, `Secure` when the base URL is HTTPS, 30-day sliding expiry.
Login and invite acceptance are rate-limited per IP. Disabling or offboarding
a member revokes all their sessions immediately.

**Authorisation.** Every admin endpoint checks a permission from the member's
role; owner-only actions (transfer) require `org.owner`. Mail endpoints resolve
the mailbox through `core.ResolveMailbox`, which requires ownership or an
explicit access grant and enforces the grant level (`read` cannot flag or
send; `send` cannot delete or move). Administrative rights never imply mail
access.

**CSRF.** State-changing `/api` requests must carry `X-Requested-With:
Mailhearth` (which browsers cannot add cross-origin without CORS) and, when
an `Origin` header is present, it must match the host.

**Untrusted mail content.**
- HTML is sanitised server-side with an allow-list (bluemonday) plus a DOM
  pass that strips dangerous CSS (`expression`, `url()`, `position:fixed`),
  rewrites `cid:` images to authenticated part URLs, blocks remote images
  unless requested, and forces `target=_blank rel=noopener noreferrer` on links.
- The sanitised document is served from its own URL with
  `Content-Security-Policy: default-src 'none'; img-src 'self' data:; style-src
  'unsafe-inline'; script-src 'none'; form-action 'none'` and displayed in an
  `<iframe sandbox="allow-same-origin allow-popups …">` without `allow-scripts`.
  `allow-same-origin` is kept only so the parent can measure the height; the
  CSP guarantees no script can run inside regardless.
- The iframe is authenticated with a short-lived HMAC view token bound to the
  member, mailbox, folder and UID, so it needs no cookies.
- Attachments are served with `Content-Disposition: attachment` unless the
  type is a safe inline type (raster images, PDF, audio/video, text/plain);
  HTML, SVG and XML are never rendered inline. `X-Content-Type-Options:
  nosniff` everywhere.
- Composed HTML and signatures pass through the same sanitiser before being
  sent, so a compromised browser session cannot inject scripts into mail.
- Message and attachment sizes are capped (`MAILHEARTH_MAX_UPLOAD_MB`,
  `MAILHEARTH_MAX_MESSAGE_MB`); text parts larger than 2 MB are truncated for
  display.

**Application headers.** `X-Frame-Options: DENY`, `Referrer-Policy:
no-referrer`, a CSP for the SPA (`script-src 'self'`), immutable caching only
for hashed assets, `no-store` for API responses.

**Purelymail credentials.** One app password per mailbox. Handover,
conversion to shared, suspension and offboarding all rotate it and also reset
the Purelymail password, so phones and desktop clients configured by the
departed person stop working. The old app password is revoked upstream.

**Sieve.** Rules are compiled from a structured model with validation of
header names, sizes, addresses and flags; users cannot submit raw Sieve.

**Audit.** Every administrative change is recorded with actor, target and
detail in `audit_log`.

## Deployment recommendations

- Terminate TLS at a reverse proxy and set `MAILHEARTH_BASE_URL` to the
  HTTPS origin so cookies are `Secure`. Set `MAILHEARTH_TRUST_PROXY=true`
  only when the proxy sets `X-Forwarded-For`.
- Back up `master.key` separately from the database; store it in your
  password manager. Without it, stored credentials must be re-created (the
  product can do so: rotate every mailbox).
- Use a Purelymail API token dedicated to Mailhearth so it can be revoked
  independently.
- Enable two-factor authentication on the Purelymail account itself; the API
  token bypasses it, which is why it is the top asset above.
