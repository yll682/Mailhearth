# Security Standards & Threat Mitigation Guide

**English** · [简体中文](security.zh-CN.md) · [繁體中文](security.zh-TW.md) · [日本語](security.ja.md) · [Español](security.es.md)

## Threat Model & Protected Assets

Architecturally, Mailhearth sits between staff browsers and upstream mail providers (Purelymail, Migadu, or custom mail servers). As a centralized platform for organizational administration and daily email communication, the system categorizes and defends digital assets in order of sensitivity:

1. **Provider Management API Credentials**: Possess full administrative authority over upstream domains, accounts, and server-side routing rules;
2. **Mailbox Protocol Credentials**: Passwords and application-specific credentials used for IMAP, SMTP, and ManageSieve operations;
3. **Email Data & Organizational Directory**: Historical messages, drafts, internal shared-mailbox notes, and team rosters;
4. **Member Active Sessions**: Active browser sessions, cached profile state, and temporary authentication tokens.

### Threat Landscape & Defense Perimeter
Mailhearth implements defense-in-depth across the following primary threat vectors:
- **Public Internet Attackers**: Attempting brute-force password guessing, Cross-Site Request Forgery (CSRF), or API parameter tampering;
- **Malicious & Phishing Senders**: Injecting malicious JavaScript (XSS) via crafted HTML email bodies, exploiting CSS phishing overlays, or tracking user reading habits via remote image beacons;
- **Departed Teammates & Unauthorized Internal Users**: Attempting to retain unrevoked access or inspect personal mailboxes and confidential shared mailboxes without explicit authorization;
- **Malicious External Clients**: Attempting unauthorized retrieval or interception of third-party application passwords.

> **Scope Note**: Complete physical server compromise or root operating system breaches lie outside the software boundary. However, Mailhearth strictly limits the blast radius through **encryption at rest**—even if an attacker steals a raw database snapshot, all stored credentials remain entirely unreadable without the separate `master.key`.

## Core Security Controls

### 1. Encryption at Rest (Secrets at Rest)
- **Cryptographic Standard**: Implemented via the `internal/secrets` package using industry-standard authenticated AES-256-GCM.
- **Key Derivation Hierarchy**: The installation master key (loaded from `MAILHEARTH_MASTER_KEY` or read from `<data>/master.key` with strict `0600` permissions) is expanded using HKDF-SHA256 into isolated, context-specific subkeys.
- **Zero Plaintext Leakage**: Routine API responses never reveal stored credentials, displaying only masked placeholders.
- **One-Time Claim Flow**: When new application passwords are generated for external clients, authorized administrators must explicitly retrieve them through an expiring, one-time claim channel. Responses enforce `Cache-Control: no-store`, and the plaintext secret is permanently purged from server memory immediately after retrieval.

### 2. Authentication & Session Hardening
- **Modern Password Hashing**: Member passwords are hash-protected using RFC 9106 argon2id (`m=19MiB, t=2`), providing robust defense against offline GPU/ASIC brute-force cracking.
- **High-Entropy Sessions**: Sessions utilize 256-bit cryptographically secure pseudorandom tokens stored exclusively as SHA-256 hashes. Cookies enforce `HttpOnly`, `SameSite=Lax`, and `Secure` (over HTTPS) with a 30-day sliding expiration window (`MAILHEARTH_SESSION_DAYS`), capped by an absolute lifetime of 90 days (`MAILHEARTH_SESSION_MAX_DAYS`). Changing one's own password signs out every other session.
- **Brute-Force Rate Limiting**: Multi-tiered IP and account rate limits protect login endpoints and invite redemption flows. Client addresses are read from `X-Forwarded-For` only when the request comes from a proxy listed in `MAILHEARTH_TRUSTED_PROXIES`.
- **Instant Offboarding Invalidation**: Disabling (`disabled`) or offboarding (`departed`) a member immediately purges all active sessions in a single database transaction.

### 3. Granular Authorization & Mailbox Isolation
- **Orthogonal Separation of Privileges**: Administrative permissions (e.g., domain configuration, member onboarding) are strictly segregated from email access rights. Being an administrator does not grant implicit rights to read personal inboxes.
- **Management Hierarchy**: Managers can only act on members whose permissions are a subset of their own (password resets, invites, profile and role changes, status changes, offboarding, deletion), and can only grant permissions they hold. The owner account can only be managed by the owner.
- **Strict Authorization Boundary**: All mail operations pass through `core.ResolveMailbox`, verifying explicit ownership or shared mailbox access tiers (`read`: view-only, cannot flag or send; `send`: read and compose, but no delete/move; `full`: unrestricted management).

### 4. Robust CSRF Defenses
- All state-mutating requests (POST, PUT, PATCH, DELETE) require the custom header `X-Requested-With: Mailhearth`. Under standard browser security policies, cross-origin sites cannot attach custom headers without preflight CORS approval.
- When an `Origin` header is present, Mailhearth validates that it matches the host of the request (or `X-Forwarded-Host` when the request comes from a trusted proxy).

### 5. Untrusted Email Content Sandboxing
Email represents the most volatile source of untrusted content. Mailhearth enforces a multi-layered sanitization pipeline:
- **Server-Side Allow-List Sanitization**: Email HTML is filtered through `bluemonday` tag allow-lists, followed by a second DOM pass that strips high-risk CSS (`expression`, `url()`, `position: fixed`), rewrites `cid:` inline assets to authenticated endpoints, and forces `target="_blank" rel="noopener noreferrer"` on all links.
- **Default Remote Image Blocking**: Remote images are blocked by default to neutralize tracking pixels and IP reconnaissance, loading only upon explicit user request.
- **Scriptless Sandboxed Rendering**: Sanitized HTML is served from an isolated URL with strict headers:
  ```http
  Content-Security-Policy: default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; script-src 'none'; form-action 'none'
  ```
  and rendered inside an `<iframe sandbox="allow-same-origin allow-popups ...">` **strictly omitting `allow-scripts`**. Even if an obscure browser parser vulnerability exists, script execution remains fundamentally blocked.
- **Cookie-Free HMAC View Tokens**: The iframe authenticates using short-lived HMAC view tokens bound to the member, mailbox, folder, and message UID, eliminating cookie exposure.
- **Attachment Disposition**: Attachments are delivered with `Content-Disposition: attachment` and `X-Content-Type-Options: nosniff`. HTML, SVG, and XML files are strictly barred from inline rendering.
- **Symmetric Composer Sanitization**: Outgoing HTML and signatures pass through the identical sanitization pipeline before SMTP dispatch, preventing compromised browser extensions from injecting malicious scripts into outbound email.

### 6. Security Header Baseline
- Clickjacking Defense: `X-Frame-Options: DENY`;
- Referrer Privacy: `Referrer-Policy: no-referrer`;
- Web Client CSP: Application shell restricted to `script-src 'self'`;
- API Cache Prevention: All `/api/` endpoints send `Cache-Control: no-store`.

### 7. Protocol TLS & Certificate Validation
- Every enabled IMAP, SMTP, and ManageSieve connection validates remote certificates and server hostnames (SNI) against host root stores or custom private CA bundles.
- Mailhearth provides **no option** to bypass TLS validation or ignore certificate mismatches.

### 8. Immutable Audit Trail
- All administrative operations (member onboarding, role updates, connection changes, credential rotations) are permanently recorded in `audit_log`, capturing actor IDs, client IPs, timestamps, and contextual change diffs.

## Deployment Security Best Practices

1. **Enforce HTTPS with Reverse Proxies**: Terminate TLS at your reverse proxy (Caddy or Nginx) and configure `MAILHEARTH_BASE_URL` with an `https://` prefix to enforce `Secure` cookies. Enable `MAILHEARTH_TRUST_PROXY=true` only when the reverse proxy is verified and trusted.
2. **Isolate `master.key`**: Never store `/data/master.key` alongside database backups in generic storage. Keep `master.key` in an enterprise password manager (e.g., 1Password, Bitwarden) or hardware security module.
3. **Use Dedicated Provider API Tokens**: Create dedicated API credentials specifically for Mailhearth within Purelymail or Migadu, avoiding shared tokens to ensure clean revocation boundaries.
4. **Enable Two-Factor Authentication (2FA)**: Always enable hardware or TOTP 2FA on your primary provider account to safeguard upstream infrastructure.
