# Dataset near-duplicate publication (DAT-104)

[DataTrove](https://github.com/huggingface/datatrove/blob/main/src/datatrove/pipeline/dedup/minhash.py) uses MinHash buckets, then clusters candidate pairs. [Datasketch's LSH documentation](https://github.com/ekzhu/datasketch/blob/master/docs/lsh.rst) describes missed neighbors and spurious bucket matches. Either can miss a leakage edge. [Xiao et al.](https://wwwconference.org/wp-content/uploads/2025/06/p131-xiaoA.pdf) describe exact set-similarity joins using globally ordered prefixes and verification. A Jaccard match at 90% requires at least 90% of each input's shingles in the intersection. Therefore their prefixes of length `size - ceil(0.9 * size) + 1` must share a shingle. An inverted prefix index followed by exact intersection finds every qualifying pair within the admitted work budget.

DataTrove ([Apache-2.0](https://github.com/huggingface/datatrove/blob/main/LICENSE)) and Datasketch ([MIT](https://github.com/ekzhu/datasketch/blob/master/LICENSE)) are design references. The implementation uses the [Go standard library](https://go.dev/LICENSE) (BSD-3-Clause) and the existing [CPython](https://docs.python.org/3/license.html) worker (PSF License). Both allow commercial and offline use and redistribution with notices. No third-party algorithm code, dependency, model, or dataset is added. Verification uses synthetic records and the existing [PostgreSQL](https://www.postgresql.org/about/licence/), [pgvector](https://github.com/pgvector/pgvector/blob/master/LICENSE) (PostgreSQL License), [pgx](https://github.com/jackc/pgx/blob/master/LICENSE) (MIT), and [google/uuid](https://github.com/google/uuid/blob/master/LICENSE) (BSD-3-Clause) fixtures.

## Initial policy

- Keep exact publication as the default, including byte-identical metadata. The optional `near_prompt_v1` rule applies to the current prompt/completion publication format. Collapse Unicode whitespace for comparison only. Compare sets of five consecutive Unicode code points, using integer Jaccard at 90%. Exact normalized prompts always share a partition. Fuzzy matching requires at least 24 distinct shingles per side.
- Every qualifying prompt pair joins its sources before split, including pairs with differing completions, exact duplicates, and transitive bridges. Use the existing seeded source-group ranking. If fewer than two populated partitions are possible, fail with the existing split error.
- Remove a row only if it directly matches an earlier retained row, both meet the shingle minimum, their normalized lengths differ by at most 10% of the longer prompt, their completions are byte-identical, and their comparison safeguards agree. Preserve the ordered non-letter, non-space characters and common English and Chinese negation markers. Choose the earliest eligible retained row. Keep a chain endpoint with no direct match to a retained row. This is lexical similarity with conservative guards, and a close lexical match can still differ in meaning.
- Keep source text, original positions, effective-source and clone behavior. Approximate removals record the original row digest and retained line in the existing duplicates asset with `match_rule`. Freeze the opt-in policy in the publication job and carry it into retries. A cloned version is a new draft, and its next publication selects a policy independently.
- Build one authoritative plan in Go outside the database transaction. It records only line references, is sealed by SHA-256 for the private worker, and is applied by the existing worker and its Go verification path. Manifest shape and row mapping stay compatible. Budget the index from configured memory, and cap posting visits and exact comparison work. An exhausted budget fails the attempt without committing a version. This opt-in route uses the existing bounded publication contract. The generic large-file intake remains streaming and unmodified.

## Public publication

[Google's compatibility guidance](https://google.aip.dev/180#adding-components)
preserves existing behavior when an optional request field is absent.
Its [request identification guidance](https://google.aip.dev/155) uses an
unchanged request identifier for replay. Add an optional `deduplication`
field to the existing Admin and Portal publication request. Omission
keeps exact deduplication and its original fingerprint. The explicit
value `near_prompt_v1` selects the existing lexical policy. Reject empty,
null, duplicate and unknown values through the strict request boundary
and the service validator. An explicit exact alias would add a second
spelling to the same intent. A separate operation would duplicate the
existing authorization, locking and retry path.

Pass the selected policy and any test ratio together to
`PublishWithOptions`. Three-way grouping still joins all known leakage
edges under either removal policy. Replaying the same key with a
different policy must conflict, and retry must carry both the frozen
policy and the test ratio. The existing worker and dependency licenses
continue to apply. DEV owns the public schema and generated types.
Deliver them with the handler on the same main-branch update.

Use the existing disconnected TLS and PostgreSQL fixture to publish
both policies from the same effective source, including an exact
duplicate, a spelling change, a distinct answer, a transitive bridge
and multiple rows sharing a source ID. Check that near removal records
its rule in the sealed duplicates asset, that protected rows remain
in one partition, and that all three file digests match the manifest.
Exercise an invalid strategy, an idempotency conflict and a retryable
worker failure. Keep the S-503 controls and generic-format preview
as separate integrations.

On Linux amd64, the real HTTPS dispatcher and CPython processor passed with
PostgreSQL 18.6 and pgvector 0.8.7 in a loopback-only network namespace.
The same eight effective rows produced two marked removals under the
near policy and one exact removal under the omitted policy. The two
distinct answers, spelling bridge and shared-source rows stayed in one
partition. All three partition downloads and the duplicates export
matched their sealed sizes and digests. Invalid strategies returned
HTTP 400, and changing the strategy under the same key returned 409.
After an injected worker exit, the HTTPS retry published all three
partitions with the original near policy and test ratio.

## Prototype and acceptance

An isolated CPython 3.12 prototype checked English and Chinese typos, a transitive bridge, changed completions, negation, numeric changes, short prompts and unrelated text. A 96-code-point Chinese example scored 87/97, below the cutoff, and remained distinct. The longer typo fixture qualified. Candidate edges matched exhaustive comparison on 524 rows and were invariant under input permutation. For a fixed source order the earliest eligible representative is deterministic. With the network disconnected and a 256 MiB container limit, 10,000 sparse synthetic rows used 780,000 index units, 74 posting visits, and 66,836 KiB peak RSS in 0.51 seconds. Forced small index and candidate budgets refused dense inputs. These are prototype measurements on synthetic data.

Run production tests against the exact comparison and an exhaustive oracle, and check dense-input refusal, source-order tie resolution, and split leakage. Exercise a real PostgreSQL publish, a failed attempt followed by retry, immutable row mapping and clone readback, and unchanged default exact publication. The Console/Portal selection and generic-format preview remain separate integration work.

For the focused publication check, configure the loopback PostgreSQL and CPython 3.12 fixture described in the [development environment](../development/environment.md), then run:

```sh
go -C services/control test -tags dataset_processor_integration ./internal/modules/datasets   -run '^(TestNearPrompt|TestActualNearPrompt|TestActualDatasetTLSContractsLargeUnicodeRowsCSRFExportAndReadiness)' -count=1
```
