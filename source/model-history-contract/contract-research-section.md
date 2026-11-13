
## Model family and bounded version history contract (DEV-101 / MOD-103)

Reviewed 2026-11-13. This work follows the [remaining model version history
boundary](#remaining-model-version-history-boundary) and retained publication
graph above. [MLflow registry concepts](https://mlflow.org/docs/latest/ml/model-registry/)
separate a registered model from its versions and mutable aliases. Its
[version search API](https://mlflow.org/docs/latest/api_reference/rest-api.html#search-modelversions)
bounds results and continues with a page token. [Kubeflow Hub's registry](https://www.kubeflow.org/docs/components/hub/overview/)
also separates model, version and artifact metadata. These are design references;
neither product becomes an ownsolid dependency.

Keep ModelVersion.id and deployment model_id as the existing exact immutable
version ID. The ready-only ModelVersion and AdminModelDetail schemas keep
state=ready. The isolated v1 prototype defines separate historical metadata
shapes with ready/retired state and an explicit model-owner family UUID. Metadata
visibility, including ready state, never grants execution permission. Deployment
and training retain their current exact-version and artifact admission.

The proposed Admin history path selects a family UUID and authorized workspace.
One page returns exact version ID, workspace, display name, model_key, kind,
catalog digest and creation time. Historical detail reuses the existing manifest
and dataset-lineage shapes and retained graph validation. It adds no provenance
store and does not scan physical model bytes while listing metadata.

Use created_at DESC, id DESC as the proposed stable keyset order, 1..100 rows
with the server's configured default, a required nullable next_cursor and optional
ready/retired filtering. Bind continuation to family, workspace, filter, current
account/audience and scope. Each page rechecks current authority; it is not a
whole-history snapshot. No total count or implicit page aggregation is promised.
The model owner must provide the cursor codec, policy and complete response bound.

Reject grouping by display name, model_key, catalog digest or derivation parent.
Distinct immutable IDs remain distinct even when they share a digest. Derived
lineage does not imply family membership. The current public model has no ordinal
version number; this candidate does not invent one or reuse a resource revision.
No latest/name/alias selection replaces an exact deployment reference.

### Domain inputs before publication

The model owner must establish a durable family ID scoped to the installed tenant
and workspace, an explicit legacy mapping/backfill, and atomic membership rules
for first registration, reimport after retirement and derived publication.
Prefer immutable membership once established, with corrections owned and audited
by that domain. DEV cannot infer these identities from current metadata.
An optional family reference on current Admin detail can preserve old-server
omission once the owner supplies it. Existing Portal and list shapes stay valid.

The owner must also provide current-authority retained ready/retired metadata
reads, complete cursor/response policy and actual error outcomes. DEV can then
finalize public OpenAPI, generated Go/Python and Help; the model owner retains
storage/authenticated routes and Web retains S-202. The isolated prototype is
unregistered and changes no product API, SDK, model store or execution admission.

### Verification and licenses

Test two explicit families sharing a name/model_key, distinct IDs sharing a
digest, ready/retired rows, immutable parent/artifact bindings, empty and final
pages, tied-time keysets, cursor filter/scope refusal, live role/session revocation
and cross-workspace refusal. A permitted retired-metadata read must coexist with
refusal of a new deployment/training request for that exact retired ID. It must
never substitute a newer version. Invalid retained graphs release no partial
metadata or lineage. Generated wire tests must preserve exact UUIDs, null cursor,
filters and omitted policy defaults. Real PostgreSQL, original dispatcher and
S-202 qualification remain separate from prototype wire checks.

No dependency, model, dataset or third-party code is added. Existing pinned SDK
tool licenses and commercial, redistribution and offline notice requirements
remain those in [sdk.md](sdk.md#licenses). The primary references are read for
design only. No public operation or roadmap item is completed by this note.
