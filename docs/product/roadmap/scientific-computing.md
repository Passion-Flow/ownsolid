# Scientific computing

Module reviewed on 2026-11-09 against Nextflow and Kubernetes Jobs, with current
upstream terms for AlphaFold 3, ESM3 and Rosetta. See
[scientific computing research](../../research/scientific-computing.md).

Models and tools that run as jobs on input files and write result files, such as protein
structure prediction, protein design, sequence optimization and metabolic modelling.
A general task-type model contract comes first; domain packs, life sciences first, build
on it. Research: [scientific computing](../../research/scientific-computing.md).

- Owns: `services/control/internal/modules/tasks` (new), `runtimes/scientific` (new),
  `catalog/apps` task templates, web console and portal task pages.
- Depends on: scheduling-and-quota (unified job model), models-and-registry (licence
  register), network-and-storage (object storage), audit-and-compliance.

AlphaFold 3 weights, ESM3 and Rosetta are customer-supplied integrations.
The customer obtains and maintains the applicable rights for the exact artifacts
and intended use. An ownsolid platform license conveys no third-party rights.
The planned SCI-101 admission path must refuse jobs with missing, expired or
incompatible authorization under MOD-110. This applies to single runs, batch
items, retries and re-runs. Restricted artifacts are excluded from product bundles.

## P1 Enterprise release

- [ ] SCI-101 Task-type model contract: a model or tool that runs as a job on input files and writes output files, declared by a manifest with inputs, parameters, resource formula, outputs and licence, and run on the unified job model (P1)
- [ ] SCI-102 Task templates: typed inputs with validation (sequence formats such as FASTA, structure files, tables), parameter presets and limits; one manifest drives the console form, the CLI and the API (P1)
- [ ] SCI-103 Batch submission of up to thousands of tasks with per-batch progress, success and failure lists, retry of failed tasks only and batch cancel (P1)
- [ ] SCI-104 Resource estimation and admission from the input, for example sequence length to GPU memory; a task that fits no allowed pool is refused with the reason, others queue with an estimate (P1)
- [ ] SCI-105 Out-of-memory and long-input handling: classify GPU out-of-memory failures, offer a larger pool or an input split, and retry on a larger pool by policy (P1)
- [ ] SCI-106 Reference data sets: large read-only databases of hundreds of gigabytes, such as genetic sequence databases, imported offline, versioned, checksummed, placed on nodes or shared storage and recorded per run (P1)
- [ ] SCI-107 Run provenance and re-run: inputs, template version, image and weight digests, reference data versions, seeds and parameters linked to outputs; any task re-runs from its record (P1)
- [ ] SCI-108 Result browser: per-run files with previews by type (tables, images, JSON, logs, structure files), downloads, and share links with expiry, revocation and access audit (P1)
- [ ] SCI-109 Load tested: a batch of 2,000 tasks and 4 concurrent GPU tasks for 72 hours, with success rate and P50/P95 duration recorded (P1)

## P2 Fleet operations

- [ ] SCI-201 Offline 3D molecular viewer bundled with the product: chains, residues, measurements, confidence colouring (pLDDT), predicted aligned error heat map and side-by-side comparison (P2)
- [ ] SCI-202 Protein and complex structure prediction templates, commercially usable open models first, with multi-chain input and native confidence metrics; models with restrictive terms such as AlphaFold 3 run only under the customer's own licence through the licence register (P2)
- [ ] SCI-203 Protein design templates: backbone generation and sequence design chained into structure prediction; licensed suites such as Rosetta only under the customer's licence (P2)
- [ ] SCI-204 Nucleic acid and mRNA design templates: sequence optimization, folding free energy (MFE) and codon adaptation (CAI) metrics, and candidate comparison (P2)
- [ ] SCI-205 Biological sequence foundation models such as ESM2 and Evo 2: embeddings and scoring as tasks and as served endpoints (P2)
- [ ] SCI-206 Metabolic modelling environment: import genome-scale models (SBML), flux balance and perturbation analysis with open solvers, and pathway maps (P2)
- [ ] SCI-207 Enzyme kinetics and thermodynamics task templates: turnover-number prediction and Gibbs free energy estimation (P2)
- [ ] SCI-208 Mass spectrometry spectrum analysis task templates (P2)
- [ ] SCI-209 Nextflow pipelines, including nf-core, run on a cell under the platform's identity, quota and audit, offline from a pinned pipeline bundle (P2)
- [ ] SCI-210 Verified on real NVIDIA hardware: a protein complex predicted end to end, with model, GPU and duration recorded (P2)
