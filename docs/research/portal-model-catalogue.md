# The portal's model catalogue

Research behind POR-104, screen S-304 of the [build plan](../product/plan/README.md): a person sees which
models their workspace may call, what each costs and what it is good for.

## What exists

- **Which models may be called.** The portal's `GET .../workspaces/{workspace}/distribution/aliases` lists what
  the gateway serves in the workspace, with `available`, `unavailable_code` and `client_origin`. A disabled alias
  stays in the list with `disabled_at` set and can never be called again.
- **What is behind a name.** An alias names a deployment. The portal's `GET .../workspaces/{workspace}/deployments`
  gives its `model_id` and `GET .../workspaces/{workspace}/models` gives the model: name, whether it arrived or was
  fine-tuned here, its base, context length, architecture and licence. All three reads are open to a member of the
  workspace on the portal entrance and scoped on the server.
- **What it costs.** TEN-107 has an internal rate history for occurrence-rated workspace showback.
  The [showback contract](metering-showback.md) still lacks an authorized read API. The portal model
  catalogue cannot use that history as a per-model price.
- **What it is good for: facts only in the browser.** The gateway serves `POST /v1/chat/completions`
  and `GET /v1/models`, and every model this release can import is a text model. GW-120 has scoped
  description handling in the PostgreSQL candidate. The generated Portal alias contract still
  exposes the predecessor fields. See the [GW-120 acceptance and rollout boundary](gateway-model-description.md).

## How others do it

- [OpenRouter's Models API](https://openrouter.ai/docs/models) gives each model a `description` of its
  "capabilities and characteristics", `context_length`, `architecture` with input and output modalities, `pricing`
  per token and `supported_parameters`. It is the fullest public set of what a person choosing a model looks at.
- [Open WebUI's models workspace](https://docs.openwebui.com/features/workspace/models/), the closest self-hosted
  product, has a description "shown in the model selector", tags, and capability switches, set in the model's
  editor, and restricts each model to "specific users or groups". A model switched off "leaves the selector, and a
  request that names it fails". Its page does not mention prices.
- [LiteLLM's AI Hub](https://docs.litellm.ai/docs/proxy/ai_hub) lets an administrator choose which models are
  public, and users "see what's available". Descriptions are whatever the administrator attaches as
  [`model_info`](https://docs.litellm.ai/docs/proxy/model_management), and cost per token appears in the
  administrator's list.

The common shape: name, what it is for in someone's words, context length, price, and only what the person can use.

## Licences

No new dependency. The page uses the portal's React, TanStack and Lucide packages already in the lock file.

## Decisions

**A name the person can call is the row.** The list is aliases, because that is what a key and a call name.
Disabled aliases are left out: they can never be called. An alias whose deployment is stopped stays, marked as
not available now, because it can come back and the person should know why their call fails.

**The browser joins three reads.** All three are portal reads that already exist, and the portal module adds no
backend capability. If the deployments or models read fails, the names and availability stay on screen and a
notice offers a retry; the person can still see what they may call.

**Cost is shown as not ready, with the reason**, as [ADR 0036](../decisions/0036-show-every-option-and-fetch-models-offline.md)
sets out. It sits in the model's drawer and not as a column, because a column of the same words tells nothing.
Inventing a price field on the alias was rejected: rate cards belong to TEN-107 and would replace it.

**Try is shown as not ready**, because S-304's primary action needs the playground (POR-105), which needs a running
gateway reachable through a portal-safe contract (POR-105). Issue a key is the available action
beside it, leading to the existing scoped issuance flow rather than opening a dialog on the model
page. Without a workspace the action is visible with the same access instructions as the keys page.
If nothing is published, the issuance form explains that an administrator must publish a model.

**An administrator's description waits for the published contract.** Open WebUI and LiteLLM both
let an administrator say what a model is for. GW-120 covers persistence, publication and editing.
The Portal can display the description once the production migration and the formal alias read
contract land. It currently shows known capabilities, base model and context length.

**POR-104 stays open** until prices and the description exist: its text names both.

## Failure modes and tests

| Failure | Behaviour | Covered by |
| --- | --- | --- |
| Alias read fails | Problem notice with Try again, nothing else claimed | `models.spec.ts` |
| Deployments or models read fails | Names stay, notice and Try again, model facts read "Not known" | `models.spec.ts`, `live-models.spec.ts` |
| A deployment or model is gone | That row reads "Not known" | `model-catalogue.test.ts` |
| A disabled alias | Not listed | `model-catalogue.test.ts`, `models.spec.ts` |
| Deployment stopped | Listed as not available, with what to do | `models.spec.ts` |
| No workspace, or nothing published | Says what to ask for | `models.spec.ts` |
| Another workspace's models | The server scopes the reads to the workspace in the path and the caller's membership | Existing portal HTTP tests of each read |
| Switch to a second permitted workspace | Models and keys from the first workspace disappear; returning restores its own key history | `live-keys.spec.ts` |
| More than ten pages of aliases | Says the list is not complete | `everyPage`'s `complete` |

## What the finished item must do

A person chooses a workspace and sees every model they may call there, with whether it can be called now; opens one
and reads the name to call, the base URL, the model behind it, its base, context length, licence, price and what it
is good for; and goes from there to issuing a key. Filters are in the address, and every state works in English and
Simplified Chinese, by keyboard and by screen reader, with the network disconnected.

## Partial read notice

A connection reset on the model facts request can happen while the aliases
and deployments answer normally. The installed browser run retained the
callable names, but the generic connectivity title described the whole
control plane as unreachable. The [partial-state rule](../product/design/README.md#4-states-every-screen-must-define)
requires the page to identify the missing part.

Use the existing `ProblemNotice` operation-specific title and detail.
Name the unavailable model details. On retryable failures, say which
fields are missing and leave the retry action and server backoff in the
shared notice. For a permanent refusal, keep its specific explanation.
The alias-list error still describes a failed catalogue read. Test that
the partial notice and callable name appear together, and that both the
notice and unknown values disappear when the real facts return. Check
the message in English and Simplified Chinese. This adds no dependency.

The English installation rerun interrupted only the model facts read
and recovered it through the visible retry. The catalogue browser suite
also checked the Simplified Chinese notice and retry at 390 px.

## Downloading trust from an endpoint

The [GW-125 contract](../../api/openapi/v1/distribution/openapi.json) has
scoped Admin and Portal downloads for an alias. The
[gateway trust research](gateway-delivery.md#downloading-caller-trust-gw-125)
defines the certificate-only format, the issuer and the server checks.
The model catalogue displays a gateway endpoint before a person issues a
key, and the Console deployment shows the same endpoint and a cURL sample.
Put the shared secondary download action beside those endpoints. It uses
the alias and the current workspace from the surrounding page. An issuer
refusal explains who can provide trust for that endpoint. Transient
failures use the existing retry notice, and a change of alias or workspace
discards the previous download state. A click fetches the file on demand
so ordinary catalogue reads stay independent of gateway trust.

Reuse the key drawer control and its official generated request types.
A link to the key page would make users issue or select a key to obtain
public trust material. Loading PEM with the catalogue would turn every
row into a separate attachment request. The shared on-demand control adds
no dependency, model or dataset. Exercise both endpoint placements in
Chromium using the activated alias from the key download fixture and
check that the saved file has the same digest and issuer and verifies the
advertised HTTPS name. Exercise a refused download without leaving the
drawer or losing the endpoint.

## Playground transport boundary

[LiteLLM's Playground](https://docs.litellm.ai/docs/proxy/ui/chat_ui)
offers model selection and model responses in the browser.
The [Fetch standard](https://fetch.spec.whatwg.org/#http-cors-protocol)
requires explicit cross-origin permission for an authenticated JSON
request to another origin. The current
[Control page policy](../../services/control/internal/modules/identity/http/handler.go)
allows connections to the Portal origin, and the
[Rust gateway router](../../services/gateway/src/routing.rs) has the
key-authenticated public model endpoints. Its browser preflight path
and a same-origin Portal model-call contract are both absent.

For POR-105, wait for a same-origin Portal contract that authorizes
the current workspace member and alias and records quota and usage
through gateway admission. That keeps the existing page policy and
lets the gateway retain control over model traffic. Direct cross-origin
calls would require separate browser policy, certificate deployment
and origin configuration. A browser-only proxy would have no
production authorization and metering boundary. This research
introduces no dependency, model or dataset.

Verify one run, two-model comparison and copying the exact request
with placeholders for credentials. Include workspace refusal, a failed
side of a comparison, cancellation with released capacity, retry after
an upstream failure, and a disconnected run through the real gateway.
The S-304 price and description still require their formal Portal
read contracts before the catalogue can display them.
