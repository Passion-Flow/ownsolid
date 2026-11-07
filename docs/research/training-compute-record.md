# Training compute record

Research behind TRN-111. Each training job records how much compute it used, in the
units regulators and model publishers use, so that a customer can show that a
fine-tune stays far below every compute threshold, with figures the platform measured
and did not guess.

## Why customers need it

Compute thresholds now appear in law, in bills and in drafts. They count operations,
not GPU hours:

| Instrument | Threshold | What counts | Status |
| --- | --- | --- | --- |
| EU AI Act, Article 51, and the Commission's guidelines on general-purpose AI models | 10^23 operations as the indicative criterion for a general-purpose model; 10^25 presumes systemic risk. A downstream modifier becomes a provider when the modification uses more than one third of the original model's training compute; if that compute is unknown, one third of 10^23 or 10^25 | Modification compute, compared with the base model's compute | In force; the guidelines are indicative ([Commission guidelines](https://digital-strategy.ec.europa.eu/en/library/guidelines-scope-obligations-providers-general-purpose-ai-models-under-ai-act), [Slaughter and May summary](https://thelens.slaughterandmay.com/post/102kxn3/long-awaited-gpai-guidelines-published-ahead-of-2-august-deadline)) |
| California SB 53, definition of a frontier model | More than 10^26 operations | Cumulative: the original training plus later fine-tuning, reinforcement learning or other material modification | In force ([FPF explainer](https://fpf.org/blog/californias-sb-53-the-first-frontier-ai-law-explained/)). The state's bill page refused automated reading, so the statute itself was not read |
| US Ban Artificial Superintelligence Act | 10^25 operations defines "advanced AI", adjustable by the proposed Secretary | Training compute | Bill in committee ([section-by-section](https://www.sanders.senate.gov/wp-content/uploads/Ban-Artificial-Superintelligence-Act-Section-by-Section.pdf)) |
| China, draft export control list for AI technologies, 2026-10-16 | Reported as 10^26 training operations, among other criteria | Model weights leaving China | Draft; thresholds seen only in secondary reports ([TNW](https://thenextweb.com/news/china-ai-export-controls-draft)). Read the official text before relying on it |

Lawfare notes that a downstream developer often cannot know the base model's compute
because publishers do not always disclose it
([Lawfare](https://www.lawfaremedia.org/article/california-enacted-ai-bills-now-officials-must-define-them)).
The product must therefore keep the base figure separate from what it measured, and say
when the base figure is missing.

The legal rows rest on summaries seen as search extracts. The Commission's page shows a
last update of 2026-04-20 and some summaries cite a later document number, so read the
current text before quoting any figure to a customer.

Values change by jurisdiction and year, so the product never hardcodes a threshold and
never draws a legal conclusion. It shows measured figures, a declared base figure with
its source, and the ratio between them; reference lines come from configuration.

## How compute is estimated

Epoch AI, whose estimates are the usual public reference, uses two methods
([Estimating training compute](https://epoch.ai/blog/estimating-training-compute),
[estimation documentation](https://epoch.ai/data/ai-models-documentation/estimation)):

1. **Arithmetic:** operations per token times tokens processed. For a dense transformer,
   about 2N operations per token forward and twice that backward, so C = 6·N·T, with T
   the tokens processed ([Kaplan et al. 2020, section 2.1](https://arxiv.org/abs/2001.08361)).
   That paper defines N as the parameters "excluding all vocabulary and positional
   embeddings". Epoch counts a fused multiply-add as two operations, matching vendor peak
   figures. The attention term (2·n_layer·n_ctx·d_attn forward per token, same paper) is
   left out of 6N; it grows with context length.
2. **Hardware:** devices times time times peak operations per second times utilization.
   Epoch reports typical utilization for large runs of 30 to 50 percent.

Fine-tuning details that change the arithmetic:

- **LoRA and QLoRA** freeze the base weights. The LoRA paper says "we do not need to
  calculate the gradient for the vast majority of the parameters" and measures a 25%
  speedup on GPT-3 175B ([Hu et al. 2021](https://arxiv.org/abs/2106.09685)). *Inference:*
  the backward pass still propagates activation gradients but skips weight gradients, so
  a LoRA step executes about 4N per token. The paper does not state that figure.
- **Gradient checkpointing** costs "an extra forward pass per mini-batch"
  ([Chen et al. 2016](https://arxiv.org/abs/1604.06174)), about 2N more per token. The
  current recipe disables it (`gradient_checkpointing` in `api/training/v1/configuration.json`).
- **Mixture of experts:** N is the active parameters per token, not the total.
- **Padding:** padded positions are computed but are not training data. Epoch and the
  instruments above count training tokens, so padding is excluded.

## Chosen approach

1. **One record per attempt, part of the experiment record (TRN-105), not a separate
   ledger.** AGENTS.md section 2 prefers finished features to new ledgers. The figures
   are columns of the attempt and fields of the experiment export.
2. **The runtime counts tokens.** Only the worker sees the real batches. It counts
   non-padding tokens fed to the forward pass, per optimizer step, and reports the
   running total in its metric events and the final total in its result. The count is
   saved inside each sealed checkpoint, so a resumed attempt continues from the
   checkpoint's total, not from zero.
3. **Headline figure: 6·N·T for every method.** That is the convention behind the
   published figures a fine-tune is compared with. It is a conventional count, not
   operations executed, and the record says so: the current LoRA recipe executes less
   (about 4N), while full fine-tuning with checkpointing (TRN-103) would execute more
   (about 8N). N comes from the loaded model: all parameters including embeddings for
   dense models, counted once when embeddings are tied. That is more than Kaplan's N, a
   deliberate choice that errs towards overstating and matters most for small models.
   Mixture of experts uses active parameters, computed from the architecture's expert
   count and experts per token. An architecture whose active count the runtime cannot
   derive reports "not available" rather than a total that would mislead. Today the
   worker accepts only `qwen3`, so the mixture path is first reachable through TRN-102.
4. **Hardware ceiling as a cross-check:** the agent's measured device-seconds times the
   device's peak dense BF16 rate, taken from vendor datasheets recorded in the device
   model with their source. No utilization is assumed, so this is a ceiling. If the
   arithmetic figure exceeds the ceiling, the record is marked inconsistent and the job
   page says so. An unknown device has no ceiling.
5. **Two totals per job.** *Lineage compute* sums the attempts whose checkpoints lead
   to the accepted result: what went into the weights, which is what the instruments
   count. *Compute spent* sums every attempt, including failed and cancelled ones, for
   chargeback (TEN-106).
6. **Declared base compute is optional and sourced.** A model version may carry the
   publisher's training compute with the text or link it came from. The job then shows
   the cumulative figure and the fine-tune's share of the base. Without a declaration it
   says "base compute not declared" and shows only measured figures. The product never
   estimates a base model's compute.
7. **Storage and wire format.** Operations reach 10^26 and exceed a signed 64-bit
   integer (about 9.2 × 10^18). Tokens and parameters are stored as integers;
   operations as exact decimals (`NUMERIC` in PostgreSQL and Dameng, `DECIMAL(38,0)` in
   MySQL) and carried as decimal strings in JSON. Display uses scientific notation.
8. **Control validates what the worker reports.** Tokens may not exceed steps ×
   per-device batch × accumulation × maximum tokens × devices from the admitted
   configuration, and totals may not decrease between events. A report that breaks
   either is refused with a stable error code and the attempt is marked for review.
9. **Reference thresholds are configuration.** A typed, commented list of named
   reference values (label, operations, source link) in the control configuration;
   the example lists the instruments above. An empty list shows no reference lines.

## Alternatives rejected

- **Method-specific factor (4N for LoRA, 8N for full fine-tuning with checkpointing) as the headline.** It
  is closer to executed operations but is not the convention used for published model
  figures, so the ratio to a base model would mix conventions. Kept out of the record
  to avoid two competing figures.
- **Hardware method with an assumed utilization as the headline.** Utilization is a
  guess; the product records only what it measured.
- **Estimating base model compute from parameters and an assumed token count.** A
  guess presented as a measurement is worse than a stated gap.
- **A separate compute ledger service.** Duplicates the attempt record and conflicts
  with AGENTS.md section 2.

## Where it lands in the code

Read at commit 85cd1c0. This is why the item is not small:

- `api/training/v1/result.go` decodes the result as an exact union with a closed field
  list per status, and a failed result carries no metrics. The final totals need new
  fields in that contract, and the figures of a failed attempt can only come from events.
- `MetricValues` holds float64 values. Token totals need their own integer event field,
  since float64 loses exactness above 2^53.
- `Bindings` and sealed checkpoints identify a resume exactly, so the saved token total
  has to enter the sealed state without changing what a resume is bound to.
- The worker contract also appears in `api/agent/v1/schema.json` and in the recorded
  fixture `tests/fixtures/training/actual-worker-v1.json`.
- Attempt columns need migrations on every database chain.
- The runtime has no unit tests in this repository, and the workstation has no PyTorch,
  so the worker side runs on a test server.

## What it waits for

- **TRN-105:** the experiment record it is part of.
- **Screens S-501 and S-502:** the job and run pages in stage 5 of the build plan. Neither
  exists yet, and an item with a screen waits for that screen's entry in the plan.
- **The module review** AGENTS.md requires before the first item of this module.

## Dependencies and licences

No new dependency. Token counting uses the collator's attention mask in the existing
pinned Transformers and PyTorch runtime; parameter counting uses PyTorch. Works fully
offline.

## Failure modes and tests

| Failure | Test |
| --- | --- |
| Padding or label masking counted as tokens | Unit test with a padded batch of known lengths |
| Resume double-counts or restarts from zero | Resume test: totals before and after the checkpoint match an uninterrupted run |
| Cancelled or failed attempt loses its figures | Cancel mid-run; lineage excludes it, compute spent includes the last reported total |
| Mixture-of-experts total used as N | Unit test on a tiny MoE configuration; unknown MoE layout reports not available |
| 64-bit overflow | Store and read back 10^26 on PostgreSQL, MySQL and Dameng |
| Worker reports impossible or decreasing totals | Refused with a stable error code; attempt marked for review |
| Arithmetic figure above the hardware ceiling | Marked inconsistent and shown on the job page |
| Unknown device | Ceiling shown as not available, no error |
| Cross-tenant read of figures | Permission-denied and cross-tenant integration tests |

## What the finished item must do

- A completed LoRA job shows tokens processed, parameters counted, operations by
  6·N·T, the hardware ceiling and its device source, lineage compute and compute spent,
  on the job page in the console and the portal, in English and Simplified Chinese.
- With a declared base compute, the job shows the cumulative figure and the share of
  the base; without one, it says the base is not declared.
- Configured reference values appear as labelled lines with their source links.
- The figures are in the experiment export and in the job's audit trace (AUD-103).
- Resume, cancel and failure keep the totals described above, verified by tests on
  real PostgreSQL and in one end-to-end run with the network disconnected.

## Open questions

- The Chinese draft's final thresholds, once the official text is read, and whether its
  23 items reach cluster management software such as ownsolid.
- Distributed training (TRN-201) sums tokens across data-parallel ranks; tensor and
  pipeline parallel ranks share tokens and must not be summed. Settle when TRN-201 is
  built.
