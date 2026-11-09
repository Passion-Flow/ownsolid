# Datasets in Admin (S-503, DAT-105)

The [build plan](../product/plan/README.md#stage-5--run-it) names a list at
`/data` whose main action is Import. The full entry is still open. This handoff
specifies the list and its connected surfaces for the plan owner. Implement
them after those entries are adopted. The Training navigation already has a
disabled Datasets destination. Keep it there until the flow works end to end.
Portal needs its own POR-109 screen entry and audience-specific routes.

[Hugging Face Datasets](https://huggingface.co/docs/datasets/package_reference/main_classes#datasets.DatasetDict)
keeps named splits. [Google's dataset guidance](https://developers.google.com/machine-learning/crash-course/overfitting/dividing-datasets)
reserves test examples for final evaluation and identifies cross-split duplicates
as leakage. [MLflow dataset tracking](https://mlflow.org/docs/latest/ml/dataset/)
uses source and digest for lineage. Use the same distinctions in the interface
with the server's immutable version and manifest as the authority. A browser
splitter would disagree with the connected-group policy, worker budgets and
retained bytes. The [publication research](dataset-three-way-splits.md) covers
the processor and its alternatives. These sources are design references.
The screen needs no new library, model, runtime service or third-party dataset.
Use synthetic JSONL for verification.

## Contract and audience

The [Datasets OpenAPI contract](../../api/openapi/v1/datasets/openapi.json)
and the [HTTP handler](../../services/control/internal/modules/datasets/http/handler.go)
define the cell-scoped paths. Use the existing typed API client, Admin session,
local cell routing and workspace selector. Admin collection and configuration
reads require a single `workspace_id`. Admin resource paths resolve scope from
the dataset ID on the server. Admin requires a current platform administrator
session. Portal paths require a Portal session and a workspace that the session
may access. An owner can publish or retry a publication. A member can import
and edit drafts they created and can read workspace drafts. The service
rechecks identity and scope on every request. A reader without write access
can only list datasets with published versions and cannot read drafts.
Display `can_import`, `can_publish` and `storage_available` from
configuration. Availability indicates that the storage and processor are
configured. A read failure carries its own error. Give a recognized refusal
actionable copy. A missing or concealed resource uses the same unavailable
message for both cases and exposes no foreign name or metadata.

The Admin and Portal draft source operations accept a whole
`application/x-ndjson` body of the configured size with `X-Content-SHA256`
and an actual `Content-Length`. The current handler uses the three-field
training row `source_id`, `prompt`, `completion`. The internal CSV, JSON
array, Parquet and alternative training formats do not use this HTTP
operation. Keep their controls visible as unavailable with that reason.
The browser supplies the length through a known-size file body. Hash exactly
the selected bytes through Web Crypto on the HTTPS origin and apply the
server's configuration limit before reading the file. At the contract's
64 MiB ceiling the client needs memory for one file digest and no in-browser
row materialization. Reuse the offline upload patterns where they fit. The
chunked model import protocol cannot be used for datasets.

Create dataset, create draft, publish and retry bind a UUID idempotency key
to the request. The retry handler already requires `Idempotency-Key`, but
the two public OpenAPI retry operations omit it. DEV owns the declaration
and generated client correction before the Web wrapper ships. The read
job shape returns seed, validation ratio, revision, status and retryability.
It does not expose the frozen test ratio. On a resumed job, show the retained
request without restating an unavailable ratio. Show the exact retained
ratios on the published manifest. Job retry is the server operation that
preserves the frozen request.

## Screens and actions

**S-503 Datasets, `/data`** is an Admin list. It helps an operator find a
dataset or bring training rows into the active workspace. Import is the
single primary action. Use the shared list shell and workspace context.
The cursor page supplies name, source, rights declaration, updated time
and retirement status. It has no name, status or aggregate-count filter.
The workspace is the server filter. A name filter may narrow the loaded
rows only if the interface labels its scope and keeps Load more reachable.
Prefer omitting it until a server filter is supported. No unfiltered
collection counts can be inferred from one page. A row opens a preview
drawer. The dataset name opens the detail page. No mutations start in the
drawer. Retired datasets show their status in text.

**Dataset detail, `/data/<datasetId>`** is a detail page for a single
dataset. Its primary action is Import source, which starts a new draft in
this dataset. The breadcrumb returns to the active workspace's list.
Overview shows the declared origin, licence, rights, and timestamps.
Drafts and Versions use their own cursor lists. A draft name opens the
import/review flow. A version opens its manifest view with the immutable
ordinal, parent version, source and manifest digests, publication time and
partition counts. Activity uses audited dataset events where the audit
contract supports targeting the dataset and its child records. Until the
Activity coverage exists, label the missing history explicitly and keep
the detail definition of done open. Retirement is outside this slice.
When adopted, use typed confirmation of the resource name and retain
published lineage.

**Import source, `/data/import` and the draft URL** is a single-page flow
with three sections and one main action at a time. It serves both creating a
new dataset and adding a draft to an existing one.

1. Source. Show the selected workspace. For a new dataset collect name,
   source description, licence, a rights attestation and an audit reason.
   For an existing dataset show those facts. Accept one local JSONL file
   and display the configured byte and row limits and selected byte count
   before starting. Create the dataset, create the draft with the exact
   filename, byte count and digest, then submit the file. Use one immutable
   intent per create request. On reload use the persisted draft URL and
   server state. If upload is incomplete and the source has not been
   retained, ask for the same file again and verify length and digest
   before sending it. An HTTP 202 enters validation, with no completion
   toast and no need to keep a file in browser storage.
2. Review. Poll the draft and its current job using the configured
   background interval. Display the real import state, total and invalid
   rows after validation, and the server's bounded diagnostics with line
   numbers and localized explanations. Preserve redacted previews and
   indicate truncated reports. Use a cursor row list and open the edit
   form in a drawer. Submit a reason and both revisions required by the
   selected row operation. Refetch on revision conflict. Fatal source
   errors lead to reselecting a corrected source in a new draft.
   A draft in editing with zero invalid rows can proceed to publication.
3. Publish. Show Train + validation as the initial option and Train +
   validation + test as the explicit opt-in. Show the server's validation
   default. In three-way mode use an editable, initially suggested 10%
   test ratio. Accept decimal percentages with at most two decimal places
   and convert exactly to integer basis points. Each held-out ratio is
   1–50%, and in three-way mode their sum is at most 90%. Show the
   remaining training target and explain that ratios apply to connected
   groups, so actual row counts can differ. Grouping keeps related rows
   together. The public route uses its existing exact-removal policy.
   Near-duplicate removal is a separate option outside this public route.
   Generate a cryptographic 16-byte seed for each new publication intent
   and show it in the review as reproducibility information. Freeze
   draft revision, seed, ratios and audit reason at submit. Omit
   `test_basis_points` for two-way publication. Before Publish say that
   the worker creates another immutable version and stores the
   partition files. Show measured source size. Leave output size pending
   until the manifest is available.

When publishing, replace the form's action with job status and a stop
request where the current job permits cancellation. A stop is requested
until the server confirms cancellation. If a response is lost, read the
draft and current job and replay the identical intent with its original
key if reconciliation is needed. Keep an accepted job in view and resume
polling from its address after reload. Show Retry publication for a
current failed job that the server marks retryable and whose draft
revision is unchanged. Collect a reason in one short dialog and submit
the job's `/retry` endpoint with a new key, reused for replay of that
retry intent. The server creates the replacement job. A changed draft
requires a new publication. For `DATASET_SPLIT_INSUFFICIENT_GROUPS`, explain
that at least three independent groups are needed. Add or correct source
groups, or choose two-way publication in a new intent. A group-budget
failure directs the operator to reduce the input. A validation or
revision refusal stays with the form and offers a reload of current state.

**Version detail, `/data/<datasetId>/versions/<versionId>`** is a detail
page. For a schema-2 version its main action is Download test set. A
schema-1 version uses its available manifest as the main download and
shows the unavailable Test set action with the reason. Schema-2 detail
shows Train, Validation and Test rows and group counts, requested
ratios and measured bytes and digest from the immutable manifest. Use the
same-origin authenticated file endpoint with the exact dataset and
version IDs and the `test` asset. Save the server's attachment filename.
Keep the manifest digest and the test asset's separate SHA-256 visible.
Check response type, length and `X-Artifact-SHA256` against the retained
asset and handle a failed transfer inline. Reuse the same-origin
download transport and keep file bytes out of persistent browser
storage. No client-assembled test export and no signed external URL.
The test split is held out from fine-tuning. Starting a test-set
evaluation awaits S-504 and its selector contract.

## Seven states

| State | List and detail | Import, publication and download |
| --- | --- | --- |
| Loading | Match the table and detail sections with skeletons after the design delay. | Match the source form, review rows and job receipt. Show transfer progress only when measured. |
| Empty, first time | “No datasets in this workspace yet” with Import. “No versions published yet” with a link to the first eligible draft. | “Choose a JSONL file to validate your first draft”. An unstarted validation has no diagnostics yet. |
| Empty, filtered | “No datasets in the selected workspace” when changing the workspace filter yields nothing. Offer switching back to the authorized default. A loaded-row search, if adopted, names the filter and has Clear. | “No validation errors match this filter” with Clear filter. A clean unfiltered report says “All rows passed validation”. |
| Partial | Keep the dataset and last successful cursor page if versions, drafts or Activity fail. Name the unavailable panel. | Keep the validated rows and last confirmed job state. Announce capped errors and distinguish the unknown result of an interrupted write from a confirmed refusal. |
| Error | Explain which read failed and provide Retry. A missing or retired resource offers the workspace list. | Name source line, format or size, stale revision, insufficient groups, storage, or interrupted transfer as supported by the response. Keep inputs and the next action inline. |
| Denied | Keep the destination visible for the audience. Point to a platform administrator or workspace owner according to the missing permission. | An import-only member sees Publish disabled with the owner requirement. The server still checks every write and download. A foreign record has no data to render. |
| Not ready | Configuration can show that the dataset store or processor needs an operator. Preserve available read views. | Show unsupported source formats, missing test assets on schema 1 and unavailable test-evaluation handoff with text and `aria-disabled`. |

Use the shared list, detail, problem and dialog components and English and
Simplified Chinese catalogues. Status always has text. A toast may confirm
a finished action but never replaces the persistent job or version receipt.
Keep one dialog depth. An actual destructive discard, if offered, uses
typed confirmation. Closing the flow keeps the draft.

## Reproduction on the same HTTPS and PostgreSQL fixture

Use the [installation browser recipe](../development/environment.md#browser-test-against-an-installation)
with the browser on the workstation. Use one disposable private network,
one Control with both Admin and Portal TLS origins, one isolated PostgreSQL
18.6 database with pgvector 0.8.7, and the shipped Python 3.12 dataset
processor and private initialized dataset artifact store. Build both Web
entrances from the tested tree and serve them from Control. Use fixture-only
licensing, actor and workspace records. Supply credentials through private
files and pin the actual TLS public key with the existing `liveOptions`.
Do not bypass certificate verification. Keep external egress disconnected
and assert the browser calls only the selected fixture origins. The
[existing HTTPS and processor tests](dataset-three-way-splits.md#dataset-train-validation-and-test-splits-dat-105)
verify the server path. The following is the browser acceptance sequence to
implement in an opt-in `live-datasets.spec.ts` after the screen lands.

1. Sign in to Admin on the fixture, select the owning workspace and open
   Datasets. Import a small synthetic source with an invalid row and
   verify a real line-number diagnostic. Correct the draft and verify
   that another tab's stale revision is refused and the fresh rows load.
   Reopen the draft by its address and verify the same draft and job.
2. In the same workspace publish once without a test ratio and read
   schema 1. Publish a separate draft with at least three disjoint
   source groups and one near-prompt bridge, select three-way mode,
   set 25% validation and 25% test, and exercise a lost publication
   response without creating a second job. Assert three nonempty
   partitions and that no connected group spans partitions. Exercise
   insufficient groups and confirm no version is published.
3. For a separate publication use the existing processor test's fixture
   failure once to reach a retryable job with no retained version.
   Restore the real processor and click Retry publication. Verify the
   new job retains revision, seed and both ratios and creates one
   immutable version. The browser verifies the receipt before
   checking the same records independently in PostgreSQL.
4. Download the test attachment from the browser. Check its filename,
   byte count and digest against the manifest and response header.
   The two-way version exposes an unavailable Test set with an
   explanation. Assert the training transfer still offers only its
   existing train and validation files. In separate browser contexts
   exercise an owner, an import-only Portal member and a foreign
   workspace. Verify denied publish, concealed foreign reads and
   foreign test-download refusal using the same TLS server.
5. Run the standard Playwright route, keyboard, localization and
   accessibility coverage in both languages. Capture and inspect
   desktop and 390px screenshots for list, flow and version detail
   without source rows or credentials in the attachments. Run
   `make check && make build && make test` and record only the
   scenarios that actually ran.

The formal plan entries and the retry header declaration release the Web
implementation. The HTTP proof already ran with the real processor.
The S-503 browser flow and its screenshots remain acceptance work.
