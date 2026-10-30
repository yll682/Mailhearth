# Operations & Deployment Guide

**English** · [简体中文](operations.zh-CN.md) · [繁體中文](operations.zh-TW.md) · [日本語](operations.ja.md) · [Español](operations.es.md)

## Capacity Planning & Sizing

Mailhearth is engineered with a tiny operational footprint, allowing it to run smoothly even on entry-level, budget VPS instances.

| Dimension | Typical Resource Usage | Operational Notes |
|---|---|---|
| Binary Footprint | ~25 MB static file | Zero cgo dependencies; no runtime library or VM requirements |
| Idle Resident Memory (RSS) | 25–40 MB | Protected by Go's soft memory limit `GOMEMLIMIT=160MiB` |
| Busy Resident Memory (10 concurrent users) | 60–120 MB | Memory is predominantly consumed by IMAP sequence fetch buffers and MIME parsing |
| CPU Utilization | Near 0% under idle/normal use | Argon2id key derivation on user login is the only computationally intensive operation |
| Disk Storage Growth | Grows only with team metadata | SQLite stores organization hierarchy, permissions, audit history, and internal notes; RFC 822 email bodies stay on upstream servers |
| Web Client Bundle | ~100 KB gzipped | Full webmail and admin console with content-hashed assets for aggressive caching |

### IMAP Connection Pool Tuning

Tune the global connection pool using the `MAILHEARTH_IMAP_MAX_CONNS` environment variable (default: 24). A practical sizing guideline: `estimated concurrent active users × 2 + total mailboxes kept open across tabs`.

The built-in connection pool enforces strict hierarchical quotas:
- **Global Concurrency Ceiling**: Defaults to 24 connections across all providers;
- **Per-Connection Limit**: A single mail provider connection can consume at most 8 simultaneous IMAP connections;
- **Per-Mailbox Limit**: A single mailbox can consume at most 3 simultaneous IMAP connections;
- **IDLE Watcher Reservation**: Background IMAP IDLE push listeners run on reserved quotas, guaranteeing that at least 4 global slots and 2 per-connection slots remain available for interactive user requests.

> **Note**: Always verify that your connection settings adhere to your upstream mail provider's (e.g., Purelymail or Migadu) connection rate limits per IP address.

## Configuration Reference

All options are environment variables. Malformed or out-of-range values stop startup with an error naming the variable; adjusted values are logged as warnings, and the effective configuration (without secrets) is logged at startup.

| Variable | Default | Purpose |
| --- | --- | --- |
| `MAILHEARTH_BASE_URL` | empty | Public URL, used for invite links. An `https://` URL enables `Secure` cookies. |
| `MAILHEARTH_LISTEN` | `:8080` | HTTP listen address. |
| `MAILHEARTH_DATA_DIR` | `./data` (`/data` in the container) | SQLite database, master key and uploads. |
| `MAILHEARTH_MASTER_KEY` | generated into `<data>/master.key` | 32-byte key (base64 or hex) that encrypts stored credentials. |
| `MAILHEARTH_SETUP_TOKEN` | empty | When set, creating the organisation on a fresh instance requires this token, so whoever reaches the instance first cannot claim it. Recommended for instances reachable before setup is finished. |
| `MAILHEARTH_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `MAILHEARTH_TRUST_PROXY` | `false` | Read `X-Forwarded-For` / `X-Forwarded-Host` from trusted proxies. |
| `MAILHEARTH_TRUSTED_PROXIES` | loopback and private networks | Comma-separated CIDRs or addresses of your reverse proxies. Forwarding headers from any other peer are ignored. |
| `MAILHEARTH_SECURE_COOKIES` | `true` when `MAILHEARTH_BASE_URL` is `https://` | Force the `Secure` cookie flag on or off. |
| `MAILHEARTH_ALLOWED_MAIL_NETWORKS` | empty | CIDRs where mail servers may live. Connections to private, loopback and link-local addresses are refused unless listed here; set this when your IMAP/SMTP/ManageSieve server is on a private network. |
| `MAILHEARTH_CA_BUNDLES_FILE` | empty | JSON file mapping bundle ids to PEM files, e.g. `{"1": "/etc/mailhearth/ca.pem"}`. Protocol endpoints refer to a bundle id to trust a private CA. |
| `MAILHEARTH_IMAP_MAX_CONNS` | `24` | IMAP connection cap. The pool supports 4 to 24; other values are adjusted with a warning. |
| `MAILHEARTH_IMAP_IDLE_SECONDS` | `90` | Idle IMAP connections are closed after this many seconds (10 to 3600). |
| `MAILHEARTH_MAX_UPLOAD_MB` | `25` | Largest single attachment upload. |
| `MAILHEARTH_MAX_MESSAGE_MB` | `40` | Largest encoded message. Attachments grow by about a third when encoded for sending. |
| `MAILHEARTH_UPLOAD_QUOTA_MB` | 3 × `MAILHEARTH_MAX_MESSAGE_MB` | Total size of one member's unsent uploads. |
| `MAILHEARTH_SESSION_DAYS` | `30` | Sliding session expiry. |
| `MAILHEARTH_SESSION_MAX_DAYS` | `90` | Absolute session lifetime; must not be shorter than `MAILHEARTH_SESSION_DAYS`. |
| `MAILHEARTH_INVITE_DAYS` | `7` | Invite link validity (1 to 90). |
| `MAILHEARTH_DEV_STACK` | `false` | Development only: in-process fake provider, IMAP and SMTP. |
| `GOMEMLIMIT` | `160MiB` when unset | Go heap limit. |

## Backups & Disaster Recovery

All runtime state in Mailhearth lives within the persistent data directory (`/data` by default):

| File / Path | Purpose | Backup Requirement |
|---|---|---|
| `/data/mailhearth.db` | Primary SQLite database running in WAL mode | Core database |
| `/data/mailhearth.db-wal` | Write-Ahead Log buffer storing recent committed transactions | Must be backed up concurrently with `.db` |
| `/data/master.key` | Master encryption key used to encrypt and decrypt stored API tokens and protocol passwords | Critical secret; keep safe in an offsite location |
| `/data/uploads/` | Transient attachments staged during email composition | Temporary; recreated automatically |

### Online Hot Backups
Use SQLite's online vacuum/backup mechanism to produce a consistent point-in-time snapshot without taking the server down:
```bash
docker compose exec mailhearth sqlite3 /data/mailhearth.db ".backup /data/backup_$(date +%Y%m%d_%H%M%S).db"
```

### Cold Backups
Stop the application container and archive the data volume:
```bash
docker compose stop
tar -czvf mailhearth-backup-$(date +%Y%m%d).tar.gz /data/mailhearth.db /data/mailhearth.db-wal /data/master.key
docker compose start
```

### Offsite Disaster Recovery
Restoring Mailhearth on a fresh host is a simple three-step process:
1. Provision a host with Docker Compose installed;
2. Place the restored `mailhearth.db` and matching `master.key` inside the designated host volume;
3. Run `docker compose up -d`. Mailhearth will verify the master key, execute any pending schema checks, and resume operations seamlessly.

## Upgrades & Database Schema

The schema is defined in `internal/db/migrations/0001_init.sql`. On first start with an empty data directory it is applied in a single transaction; if any statement fails the transaction is rolled back and the database is left empty. The schema version is stored in SQLite's `PRAGMA user_version`.

Mailhearth refuses to start on a database it did not create (for example one left behind by a pre-release build) and on a database whose schema is newer than the running binary. In the first case start with an empty data directory; in the second, run the matching release.

Later schema changes ship as additional numbered files, each applied in its own transaction.

1. **Before upgrading**: take a backup (see above) and verify your `master.key` backup.
2. **Applying upgrades**:
   ```bash
   docker compose pull
   docker compose up -d
   ```
3. **Rollback**: older binaries do not run against a newer schema. Roll back by restoring the pre-upgrade backup together with the previous image.

## Reverse Proxy & TLS Setup

In production, place Mailhearth behind a TLS-terminating reverse proxy such as Caddy or Nginx.

### Caddy (Recommended)
Caddy automatically provisions and renews TLS certificates with minimal configuration:
```caddyfile
mail.example.com {
    reverse_proxy 127.0.0.1:8080 {
        # Flush buffering immediately to ensure Server-Sent Events (SSE) stream without delay
        flush_interval -1
    }
}
```

### Nginx
When configuring Nginx, disable proxy buffering on streaming endpoints and configure appropriate timeouts:
```nginx
server {
    listen 443 ssl http2;
    server_name mail.example.com;

    ssl_certificate /path/to/fullchain.pem;
    ssl_certificate_key /path/to/privkey.pem;

    # Allow uploading large attachments
    client_max_body_size 50m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # Disable buffering for live streaming push endpoints
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

Ensure `MAILHEARTH_TRUST_PROXY=true` is set in your `.env` so client IP addresses are accurately attributed in audit logs and rate limits. If the proxy does not connect from loopback or a private network, list its address in `MAILHEARTH_TRUSTED_PROXIES`.

## Troubleshooting & Incident Response

### 1. Provider Connection Error: `provider_auth_failed`
- **Cause**: Upstream API tokens have expired, been revoked, or lack required scopes.
- **Remedy**: Go to Admin Console → Mail Connections, click edit on the affected connection, and supply a refreshed management API token. Previous working configurations remain active until the candidate token authenticates successfully.

### 2. Mailbox Reports "Protocols Unconfigured"
- **Cause**: Importing resources only ingests upstream metadata; passwords are never imported automatically.
- **Remedy**: Open the mailbox settings and configure the IMAP, SMTP, and ManageSieve endpoints with appropriate credentials. Mailhearth validates live handshakes before committing the configuration. Disable unused protocols if sending or Sieve is not supported.

### 3. Protocol Authentication Error: `mailbox_auth_failed`
- **Cause**: Stored mailbox username, primary password, or application-specific password was rejected by the server.
- **Remedy**: For manually entered passwords, update credentials through the protocol settings modal. For managed credentials, initiate a credential rotation workflow from the Admin Console.

### 4. Sieve Mail Rules Cannot Be Saved
- **Cause**: ManageSieve endpoint is misconfigured, TLS handshakes fail, or required Sieve extensions are missing.
- **Remedy**:
  - Purelymail defaults to `mailserver.purelymail.com:4190` with STARTTLS;
  - Migadu defaults to ManageSieve disabled; enable it and provide valid credentials if your plan supports it;
  - If another active script already exists on the mail server, Mailhearth prompts for confirmation before taking over the active script;
  - Automated vacation responders require the server to advertise the `vacation` extension.

### 5. Operation Enters `unknown` State
- **Cause**: An asynchronous administrative call to an upstream provider timed out or disconnected before receiving a definitive response.
- **Remedy**:
  - Inspect the step-by-step audit log of the operation. Resource locks remain in place to prevent state inconsistency;
  - If credential provisioning was interrupted without returning a remote credential ID, log in to the provider's web console to verify and delete any orphaned application passwords;
  - Once verified, click "Submit Cleanup Report" in Mailhearth. The operation is marked as `external_reported` with `systemVerified=false`, canceling the stalled operation while preserving successfully provisioned resources.

### 6. Outbox Status Shows `sent_copy_failed`
- **Cause**: The email was accepted by SMTP and queued for delivery, but storing a copy in the Sent folder via IMAP encountered a network disruption.
- **Remedy**: Do not worry about duplicate delivery! Mailhearth strictly separates delivery handoff from Sent-copy archival. Click "Retry Saving Copy"; the system will retry only the IMAP append operation without resending the message via SMTP.

### 7. No Real-Time Updates; Manual Refresh Required
- **Cause**: Your reverse proxy is buffering Server-Sent Events (SSE) HTTP streams.
- **Remedy**: Verify your reverse proxy configuration: enable `flush_interval -1` in Caddy, or disable `proxy_buffering` on `/api/mail/` in Nginx.

### 8. Slow Initial Page Load for Massive Folders
- **Explanation**: Opening a folder performs a single sequence-range FETCH retrieving envelopes and body structures for 50 messages. On Purelymail, server-side search indexing is automatically enabled for mailboxes created by Mailhearth to maintain rapid search speeds.

## Logging & Observability

Mailhearth writes structured log lines to `stderr`, easily monitored via container log tools:
```bash
docker compose logs -f --tail=100 mailhearth
```

- Standard log level defaults to `info`;
- To troubleshoot low-level network handshakes or IMAP IDLE reconnects, set `MAILHEARTH_LOG_LEVEL=debug` in `.env`;
- Every API response carries an `X-Request-ID` header, and error log lines include the same `requestId`. Server errors are logged at `error`, other client errors at `info`;
- **Security Invariant**: Sensitive credentials, API tokens, and user passwords are automatically scrubbed and never printed to log streams.

## Common Verification Commands

```powershell
# Run baseline tests
go test ./... -run '^$'

# Database schema, domain model, HTTP API and fresh-install suites
go test -count=1 ./internal/db ./internal/core ./internal/httpapi ./cmd/mailhearth

# Static analysis
go vet ./...

# Frontend test suite and production build verification
npm --prefix web run test
npm --prefix web run build
```

To run integration tests against live providers, set `MAILHEARTH_DEV_STACK=false`, configure credentials in `data/integration/multi-provider/`, set `MAILHEARTH_MULTIPROVIDER_TEST_CONFIG`, and run `go test -count=1 -v ./internal/integration/multiprovider`.
