# Public API SDK generation (DEV-103)

## Contract and approach

The Go and Python SDKs target the versioned, externally callable Admin, Portal
and model gateway operations in `api/openapi/v1`. [OpenAPI 3.1][spec] describes paths,
parameter locations, media types and response status codes for code generation.
Keep a package per domain so duplicate schema names across domains stay
independent. Project each contract from its declared `x-audiences` and retain
all referenced components. The [existing reference generator][reference] also
excludes the inherited knowledge contract. The SDK uses the same boundary and
omits agent and internal gateway operations.

Generate Go with [oapi-codegen 2.8.2][go-generator] and Python with
[openapi-python-client 0.29.1][python-generator], from files with local
references only. Generation is a development or packaging step. Both clients
receive the configured installation origin and HTTP transport from the caller.
Use one cookie jar per Admin or Portal origin, the documented CSRF handshake
for writes, and the existing gateway authorization header for inference. Use
local trust roots and disable environment proxies and redirects in the examples.

OpenAPI Generator 7.27.0 was also exercised on the datasets contract in a
container with no network. It adds a Java toolchain and lists [3.1 support as
beta][alternative]. The Go and Python tools use the repository's existing
toolchains. A handwritten endpoint map would create a second contract to update
when paths or schemas change.

## Compatibility and acceptance

The unmodified 28 contracts generated 24 Go packages and 22 Python packages
with warnings treated as failures. Some inherited machine-only schemas use
OpenAPI 3.1 boolean subschemas that the generators do not parse. The public
projection removes only unreachable schema components. A public model files
tuple has `items: false` with a fixed `prefixItems` length. In the generator
input, replace that boolean with the equivalent `{"not": {}}` and keep the
original prefix schemas and bounds. Go represents the array values as generic
JSON and the Python client generates a union of the item types. The server
remains the validation authority for the catalog contract.

Set a separate Go HTTP response suffix to avoid colliding with names such as
the identity `LoginResponse` schema. Use Python's documented
[content-type overrides][content-types] for NDJSON uploads and font downloads.
The generated dataset request retained `application/x-ndjson` on the wire.
These adjustments left the source contracts unchanged. With them, the prototype
generated all 28 contract packages, compiled the Go packages with `GOPROXY=off`
and imported 1,604 Python modules. The deliverable excludes the inherited
knowledge package in accordance with the product boundary.

Verification must check that generation completes without warnings, all
projected operations remain represented, output is reproducible, Go compiles
and Python imports offline. Exercise typed reads and a binary request through
each client, and check that authentication uses the exact origin, cookie and
CSRF header. The SDKs can cover the current contracts while DEV-101 remains
open for complete public API coverage across the product.

## Licenses

The generator binaries and wheels are build tools and stay out of the SDK
output. Their pinned upstream packages permit commercial and offline use.
Preserve upstream license notices if redistributing the tools themselves.

| Build tooling | License |
| --- | --- |
| oapi-codegen 2.8.2, go-openapi/jsonpointer 0.23.1, go-openapi/swag/jsonname 0.26.0, santhosh-tekuri/jsonschema/v6 6.0.2, speakeasy-api/jsonpath 0.6.3, vmware-labs/yaml-jsonpath 0.3.2 | Apache-2.0 |
| dprotaso/go-yit 9ba8df137936, getkin/kin-openapi 0.142.0, oasdiff/yaml 0.1.1, speakeasy-api/openapi 1.24.0 | MIT |
| go.yaml.in/yaml/v3 3.0.4, gopkg.in/yaml.v3 3.0.1, oasdiff/yaml3 0.0.14 | Apache-2.0 and MIT for libyaml-derived files |
| golang.org/x/mod 0.38.0, golang.org/x/sync 0.22.0, golang.org/x/text 0.40.0, golang.org/x/tools 0.48.0 | BSD-3-Clause |
| openapi-python-client 0.29.1, annotated-doc 0.0.5, annotated-types 0.8.0, markdown-it-py 4.2.0, mdurl 0.1.2, pydantic 2.14.3, pydantic-core 2.50.4, rich 15.0.0, ruamel.yaml 0.19.1, ruff 0.17.4, typer 0.27.3, typing-inspection 0.4.5 | MIT |
| Jinja2 3.1.6, MarkupSafe 3.0.4 | BSD-3-Clause |
| Pygments 2.21.0 | BSD-2-Clause |
| shellingham 1.5.4 | ISC |

The runtime dependencies were checked against the license files in the pinned
Go modules and Python distributions. The generated Go package uses
oapi-codegen/runtime 1.8.0 (Apache-2.0), go-jsonmerge/v2 2.0.0 (MIT), and
google/uuid 1.6.0 (BSD-3-Clause). The Python packages use attrs 25.4.0 and
httpx 0.28.1. Their complete runtime lock also includes anyio and h11 (MIT),
httpcore and idna (BSD-3-Clause), typing-extensions (PSF-2.0), and certifi
(MPL-2.0). HTTPX is BSD-3-Clause and attrs is MIT. Each license permits
commercial offline use and redistribution with its required notices. Retain
certifi's MPL notices and corresponding source when redistributing the CA
bundle. No model weights or datasets ship in either SDK.

[spec]: https://spec.openapis.org/oas/v3.1.1.html
[reference]: ../../tools/codegen/web-help.mjs
[go-generator]: https://github.com/oapi-codegen/oapi-codegen/releases/tag/v2.8.2
[python-generator]: https://github.com/openapi-generators/openapi-python-client/releases
[content-types]: https://github.com/openapi-generators/openapi-python-client#content_type_overrides
[alternative]: https://github.com/OpenAPITools/openapi-generator

## Integration evidence

The generator produced 27 public domain packages for each language. Two
generations compared byte for byte, the Go module compiled with `GOPROXY=off`,
and the wire smoke test checked both clients' CSRF headers, session cookies,
node list responses and NDJSON requests over loopback. The Go and Python SDKs
also logged into the real Admin TLS dispatcher, listed an enrolled synthetic
node and logged out with PostgreSQL 18 and pgvector 0.8.7. Both ran in a Docker
network namespace without an external route. Python installed its locked
runtime wheels with `--no-index`. PostgreSQL held the logout audit events.

## Streaming and negotiated responses

The gateway's successful chat response can be JSON or `text/event-stream`.
The pinned Python generator picks the JSON decoder for status 200. With
`stream=true`, the generated call buffers the stream and raises a JSON
decoding error. The dataset, training, and localization contracts also have
successful responses with multiple media types.

Keep the generated typed API for structured responses. Provide a raw response
context manager for both HTTPX clients. [HTTPX streaming][httpx-stream] returns
the response before reading its body and closes it when the context exits.
Reuse the pinned generator's [request builder][python-request-builder] to retain
path escaping, parameters, body encoding, and the caller's configured
authentication. A hand-maintained route map would create a second contract and
changing every endpoint template would replace working typed behavior. The
adapter adds no dependency and leaves event framing and content negotiation
visible to the caller. The Go generator already exposes raw responses through
methods without the `WithResponse` suffix.

Verify the Python adapter with an actual generated chat request. The first
event must be available before the rest of the response is read, early context
exit must close the response, and HTTP failures must remain available as raw
status, media type, and body. The existing loopback checks cover structured
requests, CSRF, session cookies, and NDJSON uploads.

The two pinned generators emitted clients for all 412 projected method and
path pairs across 27 domains. Repeated generation produced identical source
trees. The Go packages compiled with the module proxy disabled, and the
Python smoke test ran with cached wheels and an in-process SSE transport.
Both Python clients read the first event before the next chunk was consumed
and closed the response when the context exited. The asynchronous client
also retained the body of a 403 response.

[httpx-stream]: https://www.python-httpx.org/quickstart/#streaming-responses
[python-request-builder]: https://raw.githubusercontent.com/openapi-generators/openapi-python-client/v0.29.1/openapi_python_client/templates/endpoint_module.py.jinja

## Python UUID request headers

The pinned [openapi-python-client UUID template](https://raw.githubusercontent.com/openapi-generators/openapi-python-client/v0.29.1/openapi_python_client/templates/property_templates/uuid_property.py.jinja)
serializes UUIDs in JSON but has no header transform. The generated
endpoint passes a UUID object to HTTPX, whose
[header encoding](https://raw.githubusercontent.com/encode/httpx/0.28.1/httpx/_models.py)
requires strings or bytes. The GW-120 client reproduced the failure with
its UUID idempotency key. Existing generated writes have the same shape.

Preserve the public UUID contract and Python type. In generated endpoint
request builders, serialize direct header assignments of UUID parameters
as strings. Match the request-builder syntax and typed parameter so other
headers, path arguments, bodies and optional-value guards keep their
semantics. Removing the format from the Python projection would change
the generated parameter type. Copying the upstream property template
would also take ownership of the unrelated JSON and multipart paths.
No dependency, model or dataset changes. openapi-python-client remains
MIT, HTTPX BSD-3-Clause and Python under the PSF License. These allow
commercial and offline use and redistribution under their notice terms.

Test a required UUID header and an optional UUID header, including the
omitted case. Preserve string headers and body values. Verify both an
existing generated Admin mutation and the new description edit with a
real HTTPX transport object and no network access.

The pinned local SDK generation and HTTPX wire probe passed for the existing
alias cutover and the new description edit. Both requests used a Python
UUID and transmitted its canonical text. The generated Go clients compiled,
and the Python probe decoded the predecessor problem and the typed replay
result. The generator regression checks preserve omitted UUIDs and string
headers.

## Unambiguous JSON contract inputs

The contract reader uses Python's default last-member-wins decoding. A small
local fixture with duplicate paths members silently lost its first public
operation before projection. This does not claim that the currently published
contracts contain duplicates. [RFC 8259 section 4](https://www.rfc-editor.org/rfc/rfc8259#section-4)
recommends unique object names for interoperable interpretation. The
[Python JSON decoder](https://docs.python.org/3.12/library/json.html#repeated-names-within-an-object)
documents its default replacement behavior and the object_pairs_hook override.
[Kubernetes strict field validation](https://kubernetes.io/docs/reference/using-api/api-concepts/#field-validation)
also exposes duplicate-field refusal as an explicit policy.

Reject duplicate members in SDK source contracts and the admitted Components
alias owner, before generating from that domain. Treat this as ownsolid's
unambiguous-source policy, not a claim that every JSON parser must reject them.
Use the existing standard-library pair hook; regex scanning would mishandle
nesting and escaped names, while a new parser dependency adds no value. Include
the owning file and member name in the error, with no member values. Require a
JSON object at the document root and retain useful syntax-error context. Keep
all existing alias, audience and local-reference boundaries.

Cover repeated path/method/schema names, escaped-equivalent names, the shared
alias owner, malformed and non-object roots, and valid repeated names in
separate objects or array values. A failed input must publish no partial SDK
and invoke no external generator for its domain. Compare current valid contract
and projected document values before and after the parser correction. Python's
existing PSF licence permits commercial, offline and redistribution use under
its notice terms; no dependency, model, data set or third-party implementation
is added. A changed generator source hash requires its own regeneration and
release qualification; old 783/122-file evidence cannot be relabeled for it.

The same reader also rejects Python's accepted NaN and Infinity extensions:
the standard JSON grammar excludes them, and JavaScript's JSON decoder does
not accept them as portable contract input. Use the decoder's parse_constant
hook and cover all three non-finite spellings. This remains input parsing;
it is not a new general OpenAPI or JSON Schema validator.

The targeted parser, projection and audit tests passed all forty-nine cases,
including eleven new input cases. All twenty-eight current SDK source
contracts retain the same normalized document, alias set and projected
document. Their public inventory still has 430 operations and no declaration
findings. Invalid-input audit errors retain exit 2; the separate existing
explicit 409 compatibility review retains exit 1.

The independent Linux amd64 regeneration attempt used source 37cbb09c,
including the original-author 2e joint DAG, a new output destination and the
prepared oapi-codegen 2.8.2 and openapi-python-client 0.29.1 executables.
Neither tool required installation or compilation. The file-only entry reused
the existing strict resource preflight: at least 6 GiB raw free memory,
12 GiB available memory, 30 GiB disk and I/O full avg10 no greater than 5.
The prepared entry bounds execution to one CPU, at most 512 MiB Node
old-space, a 60-second command limit and a 300-second overall limit.
It opens no heavy-test carrier.

Admission returned 75 before generation: raw Free was 3,432 MiB, Available
19,826 MiB and I/O full avg10 0.20. The generator command did not start,
no SDK output or generation log was created, no owned child remained,
and the source stayed clean. This consumed attempt was not retried.
The retained 122-file archive covers only identity, not all twenty-eight
domains. Its byte comparison against a new output remains unrun; equal
contract projections do not establish equal generated bytes. No SDK
regeneration, native client run or complete repository qualification is
claimed for the changed generator. That consumed attempt remains immutable.
A distinct generator-only workload needs its own allocated segment and
reviewed process graph. The earlier 2e full-gate admission refusal, its
6 GiB Free threshold and the 783 native scope remain unchanged.

## File-only generation process graph

The pinned [oapi-codegen formatter](https://github.com/oapi-codegen/oapi-codegen/blob/v2.8.2/pkg/codegen/codegen.go#L933-L949)
calls imports.Process with default options. Its linked x/tools v0.48.0
[public entry](https://github.com/golang/tools/blob/v0.48.0/imports/forward.go#L44-L70)
creates a ProcessEnv without an explicit environment map. The
[initialization path](https://github.com/golang/tools/blob/v0.48.0/internal/imports/fix.go#L1002-L1033)
therefore calls the Go executable for env -json; supplying OS environment
variables does not fill that internal map. Module resolution can also call
[list -m -e -json ...](https://github.com/golang/tools/blob/v0.48.0/internal/imports/mod.go#L239).
The SDK supplies an explicit package name, so the separate CLI package-name
detection branch does not need its go list -f call. These findings come from
the pinned binary build metadata and matching cached source; they are not
an executed descendant trace.

The Python config has an empty post_hooks list. The
[pinned Python generator](https://github.com/openapi-generators/openapi-python-client/blob/v0.29.1/openapi_python_client/__init__.py#L132-L148)
iterates that list, so this configuration schedules no Ruff or shell hook.
Keep the actual generator interpreter, package files and locked dependency
versions in the input inventory, not only the short executable launcher.

A file-only workload can use the separate Linux admission of 2 GiB Free,
8 GiB Available, 30 GiB disk and I/O full avg10 no greater than 5,
with both existing permanent carriers acquired once nonblocking. It must
retain stricter inherited limits, use one CPU and serial domains, and have
at most 300 seconds overall and 60 seconds per external command. Keep
GOMAXPROCS at 1 and GOMEMLIMIT at most 512 MiB. The latter is a
[Go runtime soft limit](https://pkg.go.dev/runtime#hdr-Environment_Variables),
not a hard process RSS ceiling. Record existing OS limits and actual
per-command peak RSS separately. RAYON_NUM_THREADS is 1 if a reviewed
Rust-backed hook is used; no such hook is scheduled here. Mac admission
would require at least 2 GiB Free and 15 GiB disk as well as its permanent
carrier, but no prepared Mac oapi-codegen was found in the inspected paths.

The current two-generator-only execution scope excludes the transitive Go
metadata commands. No new generation is authorized by this process-graph
review. Do not hide the extra executable, synthesize its answers, disable
formatting, install tools or compile a replacement to fit the list. The
next execution plan must explicitly account for that fixed Go executable
and its metadata arguments before it takes a carrier. Until then, generated
file counts, peak memory and byte comparisons for this candidate remain
unmeasured. No dependency, model or data set is added by this review.
