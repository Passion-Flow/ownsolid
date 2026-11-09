# Product assistant display credential redaction

AGT-108 keeps assistant tool results as data and limits what can enter the
assistant context. The two reviewed reads return typed metadata. Their
application names are user-authored text. A user can paste an issued key into
that text even though the projection has no credential field.

[OWASP sensitive information disclosure](https://genai.owasp.org/llmrisk/llm022025-sensitive-information-disclosure/)
recommends limiting data and redacting known sensitive patterns before model
use. [OWASP improper output handling](https://genai.owasp.org/llmrisk/llm052025-improper-output-handling/)
requires downstream context-appropriate encoding and validation instead of
executing returned text. Keep the fixed command dispatcher and the typed
result boundary. A name containing a URL or command stays a display string.

Mask the known key shapes in application names and conversation workflow names
when returning those pages from the assistant. The model-key and gateway
contracts declare [the `pk1` bearer format](../../api/openapi/v1/model-gateway/openapi.json)
and [the `gw1` bearer format](../../api/openapi/v1/distribution/openapi.json).
Both use a canonical UUID and a 43-character Base64URL token. The gateway
credential prefix is also available from the agent wire package. Use a compiled
Go regular expression for the bounded shape and redact the entire credential.
A key identifier without its token remains ordinary metadata. Preserve scope,
audit correlation, ordinary Portal reads, names without these shapes and page
positions. Never edit the stored name or use the name as a command argument.

Generic scans for arbitrary secrets have no reliable boundary and would alter
normal labels and IDs. Calling a model or a validation service would disclose
the very text being protected. A denylist in the system prompt cannot replace
redaction at the tool response. The redactor covers the two known formats.
Free-form secret policy and model output handling remain separate acceptance.

The only added implementation uses the [BSD-style Go standard library](https://go.dev/LICENSE)
and existing ownsolid wire definitions. There are no external dependencies,
models, data sets, services or runtime network requests.

Test both shapes with synthetic tokens, multiple matches and plain labels.
Persist a synthetic key-shaped name on a real workflow and read the application
and conversation pages via the assistant, then check that the ordinary Portal
projection still returns the stored name. Verify that the audited read, result
shape and cursor stay valid with PostgreSQL disconnected from external networks.

The focused unit tests and `TestOfflineCommandsWithCurrentSessionAndWorkspace`
passed with the active PostgreSQL 18.6 schema. The synthetic key-shaped label
was masked in both assistant reads and remained intact in the ordinary
application projection.
