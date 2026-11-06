# Preparation attempt manager

This owner runs one admitted model-preparation attempt in the approved container and returns bound events/artifacts to control. It does not approve evaluation reviews, publish a model version or move a serving alias.

[config.go](config.go) validates the image/source/runtime/resource settings; [manager.go](manager.go) validates platform, installation and actual CPU/memory/GPU admission. [inputs.go](inputs.go) binds base model, adapter and held-out files. Container/Docker files preserve image and mount identity; [lifecycle.go](lifecycle.go) and [journal.go](journal.go) retain the original attempt through start, observation, stop and uncertain external outcomes. [removal.go](removal.go) handles explicitly fenced owned cleanup.

The Transport interface carries preparation-specific file/event/upload/stop contracts through the agent client. Results must match the exact expectation, not merely be valid JSON. Roots cannot overlap node identity or other workload stores. Production Linux amd64/NVIDIA checks remain separate from controlled CPU runtime tests.

Synchronize [model-lifecycle contracts](../../../../contracts/model-lifecycle), [Python preparation](../../../../runtimes/model-lifecycle/README.md), control/modelpreparations and agent runtime/client dispatch, image recipes and recovery tests. The Docker envelope implementation parallels training; mechanical fixes may need both owners, but workload policies and result semantics must stay distinct.

An attempt does not depend on control being reachable ([ADR 0032](../../../../docs/decisions/0032-static-stability.md)). Events, checkpoints and output wait for control with backoff, and only a refusal control wrote ends the attempt. A shutdown leaves a started container running and its journal in place, and `Resumable` and `Resume` let the next agent process adopt it within what remains of its wall limit. A checkpoint event counts as sent only after its upload finished.

Focused check: `go -C services/agent test ./internal/modelpreparations`. Actual container/image, cancellation, upload/readback and target merge/evaluation profiles need their declared resources and original failure receipts.
