# Gateway embeddings and rerank (GW-102)

Reviewed 2026-11-09.

[vLLM 0.31.0 pooling](https://docs.vllm.ai/en/v0.31.0/models/pooling_models/)
maps enabled model tasks to OpenAI-compatible embeddings and Cohere-style
rerank endpoints. It chooses the pooling task from the served model's
capability. [OpenAI's embeddings contract](https://developers.openai.com/api/reference/resources/embeddings/methods/create)
defines ordered vector results and prompt and total token usage. The
[vLLM rerank benchmark](https://docs.vllm.ai/en/v0.31.0/benchmarking/cli/)
exercises documents grouped under one query through /v1/rerank.

The shipped approved recipe is the local Qwen3-0.6B chat generation recipe.
It has no qualified embedding or rerank task. The current machine
AdmissionRequest also requires a positive maximum_output_tokens value,
and the request table persists the generation limit. The existing
canonical Usage can represent zero completion tokens once the admitted
operation and its input token accounting are authoritative.

Use separate approved serving task capabilities on the published deployment.
For the first path, accept bounded text inputs for embeddings and bounded
text query and document inputs for rerank. The control plane must confirm
the alias's task and scope before any backend call. Reserve verified input
tokens against the existing quota, including the engine's per-pair
tokenization cost for rerank. Validate response count, positions, vector
dimension and finite values, then persist the existing canonical receipt
before returning results. A dummy output token allowance or a capability
inferred from the alias name would blur authority and misstate budgets.

No dependency, model or dataset is selected. The existing
[vLLM 0.31.0 license](https://github.com/vllm-project/vllm/blob/v0.31.0/LICENSE)
is Apache-2.0 and permits commercial use, offline use and redistribution
with notices. Before selection, verify the pinned model files' licenses,
commercial use, redistribution and offline operation separately.

## Production prerequisites

- Serving must supply an offline approved image, weights and ready
  observation for one embedding task and one rerank task. Prove each
  executes with the network disconnected.
- DEV and the database owner must define task-aware alias discovery and
  admission, including the zero-output case and verified input bounds.
  The gateway must reject a route whose task the admitted deployment lacks.
- Exercise valid requests, oversized or malformed inputs, wrong task and
  foreign key refusal, quota denial before dispatch, malformed or
  nonfinite backend results, and durable exact usage against those real
  offline serving fixtures. Console and Portal need a complete way to
  publish the capability and issue the appropriate key.
