# MySQL migration journal and reconciliation

[catalog.go](catalog.go), [definition.go](definition.go), trigger and reference helpers define the admitted ordered statements and their expected object identity. [migrate.go](migrate.go) exposes apply, reconcile and readiness modes; [installation_session.go](installation_session.go) maintains the installation session boundary.

MySQL DDL can outlive an interrupted request and is not treated as an ordinary atomic migration transaction. The journal distinguishes applying, applied and verified stages. Readiness inspects retained step/object state; dirty or unjournaled objects block progress until the explicit reconciliation path verifies the original operation. SHOW CREATE/trigger metadata and seed postconditions are bounded and checked against the catalog.

The installation lock is scoped to the validated database and held on its real connection. An uncertain user-lock session is discarded rather than returned to the pool as if the lock were released. Lock, step, total, cleanup and metadata budgets are typed Options with validated relationships.

Changes must synchronize the active [MySQL migration catalog](../../../../migrations/mysql), selected-database readiness, runtime grants and backup/restore verification. Candidate schema files alone are not activated statements. This package depends on [mysqlstore](../mysqlstore/README.md), not on domain HTTP handlers or a generic SQL converter.

Focused native check: `go -C services/control test ./internal/platform/database/mysqlmigrate`. Actual interrupted DDL, uncertain acknowledgement, postcondition mismatch, installation-lock and restart cases need the isolated native MySQL harness. A catalog unit test cannot prove a live migration completed.

The current active catalog appends statements204–265 (22 tables,9 measured ALTER
predecessors and31 triggers) to the immutable203-statement prefix. Native
intermediate metadata supplies each expected definition; a final table count is
not substituted for this ordered chain. The265-step catalog and108 business
objects are source-registration boundaries, not a passed live migration or whole
MySQL serving profile. Keep historical private149/194/203 fixtures bound to their
original sources when the active embedded catalog grows.

The non-trigger statement boundary ignores delimiters inside ordinary comments,
quoted strings and backtick identifiers while preserving the exact stored source.
It rejects unclosed input, executable/versioned comments, optimizer hints, extra
statements and trailing tokens/comments after the sole terminal delimiter. This
is a boundary guard, not a SQL translator or replacement for source/metadata SHA
validation. Its quoting assumptions follow the kernel's fixed SQL modes: ANSI_QUOTES
and NO_BACKSLASH_ESCAPES are absent. See MySQL8.4's [comment syntax](https://dev.mysql.com/doc/refman/8.4/en/comments.html)
and [string literals](https://dev.mysql.com/doc/refman/8.4/en/string-literals.html).
