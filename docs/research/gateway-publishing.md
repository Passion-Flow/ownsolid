# Publishing a deployment to the gateway

Research behind GW-117: an administrator publishes a running deployment to the gateway from its page
(screen S-205 of the [build plan](../product/plan/README.md)), so a key can be issued for it without
the API. Keys themselves are in [gateway keys](gateway-keys.md).

## What exists

The `distribution` module (`api/openapi/v1/distribution`) already holds everything the server needs:

- **Gateway instances.** `POST .../workspaces/{workspace}/gateways` registers the gateway process that
  serves one node of a workspace: `node_id`, a display `name`, the `client_origin` callers reach it
  at, and a reason. A workspace has at most one active instance per node (`GATEWAY_INSTANCE_EXISTS`),
  and the node must be active, run an inference profile and not be fenced
  (`GATEWAY_NODE_UNAVAILABLE`). The answer carries the instance's credential
  (`gw1.<id>.<token>`) once; a replay of the same idempotency key returns the instance with
  `secret_available: false`. The origin must be a canonical HTTPS origin with no path, query or
  fragment; it is advertised metadata, never a destination control calls. Creation needs a machine
  HTTPS origin in control's configuration (`CONFIG_MACHINE_TRANSPORT_REQUIRED`) and a license that
  admits new business. Revoking an instance (`.../gateways/{id}/revoke`) is a recovery action and
  needs no license.
- **Aliases.** `POST .../aliases` names a deployment for callers: `gateway_id`, `name`
  (`^[a-z][a-z0-9.-]{0,63}$`, unique in the workspace, disabled ones included), `deployment_id`,
  `deployment_revision` and a reason. The deployment must be on the gateway's node and ready now:
  desired and observed running, at the given revision, verified, not stale, its model ready
  (`MODEL_ALIAS_UNAVAILABLE`). A workspace holds at most `distribution.alias_max_active` active
  aliases (16 by default, `RESOURCE_LIMIT_REACHED`). Disabling (`.../aliases/{id}/disable`) takes the
  alias's revision (`VERSION_CONFLICT`), is a recovery action, and stops every key that calls it.
  Every change is audited (`gateway.instance_created`, `model.alias_published`, ...).
- **The gateway process.** The Rust gateway (`services/gateway`) is configured with `gateway_id`,
  `node_id`, `backend_address` and `gateway_credential_file`, and serves `/v1/models` and
  `/v1/chat/completions` under its origin. Nothing installs or starts it yet (GW-114).

So the console only lacks a page. GW-113's live run had to provision the instance and publish the
alias through the API.

## How others do it

- **A stable public name mapped to a deployment.** LiteLLM's proxy maps a `model_name`, "the
  user-facing name to use for your deployment", to `litellm_params` that say where it runs
  ([config](https://docs.litellm.ai/docs/proxy/configs)); clients send only the name. KServe
  separates the service address (`status.url`) from the model name the request body selects, and
  an OpenAI client uses `<url>/openai/v1` as its `base_url`
  ([first GenAI InferenceService](https://kserve.github.io/website/docs/getting-started/genai-first-isvc)).
  ownsolid's alias is the same idea: the gateway origin plus `/v1` is the base URL, the alias is the
  `model`.
- **Publishing from the console.** Databricks creates a serving endpoint from the UI with a name and
  the served model and version ([create endpoints](https://docs.databricks.com/aws/en/machine-learning/model-serving/create-manage-serving-endpoints)),
  and the endpoint's page offers *Query endpoint*
  ([query](https://docs.databricks.com/aws/en/machine-learning/model-serving/score-custom-model-endpoints)).
  The design prototype's deployment view does the same: a *Use it* card with the base URL, the
  model name, a sample call and "No key yet? Create one scoped to this model".

## Licences

No new dependency. The page uses the console's existing React, TanStack Query and `@ownsolid/ui`.

## Decision

The deployment page's Overview gets an **On the gateway** card:

- **Not published:** what publishing does, and *Publish to the gateway*. It is offered only while the
  deployment is running and verified; otherwise the card says what has to happen first, because the
  server would refuse with `MODEL_ALIAS_UNAVAILABLE`.
- **Publish dialog:** the name callers use, prefilled from the model's name in the alias alphabet
  and checked as typed; the gateway that serves the deployment's node when one is active, or else
  the address and name of a new one; and a reason. Submit provisions the instance first when needed,
  then publishes. Each request keeps its idempotency key until answered, as the key dialogs do.
- **A new instance's credential is shown once**, with the gateway ID and node ID the gateway's
  configuration needs, beside the outcome of publishing. If publishing fails after the instance was
  made, the credential stays on screen and only publishing is retried. If the answer that carried
  the credential was lost, the dialog says so and offers to revoke that instance and make another,
  which is safe because nothing can be published to it yet.
- **Published:** for each active alias, its name, whether it can be called now, the base URL, a
  sample `curl` call, *Issue a key* (the issue dialog with this model chosen) and *Stop publishing*,
  a typed confirmation that names the keys that stop working.

Rejected:

- **Publishing as a step of the deploy flow** (the prototype's last step). An alias needs a ready,
  verified deployment, which the flow reaches minutes later; the deploy task does not own
  distribution. It can be offered there later as a convenience on top of this card.
- **A separate "Models on the gateway" page.** The deployment is what a person publishes, and the
  key dialog already lists the published models. Listing gateway instances, revoking one and
  replacing a lost credential belong on the Gateway page and need a credential rotation the server
  lacks: GW-118.
- **Guessing the gateway origin.** The control plane's own origins are not the gateway's; a guessed
  origin would be advertised to every caller. The person types it, and the dialog checks it is an
  HTTPS origin.

## Failure modes and tests

- Origin with a path, `http:`, credentials or a trailing slash: refused in the form (a trailing
  slash and a default port are normalized away), unit tests on the origin check.
- Alias name outside the alphabet, or taken: refused as typed, and `MODEL_ALIAS_EXISTS` from the
  server keeps the form for a new name.
- Provision succeeds, publish fails: the credential stays visible, Try again publishes only
  (browser spec, and the spec fails if the retry provisions again).
- Lost provisioning answer (`secret_available: false`): the dialog offers revoke-and-recreate
  (browser spec).
- Another administrator provisions the node's instance meanwhile: `GATEWAY_INSTANCE_EXISTS`, the
  dialog re-reads the instances and offers the existing one.
- Deployment not ready: the publish button is replaced by what to do first.
- Disable with a stale revision: `VERSION_CONFLICT`, explained, the card re-reads.
- Permission denied and license refusals: the shared problem explanations; each operation the card
  calls declares its error codes (UX-131).
- The live run publishes a real scripted deployment from the browser against a real control plane
  and issues a key for it.

## Done when

From a running, verified deployment's page an administrator publishes it under a name, gets a new
gateway's credential once when the node had none, sees the base URL and a sample call, issues a key
for that model from the same card, and stops publishing with a confirmation that names the keys
that stop; each failure above shows its explanation and can be retried; English and Chinese are
complete.

## Alias rollback boundary

The S-205 deployment detail specifies rolling back a deployment version.
GW-111 previews and applies a change to a published alias using the
alias's current revision and the previous target's retained revision.
The deployment and alias have separate identities and histories. See
the S-205 section of the [build plan](../product/plan/README.md)
and the [alias cutover contract](gateway-cutover-preview.md#public-contract-and-client-acceptance).

The current plan defines the deployment action and the gateway card's
publish, issue and stop-publishing actions. A Console alias rollback
requires its own screen definition for selecting a previous target,
reviewing the server preview and applying the scoped change. GW-111's
Console work remains open at that boundary.

## Administrator description flow

GW-120 exposes an optional description on publication and an
independently versioned edit in the [distribution contract](../../api/openapi/v1/distribution/openapi.json).
The S-205 publish dialog currently collects the alias name, gateway
and reason. Browser coverage of Admin entry and clearing can follow
once an Admin screen specifies those actions and an installation
has the numbered migration. The existing HTTPS contract fixture
checks editing and clearing against the GW-120 candidate.
