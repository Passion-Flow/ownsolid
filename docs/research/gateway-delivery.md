# Delivering the model gateway

Research behind GW-114: an installation has to run the Rust gateway so that a key reaches a model. It records
what the code required, what a prototype on a real model showed, and the approach [ADR 0052](../decisions/0052-the-model-gateway-runs-beside-the-model.md)
takes from it.

## What the code required

- **The gateway is bound to one node and one backend.** Its configuration names one `gateway_id`, one `node_id`
  and one `backend_address` (`services/gateway/src/configuration.rs`). The control plane allows one active
  gateway instance per node in a workspace, and a node serves one deployment (`placement.go`), so one process
  per serving node matches the model.
- **Both of its addresses had to be loopback.** `validate()` refused a `listen_address` or `backend_address`
  that was not loopback with `GATEWAY_LOOPBACK_BINDING_REQUIRED`. Since the prototype only the backend must
  be: the listener takes the address the operator chooses (`GATEWAY_LISTEN_ADDRESS_INVALID` for port 0,
  multicast, broadcast, or the backend's own port on the same or every address).
- **The model is only reachable on that node's loopback.** The agent publishes the inference container on
  `127.0.0.1` at the recipe's `HostPort`, on a bridge whose host binding is `127.0.0.1`
  (`services/agent/internal/inference/network.go`).
- **Its identity cannot exist at install time.** `adminProvisionGateway` needs a node and a workspace and shows
  the credential once, so it can only happen after a node has joined.
- **Nothing exposes it to callers.** `ownsolid-install` knows two origins, portal and admin. An alias advertises
  a `client_origin` that the control plane treats as metadata only. ADR 0027 calls for a model gateway entrance
  with its own hostname, and no listener serves one.

## How others deliver it

- **LiteLLM, Portkey and Kong's AI gateway** run as a central proxy in front of model servers reached over
  the network ([LiteLLM proxy deployment](https://docs.litellm.ai/docs/proxy/deploy),
  [Kong AI Proxy](https://developer.konghq.com/plugins/ai-proxy/)). That fits model servers with their own
  authentication. ownsolid's engine has none and is published on the node's loopback only, behind an egress
  guard, so the authenticating hop has to sit on the node.
- **KServe and the vLLM production stack** put a router in front of engine pods and authenticate at the
  ingress ([KServe](https://kserve.github.io/website/latest/modelserving/v1beta1/llm/huggingface/),
  [vLLM production stack](https://docs.vllm.ai/projects/production-stack/en/latest/)). The single address is
  a second tier; ownsolid's equivalent is GW-121, one entrance for every model, which routes to these node
  gateways.
- **Kubernetes' kubelet and node-local agents** generate their key on the node and send only a request for
  what control signs ([kubelet TLS bootstrapping](https://kubernetes.io/docs/reference/access-authn-authz/kubelet-tls-bootstrapping/)),
  as ARC-103 does for the agent's certificate. The gateway's credential follows the same rule.

## Prototype, 2026-11-08

Run on `ownsolid-cpu` (16 Xeon 8168 cores with AVX-512F but no AVX-512 BF16 or VNNI, no GPU) against lane B's
installation, with the network of the gateway's process limited to the host.

- **Engine.** vLLM's CPU build, `public.ecr.aws/q9t5s3a7/vllm-cpu-release-repo:v0.29.0` (Apache-2.0), the newest
  CPU tag published; the product's recipe pins the CUDA build 0.31.0, which has no CPU tag. It served the
  product's Qwen3-0.6B files (Apache-2.0) as `projectone-qwen3-0.6b` on `127.0.0.1:18100`, offline
  (`HF_HUB_OFFLINE`, no usage stats), 8 CPUs and 10 GiB, about four minutes to load and warm up.
- **Identity.** A gateway instance and an alias were provisioned through the control API, as the deployment
  page does since GW-117, for a scripted node: the deployment's state was synthetic, the model was real. The
  credential was copied into the gateway's private files by a script, which is the hand copy ADR 0052 removes.
- **Gateway.** The real `projectone-gateway` 0.7.0, built for linux/amd64 with `rust:1.99.1-slim-trixie`, ran
  in `debian:trixie-slim` on the host network, configured by a 0600 JSON file.
- **Calls.** `/v1/models` listed the alias; a chat call answered "Hello!" in 9.6 s (the first call), later
  ones in under a second; a streamed call from another network namespace (a container on Docker's bridge
  calling the listener on 172.17.0.1) ended with `data: [DONE]`; a wrong key got 401 `invalid_api_key`.
  Control's call log showed each call `reported` with the engine's real token counts (22 + 3, 20 + 9,
  28 + 8), under the key and alias that made it.

It found three defects, fixed with tests in the same item:

1. **The gateway did not start without the system's CA bundle** (`GATEWAY_BACKEND_CLIENT_INVALID`). The
   backend client is plain HTTP on loopback, but reqwest loads the platform verifier, and therefore the
   system roots, for any client not given its own trust list. A minimal image has none. The backend client
   now gets an empty trust list; the fixed binary served calls in an image without `/etc/ssl/certs`.
2. **HTTP/2 clients were dropped after the handshake.** axum-server's TLS defaults offer `h2` and `http/1.1`
   over ALPN while the server speaks HTTP/1.1 only, so curl, Go's `net/http` and any client preferring `h2`
   got a closed connection. ALPN now offers `http/1.1` only; a test hands the listener a client that prefers
   `h2` and fails when `h2` is offered again (checked by restoring the old list).
3. **Callers could not reach the listener.** Loopback-only listening left no way in from another host.
   The listener now binds the configured address; the backend rule is unchanged, because the engine has no
   authentication of its own.

The image `make image-gateway` builds (33 MB: the binary on `debian:trixie-slim`, no CA bundle, uid 10001)
then ran the same calls with a read-only root filesystem, every capability dropped and `no-new-privileges`,
its private files mounted at `/run/projectone-gateway`: a plain answer, a streamed one ending `data: [DONE]`,
404 `model_not_found` for a name the key does not allow, and exit 0 on stop.

One rule held as designed: a state directory belongs to one gateway identity, and a gateway given a new
identity over an old directory stops with `GATEWAY_STATE_OWNER_CONFLICT`. The agent therefore keeps one
state directory per gateway instance.

Not exercised: a GPU and the CUDA engine, an agent starting the gateway, a credential generated on the node,
a serving certificate issued by control, and HTTP/2 itself.

## The approach

[ADR 0052](../decisions/0052-the-model-gateway-runs-beside-the-model.md) records it. The agent runs the
gateway on the serving node beside the model, from an image in the offline bundle; the gateway's credential
is generated on the node and only control's digest of it is stored; its serving certificate is issued by
control from an installation CA for the address the node declares; callers reach the node's gateway at
that address, which is the alias's base URL. One address for every model is GW-121.

Rejected:

- **Starting the gateway from the installer on the control host.** The engine is only on the serving node's
  loopback; a control-host gateway would need the engine exposed on the network without authentication.
- **A credential issued by the installer or shown to an administrator to copy.** No node exists at install
  time, and a copied secret is the leak ARC-103 removed for the agent.
- **A forwarder on the node in front of a loopback listener.** It adds a process and a TLS hop and protects
  nothing the gateway's own TLS listener and key check do not.

## How it is built

- **Protocol** (`api/agent/v1/gateway.go`): tasks `gateway.start.v1` and `gateway.stop.v1` for the
  `docker_inference` profile at protocol 1.6; a start carries the gateway ID and the control origin
  its instance is bound to. The managed-node capability check accepts the new list beside the old
  one, so enrolled nodes keep working (migration 000057).
- **Agent** (`services/agent/internal/inference/gateway*.go`): the node's `gateway` configuration
  names the listen address, the declared origin, the image ID and a private root. A start checks
  the image, removes any older gateway container, generates the credential and a P-256 key,
  activates them, checks the certificate (its key, the declared host, server use, the returned CA),
  writes the files to `<root>/<gateway>/config` (read-only in the container) beside a kept
  `state` directory, runs the container locked down and waits for a verified TLS handshake that
  presents the issued certificate. A stop removes the container and the secrets and keeps the spool.
- **Control**: registering a gateway on such a node queues the start and stores no credential and no
  origin (`delivery='agent'`). The activation, under the task's lease, stores the credential's
  digest and the origin, issues the certificate (`agentidentity.Authority.IssueServing`) and is
  audited as `gateway.instance_activated`. Results record `delivery_state`; revoking queues the stop.
  Names cannot be published until the gateway runs (`GATEWAY_NOT_STARTED`).
- **Installer**: a separate model gateway CA, `config/model-gateway-ca.pem`, named in control's
  `model_gateway_identity`; `runtime-config.json` says `agent_gateways`.
- **Console**: the publish dialog asks no address and shows no credential for such a node, waits
  while the agent starts the gateway, names the model when it runs, and explains a failed start
  with a way to replace the gateway.

Known limits, each a roadmap item: other profiles (GW-123), certificate renewal (GW-124), handing
callers the CA (GW-125), one address for every model (GW-121), a site's own PKI (GW-122), and the
signed bundle that carries the image (DEL-101).

## What GW-114 must do

- The offline bundle carries a pinned gateway image, and an installation needs no network to run it. The
  image exists (`deploy/images/gateway`); the bundle (DEL-101) and the agent's delivery of it do not yet.
- Publishing a deployment on a node starts that node's gateway without anything copied by hand: the agent
  generates the credential, writes the configuration and private files, and runs the gateway with one state
  directory per gateway instance; revoking the gateway stops it. Stopping the deployment leaves it
  answering that the model is unavailable, because the gateway belongs to its instance, not to one
  deployment (ADR 0052, amendment).
- The gateway serves on the address the node declares, with a certificate callers can verify against the
  installation's CA, and the alias's base URL is that address.
- A call with an issued key reaches the model and appears in the key's call log, run once on an
  installation; failures (engine not ready, control unreachable, bad key) answer with the gateway's codes.

Tests: unit tests for the configuration rules and the listener's ALPN (done), agent tests for the gateway's
lifecycle and its private files, control tests for the credential digest and the certificate, and a live run
with the real image.
