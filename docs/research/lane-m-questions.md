# Lane M boundaries

The current offline import flow accepts the one embedded Qwen3-0.6B revision.
The roadmap boxes below remain open. Preserve the current contracts until the
responsible owners can extend their respective boundaries.

| Item | Boundary and continuation |
| --- | --- |
| MOD-101 | Two real pinned manifests pass source and offline readback checks. The Admin catalogue, database model-key restriction, archive verifier and Agent transfer still bind the product to one approved digest. Agree the additive catalogue and digest-bound transfer contract with the API, web and architecture owners. See the [verified snapshots and integration boundary](model-registry.md#two-pinned-source-manifests). |
| MOD-102 | The Admin catalogue endpoint returns a single approved descriptor, and the existing UI facets cover that descriptor or already imported rows. Type and capability coverage for the catalogue needs a multi-entry read contract and its UI consumer. |
| MOD-103 | Imported and derived manifests preserve some provenance today. Multiple versions and complete training and evaluation lineage require shared lifecycle producers and a version/lineage view. Evaluation is owned by SCI. |
| MOD-104 | Chat-template defaults need to reach the serving engine with an explicit command or request policy. The Agent inference adapter and its wire command are shared boundaries. |
| MOD-105 | The preparation worker accepts the existing pinned recipe. LoRA merge and format conversion need an admitted recipe and an Agent runner that can dispatch and verify the new output; the current runtime alone cannot make the job usable. |
| MOD-106 | Serving variants require recipe admission and a serving manifest accepted by the Agent, including matching quantization arguments and validation. |
| MOD-107 | Deployment and alias reference checks already run in the shared retirement callback. Complete application and key references require their owning modules to participate, and the existing UI only receives a generic refusal. |
| MOD-108 | The current exact allowlist rejects all extra files. Scanning imported models beyond the approved revision depends on MOD-101 and a shared verifier and review flow. |
| MOD-109 | The Agent fetches the approved files through the leased transfer path. Pre-warming from the site registry requires NET-102 and Agent changes. |
| MOD-110 | Existing import acknowledgement does not enforce per-version use rights. Installation policy and dispatch authorization must reach every deployment and task admission owner, including SCI and training. |
| MOD-113 | The signed index needs a trust root and delivery integration, plus a new multi-entry contract consumed by the UI. No unsigned index should replace the embedded trust decision. |
| MOD-114 | Offline search depends on MOD-113. The browser's current local filter covers imported inventory. |
| MOD-115 | The exact pinned fetch manifest depends on MOD-113. The connected-side command belongs in tools/cli, outside this lane. |
| MOD-116 | Multi-model publication and tokenizer staging depend on MOD-101 and MOD-113. The Agent must verify the same pinned files and serve exclusively from the staged path. |
| MOD-121 | A configured expiry needs an installer/control policy field and a user-facing terminal expiry reason. The existing cancellation intent is tied to a live administrator action and cannot be reused as though a user requested expiry. |
| MOD-101 / MOD-119 archive instructions | The console's BSD tar command on macOS emits USTAR headers that Go classifies as unknown, even when every pinned payload hash matches. The existing importer accepts canonical USTAR. Use GNU tar or Python's `tarfile.USTAR_FORMAT` for the verified fixture. Updating the published console instructions or widening the shared archive verifier belongs to their owners. |

## Serving P1

| Item | Boundary and continuation |
| --- | --- |
| SRV-101 | Deploy and observe are implemented for the approved Agent recipe. Generate, cancel, metrics and unload across engines need a shared runtime and gateway contract. |
| SRV-102 | The current vLLM recipe and transfer are pinned to the approved Qwen revision. General import (MOD-101) and verified model-specific Agent configuration are prerequisites. |
| SRV-103 | Embedding and rerank need imported model types, Agent tasks and gateway request/response support. |
| SRV-104 | Writable compiler and CUDA cache mounts are set on the inference container by the architecture lane. Qualification needs a real GPU with egress blocked. |
| SRV-105 | Multiple replicas and rolling replacement need shared deployment commands, per-replica readiness and gateway route draining. Reserve spare capacity before promotion. |
| SRV-106 | Queue depth and latency come from engine and gateway telemetry. Scale to zero also needs a wake-up path for new requests. |
| SRV-107 | Hot LoRA requires Agent runtime changes and matching generation semantics in the gateway. |
| SRV-108 | CPU serving requires an offline CPU image and Agent execution profile. The current Agent preflight requires a supported GPU. |
| SRV-109 | Gateway and engine owners need to propagate cancellation and deadlines across live response streams. |
| SRV-110 | The available test hosts have no qualified NVIDIA GPU for the required end-to-end run. |
| SRV-112 | Names and descriptions need versioned request and response fields and web integration. A database column alone leaves the feature inaccessible. |
| SRV-113 | Preserve recorded revision and admission state, then roll back through the health-gated route switch and drain in SRV-105. An in-place restart cannot preserve in-flight traffic. |
| SRV-114 | Deployment metrics and logs need observability and gateway producers. Scale and rollback dialogs and the rolling progress view need the web lane. |
| SRV-115 | A conservative fit breakdown needs qualified engine profiles, current hardware facts and a shared preview contract. See [serving admission research](serving-admission-and-rollout.md). |

## P2 boundaries

| Items | Prerequisites and ownership |
| --- | --- |
| MOD-117, MOD-118 | A pinned and verified source manifest (MOD-113 and MOD-116) and a configured acquisition policy. Relay and direct network access need explicit origin, redirect and DNS constraints. |
| MOD-201 | The in-site registry and Agent pre-warming (NET-102 and MOD-109) precede peer distribution and rack seeding. |
| MOD-202 | Publishing requires the signed model index/bundle flow and the separate application bundle owner. |
| MOD-203 | Checkpoint and evaluation report retention depend on training, SCI and backup policy. |
| MOD-204 | A per-version compatibility declaration depends on the model version/manifest contract and admission consumers. |
| SRV-201, SRV-202, SRV-203 | Cache-aware routing, disaggregation and cache tiers depend on replica routing (SRV-105) and engine/gateway instrumentation and isolation. |
| SRV-204, SRV-205, SRV-206 | Alternative engines, Ascend and parallel workers require the Agent engine contract (SRV-101) and hardware execution profiles. |
| SRV-207 | Traffic splitting and rollback depend on ready replicas and revision routing (SRV-105 and SRV-113). |
