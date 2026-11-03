# Protected offline operator configuration

`config.go` owns original installation JSON and explicit env-file mapping, pinned
Engine/Compose policies and its immutable fingerprint. `backup.go`,
`restore_source.go`, `restore.go`, TLS generation, process recovery and journal
capacity files own separate bounded operation requests. These inputs never
silently alter original scope identity. Protected file IO belongs to the existing
bundle loader; callers do not inherit arbitrary file, Engine or SQL authority.

`runtime_recovery.go` owns the bounded version1 absence-recovery request: exact
scope, original pending create operation and nonblank reason. Its three explicit
operation-env settings override JSON once; unknown/duplicate/null/case aliases,
invalid identities/control text and ambient policy input refuse. Only a canonical
request digest enters the private journal. See the [schema](../../../../../config/examples/release/runtime-absence-recovery.schema.json)
and adjacent commented JSON/env examples. It changes no original configuration.

`cumulative_transition.go` owns the explicit account22 M16-to-M17 operation.
Its separately prepared target and complete backup identities, release-reader
bounds and fixed loopback readiness inputs are strict and bounded. Unknown,
null, duplicate, case-alias, malformed and contradictory values refuse. The
[operation schema](../../../../../config/examples/release/cumulative-upgrade.schema.json),
JSON example and adjacent commented env example describe the same typed loader.
Explicit env-file values override JSON once per invocation; ambient variables
are never read. A request change after durable upgrade intent refuses rather
than changing a partially applied operation. Private credentials remain original
protected file payloads, never JSON/env values or diagnostic strings.

The CLI loads and validates configuration before dispatch; `internal/prepare`
selects the fixed scope operation, and scope remains the effect/recovery owner.
Run focused controls with `go test ./internal/configuration`. Parsing/compilation
proves no installed upgrade or current Engine/SQL/target behavior. See
[operator owner](../../README.md) and the versioned release contracts.

`Engine.response_bytes` also bounds complete native configuration-archive
readback. The current cumulative CPU locale role requires the explicit8MiB
[existing-field example](../../../../../config/examples/release/cumulative-cpu.env.example);
its generic1MiB default is not sufficient for that selected inventory. Choose
before `init-scope`; the immutable original fingerprint prevents in-place
adoption of changed settings. Per-file, archive, custody and resource guards
remain unchanged. See the [current input guide](../../../../../docs/milestones/m17-offline-compose/cumulative-cpu-configuration.md).

Fixed SQL/store tasks also consume `compose.process.observe_seconds`: the embedded
policy permits1..120 seconds, default10. Choose it before `init-scope` through the
[existing explicit env mapping](../../../../../config/examples/release/offline-operator.env.example).
A large task can keep running after this observation deadline. Retain its journal
intent and exact task identity; do not edit a bound configuration or repeat an
import. After independent Engine observation, the matching explicit store/current-
store reconciliation observes that pending result and may advance fresh remaining
fixed steps. It is not a wholly read-only diagnostic command. Backup requests have
a separate `observe_seconds` owner and range. See the
[store procedure](../../../../../docs/milestones/m17-offline-compose/store-restore.md).
## Optional exact restore certificate

`RestoreRequest.compatibility_file` accepts only an explicit protected canonical
absolute path; `PROJECT_ONE_OFFLINE_RESTORE_COMPATIBILITY_FILE` is its optional
operation env-file override. Absence preserves the ordinary same-release guard.
The [contract](../../../../../docs/milestones/m17-offline-compose/restore-compatibility.md)
defines signing and exact binding; it adds no runtime/Compose environment value.
