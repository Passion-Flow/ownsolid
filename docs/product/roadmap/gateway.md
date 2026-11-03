# Gateway

The Rust model gateway: one authorized entry for every model call, from users, apps,
coding tools and the product agent.

- Owns: `services/gateway`, `services/control/internal/modules/distribution`.
- Depends on: serving, identity-and-access, tenancy-metering-commerce.

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

## P2 Fleet operations

- [ ] GW-201 Small-model and large-model routing by task, cost and quality (P2) - Catalog: F074
- [ ] GW-202 Content safety hooks and data-flow rules per tenant (P2) - Catalog: F085
- [ ] GW-203 Result cache with invalidation on permission change (P2) - Catalog: F620
- [ ] GW-204 Client setup templates for SDKs, CLIs and coding tools (P2) - Catalog: F084, F087

## P3 Multi-site

- [ ] GW-301 Cross-site routing with locality and failover (P3)
- [ ] GW-302 Streaming retry and failover semantics across sites (P3) - Catalog: F619

## P4 Breadth

- [ ] GW-401 Audio, image and video endpoints (P4)
- [ ] GW-402 Gemini native protocol (P4) - Catalog: F073

LiteLLM and New API adapters are in the compatibility matrix.
Catalog rows absorbed: F071 to F089, F616 to F621.
