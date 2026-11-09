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
- **What it is good for: facts only.** The gateway serves `POST /v1/chat/completions` and `GET /v1/models`, and
  every model this release can import is a text model, so "chat and text generation" is true of all of them. No
  field holds a person's own words about a model.

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

**An administrator's description waits.** Open WebUI and LiteLLM both let an administrator say what a model is for,
and ownsolid should. It needs a column on `model_aliases`, a field on publishing and a way to change it (GW-120).
Until that contract exists the page shows the model's known facts: text and chat, its base, and
its context length.

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
