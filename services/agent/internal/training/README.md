# Training attempt manager

This manager executes admitted training attempts through an approved container. Control retains dataset/model/Workspace authorization, GPU reservation and acceptance of returned outputs. A worker exit or adapter file is not a published model.

[config.go](config.go) validates image/source/runtime settings and typed resource policy. [manager.go](manager.go) performs actual platform, image installation, CPU/memory and NVIDIA admission, then binds exact command inputs. [container.go](container.go) and [docker.go](docker.go) preserve user, mounts and image identity. [lifecycle.go](lifecycle.go), [journal.go](journal.go) and [removal.go](removal.go) retain attempt phases, stop receipts and explicitly fenced cleanup.

The Transport interface carries input downloads, ordered events, chunked output upload and stop acceptance through the existing agent client. Private workload roots must remain disjoint from node identity and other managers. Interrupted transfer, uncertain Docker action or failed output acceptance preserves its original journal and identifiers rather than starting a new attempt automatically.

Synchronize [training contracts](../../../../contracts/training), [Python runtime](../../../../runtimes/training/README.md), control/training, agent runtime/client, approved image recipes and lifecycle/recovery tests. Preparation has parallel Docker transport code; coordinate mechanical fixes without merging distinct workload contracts.

An attempt does not depend on control being reachable ([ADR 0032](../../../../docs/decisions/0032-static-stability.md)). Events, checkpoints and output wait for control with backoff, and only a refusal control wrote ends the attempt. A shutdown leaves a started container running and its journal in place, and `Resumable` and `Resume` let the next agent process adopt it within what remains of its wall limit. A checkpoint event counts as sent only after its upload finished.

The real-container test of these rules runs as a non-root member of the `docker` group on Linux: `go test -tags static_stability_integration -run TestARealContainer` with `OWNSOLID_STABILITY_DOCKER_SOCKET` and `OWNSOLID_STABILITY_IMAGE` (any Python 3.12 image). It runs a counting process in place of the training image and is lifecycle evidence, not training evidence.

Focused check: `go -C services/agent test ./internal/training`. Actual container isolation, cgroups, checkpoint/resume, current lease revocation and NVIDIA execution need their separate declared profiles. CPU/emulated checks must not be relabeled as production GPU qualification.
