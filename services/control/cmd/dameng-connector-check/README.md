# Explicit native Dameng connector check

This command owns no platform database selection, schema migrations or server
lifecycle. The [dameng library](../../internal/platform/database/dameng/README.md)
owns protected configuration, real native connections and cleanup. The
[transaction adapter](../../internal/platform/database/transaction/README.md)
executes explicit Dameng SQL on its admitted Store.

Build with the pinned Go/C toolchains and an admitted public unixODBC development
installation: `go -C services/control build -tags odbc_manager ./cmd/dameng-connector-check`.
Record the actual compiler/header/library/runtime binding on that target. A
default non-cgo/non-tagged build returns `DAMENG_CHECK_NATIVE_BUILD_REQUIRED`;
it never reports a successful native check. Linux arm64/amd64 is required to open.

Use the [quality entry](../../../../tools/quality/odbc/README.md) and the protected
[typed inputs](../../../../config/examples/domestic-platform/README.md).
`--config` is required; `--env-file` may replace its valid configuration reference.
No password, DSN, host, path or SQL comes from arbitrary client parameters.
`--print-launch-settings` returns the closed nonsecret envelope
`{format_version, environment, working_directory}` without connecting. It derives
all five loader references and, for manifest 2, the exact private `bin` cwd. A
compatible launcher applies these inputs only to its child process. The legacy
`--print-launch-environment` output remains available for manifest 1 and explicitly
refuses manifest 2. A launcher must consume the typed settings protocol before
running manifest 2; guessing or silently omitting its cwd is invalid. No library
function changes the process directory or shared native configuration.

Ordinary execution opens an actual native connection, checks metadata/session
identity and performs a real read-only transaction. `--transaction-probe=true`
additionally creates one randomly named disposable table in the selected schema,
checks commit/readback, Unicode/BIT/non-null empty binary, rollback/readback and
native read-only write refusal, then drops only its exact table. The name remains
in `retained_table` until DROP succeeds. An unknown transaction commit stops all
subsequent SQL, including DROP; reconcile the recorded table in the original
schema before any manual recovery. A failed CREATE also retains the attempted
name because an unacknowledged DDL outcome is not proof of absence. Never rerun a
failed mutation simply to obtain a passing receipt.

The v1 probe accepts only standard SQLSTATE `25006` as proof of read-only write
refusal; a timeout, generic SQL failure or different unqualified vendor spelling
cannot pass that assertion. Server commit durability, native cancellation,
recovery after process loss and platform repositories require separate target
checks. A successful probe is not whole-platform Dameng qualification.

Output is one bounded JSON observation/check list and redacted failure code.
`success` requires zero retained native resources and a closed manager. A cleanup
deadline may report pending resources; the command then exits unsuccessfully,
and process exit is not represented as successful native release. A driver that
never returns from an operation/cancel can prevent the checker reaching this
cleanup stage. Run target fault tests in an explicitly owned supervised process;
the library never frees in-use native memory to manufacture a deadline.
