# Node agent

The Go agent executes work for one enrolled node under an explicit execution profile. Control retains business authorization and task state; a local image, imported archive or successful command does not independently authorize execution.

## Entrypoints and owners

The [internal owner index](internal/README.md) links each execution manager and shared state/transport/configuration guide.

[sessiontransport](sessiontransport/README.md) is the outgoing gRPC transport owner
shared with the development simulator. It authenticates no node and retries no task.

[cmd/agent/main.go](cmd/agent/main.go) dispatches facts, enroll, run, exact fenced-workload removal and explicit Registry operations. [internal/runtime/runtime.go](internal/runtime/runtime.go) loads protected typed configuration and composes managers. [lease.go](internal/runtime/lease.go) checks the complete operation/profile/payload before dispatch.

| Internal owner | Responsibility |
| --- | --- |
| [client](internal/client) | Versioned authenticated control calls and task/result transport |
| [discovery](internal/discovery) | Host/node facts |
| [components](internal/components) | Admitted managed component lifecycle |
| [inference](internal/inference) | Approved inference image, workload/network/GPU ownership and recovery |
| [training](internal/training) | Approved training container, input/output transfer and journal |
| [modelpreparations](internal/modelpreparations) | Approved preparation container, input/output binding and journal |
| [kubernetes](internal/kubernetes) | RKE2 install, join, bootstrap tokens and readiness for the kubernetes_node profile (ADR 0051) |
| [imageregistry](internal/imageregistry) | Verified offline Registry import/loading |
| [progress](internal/progress) | What a running task reports: its steps and its log, bounded, and never deciding the task |
| [runtime](internal/runtime) | Profile dispatch, lifecycle and fenced-removal entrypoints |
| [settings](internal/settings) | Validated bounded runtime policy |
| [state](internal/state) | Private persisted node identity, process lock and durable creation |

## Offline read-only enrollment

Once the local Agent binary, site CA, private state directory, protected JSON
configuration and token file are prepared, run:

```sh
agent enroll --if-needed --config /etc/ownsolid-agent/agent.json
```

A first run enrolls over verified HTTPS. A completed matching enrollment is
recognized locally, including a pre-certificate read-only identity. An
installer can replay this command after removing the spent token file and
start `agent run` only after it succeeds. Bare `agent enroll` still refuses an
existing identity. On an uncertain enrollment or a committed enrollment with
a local write failure, preserve the state and reconcile the Control record
before attempting a new join. See [offline enrollment and release handoff](../../docs/research/agent-offline-enrollment.md).

## Configuration, data and failures

Use [agent examples](../../config/examples/agent), the corresponding [agent contracts](../../contracts/agent) and explicit protected config/env-file flags. Component, inference, training and preparation roots must satisfy their profile and disjointness rules. Credentials stay in protected state/reference files; never place them in task logs or image layers.

Managers inspect actual image identity, architecture, entrypoint, source/version labels and container/mount ownership. Task leases and persisted journals identify exact workload attempts. Revocation, timeout, partial transfer and interrupted external work need their existing recovery/fencing paths. Cleanup requires exact admitted workload ownership and retained receipt, not a broad container prune.

Training and preparation currently contain parallel Docker transport/image-envelope implementations. Changes to shared protocol behavior need both paths checked; workload-specific policy must remain distinct. The Registry loader consumes the shared bundle library under tools/release/offline, which is therefore a production source dependency.

## Changing and checking

Synchronize control/nodes and the owning domain, contracts/agent and task contracts, agent client/profile dispatch, manager policy/journal, typed configuration examples, systemd/container recipes and source/image manifests.

Focused native example after toolchain/resource admission:

    go -C services/agent test ./internal/runtime ./internal/state ./internal/training ./internal/modelpreparations

Actual daemon, Linux ownership/cgroups, NVIDIA execution and target images require their declared harnesses. Native or emulated tests do not establish target qualification. See [backend audit](../../docs/architecture/current-product/backend-audit.md).

## Agent gateway preference

With multiple `session_addresses`, a node on a fallback gateway periodically tries
an authenticated heartbeat on a higher-ranked gateway. It moves future claims
only after that heartbeat succeeds. With a single address or no explicit gateway
list, there is no preference check.

Set `gateway_rebalance_seconds` in the agent JSON configuration to change the
minimum wait between checks. It defaults to 60 seconds and accepts 5 through
3,600 seconds. Each wait adds up to one more interval of jitter. Restart the
agent to apply a change.

Dedicated gateways can also advertise their existing physical connection age in
optional first-reply metadata. A newer agent authenticates a replacement before
that age between exchanges, then leaves the old transport to retire naturally.
This applies with one gateway as well as multiple gateways. No protocol cutover or
simultaneous fleet upgrade is required; missing or invalid hints retain the prior
reconnect behavior. See [rollout and remaining acceptance](../../docs/research/agent-gateway-rebalance.md#age-handoff-prototype-requirements).


A session that has never completed a valid stream heartbeat keeps the original
two paired probes and five-minute HTTPS fallback. Once that process session has
used an active or disabled stream, a later fallback becomes eligible to retry
after the lesser of five minutes and its currently adopted maximum reconnect
backoff. The next ordinary paced cycle and retry hints still govern the actual
attempt; no background probe or task replay is added.
Restarting the Agent forgets this transport history while preserving its normal
persisted identity and acknowledged cursor. See [the policy decision](../../docs/decisions/0083-bound-fallback-after-a-usable-agent-stream.md).
