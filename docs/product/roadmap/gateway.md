# Gateway

The Rust model gateway: one authorized entry for every model call, from users, apps,
coding tools and the product agent.

- Owns: `services/gateway`, `services/control/internal/modules/distribution`.
- Depends on: serving, identity-and-access, tenancy-metering-commerce.

Reviewed 2026-11-08 against LiteLLM virtual keys, Portkey, OpenAI project API keys and Bifrost documentation ([research](../../research/gateway-keys.md)). The review added GW-114, GW-115 and GW-116, and GW-113's live run added GW-117: the installer starts no model gateway at all, keys cannot rotate on a schedule or warn their owner before they expire, and a key cannot be narrowed to some endpoints or made read-only, and no page publishes a model to the gateway. GW-117's research added GW-118: a gateway instance's credential cannot be replaced, so a lost one retires every alias on that node. GW-114's prototype added GW-121 and GW-122: each node's gateway is its own base URL until one entrance routes to them, and control can only issue certificates from its own CA ([research](../../research/gateway-delivery.md)). Building GW-114 added GW-123, GW-124 and GW-125: only the inference profile runs gateways, their certificates are not yet renewed, and callers have no page that hands them the CA.

## P1 Enterprise release

- [ ] GW-101 OpenAI Chat Completions with streaming (P1) - Catalog: F073
  - Text-only Chat messages accept strings and ordered text parts. [Native TLS acceptance](../../research/gateway-chat-streaming.md) verifies the same prompt at tokenization and generation, strict rejection and durable JSON/SSE usage. Current production backend and UI qualification remain open.
- [ ] GW-102 OpenAI Embeddings and rerank endpoints (P1) - Catalog: F073
  - [Pooling prerequisites](../../research/gateway-pooling.md) identify the offline serving recipes and task-aware admission required before gateway implementation.
- [ ] GW-103 OpenAI Responses API and Anthropic Messages API for coding tools (P1) - Catalog: F073
  - Foreground Responses accepts strings and stateless text message items over JSON and SSE and uses the same authorization and token accounting as Chat Completions ([protocol and acceptance](../../research/gateway-responses.md)). Anthropic foreground text Messages uses the same pipeline ([protocol and acceptance](../../research/gateway-messages.md)). The [tool round-trip research](../../research/gateway-tool-roundtrip.md) records why the pinned serving recipe keeps tool calls disabled. Stateful requests, tool calls, generated public contracts, coding-tool interoperability, and real engine acceptance for these adapters remain open.
- [ ] GW-104 Model aliases mapped to deployments, with capability metadata (P1) - Catalog: F072
  - Publication binds a ready deployment revision. The native gateway also checks alias identity after queueing and accepts a fresh target for the same alias. [Offline acceptance and capability design](../../research/gateway-alias-capabilities.md). The public capability fields, generated client contract and catalogue UI remain open.
- [ ] GW-105 Keys scoped by workspace, app, model and expiry; upstream credentials never exposed (P1) - Catalog: F078, F079
  - [One-use key rotation regression and candidate](../../research/gateway-key-purpose.md) verified in disconnected PostgreSQL. Production integration needs the shared catalog owner to land both schema signatures with the migration. Persistent application identity remains open.
- [ ] GW-106 Rate limits, concurrency and token-aware budgets per tenant, app and key (P1) - Catalog: F080, F617
  - [Native quota acceptance](../../research/gateway-limits.md) used real workspace keys and PostgreSQL receipts to verify one application's shared monthly ceiling and another application's continued access. Automatic application assignment and policy UI remain open.
- [ ] GW-107 Usage ledger: tokens, requests, latency per call, exportable (P1) - Catalog: F081
  - Admin and Portal download scoped CSV attachments through the production request dispatcher. [Offline TLS and PostgreSQL acceptance](../../research/gateway-usage-history.md) covers JSON-cursor continuation, retries, cancellation, unknown usage and receipt integrity. A shared-writer continuation link passed in an isolated overlay. The [CSV contract](../../research/gateway-usage-csv-contract.md) uses JSON cursors. Production link integration and UI remain open.
- [ ] GW-108 Load balancing, failover and circuit breaking across replicas (P1) - Catalog: F075, F076
- [ ] GW-109 Request tracing from caller to deployment and GPU (P1) - Catalog: F082
  - [Request correlation acceptance](../../research/gateway-request-tracing.md) followed native Responses and Messages UUIDs through engine dispatch, PostgreSQL history and authenticated Admin placement lookup. Real GPU execution and distributed spans remain open.
- [ ] GW-110 Optional external OpenAI-compatible upstreams for connected sites (P1) - Catalog: F071
- [ ] GW-111 Configuration publishing with diff preview and rollback (P1) - Catalog: F083
  - The Admin preview route checks pause, resume, retarget and rollback with the same current state as apply. [Disconnected TLS and PostgreSQL acceptance](../../research/gateway-cutover-preview.md) covers parity, no writes, CSRF, stale revision and foreign refusal. The public contract and Admin CLI are available. The Console flow and native cutover and rollback between two authorized ready deployments remain open.
- [ ] GW-112 Load tested: requests per second and added latency at measured scale, published (P1) - Catalog: F086
  - The [component and native-path measurements](../../research/gateway-throughput.md) record synthetic throughput, two removed PostgreSQL aggregates and prompt capacity release after durable outcomes, and native retry after a committed acknowledgement is lost. Sustained installation-scale throughput and added latency remain open.
- [x] GW-113 Gateway and keys page: issue, rotate and revoke keys, with each key's call log (S-206) (P1) - Evidence: 4c1eb36, b9e6728; live run on an installation, 2026-11-08, twice: keys issued, rotated with an overlap and revoked from the browser against a real control plane and database, calls admitted over the real gateway protocol by a stand-in for the gateway process (GW-114) and read per key; no model ran, and aliases are published through the API until GW-117
- [x] GW-114 The model gateway runs on each serving node, started by its agent when a deployment is published, with its identity provisioned on the node and no network needed ([ADR 0052](../../decisions/0052-the-model-gateway-runs-beside-the-model.md)) (P1) - Evidence: 90ebbad; live on an installation, 2026-11-09: a real agent (protocol 1.6) started the real gateway image when its gateway was registered, served TLS with the certificate control issued for its declared origin, and stopped it with its secrets removed when revoked; from the browser a deployment was published through its node's own gateway with no address typed and no credential shown, a key issued from the card, and calls with it reached vLLM's CPU build serving Qwen3-0.6B (plain and streamed, wrong key refused, each call in the key's log with the engine's token counts). The deployment's node was scripted, with its gateway tasks run by the agent's own gateway code; no GPU
- [x] GW-117 Publish a deployment to the gateway from the console: provision a gateway instance and name the alias a key calls (P1) - Evidence: c6936b9, b45b879; live run on an installation, 2026-11-08: a verified deployment on a scripted node was published from its page, which registered the node's gateway and showed its credential once; a key issued from the same card made two calls admitted over the real gateway protocol with that credential by a stand-in for the gateway process (GW-114), and after Stop publishing the name was refused; no model ran
- [ ] GW-118 Gateway instances on the Gateway page: list each node's gateway, revoke one, and replace a lost credential without retiring its aliases (P1)
- [ ] GW-120 Say what a published model is for: an administrator's description, written when publishing and changeable later, shown in the portal's catalogue (P1)
  - [Disconnected PostgreSQL and HTTPS qualification](../../research/gateway-model-description.md) covers publication, independent edits, legacy replay, concurrent revision conflicts and Portal reads against the candidate. The public contract is available. Production migration order and catalogue signatures, Console editing and S-304 remain open.
- [ ] GW-121 One address for every model: the model gateway entrance on the installation's TLS port routes each call by its name to the node gateway that serves it ([ADR 0027](../../decisions/0027-entrance-addresses.md), [ADR 0052](../../decisions/0052-the-model-gateway-runs-beside-the-model.md)) (P1)
- [ ] GW-122 Node gateways under a site's own PKI: place the serving certificate and key on the node instead of control issuing them (P1)
- [ ] GW-123 The AI and model lifecycle profiles run node gateways too, as the inference profile does since GW-114 (P1)
- [ ] GW-124 Node gateways renew their serving certificates before they expire, without dropping calls (P1)
- [ ] GW-125 Callers get the model gateway CA from the console and the portal, beside each endpoint and code sample (P1)
  - Authorized aliases serve the loaded gateway CA as a PEM attachment. [Disconnected PostgreSQL and TLS acceptance](../../research/gateway-delivery.md#downloading-caller-trust-gw-125) verifies a serving certificate and rejects mixed PEM. The [CA download contract](../../research/gateway-trust-download-contract.md) is published. Console and Portal links remain open.

## P2 Fleet operations

- [ ] GW-201 Small-model and large-model routing by task, cost and quality (P2) - Catalog: F074
- [ ] GW-202 Content safety hooks and data-flow rules per tenant (P2) - Catalog: F085
- [ ] GW-203 Result cache with invalidation on permission change (P2) - Catalog: F620
- [ ] GW-204 Client setup templates for SDKs, CLIs and coding tools (P2) - Catalog: F084, F087
- [ ] GW-115 Scheduled key rotation, and a notice to a key's owner before the key expires (P2)
- [ ] GW-116 Restricted keys: allow a key only some endpoints, or only reads (P2)
- [ ] GW-119 Decide who may issue keys: a workspace policy that lets an administrator allow or stop members issuing their own keys, enforced on the server and explained in the portal (P2)

## P3 Multi-site

- [ ] GW-301 Cross-site routing with locality and failover (P3)
- [ ] GW-302 Streaming retry and failover semantics across sites (P3) - Catalog: F619

## P4 Breadth

- [ ] GW-401 Audio, image and video endpoints (P4)
- [ ] GW-402 Gemini native protocol (P4) - Catalog: F073

LiteLLM and New API adapters are in the compatibility matrix.
Catalog rows absorbed: F071 to F089, F616 to F621.
