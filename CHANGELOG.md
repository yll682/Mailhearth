# Changelog

**English** · [简体中文](CHANGELOG.zh-CN.md) · [繁體中文](CHANGELOG.zh-TW.md) · [日本語](CHANGELOG.ja.md) · [Español](CHANGELOG.es.md)

All notable changes to Mailhearth are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project adheres to [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added & Improved

- **Expanded Localization**: Full frontend and administrative interface translations for Traditional Chinese (Taiwan), Japanese, and Spanish, selectable from the login screen and Settings. Dictionary coverage and template variables are validated to 100% via TypeScript AST tests.
- **Multi-Provider Architecture**: Added support for Migadu and generic manual IMAP/SMTP mail servers, enabling independent per-connection management credentials and endpoint defaults.
- **Orphaned Credential Reconciliation**: Introduced offline cleanup reporting and administrative reconciliation workflows for interrupted credential provisioning, eliminating dangling tasks.
- **Comprehensive Multilingual Documentation Rewrite**: Complete human-centered overhaul of Architecture, Operations, Security, Integration Testing, and Project Guides, ensuring complete structural and factual parity across all five languages.

## [0.1.0] - 2026-09-20

First public release: a self-hosted business email platform that transforms upstream email provider accounts into a polished, team-collaborative email product.

### Added Features

- **First-Run Setup Wizard**: Create the initial organization administrator, validate provider API tokens, and discover existing domains, mailboxes, and routing rules with read-only, non-destructive import.
- **People-First Organizational Model**: Manage members with titles, departments, roles, and explicit lifecycle states (invited, active, disabled, departed); built-in owner, admin, and member roles alongside customizable granular permissions; immutable administrative audit logs and organizational ownership transfer.
- **Mailbox Lifecycle Management**: Provision personal and shared mailboxes with granular access tiers (Full Access, Send & Read, Read Only); server-side generation and rotation of application passwords; one-step offboarding workflows that reassign, archive, forward mail, revoke active sessions, and clean up group memberships.
- **Flexible Address Topology**: Native support for primary addresses, aliases, forwarding, catch-all rules, prefix routing, and dynamic distribution groups synchronized with team rosters.
- **Domain Management & DNS Health**: Manage multiple custom domains with visual DNS status indicators (MX, SPF, DKIM, DMARC) and copy-paste DNS record values.
- **Modern Responsive Webmail Client**: System and custom folders, paginated browsing, instant server-side search, flags, bulk operations, attachment previews and chunked downloads, autosaved drafts, rich text editing, custom signatures, multiple sending identities, IMAP IDLE desktop push notifications, global keyboard shortcuts, and dark/light themes.
- **Shared Mailbox Collaboration**: Real-time triage context—view which teammate replied, assign owners, mark conversations resolved, and append internal private notes; collaboration state is anchored to the message's `Message-ID`, surviving folder moves.
- **Server-Side Rules & Auto-Reply**: Create visual filtering rules and vacation responders compiled to RFC-compliant Sieve scripts and deployed via ManageSieve, running server-side even when members are offline; explicit confirmation before taking over existing active scripts.
- **Defense-in-Depth Security**: All API tokens and mailbox passwords encrypted at rest using AES-256-GCM derived from the installation master key; argon2id password hashing, CSRF enforcement, and login rate limiting; server-side HTML sanitization rendered in a scriptless, sandboxed iframe under strict CSP with remote images blocked by default; attachments served with `nosniff` and safe download disposition.
- **Lightweight Monolithic Deployment**: Single Go binary embedding all static web assets, pure-Go SQLite storage via `modernc.org/sqlite`, accompanying `Dockerfile` and `docker-compose.yml`, and CI workflows publishing multi-arch images.
- **Developer & Testing Tooling**: In-process mock email provider with pre-seeded demo fixtures (`make dev`), browser UI screenshot scripts, and automated integration test suites for real email providers.

[Unreleased]: https://github.com/yll682/Mailhearth/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/yll682/Mailhearth/releases/tag/v0.1.0
