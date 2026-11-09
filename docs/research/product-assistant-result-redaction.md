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

## Directional controls in display text

Workflow names can contain Unicode directional formatting characters. Such a
name can change the order of its own text or influence adjoining text when a
consumer renders the result without isolation. It can also split a key's
logical representation while keeping it recognizable on screen.
[Unicode UAX #9](https://unicode.org/reports/tr9/#Directional_Formatting_Characters)
defines the `Bidi_Control` property for overrides, embeddings, isolates and
directional marks. [W3C bidi guidance](https://www.w3.org/International/questions/qa-bidi-unicode-controls)
describes the effects of explicit controls and the role of the presentation
layer in setting direction.

Remove that specific property from assistant display names before matching
credential shapes. Use the [standard Go table](https://pkg.go.dev/unicode)
and preserve ordinary letters, including right-to-left scripts, as well as
joiners used in names. A broad format-character filter would alter valid
orthography. HTML isolation alone would leave directional instructions and
split credentials in the JSON returned by the assistant. Stored names and
ordinary application and conversation projections keep the original text.
IDs, navigation and command admission do not use the sanitized labels.
The [application response contract](../../api/openapi/v1/applications/openapi.json)
requires nonempty names. If removing directional controls leaves only white
space or an empty label, return the existing redaction marker. Cover that
case with a published application and the ordinary projection of its name.

In unit tests cover overrides, isolates, marks, ordinary right-to-left text
and a synthetic credential split by a directional control. Persist a label
with an override and a split key in PostgreSQL. Verify both assistant
projections suppress the controls and mask the key while the ordinary Portal
projection returns the original name.

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

The same disconnected PostgreSQL fixture verified the directional-control case
in both assistant reads and matched the original name in both ordinary Portal
projections. The focused race tests also passed.

## Model-shaped output at the command boundary

The [model gateway request contract](../../api/openapi/v1/model-gateway/openapi.json)
rejects arbitrary tools. The product assistant's staged model-independent
adapter accepts a complete command and optional cursor. A tool result, model
completion envelope, or text with embedded instructions carries no command
authority. Preserve the existing strict object decoder and exact alias lookup.
Adding a model response parser before a real gateway route would create an
unused second admission path. Rendering and admitting structured proposals from
a real model remain part of the model integration.

[OWASP improper output handling](https://genai.owasp.org/llmrisk/llm052025-improper-output-handling/)
calls for validating model-controlled text before it reaches application actions
and for encoding it in its output context. Qualify the existing boundary through
the authenticated HTTPS dispatcher. Seed a published application name containing
markup, a valid command phrase and an external URL. The scoped read must return
the full name in its data field and no navigation destination. Resubmitting
the name as a command must fail. A model completion envelope and a command
object carrying model tool metadata must also fail, even when they contain a
valid command phrase. Check the real audit store and Admin audit response for
each refused request. These checks need the same offline PostgreSQL fixture,
Go standard library and synthetic data as the existing adapter tests.

The staged dispatcher passed these cases in
`TestAuthenticatedAssistantHTTP` using real TLS, a Portal session and isolated
PostgreSQL 18.6. The read remained ordinary typed data. Each refused request
returned no result and produced no event in PostgreSQL or the Admin audit list.
