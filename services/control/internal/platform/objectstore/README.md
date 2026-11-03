# Private object transport and owned staging

This package implements configured S3 operations and a private local staging namespace. [Artifact stores](../../modules/artifactstores/README.md) owns current user/tenant/Workspace/object authorization, SQL intent and recovery. The transport never grants authority from a supplied bucket key or performs prefix-wide deletion.

## Exact transport and configuration

[Ref](contract.go) accepts server-resolved canonical nonzero UUIDs and derives one exact tenant/Workspace/object content key. `Transport` names explicit object and upload identities for multipart creation, upload, inspection, completion, abort, head, conditional open/delete and profile admission. Download bodies must be closed by consumers so operation reservations are released.

[NewS3](s3.go) accepts `config.ObjectStorageConfiguration`; [the configuration owner](../config/object_storage.go), [env mapper](../config/object_storage_environment.go) and [deployment example](../../../../../config/examples/object-storage/control.example.yaml) own endpoint, pinned dial addresses, CA/protected credentials, bucket, refusal credentials, management probes, staging and resource budgets. Construction reads local protected material; `VerifyProfile` performs remote admission. [Server composition](../../../cmd/server/artifactstores.go) owns opening, admission and shutdown.

The SDK is constrained to the selected endpoint and explicit addresses, disables automatic retries and endpoint-changing redirects, and uses typed transfer concurrency/deadlines. Profile admission checks actual bucket capabilities, configured authority, private access and management listener restrictions. A timeout or unavailable negative probe is not evidence of access denial. The package does not create or reconfigure buckets to make admission pass.

## Mutation uncertainty and reconciliation

Definite authorization, not-found, unsupported-capability and precondition refusals remain distinguishable from `OBJECT_REMOTE_OUTCOME_UNKNOWN`. A network failure during a mutation must not become success or trigger an invisible SDK retry. The domain retains intent and reconciles exact multipart/object state; successful transport receipt alone does not establish a committed business transition. Conditional object identity and multipart readback protect against replacement or truncated listings. Upload/part observations require explicit completeness flags, unique exact upload IDs and bounded noncyclic continuation; incomplete observations return no partial result. See the [list completeness contract](../../../../../docs/milestones/m22-object-storage/list-completeness.md). Multipart receipts/pages also require exact returned bucket, object and upload identity where the protocol supplies them; inconsistent mutation receipts remain unknown. See [response identity](../../../../../docs/milestones/m22-object-storage/multipart-response-identity.md).

Keep the [bucket-authority contract](../../../../../docs/milestones/m22-object-storage/bucket-authority-contract.md) and [wire-fault contract](../../../../../docs/milestones/m22-object-storage/wire-fault-contract.md) aligned when transport behavior changes.

Completion also admits retained part sizes against the existing configured part
size and object budget before issuing the mutation. Nonfinal parts are full-sized;
zero, negative, oversized or over-budget plans return no receipt and make no remote
request. See [completion byte admission](../../../../../docs/milestones/m22-object-storage/completion-byte-bounds.md).

## Staging ownership and cleanup

[InitializeStage](staging.go) adopts only an existing empty private directory. It never creates a missing mount or takes over foreign contents. `OpenStage` verifies namespace, tenant/backend identity and exclusive ownership. Writes reserve capacity, bind expected length/digest and preserve failures for reconciliation. Directory/file permissions, symlink resistance and descriptor identity are part of this boundary.

[Stage records](staging_records.go) bind each attempt to namespace, expected bytes/digest and actual device/inode. `Records` returns only bounded journal-owned attempts; `RemoveAttempt` requires the same receipt and a nonblocking exclusive file lock. It refuses replacement inodes, active consumers and unjournaled files. Root/domain maintenance must prove references are closed before invoking cleanup; an arbitrary path or broad disk purge is not recovery. [Recovery operations](../../operations/recovery/object_stage.go) and snapshot handling consume this ownership contract.

## Verification and coordinated changes

With the pinned toolchain and admitted cache/temp roots:

```sh
go -C services/control test ./internal/platform/objectstore
```

Go 1.27.2 `testing.TempDir` also follows `GOTMPDIR`. Keep compiler tmpfs separate
from durable runtime temp when qualifying staging sync and journal behavior; see
the exact [runtime storage profile](../../../../../docs/milestones/m22-object-storage/staging-runtime-storage-profile.md).

Controlled checks cover SDK fault framing, redirect refusal, exact scope, negative authority probes and real local staging ownership. Native authority diagnosis requires `object_storage_integration && object_storage_diagnostic` and an explicitly prepared private backend; see [authority diagnostic](../../../../../docs/milestones/m22-object-storage/authority-diagnostic.md). Paired SQL/domain and browser paths remain separate. Change transport policy, schema/env/deployment inputs, artifactstores reconciliation and storage readback together; never turn a controlled protocol fixture into a claimed production storage acceptance.
