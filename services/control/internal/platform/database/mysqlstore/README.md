# MySQL connection and session owner

[configuration.go](configuration.go) and defaults.json define the typed connection/policy boundary. [private.go](private.go) and [input_binding.go](input_binding.go) read protected credential/CA inputs and retain their identity. [pool.go](pool.go) constructs the pinned MySQL driver connector, checks the authenticated server/session and owns the resulting pool.

The fixed execution context includes utf8mb4, binary collation, UTC, READ-COMMITTED and strict SQL modes. TLS verifies the configured CA and server name. Multi-statements, client-side parameter interpolation, local file loading, legacy/cleartext authentication and fallback to plaintext are disabled. Adjustable deadlines, packet and connection limits come from the typed policy; those fixed security/protocol invariants are not bypass switches.

[session.go](session.go) and [transaction.go](transaction.go) expose bounded real operations. Driver errors and diagnostics are sanitized; credentials, SQL arguments and raw driver messages must not become logs. The selected composition root owns lifetime, while [mysqlmigrate](../mysqlmigrate/README.md) owns schema journaling and [transaction](../transaction/README.md) provides the common repository boundary.

Synchronize control database-selection/config mapping, [MySQL examples](../../../../../../config/examples/database/mysql), operator credential/grant/restore binding and native session tests when changing this package. Focused check: `go -C services/control test ./internal/platform/database/mysqlstore`. Actual TLS/authentication, server invariants and restricted principal behavior need the real isolated MySQL profile; connector configuration inspection alone cannot establish them.
