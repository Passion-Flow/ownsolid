# Scientific computing and regulated research and development

Research behind the scientific-computing module and the related items in other modules
(MOD-110, DEV-108, DEV-205, AUD-109, AUD-205 to AUD-207, FLT-211, CAT-207). It started
from a customer's request for a research AI platform at a vaccine maker: protein and
complex structure prediction, protein design, mRNA sequence optimization, metabolic
modelling, literature knowledge, GPU scheduling, and GxP-grade security and records.

Each item still gets its own research before it is built (AGENTS.md section 0). This note
records what is known now and what must be settled then.

## What the request needs that ownsolid lacked

| Need | Existing coverage | Gap |
| --- | --- | --- |
| GPU queues, priorities, quota, admission, retry and cancel | Scheduling and quota, GPU operations | None for the scheduler itself |
| Models that run minutes to hours on input files, not request and response | Serving is built for chat, embedding and rerank endpoints | A task-type model contract, templates, batches and results |
| Large reference databases next to the compute | Model distribution | Versioned reference data sets |
| Reproducible runs | Training experiment records | Provenance for every task, re-run from the record |
| 3D structure review with confidence metrics | None | Offline molecular viewer |
| Licence status of each model | Catalog shows a licence field | A register that enforces permitted use |
| Encryption with the customer's keys | Encrypted backups | Encryption at rest with an external KMS |
| GxP evidence and electronic signatures | Hash-chained audit | Validation pack, Part 11 and Annex 11 signatures |
| Literature and internal-document question answering | DeepParser through the catalog | Permission sync from ownsolid projects to DeepParser |
| ELN and LIMS | Webhooks, REST API | Connector contract and first adapters |
| Public cloud with elastic GPUs and spot capacity | Fixed fleets | Cloud node pools for connected deployments |
| Running under another enterprise AI platform | OpenAI-compatible gateway | Delegated identity, quota mapping, job API and callbacks |

Knowledge stays with DeepParser (AGENTS.md section 1). ownsolid's part is identity,
permissions, models and operations for it, not document processing.

## Findings

### Task-type models are a general pattern

Structure prediction, protein design, OCR batches and offline inference share one shape:
files in, a long GPU run, files out. NVIDIA packages such models as self-hosted
containers ([BioNeMo NIM](https://docs.nvidia.com/nim/bionemo.md)), each with its own
API. A product-level contract (manifest with inputs, parameters, resource formula,
outputs and licence) lets one scheduler, one result store and one audit trail serve all
of them. This builds on the unified job model (SCH-101), not on the serving engines.

Bioinformatics groups already run pipelines with Nextflow, which is Apache 2.0 licensed
and runs on Kubernetes through its `k8s` executor
([Nextflow plugin registry](https://registry.nextflow.io/plugins/nf-k8s@1.5.3),
[CLIMB nf-core example](https://docs.climb.ac.uk/notebook-servers/using-nextflow)).
Supporting it avoids rebuilding pipelines that customers own.

### Model licences decide what can ship

| Model or tool | Terms found | Consequence |
| --- | --- | --- |
| AlphaFold 3 weights | Code Apache 2.0; weights for non-commercial organizations only, no commercial use, weights not redistributable, obtained from Google directly ([official weight terms](https://github.com/google-deepmind/alphafold3/blob/main/WEIGHTS_TERMS_OF_USE.md)) | Never bundled. Runs only with weights the customer obtains under its own terms, gated by the licence register |
| Boltz-2 | MIT for code and weights, commercial use allowed ([MIT Jameel Clinic](https://jclinic.mit.edu/boltz-2)) | First candidate for bundled structure prediction |
| Protenix | Code and parameters Apache 2.0 per its package page ([PyPI](https://pypi.org/project/protenix/1.0.8/)) | Candidate; verify upstream repository |
| OpenFold3 | Apache 2.0 per a secondary source ([aggregator](https://freeapihub.com/ai-models/openfold3)) | Candidate; verify upstream repository |
| RFdiffusion | BSD 3-Clause, commercial use allowed ([RosettaCommons downloads](https://rosettacommons.org/download/)) | Can be bundled |
| Rosetta | Free for non-commercial users; commercial users need a paid licence ([RosettaCommons downloads](https://rosettacommons.org/download/)) | Customer licence only |
| ProteinMPNN | Not confirmed in this pass | Verify before bundling |
| ESM2 | MIT, commercial use ([NVIDIA catalog](https://docs.api.nvidia.com/nim/reference/meta-esm2-650m)) | Can be bundled |
| ESM3 open small | The [historical Cambrian terms](https://www.evolutionaryscale.ai/policies/cambrian-non-commercial-license-agreement) limit the covered artifacts to non-commercial use. Biohub's [current model card](https://huggingface.co/biohub/esm3-sm-open-v1/blob/main/README.md) identifies the current release as MIT | Customer-supplied only. Verify the applicable terms for the exact imported artifact and revision |
| Evo 2 | Apache 2.0 and NVIDIA Open Model License both cited ([BioNeMo docs](https://docs.nvidia.com/bionemo-framework/latest/models/evo2/)) | Verify the Arc Institute upstream terms |
| LinearDesign | Patented by Baidu Research; commercial use has been licensed separately ([Sanofi agreement](https://prnewswire.com/news-releases/baidu-enters-into-license-agreement-with-sanofi-to-enable-next-generation-mrna-therapeutics-and-vaccines-301429737.html)) | Customer licence only until the licence file is verified |
| cobrapy | GPL or LGPL at the user's choice; binaries with GLPK follow GLPK's terms ([README](https://github.com/opencobra/cobrapy/blob/0.5.6/README.rst)) | Ship as a separate runtime image with its notices |
| Mol* viewer | MIT, embeddable as a library and web component ([npm](https://npmjs.com/package/molstar), [Wikipedia](https://en.wikipedia.org/wiki/Mol*)) | Bundle offline for the viewer |

The register (MOD-110) records these facts per model version and refuses uses the
installation has not declared as permitted, with the reason.

### SCI-101 job admission and customer authorization

[Nextflow processes](https://docs.seqera.io/nextflow/reference/process/) declare
inputs, outputs and execution resources separately from the workflow that invokes
them. [Kubernetes Jobs](https://kubernetes.io/docs/concepts/workloads/controllers/job/)
provide bounded retries and distinguish completed and failed work. SCI-101 through
SCI-109 cover those patterns at the platform level. The manifest describes typed
inputs, parameters, the resource estimate, artifact identities and result files.
Use SCH-101 for scheduling, retry and cancellation and MOD-110 as the policy
authority. Do not introduce a scientific scheduler or a separate license registry.
No additional dependency is selected for this contract. Nextflow and Kubernetes
are references for this item. No model, dataset or reference database is admitted
or distributed by this review.

Record the exact release and digest for each artifact, including model weights,
code or binaries, and reference data, because these can have separate terms.
Record the source and terms revision, the installation's authorized use and the
scope and validity of any separate grant. [AlphaFold 3 output terms](https://github.com/google-deepmind/alphafold3/blob/main/OUTPUT_TERMS_OF_USE.md)
also govern downstream use of some results. The
[Rosetta license](https://github.com/RosettaCommons/rosetta/blob/main/LICENSE.md)
separates internal non-commercial use from a commercial agreement and points to
separate PyRosetta terms. A public repository, open source code, a paid ownsolid
license or access to a copy of the weights cannot stand in for the applicable
artifact rights. In particular, the current ESM3 card does not determine the
terms of an older import under the Cambrian agreement.

AlphaFold 3 weights, ESM3 and Rosetta follow customer-supplied distribution policy.
The customer obtains the artifacts and the rights applicable to its exact
use and keeps its authorization current. ownsolid records and enforces the
customer's declared permissions. The product does not acquire, issue or
interpret third-party grants on the customer's behalf. Customer-supplied packages
are excluded from platform bundles and automatic redistribution.

Check authorization server-side before queueing and recheck before execution so
expiry or revocation while queued cannot permit a launch. Use the same check for
single runs, batch items, retry and provenance-based re-run. Bind the decision to
tenant and artifact digests. Fail closed on missing or incompatible records and
audit the reason without exposing license documents or credentials. Test these
paths with synthetic grants and artifacts. Exercise commercial use denied
under a non-commercial grant, a scope mismatch between tenants, a matching
customer grant, expiration while queued, revocation and a model digest mismatch.
Confirm the accepted path actually runs offline through the scheduler before
ticking SCI-101. SCH-101 and MOD-110 are prerequisites for that path.

### Reference data is large

AlphaFold 3's genetic databases are about 252 GB to download and about 630 GB unpacked,
and fast local storage is recommended for the search stage
([installation notes copy](https://code.rc.uab.edu/rc-data-science/community-containers/Alphafold3/-/blob/77f84ea6a7a44c65d273f1900fdd247b1d5c19cd/docs/installation.md)).
Reference data needs its own lifecycle: offline import, checksums, versions, placement on
nodes or shared storage, and a record of the version each run used (SCI-106).

### Regulated records

- 21 CFR Part 11 requires signed records to show the signer's name, the time and the
  meaning of the signature (§11.50) and binds signatures to their records (§11.70)
  ([eCFR text](https://www.govinfo.gov/content/pkg/CFR-2025-title21-vol1/pdf/CFR-2025-title21-vol1-sec11-200.pdf)).
- The EU GMP Annex 11 draft of July 2025 gives audit trails their own section and makes
  them mandatory for systems that manage GMP data, with review tooling; the final text is
  expected in 2026 ([ECA analysis](https://www.gmp-compliance.org/gmp-news/annex-11-draft-first-analysis)).
- Under GAMP 5 second edition and the FDA computer software assurance guidance, the
  regulated company keeps accountability for validation while suppliers provide
  documentation it can reuse ([ECA on GAMP 5 changes](https://casrai.org/guides/gamp-5-second-edition-changes)).
  ownsolid's part is a validation support pack per release (AUD-205), not validation
  itself.

### Keys and encryption

Envelope encryption keeps data keys wrapped by a key-encryption key that never leaves
the KMS. The go-kms-wrapping library supports Vault-compatible transit, KMIP and PKCS#11
backends ([pkg.go.dev](https://pkg.go.dev/github.com/openbao/go-kms-wrapping/v2),
[OpenBao transit](https://openbao.org/docs/secrets/transit/)). Licences of each library
and the customer KMS types to support first are settled in AUD-109's research.

### Laboratory systems

SiLA 2 covers instrument and service control over HTTP/2 and Protocol Buffers; Allotrope
covers analytical data formats and ontologies; neither replaces an ELN or LIMS API
([overview](https://intuitionlabs.ai/articles/lab-instrument-integration-sila-2-allotrope-lims)).
DEV-205 defines an adapter contract and chooses the first ELN and LIMS products by
research into what customers in China run.

### Cloud elasticity

Karpenter chooses GPU instance types per pending pod and handles spot interruption
notices; the cluster autoscaler only scales predefined node groups
([comparison](https://eastondev.com/blog/en/posts/dev/20260504-karpenter-vs-cluster-autoscaler/)).
Both are AWS-centred in the sources found; Tencent Cloud and Alibaba Cloud autoscalers
need their own research in FLT-211. The air-gapped default is unchanged.

## Open questions for item research

- Which open structure predictors match AlphaFold 3 accuracy on the customer's targets,
  and their exact licences from upstream repositories.
- ProteinMPNN, Uni-Fold, LinearDesign, MEWpy, Escher, eQuilibrator and GLPK licence files.
- Whether Nextflow pipelines with GPU steps run under ownsolid quotas without changes.
- Which ELN and LIMS products the first customers run.
