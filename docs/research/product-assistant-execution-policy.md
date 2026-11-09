# Product assistant installation execution policy (AGT-112)

## References

GitHub documents [enterprise controls for blocking agentic features](https://docs.github.com/en/copilot/how-tos/administer-copilot/manage-for-enterprise/manage-agents/block-agentic-features) and a
[disabled default for unconfigured features](https://docs.github.com/en/copilot/concepts/enterprise/default-availability).
An installation-level denial takes precedence over a user's access to a feature.
[OWASP LLM06](https://genai.owasp.org/llmrisk/llm062025-excessive-agency/) calls for
complete mediation at the action boundary. That applies to navigation and reads as
well as confirmed actions when they share an assistant execution policy.

No new dependencies, models or datasets are required. The new boundary uses the
[Go standard library](https://go.dev/LICENSE), whose BSD-style terms allow
commercial use, offline use and redistribution with notices. The existing test
fixture uses [PostgreSQL](https://www.postgresql.org/about/licence/) and
[pgvector](https://github.com/pgvector/pgvector/blob/v0.8.7/LICENSE), both under
the permissive PostgreSQL License, and [Python](https://docs.python.org/3.12/license.html)
under the PSF license for the build overlay. Those licenses allow commercial use,
offline use and redistribution with notices. The test uses synthetic records and
requires no models or customer data.

## Decision

Resolve the installation policy on each otherwise-admitted command in
`RunForRequest`, before calling a read adapter or returning a navigation result.
`Run` delegates to the same method. The decision takes only the server context.
The existing authenticated dispatcher supplies the current identity, and the
operation still rechecks session, workspace and permission in its own transaction.
Only a successful operation may commit a success audit event. Command discovery
continues to describe the static grammar for the authenticated entrance.

A trusted local source must distinguish an explicitly enabled installation,
an explicitly disabled installation and an unavailable decision. Explicit denial
uses the existing `ACCESS_DENIED` response (403). Missing or unreadable policy
uses `SERVICE_NOT_READY` (503), with no private reader error in the response.
The service holds no policy cache. A later request rechecks the source. This
boundary admits new commands and does not cancel actions already admitted.

The control plane's [typed configuration](../../services/control/internal/platform/config/config.go)
rejects unknown fields and currently has no assistant policy. Reuse the typed
configuration and startup validation model for the production source. The
configuration owner must preserve the distinction between an absent setting
and explicit false. The identity HTTP owner binds that source at `productagent.New`
when registering the staged route. The source must be local and honor request
cancellation. A nil source denies execution until integration exists. Reusing
`workflows.enabled` would bind independent capabilities together. An environment
variable, request flag or assistant-owned configuration parser would create a
second policy authority. Enforcement only in the browser or gateway would miss
model-independent commands.

User budgets, loop bounds, user-scoped policy, privileged management and an
operator-facing control remain separate acceptance requirements. Future model
and confirmed-action entry points must pass through the same installation
policy before any assistant tool action.

## Acceptance

- In a network-isolated PostgreSQL fixture using the real HTTPS dispatcher,
  confirm Admin navigation and Portal reads succeed with an explicit enable,
  using current session and workspace, and emit correlated success audit events.
- Use both entrances to verify explicit disable, absent source and source read
  failure. Expect the status and code above, no result data or private error text,
  and no event for the rejected request in PostgreSQL or the Admin audit API.
- Verify both service entry methods deny navigation and reads before any tool or
  storage call. Retry the same eligible command after policy recovery to show
  that a previous decision is never reused. Retain the existing authentication,
  scope, CSRF, risk-tier and audit failure tests.

The boundary passed the focused race tests and
`TestAuthenticatedAssistantHTTP` through the real TLS dispatcher with temporary
registration. `TestOfflineCommandsWithCurrentSessionAndWorkspace` also passed.
Both integration runs used PostgreSQL 18.6 with pgvector 0.8.7 in a Docker
container with no external network. This verifies the policy boundary with a
fixture source. Production configuration and browser acceptance remain open.
