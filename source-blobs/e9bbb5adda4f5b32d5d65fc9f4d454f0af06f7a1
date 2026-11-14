# Lane F decisions

## DAT-101 intake and product boundary

**Decision.** Build an additive bounded Go intake API for upload bodies and
authorized S3 objects. Use the existing S3 transport and run Arrow in the
Python data runtime. Commit private JSONL only after the source and its
optional receipt are verified. Return statistics to the product caller.
Keep the existing training draft, API, schema, and release image unchanged.

**Why.** The draft API and worker contract enforce a 64 MiB and 10,000-row
limit and materialize records in SQL. That path cannot meet the DAT-101 size
or generic-field requirements. The lane owns the datasets and data runtime
directories. The S3 client already owns endpoint and credential-file
configuration and constrains reads to an exact object.

**Alternatives.** Raising the draft limits would scale memory with row count
and change shared contracts. Reading a caller-supplied object URL would bypass
storage and workspace authority. Adding a Parquet module to the Go dependency
graph would change shared dependency and release inputs. Guessing legacy CSV
encoding can silently alter data that is valid in two encodings.

**Impact.** The original draft behavior remains compatible. A new product
caller must bind the intake result to authenticated workspace authority and
persist its report. A wrong format or GBK selection is recoverable by
resubmitting the source with the correct declaration.

**Boundary.** Keep DAT-101 unticked until the Console/Portal upload, durable
dataset registration and report readback, and the pinned Arrow offline image
are verified together. The existing object-ingestion product caller is outside
Lane F and still targets the legacy JSONL draft flow. Product completion
requires the relevant UI, API/composition, storage, and release owners to
integrate the new boundary. This lane's verification covers the intake API.

## DAT-102 training format boundary

**Decision.** Add explicit row-schema selection to the Lane F intake boundary.
Validate text, text-only messages, instruction records and standard explicit
preference pairs while keeping the complete accepted record.

**Why.** The four shapes have existing upstream training consumers. Validating
them during intake provides early, actionable errors without accumulating rows
or rendering model-specific templates.

**Alternatives.** Automatic shape inference is ambiguous for records with
multiple compatible fields. Mapping every shape to prompt/completion would
discard conversation structure or the rejected side of a preference pair.

**Impact.** A wrong schema choice is recoverable by reimport. Intake accepts
syntactically valid text messages independently of model-specific role order
and template constraints. Training selection and compatibility remain owned by
the published version and trainer.

**Boundary.** The inherited publication and training handoff encode only
prompt/completion rows. End-to-end completion requires the durable generic
import from DAT-101 and an additive format-aware publication, training and UI
handoff. Keep the item unticked until those paths run.

## Remaining P1 boundaries

| Item | Existing behavior and decision | Dependency needed to complete the item |
| --- | --- | --- |
| DAT-103 | The draft reports invalid legacy rows and supports row edits. Keep those semantics and use a separately versioned cleaning policy for generic sources. A filter over the old three-column draft would omit metadata and hide which rows changed. | The persisted generic source and report from DAT-101 and the S-503 dataset flow that can select a policy and show before/after samples. |
| DAT-104 | Publication already deduplicates exact prompt/completion pairs and joins their source groups. Keep that version contract. Applying a generic approximate rule before selecting the comparable fields could drop valid preference or conversation records. | The row schema from DAT-102 and the cleaning preview from DAT-103 so removals and thresholds are reviewable. |
| DAT-105 | The current worker produces deterministic train and validation files with source-group isolation. The manifest lists those exact files, which training validates. Appending a test split to that existing manifest would break older readers. | A versioned split contract and compatible training/evaluation readers that can bind and verify the third partition. |
| DAT-106 | Dataset versions and parent links already exist. The training input path uses immutable dataset references. End-to-end lineage must additionally survive model creation and be visible from the run and model. A second dataset-only ledger would duplicate existing version authority. | Training and model owners must integrate the lineage projection with the user-facing run/model views. |
| DAT-107 | Token counts depend on the model's installed tokenizer and template. Character counts or a generic tokenizer would produce misleading truncation previews. | An admitted offline tokenizer/template asset from the model runtime and a dataset preview entry point in S-503. |

The lane can resume at the earliest P1 item when its listed dependency is
available. Existing draft, publication and training readers remain usable
under their current contracts.

## DAT-101 offline decoder wheel

**Decision.** Admit the CPython 3.12 manylinux x86-64 PyArrow wheel with an
architecture-specific requirements lock. Stage it during connected release
preparation and install it in a disconnected build with the package hash
checked.

**Why.** The control image already copies the data runtime into Debian Bookworm.
A lock and tested install path give the image owner an exact offline dependency.

**Alternatives.** The Alpine arm64 fixture uses a different libc and architecture.
Building Arrow from source would introduce a separate toolchain and a larger
dependency closure.

**Impact.** An incompatible or altered wheel fails the image build. An upgrade
changes the version, the verified wheel hash and the runtime test together.

**Boundary.** The production image recipe and its release inputs are owned
outside Lane F. The lock and install test establish the decoder component's
delivery contract. The release owner must add the wheelhouse build input.
A generic durable import also needs the release lineage and backup contract
to move with the database migration. Those shared contracts are outside the
lane's allowed additions.

## DAT-101 authority and DAT-103 independent work

**Decision.** Keep registration in the dataset domain with the existing
workspace and store authorities. Add the generic import reference only
alongside the shared release and restore updates. In the meantime, enable
explicit cleaning and filtering with a bounded preview in the current
private intake path.

**Why.** The current draft and version references require legacy training
semantics. An artifact ingestion receipt requires a draft on submission.
A separate file registry would also bypass the database and snapshot
inventories that determine which dataset artifacts are recoverable.

**Alternatives.** Storing the report in an existing audit event would mix
audit with resource authority. Using a private sidecar as the authoritative
record would lose the atomic scope, report and audit commit. Raising legacy
draft ceilings would change existing client and worker contracts.

**Impact.** Import and cleaning produce a private candidate and verifiable
statistics. Product registration and authorized preview still require the
existing runtime and UI owners to adopt the candidate. The exact integration
points and verification commands are in the
[registration handoff](dataset-import.md#registration-and-product-handoff).
