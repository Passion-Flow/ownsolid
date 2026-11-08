# 0052: The model gateway runs beside the model

- Status: accepted (GW-114)
- Date: 2026-11-08

## Context

An installation starts no model gateway, so an issued key reaches no model. ADR 0049 scoped GW-114 to the
installer and `services/gateway`. [The research](../research/gateway-delivery.md) showed that this does not
work: the engine is published only on the serving node's loopback, behind an egress guard, and has no
authentication of its own. The gateway therefore has to run on that node. A prototype on `ownsolid-cpu` ran
the real gateway beside a real engine (vLLM's CPU build serving the product's Qwen3-0.6B), admitted calls with
an issued key through control, and found three defects in the gateway that are now fixed.

## Decision

**The agent runs the gateway on the serving node, beside the model.** The gateway is a container image in the
offline bundle, pinned by digest like the engine. Registering a node's gateway (GW-117) starts it there,
revoking the gateway stops it, and the agent keeps one state directory per gateway instance, because a spool
belongs to one gateway identity (`GATEWAY_STATE_OWNER_CONFLICT`). The installer delivers the image; it does
not run the gateway, since the control host serves no model.

**The credential is generated on the node.** The agent generates the gateway's secret, writes it into the
gateway's private files and returns it once over its authenticated session; control stores only its digest,
as it does today. No person sees or copies it, and no task payload or table holds it. GW-117's dialog stops
showing the credential once the agent delivers gateways.

**The listener takes the address the node declares; the backend stays on loopback.** The gateway terminates
TLS and checks every key through control, so it can face callers directly. The engine cannot, so the backend
rule stays. The node declares its gateway address in the agent's configuration, and that address, not one
typed in the console, becomes the alias's base URL.

**Control issues the gateway's serving certificate from an installation CA.** The agent sends a CSR for the
declared address with a key that never leaves the node; control signs it with a gateway CA that the
installer generates, kept apart from the agent CA (ADR 0050) and the console's serving certificate, and added
to the CA bundle clients trust. A site that brings its own PKI places the certificate and key on the node
instead (GW-122).

**The listener speaks HTTP/1.1 and says so.** ALPN offers `http/1.1` only, so HTTP/2 clients fall back
instead of being dropped.

**One address for every model is a separate tier.** ADR 0027 gives the model gateway its own hostname on the
installation's TLS port. That entrance, routing each call by its alias to the node that serves it, is GW-121;
until it exists each alias's base URL is its node's gateway.

The agent's part lives in paths lane A owns. Lane B builds it after ARC-103 is on `main`, keeps the change to
a new task type and the gateway's own lifecycle beside the inference runtime, and leaves the session and
certificate code as ARC-103 leaves it.

## Consequences

- A caller needs a route to the serving node's declared address. Sites that cannot open one wait for GW-121.
- Each serving node holds one more process and one more certificate; their expiry and renewal follow the
  agent's (ADR 0050) and appear with it in the console (ARC-114).
- Gateways on nodes run one per node and workspace, as control already allows; replicas behind one name are
  GW-108.
- The engine's CPU build is a test vehicle only. The product recipe stays the CUDA build until a CPU engine is
  a roadmap item of its own.

## Amendment, 2026-11-09

Building it settled what the decision left open:

- **A node that cannot run gateway tasks keeps the administrator's credential.** Agent delivery needs
  PostgreSQL, the model gateway CA in control's configuration, and a node enrolled with the gateway
  tasks. Otherwise registering a gateway works as before and shows its credential once; the console
  shows that path only for such nodes. Sites that place their own gateway get a better path in GW-122.
- **Only the `docker_inference` profile runs gateways for now**, at protocol 1.6. The AI and model
  lifecycle profiles keep their task lists and versions until GW-123, so their nodes need no new
  enrollment. As with every protocol change before FLT-110, an inference node re-enrolls to gain the
  tasks; until then it keeps the administrator's path.
- **The credential and key are activated by one call bound to the start task's lease**
  (`.../tasks/{task}/gateway-activation`). A start that lost its lease, a revoked gateway and another
  node's credential activate nothing, and a retry replaces both secrets.
- **Revoking stops the gateway; stopping the deployment does not.** The gateway's lifecycle is its
  instance's: with no model behind it, it answers that the model is unavailable. A revoke on a node
  that is busy cannot queue the stop; the gateway refuses every call because control refuses its
  credential, the next start on the node removes it, and revoking again asks again. A start or stop
  whose task ends without a result reads as failed with `GATEWAY_TASK_EXPIRED`.
- **Readiness is checked against the issued certificate.** The agent completes a verified TLS
  handshake with the listener for the declared origin and accepts only the certificate this start
  was given.
- **The image is pinned by its ID in the agent's configuration** and loaded with `docker load`; the
  signed bundle that carries it is DEL-101. Certificates last a year and are renewed by publishing
  again until GW-124; callers get the CA bundle from the installation root until GW-125 serves it.
