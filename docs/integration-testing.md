# Multi-provider integration testing

**English** · [简体中文](integration-testing.zh-CN.md) · [繁體中文](integration-testing.zh-TW.md) · [日本語](integration-testing.ja.md) · [Español](integration-testing.es.md)

## Local checks

Run from the repository root. Keep caches and intermediate files under ignored `data/`.

```powershell
$env:GOCACHE=Join-Path (Get-Location) 'data/go-build-cache'
$env:GOTMPDIR=Join-Path (Get-Location) 'data/integration/go-work'
New-Item -ItemType Directory -Force $env:GOCACHE,$env:GOTMPDIR | Out-Null
$env:TEMP=$env:GOTMPDIR
$env:TMP=$env:GOTMPDIR
go test ./... -run '^$'
gofmt -l ./internal ./cmd
go test -count=1 ./... -timeout 180s
go test -count=1 ./... -run '^TestMultiProvider' -timeout 120s
go vet ./...
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
git diff --check
```

Compilation of all packages uses `-run '^$'`. Selected tests use real SQLite and
the application's HTTP server, plus pure algorithms for associations, authorization,
operations, routing, forwarding import/observations, submission state and Sieve.
Frontend tests cover requestId stability, translation coverage and interpolation
using the TypeScript parser. These checks do not exercise external delivery or browser interactions.
Repeated concurrency/forwarding checks can use `-count=20`; race testing requires a C compiler and cgo.

## Real environments and safety

`internal/integration/multiprovider` uses real API and protocol connections. Disable
`MAILHEARTH_DEV_STACK`. Tests explicitly skip when the configuration path is unset; an invalid supplied configuration fails. Use dedicated test
accounts/domains and mailboxes containing no important messages. Protocol tests send
real mail and change endpoint settings in their isolated local installation. Inspect
the selected tests before execution; existing external mailbox passwords are not reset
by these prerequisite/protocol checks. Further lifecycle acceptance can create/delete
resources, revoke credentials and change rules, and must use dedicated resources.

Store configuration only under `data/integration/multi-provider/`; paths are resolved
and checked against this directory. Never commit credentials or put them in chat or logs.
Each run stores an isolated database and master key beneath this directory. Test artifacts
and reports remain there for inspection and must be secured and cleaned up appropriately.

## Configuration

The JSON reader rejects unknown fields and extra JSON values. Configure all environments:

| Field | Required content |
|---|---|
| `purelymail` | `apiKey`, `domain`, `imap`, `smtp`, `managesieve` |
| `migadu` | Above fields plus `apiUsername`; explicit disabled ManageSieve when unavailable |
| Each protocol template | `enabled`; if enabled, `host`, `port`, `tlsMode` (`tls`/`starttls`), optional `caBundleId` |
| `manual` | `primaryMailbox`, `secondaryMailbox`, `independentSmtp`, `privateCaMailbox`, `noSieveMailbox` |
| Each manual mailbox | `address`, `credentials` (`clientKey`, `secret`), `endpoints` for `imap`, `smtp`, `managesieve` |
| Enabled manual endpoint | `networkMode: override`, `network` template, `authMode: password`, `username`, `credential: {clientKey}` |
| Disabled manual endpoint | `networkMode: disabled` |
| `deliveryTimeoutSeconds` | Positive integer; default 180 |

IMAP and SMTP in `independentSmtp` must use different usernames and secrets. The
private-CA case requires a CA available to the test installation; configure its
`caBundleId`. `MAILHEARTH_CA_BUNDLES_FILE` points to a JSON mapping of positive CA IDs
to PEM file paths. `noSieveMailbox` must disable ManageSieve while IMAP/SMTP remain usable.
Provider management authentication does not provide mailbox protocol passwords.

```powershell
$env:MAILHEARTH_MULTIPROVIDER_TEST_CONFIG=Join-Path (Get-Location) 'data/integration/multi-provider/config.json'
go test -count=1 -v ./internal/integration/multiprovider -timeout 40m
go test -count=1 -v ./internal/purelymail ./internal/mailproto/mailops -timeout 40m
```

## Coverage and results

- `TestRealMultiProviderPrerequisites`: real Purelymail/Migadu management authentication
  and manual protocol credentials. Writes `prerequisites.json` after success.
- `TestRealMultiProviderTransactions`: connection isolation, same-address registration,
  rejected candidates, revision conflicts, requestId/content checks and safe query fields.
  Corresponds to parts of T01, T03, T08, T17–T19 and T40; writes `transactions.json`.
- `TestRealMultiProviderProtocols`: independent credentials and actual delivery (T04),
  SMTP-disabled reading (T05), ManageSieve-disabled reading/delivery (T06), endpoint
  revision and old-connection closure (T37). Writes `protocols.json`.
- `TestClientAgainstRealPurelymail`: real account queries, user/password and routing-rule
  operations, with IMAP checks for authentication and revocation of resources created by the test.
- `TestMailOpsAgainstRealProtocols`: folders, pagination, search, flags, COPY/MOVE/deletion,
  MIME, attachments, SMTP delivery and IDLE, using isolated folders and unique message identifiers.
- `TestListFoldersOnRev1Server`: real IMAP4rev1 LIST/STATUS and special folders. This test
  requires `manual.primaryMailbox` without IMAP4rev2, LIST-EXTENDED, LIST-STATUS or SPECIAL-USE;
  select individual test names when using a different server for the other protocol checks.

Reports retain `acceptanceComplete=false`. The complete T01–T40 and V01–V07 matrix
still requires test implementation and real execution; the legacy Purelymail suite
in `internal/integration` still requires migration to the current interfaces. A local
test pass or successful import does not complete real-provider acceptance. Migadu
forwarding delivery modes remain `unverified` until V03 succeeds.

For delivery failures inspect MX/SPF, credentials, enabled protocols, target confirmation,
folders including Junk and Message-ID. A network error does not prove revocation or
resource deletion. Preserve operationId/submissionId and reconcile unknown outcomes;
do not resend automatically. Keep real credentials out of ordinary CI; run dedicated
acceptance after provider, SMTP/IMAP, migration or Sieve changes and before a release.
