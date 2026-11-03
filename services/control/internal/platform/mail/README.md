# Durable identity mail

This package delivers identity mail through an encrypted SQL outbox and a configured SMTP relay. [Identity](../../modules/identity/README.md) owns invitation/reset authority and enqueues delivery in the same business transaction; this package owns encrypted payloads, leases, rendering and transport. It is not a general job scheduler.

## Composition and durable state

[Outbox](outbox.go) accepts the selected `transaction.Store`, keyring, typed control configuration and loaded templates. The legacy PostgreSQL `DB` field remains a compatibility path; supplying it together with `Storage` is rejected. `EnqueueWithin` in [repository.go](repository.go) borrows an existing transaction. It builds token links from the configured Portal/Admin origin and stores a stable Message-ID with a new `mail_deliveries` row. SMTP is performed after the claiming transaction commits.

[crypto.go](crypto.go) loads a protected keyring with an explicit active key. AES-GCM uses a random nonce and binds ciphertext to delivery ID, purpose and payload schema through associated data. Preserve old decryption keys for all retained nonterminal payloads, including `queued` and `sending` deliveries with expired leases, and for database backups that may be restored. The [server composition](../../../cmd/server/main.go) loads the keyring once at startup and injects its decoded in-memory snapshot into the outbox; replacing the file does not reload a running worker. A committed terminal transition clears ciphertext, nonce, key ID and lease data; this does not erase previously retained database backups.

## Configuration and rendering

The [control configuration type](../config/config.go) owns `security.mail_keyring_file` and `smtp` host, port, sender, TLS mode, CA, authentication file references and connect/operation deadlines. [Runtime policy](../config/policy.go) owns `runtime.mail` poll interval, complete attempt budget, lease, retry ceiling, delays and jitter. Validation keeps the lease longer than the complete attempt and SMTP budget. Use the selected control YAML and protected configuration inputs; changing a timeout is not a source edit.

[templates.go](templates.go) consumes the admitted [offline localization registry](../localization/README.md). It compiles plain-text and HTML templates with missing-key errors, renders escaped dynamic HTML and produces UTF-8 MIME with the selected language/direction. A missing requested locale falls back to the English purpose template; a missing English template fails. Attribution is attached only when the admitted resource explicitly carries that verified metadata. Rendering does not call a translation service.

## Failure and recovery

The state path is `queued -> sending -> sent | queued | failed | cancelled`. A claim locks one eligible row, creates a fresh lease and increments attempts. Before sending, current invitation generation/state, reset expiry and relevant user/tenant/Workspace state are rechecked. Obsolete authority cancels the delivery; it never extends a token's lifetime.

[SMTP](smtp.go) requires verified implicit TLS or required STARTTLS, with no plaintext downgrade. Permanent TLS/auth/recipient failures terminate delivery; transient failures use the bounded configured retry schedule. Completion and requeue updates are fenced by the current unexpired lease. Exhausted stale sending leases become failed with `smtp_unknown_outcome`. A crash after relay acceptance can still cause duplicate mail: stable Message-ID assists correlation, but there is no exactly-once or inbox-delivery guarantee.

`Outbox.Run` logs a safe storage category and keeps polling after dispatch errors; the [runtime lifecycle](../../../cmd/server/runtime_lifecycle.go) owns cancellation and joining. If required decryption keys are missing, stop and join the worker through that owning lifecycle before repairing the protected keyring, then restart with the complete keyring. Fix relay trust through its protected inputs without bypassing TLS verification.

A decryption failure calls `finish` with state `failed`. If that terminal update commits, its encrypted payload is cleared: restoring a missing key later cannot resume that delivery. Do not reset it to `queued`; any new send must use the identity domain's current-authority workflow. If the completion transaction failed or its commit outcome is unknown, preserve that uncertainty and inspect the actual delivery/audit state before making a recovery decision. Do not assume rollback, fabricate a terminal state, or manually replay an uncertain transaction to make the queue look healthy. Existing bounded retry and stale-lease recovery remain the worker's responsibility; they do not provide exactly-once SMTP delivery.

## Change and verification

Keep identity token transitions, transactional audit, mail schema, selected PostgreSQL/MySQL statements and locale templates synchronized. Start with the isolated crypto/MIME checks:

```sh
go -C services/control test ./internal/platform/mail -run 'TestM01MailCrypto|TestLocalizationMail'
```

The broader [mail tests](mail_test.go) use the identity/SMTP fixtures, and [repository_integration_test.go](repository_integration_test.go) requires the `repository_integration` product-schema profile. [Offline mailbox verification](../../testing/offlinemailbox/mailbox_integration_test.go) is a separate real-message path. A crypto or rendering check is not relay delivery, SQL recovery or target-mailbox qualification.

[smtp_transport_test.go](smtp_transport_test.go) adds ten real loopback TLS/auth
cases and two explicit certificate recovery phases with the production client.
Its responder is an identified synthetic fixture and records only counters/ports,
never credentials or message contents. Use the bounded [SMTP quality owner](../../../../../tools/quality/mail/README.md)
and [contract](../../../../../docs/engineering/deployment-transport/smtp-verified-auth.md).
No SQL is needed for this transport-only group; outbox persistence and target
inbox delivery remain separate gates.

The current M01 mail recovery scope adds current_crypto_test.go for explicit
AEAD nonce/purpose binding and selects the existing repository-private expired
lease child without replaying unrelated transport tests. Identity HTTP tests own
actual dispatcher-child SIGKILL and account recovery, synthetic TLS failures and
canonical state assertions. The [frozen contract](../../../../../docs/engineering/deployment-transport/current-m01-mail-recovery.md)
keeps the identified test pause, actual process termination and full server restart
qualification separate; no production crash switch is exposed.

## Native Dameng contribution

Wave5 supplies explicit native enqueue, latest/cancellation, claim, stale-lease
recovery, eligibility, completion and requeue statements. Claim uses the native
one-acquired-row `FOR UPDATE 1 SKIP LOCKED` form. Retry durations must be exact
microseconds. Enqueue requires one confirmed row; a failed/uncertain claiming
transaction releases no claimed DTO, lease or encrypted payload to dispatch.
The account/mail schema contribution adds invitation/lease lookup indexes and
preserves delivery recipient/source/message identity across retries.

The real native account harness also claims a synthetic password-change notice,
fails decryption with an explicitly empty test keyring, and verifies terminal
payload/lease erasure plus audit without reaching SMTP. That is separate from
SMTP, retry contention, crash recovery and actual mailbox qualification. The
[wave5 handoff](../../../../../docs/execution/mvp/parallel-wave5-account.md) records
exact build tags, prerequisites and still-pending coordinator/target checks.
