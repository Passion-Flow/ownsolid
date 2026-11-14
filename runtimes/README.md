# Bounded AI and data execution

Python runtimes perform computation for Go domain owners. They do not serve the Portal/Admin API, authenticate customers, hold business database credentials or commit business state.

| Runtime | Input and output | Business owner |
| --- | --- | --- |
| [data-processing](data-processing/README.md) | Protected JSON request and source; validation or prepared publication files | Control datasets |
| [training](training/README.md) | Protected attempt/model/dataset configuration; events, checkpoint and adapter/result files | Control training and admitted node agent |
| [model-lifecycle](model-lifecycle/README.md) | Protected accepted adapter/model/held-out inputs; evaluation and prepared merged model | Control modelpreparations and admitted node agent |
| [knowledge](knowledge/README.md) | Bounded stdin JSON and local encoder; bounded chunk/vector JSON result | Control knowledge |

## Shared boundary

The caller verifies installed executable/source identity, creates a private attempt namespace and sends an exact versioned request. The worker validates inputs and resource limits, uses pinned offline dependencies/models and returns bounded structured results. Go independently checks current authorization, state, inputs and output integrity before publication or acceptance.

Prepared output, exit zero, CPU verification and container import are distinct from business acceptance and native target qualification. Failures and partially durable artifacts remain bound to the original attempt; do not reuse an old output directory for a new attempt or treat missing result bytes as success.

## Dependencies and distribution

Training imports data-processing helpers. Model-lifecycle imports both training and data-processing and inherits the training runtime/image dependencies; it is not independently installable from its own directory. Knowledge owns its separate pinned CPU encoder stack. These dependencies are reflected in the release builders and must stay in source/worker fingerprints.

When changing a runtime, synchronize its contracts/generators, Go request/result validator, agent profile where applicable, dependency locks, image recipe, source/archive manifest, resource policy and actual failure/cancellation/recovery tests. Python should not gain control credentials to bypass an inconvenient orchestration step.

See [services](../services/README.md), the [backend audit](../docs/architecture/current-product/backend-audit.md) and [STATUS](../docs/STATUS.md).
