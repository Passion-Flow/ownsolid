# Delivering the model gateway

Research behind GW-114: an installation has to run the Rust gateway so that a key reaches a model. It records
what the code requires today, which changes the shape of the item. No prototype has been run yet, so the
direction below is a proposal, not a proven approach.

## What the code requires

- **The gateway is bound to one node and one backend.** Its configuration names one `gateway_id`, one `node_id`
  and one `backend_address` (`services/gateway/src/configuration.rs`). The control plane allows one active
  gateway instance per node in a workspace, and a node serves one deployment (`placement.go`), so one process
  per serving node matches the model.
- **Both of its addresses must be loopback.** `validate()` refuses a `listen_address` or `backend_address` that
  is not loopback with `GATEWAY_LOOPBACK_BINDING_REQUIRED`, and the configuration schema states the same.
- **The model is only reachable on that node's loopback.** The agent publishes the inference container on
  `127.0.0.1` at the recipe's `HostPort`, on a bridge whose host binding is `127.0.0.1`
  (`services/agent/internal/inference/network.go`).
- **Its identity cannot exist at install time.** `adminProvisionGateway` needs a node and a workspace and shows
  the credential once, so it can only happen after a node has joined.
- **Nothing exposes it to callers.** `ownsolid-install` knows two origins, portal and admin. An alias advertises
  a `client_origin` that the control plane treats as metadata only. ADR 0027 calls for a model gateway entrance
  with its own hostname, and no listener serves one.

## What this means for GW-114

ADR 0049 scoped the item to `deploy/` and `services/gateway`, with the agent paths untouched. That is not
enough. Starting the process from the installer on the control host only works where the control host is also
the serving node, and even there callers cannot reach a loopback listener. Three questions need answers:

1. **Where the process runs.** Proposed: beside the model on the serving node, as `deploy/components/agent`
   already delivers the agent, which is the only place the loopback backend exists. This touches paths lane A
   owns, so it has to be coordinated.
2. **How it gets its credential.** Proposed: the provisioning that GW-117 shows once is handed to the node
   rather than copied by hand. Rejected for now: a credential issued by the installer, since no node exists then.
3. **How callers reach it.** Either the gateway's listener becomes configurable beyond loopback, with its TLS
   certificate and an explicit address (the loopback rule is deliberate and needs its reasoning reviewed before
   it changes), or an entrance on the node forwards to the loopback listener.

## Before building

Prototype one serving node with a real engine: the gateway beside it, a credential delivered without a hand
copy, and a call from another host through the chosen listener, then record the result here and take the
decision in an ADR. `ownsolid-cpu` has no GPU, so the prototype has to use a model that runs on a CPU, or state
exactly what it did not exercise. Licences of any added image are recorded with that decision.
