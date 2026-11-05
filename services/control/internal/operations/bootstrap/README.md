# Initial administrator bootstrap

This package owns protected local creation and reconciliation of the initial administrator. It is called by [the control command dispatcher](../../../cmd/server/main.go), not an HTTP handler. See the [operations boundary](../README.md) for shared installation-lock and operator rules.

## Entry points and inputs

- [Run](bootstrap.go) implements `bootstrap-admin-file`, `reconcile-bootstrap-file`, and `resume-bootstrap-file` for PostgreSQL; [RunMySQL](mysql.go) implements the selected MySQL path.
- [PreparePacket and RunPacket](packet.go) implement `prepare-bootstrap-packet`, `apply-bootstrap-packet`, and `check-bootstrap-packet`. PostgreSQL packet preparation is offline: it creates a protected credential packet without loading the database or contacting a server.
- [MySQL packet preparation and application](mysql_packet.go) use `prepare-mysql-bootstrap-packet` and the shared apply/check commands with an actual selected MySQL operator context and runtime-access input. Do not treat MySQL preparation as equivalent to the offline PostgreSQL operation.

The strict request supplies the administrator email, display name, installation name, and Workspace name. Configuration comes from the existing control configuration and explicitly selected protected runtime input. The input and generated output are credential-bearing files. Passwords, packets, and intent contents must not be copied into logs, command arguments, or evidence attachments. [File handling](files.go) owns their validation and persistence.

## Exact packet CLI inputs

The [packet flag validator](../../../cmd/server/bootstrap_packet.go) accepts only the following flags for each command. Values are paths to protected local files; no password belongs in an argument. An unrelated flag is rejected even if its supplied value is empty or false.

| Command | Complete allowed and required flag set | Preparation or effect |
| --- | --- | --- |
| `prepare-bootstrap-packet` | `--bootstrap-input`, `--credential-output` | Offline PostgreSQL packet creation; no `--config` or database access |
| `prepare-mysql-bootstrap-packet` | `--config`, `--runtime-config`, `--bootstrap-input`, `--credential-output` | MySQL selection and actual locked target/runtime binding are required |
| `apply-bootstrap-packet` | `--config`, `--runtime-config`, `--bootstrap-packet` | Select the configured engine; inspect the original packet before any initial-account mutation |
| `check-bootstrap-packet` | `--config`, `--runtime-config`, `--bootstrap-packet` | Inspect the same packet and actual account state; do not mutate the account |

Legacy `bootstrap-admin-file`, `reconcile-bootstrap-file`, and `resume-bootstrap-file` use the separate request/credential-output path in `Run`/`RunMySQL`; their intent format is not a prepared packet. Keep the original input and output for the chosen command family.

## State, authority, and effects

Online operations require the actual held installation lock and a database target matching the selected connection. PostgreSQL intent binds the request, the database target (connection identity and the database, schema and table it names) and the generated password, not the rest of the control configuration, so origins and runtime settings may change after install; MySQL binds its corresponding selected target and protected input. Creating an administrator also establishes the initial tenant, Workspace, membership, and installation state through the identity owner. Runtime database access must satisfy the applicable checks before packet application.

A prepared file is not evidence of a committed account. Legacy file bootstrap durably writes its original intent before mutation. Packet application instead reads the already retained protected packet and constructs the corresponding intent in memory; it does not write a second intent file. Both paths observe exact persisted identities and credential validity. A matching repeated packet application observes the existing result; it does not reset the password. An existing installation with different request or credential state is a conflict.

## Recovery

1. Stop further bootstrap attempts after an uncertain mutation and retain the original protected input, packet/intent, selected configuration, and diagnostic code.
2. Reacquire the installation lock on the same target. Check/reconcile the original material; do not generate another password or substitute another administrator request.
3. Resume only through the corresponding command when its observed state permits it. `BOOTSTRAP_MUTATION_UNCERTAIN_RECONCILE_REQUIRED` is not a failure that authorizes blind replay.
4. Treat missing, replaced, malformed, or conflicting protected files as a refusal requiring investigation. Do not repair a committed account by editing bootstrap records or overwriting a packet.

## Maintenance and verification

Keep PostgreSQL and MySQL entry points aligned with dispatcher selection while preserving their different preparation prerequisites. Packet format or binding changes require strict decoding, private-file, mismatch, repeated-apply, and process-loss coverage.

From `services/control`, the package test entry is `go test ./internal/operations/bootstrap`. [Packet tests](packet_test.go), [bootstrap tests](bootstrap_test.go), [process-loss tests](process_test.go), and [packet process tests](packet_process_test.go) separate file/protocol checks from actual PostgreSQL mutation and recovery. Actual PostgreSQL cases require an independently admitted disposable fixture (`PROJECT_ONE_TEST_DSN`); inspect skip counts and fixture setup before claiming execution. [MySQL packet tests](mysql_packet_test.go) validate their declared protocol scope, not a production MySQL deployment.

This README was derived from source; no runtime or target qualification was performed for its preparation.

`dameng.go` adds RunDameng under databaseaccess.WithDameng. Its private v1 intent
is native-target-bound, written exclusively and synced before the account
mutation. Reconcile observes only; resume retains the same password/request and
checks the original target. An uncertain result never automatically retries.
Readback also requires the initialized preference row. Identity/audit SQL belongs
to the account owner. The native `dameng-schema` CLI now dispatches these operator
commands; this does not activate the control server selector. Its protected
operator policy roundtrips the existing `config.RuntimePolicy` JSON projection
and rejects duplicate, null, unknown, missing and case-aliased fields before
injecting the typed policy. Ordinary bootstrap intents retain their wire decoder.
