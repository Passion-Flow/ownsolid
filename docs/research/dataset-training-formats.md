# Dataset training formats (DAT-102)

## Sources and approach

[Hugging Face TRL's dataset format contract](https://huggingface.co/docs/trl/dataset_formats)
defines language-modeling text and conversation records and paired preference
records. It recommends an explicit prompt for standard preference pairs.
[Stanford Alpaca's data schema](https://github.com/tatsu-lab/stanford_alpaca#data-release)
separates an instruction, optional input context, and output.

Allow the intake caller to select one row schema for the entire source.
File syntax and row schema are independent. Validate one decoded row at a
time before writing it to the normalized JSONL artifact. Keep its original
content, extra top-level metadata, and field types. Use the same bounded
diagnostics and failure policy as source import. A schema selection provides
these text training shapes:

| Selection | Row requirements |
| --- | --- |
| Text | A nonblank string in `text`. |
| Messages | A list of textual `role` and `content` pairs. Supports an optional leading system message and at least one user and one assistant message. |
| Instruction | Nonblank `instruction` and `output` strings. `input` may be absent, null, or a string. |
| Preference | Nonblank string values for `prompt`, `chosen`, and `rejected`. The two completions must differ. |

The legacy training worker consumes exact `source_id`, `prompt`, and
`completion` records and its publication hashes bind that schema. Keep that
interface intact. Guessing a training schema from columns is ambiguous, and
flattening a conversation or preference into one completion loses structure.
Template rendering belongs to tokenizer preview and the model-specific trainer.
Conversational preferences and tool or multimodal messages require typed
consumer contracts beyond this intake subset.

## Dependencies and verification

Reuse the Go standard library ([BSD-3-Clause](https://go.dev/LICENSE)).
TRL documentation and the Alpaca schema are references. The intake ships no TRL,
Alpaca code, model, or data. The Alpaca reference dataset is
[CC BY-NC 4.0](https://github.com/tatsu-lab/stanford_alpaca#data-release) and
unsuitable for commercial redistribution. Tests generate all rows locally.

Verify the four shapes, mixed and malformed records, invalid preference
completions, optional context, Unicode, field locations, and both rejection
policies through the real source intake. Keep DAT-102 open until a user can
select a schema, register the artifact, and run a compatible offline trainer
against it.

## Published single-turn messages

TRL's [conversational example](https://huggingface.co/docs/trl/dataset_formats#language-modeling)
uses an ordered `messages` array with textual `role` and `content`.
[Transformers chat templates](https://huggingface.co/docs/transformers/chat_templating)
show why role order and model-specific formatting matter. The ownsolid
completion-only trainer renders exactly one user turn and encodes the target
separately in [data.py](../../runtimes/training/projectone_training/data.py).
A single user message followed by one assistant message is equivalent to the
existing prompt and completion pair for that consumer.

- Select `single_turn_messages_v1` explicitly when attaching a JSONL source.
  Each line has exactly `source_id` and `messages`. Each message has exactly
  `role` and `content`. Require `user`, then `assistant`, with nonblank text
  within the existing field limits. Map the contents without rewriting them.
  Source IDs, physical line numbers, and raw bytes retain their meaning.
- Freeze the option with the original import job and bind the private
  inspection receipt to it. Same-digest replays and retries retain that
  interpretation. Publish and clone the resulting canonical prompt/completion
  rows. The existing effective-source digest and row mapping relate each
  published row to its draft row and original source line. The draft's source
  digest and object continue to identify the raw upload.
- Report `DATASET_MESSAGE_SEQUENCE_INVALID` for a list other than one user
  followed by one assistant. Reject extra fields, content arrays, preference
  pairs, and unsupported import selections. Truncated JSON produces a line
  error and long lines or text are rejected at the existing limits. The
  processor never clips content. The trainer still applies its own token
  limit after rendering the frozen row.
- Keep the generic intake's format validation separate from training admission.
  Rendering conversation templates at intake would bind data to a model and
  would change completion-only masking. Dropping a system turn, later turns,
  or a rejected response would change the training example.

The publication path uses the existing [CPython](https://docs.python.org/3/license.html)
worker (PSF License) and Go standard library. [TRL](https://github.com/huggingface/trl/blob/main/LICENSE)
and [Transformers](https://github.com/huggingface/transformers/blob/main/LICENSE)
are Apache-2.0 design references. The low-resource integration fixture uses
synthetic rows and the existing [PostgreSQL](https://www.postgresql.org/about/licence/),
[pgvector](https://github.com/pgvector/pgvector/blob/master/LICENSE),
[pgx](https://github.com/jackc/pgx/blob/master/LICENSE) (MIT), and
[google/uuid](https://github.com/google/uuid/blob/master/LICENSE) (BSD-3-Clause).
These licenses permit commercial offline use and redistribution under their
notice terms. The path adds no library, model, or dataset.

The disconnected CPython and PostgreSQL tests checked role order, unsupported
shapes, field and line limits, truncated JSON, retries, and replay conflicts.
Both split variants retained the original source bytes, source IDs, and line
mapping while the existing training projection verified the canonical assets.
The frozen training reader preserved completion-only labels and refused a
too-long sequence in a smoke check using a fixture tokenizer.

DEV/Web owns public selection, format help, and validation presentation.
Offer the versioned choice when attaching a source, surface its frozen value
from the original import, and present sequence errors with their physical
line numbers. Deploy the matching processor before enabling the choice.
DAT-102 remains open for public workflow verification and the other training
formats.

With the [PostgreSQL and CPython fixture](../development/environment.md)
configured, run the focused publication tests:

```sh
go -C services/control test -tags dataset_processor_integration ./internal/modules/datasets -run '^(TestActualProcessorSingleTurnMessages|TestActualSingleTurnMessages)'
```
