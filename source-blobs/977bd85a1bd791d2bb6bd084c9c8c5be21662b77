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

Scheduling needs a durable intent writer and a production schema with its
matching catalog fingerprints. Prefer writing the intent in the authenticated
training creation transaction, then reserving resources in the existing
dispatch transaction. The scheduling and database owners supply the shared
write contract and schema. The training lane keeps the existing dispatch
authority until that contract is available. See the
[training transaction handoff](training-job-lifecycle.md#scheduler-handoff).

## P2 dependency order

TRN-201, TRN-202 and TRN-204 need the admitted runtime and scheduler path for
multiple ranks. TRN-203 needs the scoped job and budget controls, and TRN-205
needs the train-to-deploy comparison. DAT-201 to DAT-205 need the registered
version and its authority before policies, reviews, gates, external-source
exports or data cards can bind to it. The current lane keeps those
dependencies visible rather than creating parallel resource authorities.


## TRN-109 interactive environment boundary

The training attempt/Agent contract has no interactive lifecycle or private
browser/home authority. Safest option: keep notebook/editor/terminal processes
and files separate from production training and prototype one offline CPU
JupyterLab path before admitting shared contracts. Do not launch an editor
from train.py, bypass current membership with a retained server cookie or
expose the Docker socket. The [primary-source review and concrete failure
coverage](training-interactive-environments.md) identify Agent, identity,
quota, storage/backup, serving and UI ownership. No image is installed or
process started by this research; TRN-109 stays open.


## Training Agent cost node-owner contract

The frozen 17e46bdd cost paths retain their measured overruns. Source review
finds three PreparationHistoryWithin calls per successful active authority
pass: delegated target, scoped credential and leased task. Training can
coalesce its own event progress writes while preserving per-event audits.
The proposed [borrowed node-fence callback and exact call sites](training-agent-cost.md#node-owner-shared-fence-prerequisite)
belong to ARC. The training owner will not replace node SQL or carry cached
authority across file I/O. Release requires the typed owner API, negative
lifetime/scope tests and the original 50/3,000 HTTPS profile under unchanged
budgets. This handoff claims no new measured statement count.
