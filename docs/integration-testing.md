# Integration testing against a real Purelymail account

**English** · [简体中文](integration-testing.zh-CN.md)

The unit tests run against an in-memory fake of the Purelymail API. That fake
proves Mailhearth is self-consistent, but it cannot prove that Mailhearth
matches the live service: the fake is written from the same reading of the API
that the client is. Only a real account can catch a wrong field name, a rule
Purelymail interprets differently than we assumed, an app password that is not
actually revoked, or mail that never arrives.

The suite in `internal/integration` closes that gap. It is skipped by default,
so `make test` and CI stay offline and fast.

## What it covers

Each test drives Mailhearth's own service layer, then checks the result against
the live account rather than against Mailhearth's database.

| Test | What it proves |
| --- | --- |
| `TestImportExistingAccount` | Connecting to an account that already holds users and rules imports them faithfully, marks them as imported, mints no app passwords, and changes nothing upstream. A second sync is a no-op. |
| `TestMailboxLifecycle` | Creating a mailbox creates a real user with a working app password; IMAP login, SMTP submission, delivery, rendering and the Sent copy all work; rotation invalidates the previous password at the provider; deletion removes the user. |
| `TestRoutingRules` | An alias becomes a routing rule the provider honours, mail addressed to it arrives, and deleting the address withdraws the rule. |
| `TestGroupDistribution` | A group address reaches every member, and removing a member rewrites the rule upstream. |
| `TestSharedMailboxAccess` | Grants decide who can open a shared mailbox. Administrator rights alone never grant mail access. Revoking closes the door. |
| `TestSieveRules` | Mailhearth's compiled Sieve script is accepted by the provider, becomes the active script, and actually files mail into the target folder instead of the inbox. |
| `TestOffboarding` | A departing person loses every way in, including the credential their desktop mail client held, while their mailbox and its history transfer to the successor. |
| `TestSuspendAndReactivate` | Suspension locks everyone out but keeps accepting mail; reactivation restores access and the mail that arrived meanwhile is there. |
| `TestPasswordResetForExternalClients` | The password handed out for Thunderbird really authenticates, and Mailhearth's own app password survives the reset and stays distinct from it. |
| `TestExternalForwarding` | A forward to an address outside the account becomes the rule we expect. |
| `TestTokenRejection` | A wrong API token is refused and does not damage the stored working connection. |

## Safety

The suite creates and deletes real mailboxes and real routing rules, and
deleting a Purelymail user deletes its mail. Two mechanisms keep that
contained.

Every object the suite creates is named `<prefix>-<runid>-<role><n>`, where the
prefix defaults to `mh-it`. Before any destructive helper touches an address it
calls `guardOwned`, which aborts the run unless the address is on the
configured test domain **and** carries the prefix. A test cannot delete a
mailbox it did not create, even if a code change asked it to.

Each test also registers its objects for cleanup as it creates them, so an
interrupted or failing run still tears down what it made.

Even so: **point the suite at a domain that holds nothing you care about.** A
dedicated test domain on the account is the right setup. The guardrails protect
against the suite misbehaving, not against a typo in `MAILHEARTH_IT_DOMAIN`
that happens to name your production domain and prefix.

Each test builds its own empty database with its own master key in a temporary
directory, so nothing touches your real installation.

## Running it

The domain must already exist in the Purelymail account with working MX
records, since delivery is what most of these tests measure. The API token
needs full access.

```bash
export MAILHEARTH_IT_TOKEN=your-api-token
export MAILHEARTH_IT_DOMAIN=test.example.com

go test ./internal/integration -v -timeout 40m
```

Expect the full suite to take several minutes: most of the wall clock is
waiting for mail to actually arrive. Run one test while iterating:

```bash
go test ./internal/integration -run TestMailboxLifecycle -v -timeout 15m
```

The tests create and delete users on the account. Purelymail bills per user, so
a full run costs a few fractions of a cent.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `MAILHEARTH_IT_TOKEN` | — | API token. Required; the suite skips without it. |
| `MAILHEARTH_IT_DOMAIN` | — | Test domain. Required; the suite skips without it. |
| `MAILHEARTH_IT_PREFIX` | `mh-it` | Local-part prefix marking suite-owned objects. Must start with `mh`. |
| `MAILHEARTH_IT_EXTERNAL` | — | An address outside the account. Enables `TestExternalForwarding`. |
| `MAILHEARTH_IT_DELIVER_SECONDS` | `180` | How long to wait for a message to arrive before failing. |
| `MAILHEARTH_IT_KEEP` | unset | Leave created objects behind for inspection. You must clean them up yourself. |
| `MAILHEARTH_IT_API_URL` | `https://purelymail.com/api/v0` | API endpoint. |
| `MAILHEARTH_IT_IMAP_ADDR` | `imap.purelymail.com:993` | IMAP address. |
| `MAILHEARTH_IT_SMTP_ADDR` | `smtp.purelymail.com:465` | Submission address. |
| `MAILHEARTH_IT_SIEVE_ADDR` | `mailserver.purelymail.com:4190` | ManageSieve address. Empty skips the Sieve test. |
| `MAILHEARTH_IT_IMAP_TLS` | `tls` | `tls`, `starttls` or `none`. |
| `MAILHEARTH_IT_SMTP_TLS` | `tls` | `tls`, `starttls` or `none`. |
| `MAILHEARTH_IT_SIEVE_TLS` | `starttls` | `tls`, `starttls` or `none`. |

## When a test fails

Delivery timeouts are the common failure and usually mean the domain's DNS is
incomplete rather than that Mailhearth is broken. Check the MX and SPF records
first, then raise `MAILHEARTH_IT_DELIVER_SECONDS`. Purelymail's spam filtering
can also file a test message into Junk; the failure message names the folder it
searched and how many messages it saw there.

Re-run with `MAILHEARTH_IT_KEEP=1` to leave the objects in place and inspect
them in the Purelymail web interface. Remember to delete them afterwards, since
they keep costing money and the next run will not adopt them.

If a run is killed hard enough to skip cleanup, the leftovers are easy to find:
every one of them carries the prefix.

## Keep it out of CI

Do not wire this suite into the pull-request pipeline. It needs a real token,
costs money, and its delivery waits make it far too slow for a pre-merge gate.
Run it before a release, and after any change to the Purelymail client, the
IMAP or SMTP paths, or the Sieve compiler.
