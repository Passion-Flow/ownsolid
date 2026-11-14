# Model license admission

## Source and limits

[Hugging Face Hub](https://huggingface.co/docs/hub/repositories-licenses)
distinguishes standard metadata identifiers from custom terms.
[Amazon SageMaker JumpStart](https://docs.aws.amazon.com/sagemaker/latest/dg/jumpstart-foundation-models-choose.html)
requires applicable provider terms at deployment or fine-tuning. Keep the
publisher's terms and the installation's declared use separate. An import
checkbox, public download, paid ownsolid entitlement, or upstream SPDX label
alone cannot authorize a workload. Custom and restricted terms need an
explicitly reviewed version and, where applicable, a site-specific grant.

The first record covers the existing approved Qwen3-0.6B catalogue digest.
Its [pinned license file](https://huggingface.co/Qwen/Qwen3-0.6B/blob/c1899de289a04d12100db370d81485cdf75e47ca/LICENSE)
and [catalogue file inventory](../../api/agent/v1/model-catalog.json) identify
the exact legal text and payload. [Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0)
permits local offline use, including commercial use. Distribution requires
the license, applicable notices, and modification notices as described in
section 4. This deployment slice transfers the model within the declared
site and does not authorize third-party redistribution. There are no new
software dependencies, models or datasets. Go and existing PostgreSQL test
dependencies retain their [Go](https://go.dev/LICENSE) and
[PostgreSQL](https://www.postgresql.org/about/licence/) license conditions.

The v1 merged catalogue copies the base license file and identifies its
source, but does not declare separate adapter and training input rights.
A new deployment of that version requires its own reviewed record. Keep
the original metadata readable and retain the lifecycle operations needed
to observe, stop and remove an existing deployment.

## Decision and validation

Bind the local register to the exact artifact kind and catalogue digest
already verified by Models under server-resolved workspace scope. Use a
typed installation input for commercial or non-commercial on-site use.
Preserve the existing unconfigured site's on-site behavior by treating it
as commercial, the more restrictive assumption. A site explicitly marked
undeclared refuses new workloads. Invalid use must fail startup when the
shared configuration is wired and fail closed in admission in the meantime.

Apply the gate to placement, new deployment intents (including registry
bound intents), and an explicit action to deploy again. Preserve idempotent
reads of already-committed intents and the lifecycle paths for existing
deployments. Scope denial, invalid persisted catalogues, and runtime
preflight errors retain their own semantics. Policy refusals are distinct
inside the owner. They use the existing public access-denied response
until the shared problem registry and clients can carry the specific codes.

Verify a real fixed-revision model imported from an offline bundle, actual
Admin TLS and SQL writes, no writes after a refusal, exact replay after
policy changes, different-workspace refusal and an independent hardware
preflight failure. Hardware observations in the local acceptance are a
protocol fixture. Physical serving remains separate. A missing version
license must fail closed even when the base family is familiar. Avoid a
second registry, per-request purpose declarations, floating metadata, and
restrictive test models or data that cannot be redistributed.

## Offline deployment admission

`TestActualOnSiteDeploymentModelLicenseAdmission` used an isolated Linux
arm64 PostgreSQL 18.6 and pgvector 0.8.7 namespace with loopback only.
The model worker verified the nine original files from a 1,519,216,640-byte
USTAR archive and committed the fixed catalogue
`4d32aa5cd4b2d86f12beff791f0428f0813c2d3479041080d13b0be6bc6842bc`.
The [official license text](https://huggingface.co/Qwen/Qwen3-0.6B/blob/c1899de289a04d12100db370d81485cdf75e47ca/LICENSE)
matched its frozen SHA-256
`832dd9e00a68dd83b3c3fb9f5588dad7dcf337a0db50f7d9483f310cd292e92e`.

The Admin HTTPS route committed a real deployment intent and GPU reservation
under internal commercial use. The agent handshake, hardware observations and
execution results were protocol fixtures. Recomposition under undeclared use
returned HTTP 403 for new deployment, placement and relaunch. The owner returned
`MODEL_LICENSE_USE_UNDECLARED`. No additional deployment or task was written.
The original idempotency key replayed the existing deployment, which remained
readable and could stop and release its reservation. A foreign-workspace node
returned 404. An unavailable GPU on the authorized site retained
`INFERENCE_GPU_UNAVAILABLE`. The register tests separately refuse a missing
exact version, a derived catalogue without reviewed terms, an invalid purpose,
and commercial use when reviewed terms do not grant it.

Run the test with `-tags model_integration`,
`OWNSOLID_TEST_MODEL_LICENSE_ARCHIVE` pointing to the approved USTAR file,
and `PROJECT_ONE_TEST_DSN_FILE` pointing to a private disposable loopback
database. The model import and admission use the normal repository and Admin
routes. The fixture supplies the site purpose when composing the service.
The disconnected admission case also passed with the approved archive
review and complete-file transfer checks present.

## Consumer handoff

### Invocation contract for TRN and SCH

The execution owner resolves the exact model kind and catalogue digest from
its authorized tenant/workspace transaction, then passes them to
`models.CheckOnSiteUse`. Scope authorization, current model admission,
dataset rights and lease ownership remain with that owner. Compose an
explicit `OnSiteCommercial`, `OnSiteNonCommercial` or `OnSiteUndeclared`
from the root-owned installation policy. Use `OnSiteUndeclared` while that
declaration is unavailable. The producer retains its existing empty-value
compatibility for legacy callers.

TRN's `DispatchNext` and `ClaimTraining` verify dataset bytes outside their
delegated transaction and then re-enter it. Recheck licence eligibility in
that final transaction before creating a reservation, attempt or executable
claim. A previous successful check is metadata from the previous decision.
SCH and SCI apply the same rule at their existing final admission and launch
boundaries. Retained CPU output, an approved base's terms and a training
result each retain their own provenance. A derived or restricted catalogue
still needs its own reviewed terms and any required current customer grant.

Keep the producer's unknown, undeclared and denied reasons intact inside
the owner. Until DEV publishes the dedicated problems, keep the existing
HTTP projection. Consumer acceptance must show zero new attempt, GPU lease
or node task on refusal, a repeated eligibility check after physical IO,
cross-workspace refusal, and readable existing idempotent results with the
usual permission checks. Stop and release continue through their existing
authorization path. The installed consumer remains open until its owner
wires and runs that path.

- Shared configuration owns a startup-validated installation field for
  on-site model use. Apply `models.OnSiteUse.Validate` semantics before the
  composition in `identity/http/agent_path.go` sets
  `deployments.Service.ModelUse`. Supply the same declaration to all
  execution owners. The default currently uses the on-site commercial rule.
- Node delivery rechecks the same current purpose at the inference
  deploy branch of `nodes.Service.claimSelected` before handing out a
  previously queued launch. Use the retained command's scoped model and
  catalogue. Apply the existing task refusal and release rules. Exact
  idempotent HTTP replay stays a read, and stop, observation and removal
  remain available. The current deployment change gates the creation of
  intents and explicit relaunches.
- The public problem owner registers `MODEL_LICENSE_UNKNOWN`,
  `MODEL_LICENSE_USE_UNDECLARED` and `MODEL_LICENSE_USE_DENIED` as 403
  responses for placement, deployment creation and deploy actions. Update
  the deployment OpenAPI contract and localized callers together before
  removing the existing `ACCESS_DENIED` transport projection.
- Training calls `models.CheckOnSiteUse` with the authorized model
  kind and exact catalogue digest in `training.Service.Create` and
  rechecks before reserving or handing out work in `DispatchNext` and
  `ClaimTraining`. Keep the existing dataset rights and job scope checks.
- SCI consumes the version-bound check at the existing SCH-101 admission
  and launch boundary. Restricted scientific weights need a separately
  reviewed exact-revision record and a customer grant tied to the current
  tenant, artifact, permitted use, expiry and revocation. Coordinate the
  record with the scientific model owner before adding any restricted terms.

MOD-110 remains open for those installed consumers, the configuration and
public contract, and reviewed terms for versions beyond the first catalogue.
