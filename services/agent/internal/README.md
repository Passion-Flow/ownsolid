# Agent execution owners

These packages execute admitted node work under the profile selected by [cmd/agent](../cmd/agent/main.go). They do not own tenant membership, user sessions, model publication or business scheduling.

| Owner | Responsibility |
| --- | --- |
| [runtime](runtime/README.md) | Protected configuration, enrollment/run orchestration, profile/lease dispatch and recovery entrypoints |
| [client](client/README.md) | Authenticated, bounded control wire protocol and artifact/event transfers |
| [settings](settings/README.md) | Shared typed agent timeouts, connection limits and policy relationships |
| [state](state/README.md) | Private node identity and exclusive process lock |
| [discovery](discovery/README.md) | Linux host facts and bounded parsing |
| [components](components/README.md) | Admitted node-exporter/systemd lifecycle and maintenance journal |
| [inference](inference/README.md) | Approved inference image/model, workload/GPU/network ownership and recovery |
| [training](training/README.md) | Training attempt lifecycle, private inputs, events and output transfers |
| [modelpreparations](modelpreparations/README.md) | Preparation attempt lifecycle and exact held-out/adapter/result binding |
| [imageregistry](imageregistry/README.md) | Verified offline image import, loading and retained receipts |

Runtime composes owners and validates complete task leases before dispatch. Managers borrow the configured client/transport and persist exact attempt identities. Shared helpers must not weaken workload-specific image, profile, root, GPU or cleanup requirements. A manager may close only the transport/resources it owns.

Synchronize [agent contracts](../../../contracts/agent), control/nodes and affected business domains, typed [agent examples](../../../config/examples/agent), image/deployment recipes and actual lifecycle tests. Native package tests on a development host do not qualify Linux/systemd/Docker/GPU paths. Preserve the exact profile in every receipt and never substitute a host-compatible profile silently.
