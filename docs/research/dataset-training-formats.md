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
