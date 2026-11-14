# Admin CLI gateway request history (DEV-102, GW-107)

[LiteLLM's spend tracking](https://docs.litellm.ai/docs/proxy/cost_tracking)
provides paginated per-call metadata with an optional API key filter. The
[GitHub CLI](https://cli.github.com/manual/gh_api) makes reading all pages
an explicit opt-in and passes the API's continuation state to the next
request. [kubectl](https://kubernetes.io/docs/reference/kubectl/) provides a
human-readable table alongside structured output.

Reuse the registered Admin [Distribution
contract](../../api/openapi/v1/distribution/openapi.json) and the existing
CLI session, configured site and cell, and workspace selection. The
server owns membership, request visibility, optional key filtering,
receipt integrity, and cursor binding. Read one bounded page, expose its
continuation unchanged in JSON and YAML, and show it below the table for
interactive use. Preserve the full detail record for structured output,
including the difference between null usage and known zero. Validate the
returned resource ID and workspace before displaying any row.

Fetching all pages by default would make the resource use of an incident
lookup depend on the size of the workspace. CSV has a bounded service
projection but no registered production route. Keep CSV delivery with the
HTTP owner and use the JSON contract already available to operators.

Use the existing CLI dependencies. [Cobra](https://github.com/spf13/cobra/blob/v1.10.2/LICENSE.txt)
and mousetrap use Apache-2.0. The pinned pflag, Go, and golang.org/x/sys,
x/term and x/text use BSD-3-Clause. The pinned go.yaml.in/yaml/v3 uses
Apache-2.0 and MIT for libyaml-derived files. See the [offline history fixture licenses](gateway-usage-history.md#licenses-and-verification)
for its PostgreSQL test stack. These permit commercial use, redistribution
with their notices, and offline use. No dependency, model, or dataset is
added.

Exercise a compiled CLI against the real dispatcher on isolated TLS and
PostgreSQL. Verify a single-row page, replay at the same cursor, final and
empty pages, explicit key selection, and detail for an unreported receipt,
a reported receipt with null usage, and a receipt with a known zero token
count. Reject a cursor reused with a different key, a foreign workspace, an
invalid identifier, and a corrupt receipt without printing a partial result.
Check in unit tests that unexpected server scopes or malformed page cursors
are refused before output.

The offline fixture uses the repository's Python 3 runner under the
[PSF License](https://docs.python.org/3/license.html). Run
`tools/cli/testdata/run-gateway-history-tls.sh` with the pinned
toolchain and `PROJECT_ONE_TEST_DSN` set to a disposable PostgreSQL
fixture. The runner overlays only a test and calls the registered API.

The Linux amd64 run used PostgreSQL 18.6 and pgvector 0.8.7 in a network
namespace with loopback only. It reproduced the same bytes on each
unchanged one-row page, exhausted the cursor, and returned an empty final
page for an unused key. It distinguished unreported, cancelled with null
usage, and known zero completion tokens. The foreign workspace, changed
key cursor, and stored receipt corruption all failed without printing rows.
