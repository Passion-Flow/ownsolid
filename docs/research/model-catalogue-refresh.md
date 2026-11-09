# Installed offline model catalogue refresh

## MOD-113 current inputs

[ADR 0036](../decisions/0036-show-every-option-and-fetch-models-offline.md)
calls for a discovery index covering Hugging Face and ModelScope, shipped
locally and refreshed from signed media. Discovery metadata is distinct
from model weights and from the immutable catalogue used to admit an
import or a node task.

TRN-108 commit `8b7155ab` reads the approved single-model file installed at
`api/agent/v1/model-catalog.json`. The
[training worker](../../runtimes/training/projectone_training/boundary.py)
tries `contracts/agent/v1/model-catalog.json` only if the current file is
absent. It refuses a nonregular or group- or world-writable current
file and checks its size before reading. It decodes with duplicate-key
rejection and compares the canonical digest
with the request's `model_catalog_sha256`. That digest authenticates
the selection against the trusted request. It provides no signed
discovery index, refresh version or expiry.

The [Models catalogue route](../../services/control/internal/modules/models/http/handler.go)
returns the one catalogue embedded by
[`wire.ApprovedModel`](../../api/agent/v1/models.go). The model import
resolver and Agent use this exact digest as admission authority. Models
startup config supplies the artifact root and worker limits, and
[composition](../../services/control/internal/modules/identity/http/agent_path.go)
injects no prepared release, public trust policy or verified catalogue
binding. The artifact root contains model imports and published payloads.
It supplies no trusted index source.

## Existing release verification

The offline release verifier already has
[`bundle.InspectCurrent` and `bundle.UnpackCurrent`](../../tools/release/offline/pkg/bundle/current.go).
It checks the signed manifest against an independently configured public
trust root, and binds each payload by asset ID, kind, size and SHA-256.
The current profile accepts a generic `model` asset, but its
[required inventory](../../tools/release/offline/internal/factory/current_manifest.go)
declares no discovery index role or required index version. The
[installer binding](../../deploy/offline/internal/install/bundle.go)
retains release identity, manifest SHA-256 and four selected image
identities. It supplies no model index to Control. The existing
[image registry consumer](../../services/control/internal/modules/imageregistry/catalog.go)
reverifies a configured prepared release for image assets and is an
example of a typed release binding. Its configuration conveys image
authority.

The [TUF client workflow](https://theupdateframework.github.io/specification/latest/#detailed-client-workflow)
uses trusted roles, bounded input, monotonic metadata versions and
expiry checks before persisting new metadata. Use these properties as
a design reference when extending existing release verification.
A root's expiry controls the authority of the signer. A separate
authenticated index expiry controls the freshness of an index issued
by a valid signer. The two checks need separate fixtures.

No dependency, dataset or model is added. This review reuses the
existing Go [BSD-3-Clause standard library](https://go.dev/LICENSE)
and the installed training reader. The pinned Qwen artifact and its
Apache-2.0 terms remain recorded in the
[offline model registry research](model-registry.md#two-pinned-source-manifests).
No model source or registry was contacted.

## Activation handoff

- Release and OPS supply a real release or dedicated refresh medium
  with a declared model index asset role, exact bytes and notices.
  Bind the role, schema, generation, expiry and payload digest under
  the existing release signature and installed trust. Supply a valid
  initial index and its successor for the first installation and
  refresh tests. A general `model` asset or an image registry receipt
  cannot establish this role.
- ARC supplies a startup-validated binding to the verified protected
  payload, its release manifest and the accepted generation. Keep
  the binding installation-owned. A request body or a writable file
  inside the artifact root must not select the trust policy or index.
- Models verifies the exact payload through the existing release
  boundary and exposes a bounded read-only discovery projection.
  Once the format is defined, validate the registry identifier,
  repository and immutable revision, metadata bounds and canonical
  file hashes. Commit an accepted generation and digest together.
  A failed, interrupted or same-generation changed refresh leaves
  the prior verified generation intact. Reusing the same generation
  and digest is a read or exact replay. An expired retained index may
  be preserved for diagnosis, with a stale state if displayed.
  Activation and new fetch selection require a valid generation.
- DEV and Web publish and consume an additive collection separate
  from the existing import admission catalogue. Listing an entry
  does not admit its weights or grant a workload. MOD-101, MOD-115
  and MOD-116 retain their independent artifact contracts.

Once those inputs are installed, verify that a valid signed successor
is the only replacement. Wrong signer, modified or truncated bytes,
expired signer, expired index, rollback, equal generation with different
content, concurrent refresh and a restart during activation must
leave the old verified generation readable where valid. Exercise the
same operations through the installed read-only route with no external
network. Existing release verifier tests prove the media boundary
only. No installed Models refresh or retention path exists to test yet.

## Disconnected prerequisite checks

At `d48d4a57`, a Linux arm64 container with no network ran the five
existing training boundary tests and these release factory suites:

- `TestArchiveTrustAndEntryAttacks` refused modified assets, bad
  signatures, unknown signers and truncated archives.
- `TestPreparedInventoryCorruptionIsNeverAdopted` refused corrupted
  prepared assets and inconsistent verification records.
- `TestTrustRotationRequiresBothThresholdsAndSuccessorVersion`
  checked signature thresholds and root succession.
- `TestTrustRotationKeepsThePinnedFileUntilAnUnexpiredChainVerifies`
  refused an expired successor and preserved the prior trust file.

The training reader selected the installed path and used the inherited
path only when the current file was absent. It refused an invalid
current path even with a valid inherited file. The trust retention
assertion above is for the release root. Index retention and index
expiry acceptance require the actual installed index asset and binding.
