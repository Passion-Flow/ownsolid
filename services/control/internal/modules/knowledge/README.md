# Knowledge ingestion, retrieval and answers

This module owns scoped knowledge bases, TXT sources, document generations/chunks, search and answer orchestration. The [Python encoder](../../../../../runtimes/knowledge/README.md) performs bounded offline computation; it has no business database or document visibility authority.

Use [service.go](service.go), document/base/generation paths for lifecycle; [processor.go](processor.go), [worker.go](worker.go) and recovery/retention paths for ingestion; [search.go](search.go) and [search_repository.go](search_repository.go) for authorized retrieval. Answer/gateway paths borrow distribution, while workflow-authority paths expose the same current scope to workflows. Encoder configuration binds approved model identity, dimension and resource limits.

Migration 000012 stores bases, generations, documents, source versions, attempts, chunks and mutation intents. Search checks current authority before encoding and rechecks the active generation afterward. PostgreSQL uses its vector query path; MySQL streams the full authorized active candidate set with bounded top-K memory and cosine scoring. The latter is not ANN and must not gain a hidden scan cap that silently changes results.

Keep the typed Knowledge configuration, protected scratch paths, [knowledge contracts](../../../../../contracts/knowledge), [frontend](../../../../../apps/web/packages/knowledge), [M12](../../../../../docs/milestones/m12-knowledge) and [SQL/encoder/browser harnesses](../../../../../tools/quality/knowledge) synchronized. A future split should retain generation/publication authority in this owner and isolate mechanical encoding transport, not move authorization into Python.

Focused native check: `go -C services/control test ./internal/modules/knowledge`. Actual selected-SQL generation races, encoder failure/cleanup and browser answer paths require their own admitted fixtures and source-bound receipts; a model file or generated vector is not accepted ingestion.

## Cumulative M12 source integration

The explicit `knowledge` profile retains M01–M11 (SSO source validation and joined
maintenance, preparation/holdout and all four stores) and adds only the existing
knowledge owner. `SSORuntime`/`LifecycleRuntime` select inherited responsibilities;
exact `SSOOnly`/`LifecycleOnly` predicates remain distinct. Forward account18 and
`upgrade-knowledge` preserve the17-entry prefix/existing principal; the expected
catalog is74tables/18entries and passes the current native SQL gates.
Protected `knowledge-policy.env.example` maps every installed reference/policy
through the existing typed `--env-file`, with strict booleans and a protected
gateway inventory. The cumulative restore calls knowledge last in the same
transaction. See the [current integration contract](../../../../../docs/execution/mvp/m12-cumulative-integration-20261026.md)
and [single current handoff](../../../../../docs/execution/mvp/m12-closeout-current.md)
for complete initial source/local evidence and separately pending physical targets.

The PostgreSQL worker locks mutable user authority with `FOR NO KEY UPDATE`,
then Workspace/base/generation/document/attempt. Independent foreign-key key-share
readers remain compatible. `knowledgeError` preserves the identity owner's typed
rate refusal and measured Retry-After; it must not collapse429 into503. The actual
cumulative concurrency/rate/process/SQL source suite is run through
`tools/quality/knowledge/cumulative.py --group backend` with explicit pinned
Python/model/native-fixture references. Controlled output doubles are separately
labelled and do not replace actual persisted encoder results.


Post-package encoder integrity is a runtime prerequisite, not just a byte checksum.
The Python boundary requires exactly one link per configured model file; preserve
that invariant when manufacturing packages. Use the independent-copy
[encoder package owner](../../../../../tools/release/knowledge/README.md), configure
only its exact `files/` root, and run the actual packaged/relocated SQL/Go/Python
gate after final packaging. A failed prerequisite must remain a useful fixture
failure, never a nil-attempt panic or a weakened integrity check.
