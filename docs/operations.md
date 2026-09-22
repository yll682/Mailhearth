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
| Disk | a few MB | SQLite: organisation model + collaboration metadata; mail stays on Purelymail |
| Front end | 51 KB gzipped | one JS chunk, one CSS file, cached immutable |

Tune `MAILHEARTH_IMAP_MAX_CONNS` (default 24) to roughly `2 × concurrent
users + number of mailboxes people keep open`. Each IDLE watcher holds one
connection. Purelymail allows plenty of IMAP connections per user but there
is no reason to hold more than needed.

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

Migrations run automatically at start-up and are additive. Pull the new
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

**"Purelymail rejected this API token"** — the token was revoked or mistyped.
Replace it under Admin → Connection.

**A mailbox shows "not connected"** — imported mailboxes get an app password
only when someone binds them (onboarding) or an admin presses *Connect*. If
Purelymail refuses (`createAppPassword` failing), check that the user still
exists on the account and run *Sync now*.

**"the mail server rejected this mailbox credential"** — the app password was
deleted in the Purelymail portal or the user's password was reset outside
Mailhearth. Use *Rotate credential* on the mailbox.

**Rules cannot be saved** — ManageSieve at `mailserver.purelymail.com:4190`
must be reachable from the host (STARTTLS). Some VPS providers block outbound
ports; test with `openssl s_client -starttls sieve -connect
mailserver.purelymail.com:4190`.

**No live updates** — SSE is being buffered by a proxy; see above. The client
falls back to manual refresh and still polls folders when actions happen.

**Slow first page of a huge folder** — listing is a single sequence-range
FETCH of 50 envelopes plus body structures. Purelymail's server-side search
index (enabled per user) makes searches fast; it is on for mailboxes Mailhearth
creates.

## Logs

Structured text logs on stderr. `MAILHEARTH_LOG_LEVEL=debug` adds per-request
lines and IMAP watcher reconnects. Nothing in the logs contains credentials.

## Development stack

`MAILHEARTH_DEV_STACK=1` swaps Purelymail, IMAP and SMTP for in-process
fakes. `-seed-demo` adds two domains, five users, routing rules and sample
mail. The fake API token is `dev-token`; users authenticate with
`<name>-pass`. The Go tests use the same fakes, so `go test ./...` exercises
the whole stack including IMAP IDLE, SMTP submission and HTML sanitisation.
