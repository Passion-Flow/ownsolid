# Training and data integration handoff

## Training artifacts and execution

TRN-102 and TRN-103 need one agreed method and artifact discriminator across
training admission, Agent execution and the model registry. Prefer additive
typed artifact variants alongside the existing LoRA schema. An independent
worker mode could produce files that control cannot verify or accept, so the
lane keeps the existing runtime contract and advances checkpoint reliability.

TRN-104, TRN-108 and TRN-110 need the release owner to package the pinned Linux
runtime and coordinate the exact image and source digests with the Agent owner.
Use one admitted runtime image for the NVIDIA acceptance before introducing
distributed variants. The worker seal has been tested in isolation while these
integration gates remain open.

## Product paths and data authority

TRN-101, TRN-106 and TRN-107 need the CLI and web owners to use the scoped
training HTTP operations and accepted model outputs. Keep the existing
resource and permission checks as the source of authority.

DAT-101 to DAT-107 need the generic registration path in the
[dataset import handoff](dataset-import.md#registration-and-product-handoff).
Prefer an additive registry reference to the existing dataset plus a release
and backup inventory entry. Reusing the legacy draft would materialize rows
and impose its 64 MiB and three-field limits. The lane keeps the existing
generic candidate pipeline streaming and leaves the product items open.

## SCH-101 durable training admission

The scheduler now supplies the durable attempt writer and candidate SQL.
Train dispatch can reserve and release within the existing owner transaction
on that candidate. Production activation needs the matching catalog
fingerprints, runtime grants and a plan for existing physical leases. Keep
the training create operation as queued demand and take capacity in dispatch.
See the [training transaction handoff](training-job-lifecycle.md#scheduler-handoff).

## P2 dependency order

TRN-201, TRN-202 and TRN-204 need the admitted runtime and scheduler path for
multiple ranks. TRN-203 needs the scoped job and budget controls, and TRN-205
needs the train-to-deploy comparison. DAT-201 to DAT-205 need the registered
version and its authority before policies, reviews, gates, external-source
exports or data cards can bind to it. The current lane keeps those
dependencies visible rather than creating parallel resource authorities.
