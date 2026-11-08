# Model-independent product assistant commands

AGT-102 covers the command path used when the model gateway is down or no model
has been installed. The deterministic path also gives the panel a reliable way to
open a known page or read a narrow projection when a model is available.

## Upstream and scope review

- The [VS Code command palette](https://code.visualstudio.com/docs/editing/getting-started/userinterface)
  selects a known application command from text. [kubectl conventions](https://kubernetes.io/docs/reference/kubectl/conventions/)
  give an exact command a predictable action and stable output for automation.
- [GitHub Copilot CLI approvals](https://docs.github.com/en/copilot/how-tos/copilot-cli/use-copilot-cli/allowing-tools)
  make tool admission explicit. ownsolid requires a separate human confirmation
  for each write and keeps permission checks in the resource owner.
- [Terraform plan](https://developer.hashicorp.com/terraform/cli/commands/plan)
  separates preview from applying a saved set of operations. The ownsolid plan
  needs a shorter lifetime and must bind the authenticated session, resource
  scope, typed arguments and revision. A model must never receive confirmation
  credentials. [OpenAPI operation IDs](https://spec.openapis.org/oas/v3.1.1.html#operation-object)
  provide stable catalog references, with a reviewed local risk and permission
  overlay. The existing P1 items cover these controls individually.

## Deterministic path

Select an exact, normalized English or Simplified Chinese command from a closed
catalog. Use localized aliases bundled into the binary, a fixed destination for
navigation, and the existing application projection service for the signed-in
portal user's workspace. Bound input length and reject controls and line breaks.
An unknown phrase returns a stable error. It must not fall through to a model,
try a shell, use a caller URL or dispatch a guessed write. The product assistant
accepts a server-authenticated identity and cannot choose another user or workspace.

The first slice exposes navigation and application/conversation metadata. Actual
writes need the reviewed tool catalog, authorization, a signed server plan, an
explicit user confirmation and a durable single-execution contract with the
owning service. A generic method-and-URL proxy would bypass those controls. A
model-based classifier would lose the offline fallback. Substring matching would
admit compound or negated text as an instruction. Keep these out of the dispatcher.

Following the [VS Code command palette](https://code.visualstudio.com/docs/editing/getting-started/userinterface),
provide a command directory so an authenticated caller can discover exact
localized phrases before executing them. Use the same bundled phrase files and
read admission data that the dispatcher uses. Derive an ordered list for the
authenticated entrance. Include a workspace read only when the server context
has a workspace. Each execution still rechecks current membership and
ownership because permission can change after discovery. Use the platform
locale tags `en` and `zh-CN` and reject unsupported tags with a stable code.
Do not return destination URLs, credentials or internal tool policy from
discovery. A separate hand-maintained help list would drift from accepted
commands.

No new module dependency, model, dataset or shipped service is needed. The Go
standard library is [BSD-licensed](https://go.dev/LICENSE), permits commercial use,
offline operation and redistribution with notices. Application reads use the
existing control-plane package, its already pinned [pgx driver](https://github.com/jackc/pgx/blob/v5.11.0/LICENSE)
(MIT) and already deployed [PostgreSQL](https://www.postgresql.org/about/licence/)
(PostgreSQL License). Those permissive licenses allow commercial and offline use
and redistribution with their notices. The comparable products are references only.

## Acceptance and failure cases

- Both languages resolve to the same typed result and a fixed same-origin page.
- Invalid UTF-8, control characters, excess length, unknown commands, URLs and
  multiple instructions never dispatch. An empty or stale workspace never
  returns data. The portal cannot navigate to or invoke a console action.
- Ordinary reads follow existing session and membership rechecks. An expired or
  revoked user sees an error without a partial projection. Conversation metadata
  stays caller-private. Pagination must use the existing bounded API policy.
- Discovery shows the accepted phrases for each locale and entrance, omits
  workspace reads without a workspace, rejects revoked sessions and keeps the
  compiled phrase table immutable if a caller changes a returned list.
- The core works with no model client and no external network. The final release
  must also pass the authenticated browser flow on both entrances with the model
  gateway disabled. A package test alone cannot close AGT-102.
