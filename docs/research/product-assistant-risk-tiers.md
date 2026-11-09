# Product assistant risk tiers

AGT-104 classifies an operation from the reviewed server command and tool
catalog before dispatch. Keep risk separate from the caller's authority over
the resource. The operation owner still rechecks the current session,
workspace and resource access when it reads or writes.

- [GitHub Copilot CLI tool permissions](https://docs.github.com/en/copilot/how-tos/copilot-cli/use-copilot-cli/allowing-tools)
  distinguish read and write operations and give explicit denials priority.
- [OWASP LLM06 Excessive Agency](https://genai.owasp.org/llmrisk/llm062025-excessive-agency/)
  recommends limiting the exposed tools, authorizing downstream requests and
  requiring human approval for consequential actions.
- [Kubernetes authorization](https://kubernetes.io/docs/reference/access-authn-authz/authorization/)
  checks the request's authenticated identity, verb and resource scope.

## Admission and execution

The built-in command definitions identify navigation and read commands. An
exact localized alias selects one of those definitions. Navigation can only
return the fixed path for the authenticated entrance. Opening the audit or
keys page grants no access to that page's data. A navigation command cannot
share an ID with a catalog tool. A read must have a matching reviewed tool
with read risk, the workspace-member policy, the GET method and the Portal
audience. Keep the typed application and conversation projections and their
current workspace, owner and cursor checks.

Use the same policy in discovery and execution. Execute only after admission
and a current session and workspace check. Keep the audit append and the
result under their existing transaction boundary. Deny missing or unknown
risk, mismatched policy and unimplemented tiers. Use the existing public
denial code to avoid changing the command response contract.

Sensitive reads need an explicitly reviewed typed projection, a dedicated
resource permission and a defined field redaction and audit contract. The
existing read or navigation admission cannot authorize them. Reversible
writes require the separate signed plan and explicit human confirmation in
[AGT-105](product-assistant-write-confirmation.md), with the action and audit
record committed in the resource owner's transaction. The command dispatcher
has no execution or confirmation path for either tier.

Do not infer risk from a destination, the HTTP method, a caller field or model
text. GET alone does not establish that a read can disclose its result. Giving
each transport its own classifier would allow discovery and execution to
diverge. A generic HTTP proxy or model classifier could turn an unreviewed
operation into an allowed command. The HTTP adapter accepts only the command
text and optional cursor. Structured tool proposals are outside this command
input contract and cannot attach risk, permissions, scope or confirmation.

This uses the existing ownsolid command and OpenAPI inventory, the
[BSD-style Go standard library](https://go.dev/LICENSE) and the existing
[PostgreSQL License](https://www.postgresql.org/about/licence/) database. Both
licenses allow commercial use, redistribution with notices and offline use.
There is no new dependency, model or dataset.

## Acceptance

Run the real command dispatch with a scoped session. Exercise allowed
navigation and both Portal reads and verify they still append the same audit
records. Reject wrong-entrance commands, cross-workspace cursor reuse, raw
structured proposals and additional HTTP fields claiming a lower tier or
approval. Inject conflicting server definitions in a unit fixture to prove
missing and unknown tiers, sensitive reads and reversible writes fail before
data access, even when paired with an ordinary read entry or a fixed path.
Repeat audit refusal and retry on PostgreSQL with the network disconnected.
The item remains open until the authenticated route and UI flow are released.

The command and HTTPS fixtures passed on network-isolated PostgreSQL 18.6
with pgvector 0.8.7. The HTTPS run used the real dispatcher with a build-only
route registration. It rejected risk claims in JSON and the query, ignored a
risk header when checking the caller's entrance, and refused structured
proposals and claimed confirmation. Neither denial nor audit failure returned
data or left a successful assistant audit event.
