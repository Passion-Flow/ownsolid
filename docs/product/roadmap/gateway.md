# Gateway

The Rust model gateway: one authorized entry for every model call, from users, apps,
coding tools and the product agent.

- Owns: `services/gateway`, `services/control/internal/modules/distribution`.
- Depends on: serving, identity-and-access, tenancy-metering-commerce.

Reviewed 2026-11-08 against LiteLLM virtual keys, Portkey, OpenAI project API keys and Bifrost documentation ([research](../../research/gateway-keys.md)). The review added GW-114, GW-115 and GW-116, and GW-113's live run added GW-117: the installer starts no model gateway at all, keys cannot rotate on a schedule or warn their owner before they expire, and a key cannot be narrowed to some endpoints or made read-only, and no page publishes a model to the gateway. GW-117's research added GW-118: a gateway instance's credential cannot be replaced, so a lost one retires every alias on that node.

## P1 Enterprise release

- [ ] GW-101 OpenAI Chat Completions with streaming (P1) - Catalog: F073
- [ ] GW-102 OpenAI Embeddings and rerank endpoints (P1) - Catalog: F073
- [ ] GW-103 OpenAI Responses API and Anthropic Messages API for coding tools (P1) - Catalog: F073
- [ ] GW-104 Model aliases mapped to deployments, with capability metadata (P1) - Catalog: F072
- [ ] GW-105 Keys scoped by workspace, app, model and expiry; upstream credentials never exposed (P1) - Catalog: F078, F079
- [ ] GW-106 Rate limits, concurrency and token-aware budgets per tenant, app and key (P1) - Catalog: F080, F617
- [ ] GW-107 Usage ledger: tokens, requests, latency per call, exportable (P1) - Catalog: F081
- [ ] GW-108 Load balancing, failover and circuit breaking across replicas (P1) - Catalog: F075, F076
- [ ] GW-109 Request tracing from caller to deployment and GPU (P1) - Catalog: F082
- [ ] GW-110 Optional external OpenAI-compatible upstreams for connected sites (P1) - Catalog: F071
- [ ] GW-111 Configuration publishing with diff preview and rollback (P1) - Catalog: F083
- [ ] GW-112 Load tested: requests per second and added latency at measured scale, published (P1) - Catalog: F086
- [x] GW-113 Gateway and keys page: issue, rotate and revoke keys, with each key's call log (S-206) (P1) - Evidence: 4c1eb36, b9e6728; live run on an installation, 2026-11-08, twice: keys issued, rotated with an overlap and revoked from the browser against a real control plane and database, calls admitted over the real gateway protocol by a stand-in for the gateway process (GW-114) and read per key; no model ran, and aliases are published through the API until GW-117
- [ ] GW-114 The installer delivers and starts the model gateway on its entrance, with its identity provisioned and no network needed (P1)
- [ ] GW-117 Publish a deployment to the gateway from the console: provision a gateway instance and name the alias a key calls (P1)
- [ ] GW-118 Gateway instances on the Gateway page: list each node's gateway, revoke one, and replace a lost credential without retiring its aliases (P1)

## P2 Fleet operations

- [ ] GW-201 Small-model and large-model routing by task, cost and quality (P2) - Catalog: F074
- [ ] GW-202 Content safety hooks and data-flow rules per tenant (P2) - Catalog: F085
- [ ] GW-203 Result cache with invalidation on permission change (P2) - Catalog: F620
- [ ] GW-204 Client setup templates for SDKs, CLIs and coding tools (P2) - Catalog: F084, F087
- [ ] GW-115 Scheduled key rotation, and a notice to a key's owner before the key expires (P2)
- [ ] GW-116 Restricted keys: allow a key only some endpoints, or only reads (P2)

## P3 Multi-site

- [ ] GW-301 Cross-site routing with locality and failover (P3)
- [ ] GW-302 Streaming retry and failover semantics across sites (P3) - Catalog: F619

## P4 Breadth

- [ ] GW-401 Audio, image and video endpoints (P4)
- [ ] GW-402 Gemini native protocol (P4) - Catalog: F073

LiteLLM and New API adapters are in the compatibility matrix.
Catalog rows absorbed: F071 to F089, F616 to F621.
