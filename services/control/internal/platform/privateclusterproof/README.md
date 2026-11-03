# Bounded private-cluster probe proof

This package owns the closed program run by the M28 Kubernetes Job and verification of its bounded response. It proves an observed execution matching a specific challenge; it is not cryptographic hardware attestation, general command execution or cluster-management authority.

## Inputs and consumers

[proof.go](proof.go) defines `Challenge` with run, namespace and installation IDs, nonce, architecture and non-root UID. `Encode`/`Decode` require canonical bounded JSON. `Arguments` produces the exact `--challenge` and `--pod-uid` arguments, with the Pod UID supplied through the named downward-API variable. The fixed command/field identifiers are protocol contracts, not per-deployment settings.

[private-cluster-probe/main.go](../../../cmd/private-cluster-probe/main.go) invokes `Run` and emits a fixed diagnostic on failure. `Run` checks Linux, the actual process architecture and effective UID, then writes one canonical bounded proof to the supplied writer. It does not execute another program, open a network/file, or read ambient credentials. It observes runtime identity through Go and the supplied Pod UID.

The [privateclusters owner](../../modules/privateclusters/README.md) constructs the challenge and Job, resolves cluster/installation/namespace authority, and checks provider observations. Its [provider](../../modules/privateclusters/provider.go), [integrity checks](../../modules/privateclusters/integrity.go) and [run state](../../modules/privateclusters/runs.go) are the upstream consumers. This package does not choose a cluster or authorize a Job by itself.

## Result and failure contract

`Verify` accepts only the exact expected run/namespace/installation/challenge digest, architecture, UID and Pod UID, with the canonical newline-terminated representation. Substituted fields, unknown bytes, unresolved Pod-UID expansion and malformed output fail. A short or failed output write is an error; a partial proof is not success.

Retry/reconciliation, Job UID ownership, log acquisition and cleanup remain with the privateclusters domain. Never weaken the challenge or reinterpret malformed output to recover a failed Job. A completed probe alone does not qualify CUDA, Ascend, model execution, CSI or an arbitrary Kubernetes installation.

## Verification and coordinated changes

With the pinned toolchain and admitted cache/temp roots:

```sh
go -C services/control test ./internal/platform/privateclusterproof
```

[proof_test.go](proof_test.go) verifies canonical challenge/output bindings. The real integration contract is [M28 mainline-contract.md](../../../../../docs/milestones/m28-private-cluster/mainline-contract.md), alongside [the release private-cluster contract](../../../../../contracts/release/v1/private_cluster.go). Changing the proof protocol requires synchronizing this producer/verifier, the manufactured probe artifact and domain Job/log admission; unit proof checks do not replace actual cluster evidence.
