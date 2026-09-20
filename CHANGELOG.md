# Changelog

**English** · [简体中文](CHANGELOG.zh-CN.md)

All notable changes to Mailhearth are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-09-20

First public release: a self-hosted business email platform that turns a
Purelymail account into a team mail product.

### Added

- **First-run setup**: create the organisation, validate the Purelymail API
  token, and import existing domains, mailboxes and routing rules without
  changing anything on the account. The import is read-only and repeatable.
- **Organisation model**: members with title, department, role and status
  (invited, active, disabled, departed); built-in owner, admin and member
  roles, plus custom roles built from fine-grained permissions; an audit log
  of administrative actions and ownership transfer.
- **Mailbox lifecycle**: personal and shared mailboxes, access grants at
  `full`, `send` and `read` level, one application password per mailbox created
  and rotated by the server, and one-step offboarding that hands a mailbox
  over, converts it to shared, keeps or locks it, forwards new mail, rotates
  every credential and removes the member from groups.
- **Addressing**: primary addresses, aliases, forwards, catch-all and prefix
  rules, and group distribution addresses that follow group membership.
- **Domains**: domain management with DNS health for MX, SPF, DKIM and DMARC,
  and copy-paste DNS records.
- **Webmail**: folders, paging, server-side search, flags, bulk actions,
  attachments, drafts with autosave, signatures and multiple sending
  identities, desktop notifications over IMAP IDLE, keyboard shortcuts, light
  and dark themes, English and Simplified Chinese.
- **Shared mailbox teamwork**: see who replied, assign a message, mark it
  resolved, and leave internal notes. Collaboration state is keyed by
  `Message-ID`, so it survives moves between folders.
- **Rules**: structured conditions and actions, plus the vacation auto-reply,
  compiled to Sieve and installed over ManageSieve so they keep working while
  nobody is signed in.
- **Security**: the Purelymail API token and every mailbox application password
  are encrypted at rest with AES-256-GCM under a key derived from the master
  key and never leave the server; argon2id password hashing, CSRF protection,
  rate-limited sign-in; message HTML sanitised server-side and rendered in a
  sandboxed, script-free iframe under a strict CSP with remote images blocked
  by default; attachments served with `nosniff` and a download disposition.
- **Deployment**: one static Go binary with the web client embedded, SQLite
  storage through the pure-Go `modernc.org/sqlite` driver, a `Dockerfile` and
  `docker-compose.yml`, and a GitHub Actions workflow that runs the test suite,
  builds the binary and publishes multi-architecture images to GHCR.
- **Development and verification**: an in-process fake of the Purelymail API,
  IMAP and SMTP with demo data (`make dev`), a screenshot script that drives
  the real interface, and an integration suite that runs against a real
  Purelymail account.

[Unreleased]: https://github.com/yll682/Mailhearth/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/yll682/Mailhearth/releases/tag/v0.1.0
