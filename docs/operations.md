# Operations

**English** · [简体中文](operations.zh-CN.md) · [繁體中文](operations.zh-TW.md) · [日本語](operations.ja.md) · [Español](operations.es.md)

## Sizing

Mailhearth is built for the smallest hosts money can buy.

| Resource | Typical | Notes |
|---|---|---|
| Binary | ~25 MB static | no cgo, no runtime dependencies |
| RSS idle | 25–40 MB | `GOMEMLIMIT` defaults to 160 MiB |
| RSS busy (10 users) | 60–120 MB | dominated by IMAP fetch buffers |
| CPU | negligible | argon2id on login is the heaviest step |
| Disk | grows with organisation data | SQLite stores operations, submissions and collaboration metadata; mail remains on its IMAP service |
| Front end | about 100 KB gzipped | JS and CSS measured on 2026-10-09; content-addressed asset names |

Tune `MAILHEARTH_IMAP_MAX_CONNS` (default 24) to roughly `2 × concurrent
users + number of mailboxes people keep open`. Each IDLE watcher holds one
connection. The global default is 24; limits are 8 per connection and 3 per mailbox.
IDLE has separate quotas and ordinary requests retain connection capacity.
Respect each provider's actual connection limits.

## Backups

The whole state is the data directory:

| Path | Contents |
|---|---|
| `/data/mailhearth.db` | SQLite database (WAL mode) |
| `/data/mailhearth.db-wal` | Write-ahead log; copy it together with the database |
| `/data/master.key` | Encryption key for stored credentials |
| `/data/uploads/` | Composer attachments awaiting send (transient) |

Back up with the container stopped, or use `sqlite3 mailhearth.db ".backup
out.db"` for a consistent online copy. Keep `master.key` in a separate secure
location. Restoring on a new host: place both files, start the binary.

## Upgrades

Migrations run automatically at start-up, including table conversions and
reference checks. Save a consistent database and master-key backup. Pull the new
image, `docker compose up -d`. Downgrading across a migration is not
supported; restore a backup instead.

## Reverse proxy

Caddy:

```caddyfile
mail.example.com {
    reverse_proxy 127.0.0.1:8080 {
        flush_interval -1      # required for Server-Sent Events
    }
}
```

nginx: set `proxy_buffering off;` and `proxy_read_timeout 3600s;` on
`/api/mail/` so SSE streams are not buffered, and `client_max_body_size 50m`
for attachments.

## Troubleshooting

**`provider_auth_failed`** — update management authentication for the identified
connection under Admin → Connections. Failed candidate authentication preserves
the active configuration. Mail protocol authentication is checked separately.

**Mailbox protocols are unconfigured** — import registers selected resources.
Configure IMAP, SMTP, ManageSieve and entered credentials explicitly, or use a
managed-credential capability provided by that connection. Enabled protocols
must authenticate before configuration is committed. SMTP and ManageSieve can
be disabled independently.

**`mailbox_auth_failed`** — check the affected protocol's username and password.
Update entered credentials through protocol configuration; rotate managed
credentials through their operation. Network failures, temporary rejection and
unclassified ManageSieve responses remain unconfirmed. Verify remote revocation
using a fresh connection.

**Rules cannot be saved** — inspect the mailbox's ManageSieve endpoint, TLS and
server extensions. Purelymail defaults to `mailserver.purelymail.com:4190` with
STARTTLS. Migadu's template starts disabled; use the account's actual settings.
Taking over an existing active script requires explicit consent and its current
content hash. Vacation also requires the `vacation` extension.

**An Operation is `unknown`** — inspect its steps and reconcile remote results
before explicit retry. Resource locks remain held. If a credential-create response
was lost without a saved remote ID, resolve the credential in the provider portal
and submit an administrator cleanup report. The report cancels the operation and
retains created resources: `external_reported`, `systemVerified: false`.

**A Submission is `sent_copy_failed`** — SMTP has accepted the message; retry
only the Sent copy. For SMTP or APPEND `unknown`, inspect the Submission and its
unique marker. Preserve the original requestId.

**Sync finds new resources** — select resources to import from that connection's
sync result. Sync preserves credentials, access and history. Failed reads cannot
establish that a resource was deleted.

**No live updates** — SSE is being buffered by a proxy; see above. The client
falls back to manual refresh and still polls folders when actions happen.

**Slow first page of a huge folder** — listing is a single sequence-range
FETCH of 50 envelopes plus body structures. Purelymail's server-side search
index (enabled per user) makes searches fast; it is on for mailboxes Mailhearth
creates.

## Logs

Structured text logs on stderr. `MAILHEARTH_LOG_LEVEL=debug` adds per-request
lines and IMAP watcher reconnects. Nothing in the logs contains credentials.

## Checks

```powershell
go test ./... -run '^$'
go test -count=1 ./internal/db ./internal/core ./internal/httpapi ./internal/mailproto/sieve ./internal/provider -run '^TestMultiProvider'
go vet ./...
npm --prefix web run test
npm --prefix web run build
```

Disable `MAILHEARTH_DEV_STACK` for real integration tests. Keep authentication
configuration under `data/integration/multi-provider/`, set
`MAILHEARTH_MULTIPROVIDER_TEST_CONFIG`, and run
`go test -count=1 -v ./internal/integration/multiprovider`. Missing configuration
fails explicitly. Record local checks separately from real acceptance; unexecuted
acceptance cases remain incomplete.
