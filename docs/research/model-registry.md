# Offline model registry

## Upstream patterns

- [Kubeflow Hub](https://www.kubeflow.org/docs/components/hub/overview/) separates
  model versions and artifact locations from the bytes. [Red Hat OpenShift AI](https://docs.redhat.com/en/documentation/red_hat_openshift_ai_self-managed/2.25/html-single/working_with_the_model_catalog/registering-a-model-from-the-model-catalog_working-model-catalog)
  registers catalogue entries as a model and version. Keep the discovery index
  separate from the installation's verified workspace inventory.
- [Hugging Face Hub](https://huggingface.co/docs/huggingface_hub/en/package_reference/file_download)
  supports exact-revision snapshots. [Its cache documentation](https://huggingface.co/docs/hub/en/local-cache)
  distinguishes Git blob identifiers from LFS payload SHA-256. [ModelScope's SDK](https://github.com/modelscope/modelscope_hub/blob/main/src/modelscope_hub/_download.py)
  checks payload SHA-256 when the registry provides it. A catalogue builder must
  verify the upstream identifier according to its algorithm and bind the transported
  payload to a SHA-256 in the signed index. Treating a Git blob ID as a plain-file
  digest would reject valid files or undermine provenance.
- [NVIDIA NIM](https://docs.nvidia.com/nim/large-language-models/2.0.0/deployment/air-gap-deployment.html)
  separates pre-staging and isolated serving. [ADR 0036](../decisions/0036-show-every-option-and-fetch-models-offline.md)
  applies that pattern to the catalogue, fetch manifest and bundle. Keep weights,
  tokenizer and configuration at the same pinned revision.
- [Hugging Face model cards](https://huggingface.co/docs/hub/en/model-cards)
  record publisher-declared metadata. [Amazon SageMaker Model Registry](https://docs.aws.amazon.com/sagemaker/latest/dg/model-registry.html)
  tracks versions, lineage and approval. Preserve publisher terms as reported and
  keep the installation's authorization decision separate from registry metadata.
- [tus](https://tus.io/protocols/resumable-upload) separates checksums, offsets
  and upload expiry. [S3 multipart lifecycle](https://docs.aws.amazon.com/AmazonS3/latest/userguide/mpu-abort-incomplete-mpu-lifecycle-config.html)
  expires unfinished uploads independently of completed objects. MOD-120 reclaims
  completed staging. MOD-121 covers abandoned uploads that never publish, so they
  cannot retain disk space and occupy a workspace's only active import indefinitely.

The review introduces no software dependencies, model weights or datasets. External
products are references. Software licenses, model terms and redistribution rights
must be verified separately for each implementation and model revision.

## MOD-101: Local media or an internal mirror

Retain the existing persisted offset, scoped import identity and per-chunk SHA-256.
Both sources should enter the same verification and publication path. A trusted
manifest binds immutable revisions to expected files. Model code and unsafe
serialization remain subject to MOD-108. Use a configured mirror with a restricted
origin and trust roots. Arbitrary request-supplied URLs would allow access to other
internal services and create an unapproved outbound path. Direct internet access
belongs to MOD-118.

The current Admin catalogue endpoint returns one embedded model. Import storage,
version validation, transfer, and the Agent use its digest and file set as authority.
PostgreSQL also restricts the approved base model key. Changing only the model
importer would publish models that the existing UI cannot select and the Agent
cannot verify. A compatible multi-model manifest and transfer contract must be
agreed with those owners before widening admission.

Acceptance requires importing a second immutable revision and a different model
through the existing console flow, from media and from a configured internal mirror.
Interrupt and resume from the committed offset. Cover wrong size/hash, mismatched
revision, missing tokenizer, unsafe archive entries, concurrent requests, denied
roles and cross-workspace requests. Keep the model invisible until verification and
publication succeed. Run with registry egress blocked and confirm no fetch is
attempted during serving.

## Two pinned source manifests

The first compatibility pair uses the shipped
[Qwen3-0.6B revision](https://huggingface.co/Qwen/Qwen3-0.6B/tree/c1899de289a04d12100db370d81485cdf75e47ca)
and
[SmolLM2-135M-Instruct at 12fd25f](https://huggingface.co/HuggingFaceTB/SmolLM2-135M-Instruct/tree/12fd25f77366fa6b3b4b768ec3050bf629380bac).
They use `Qwen3ForCausalLM` and `LlamaForCausalLM` respectively, so the second
fixture also exercises the need for architecture-specific validation.
Both publishers declare Apache-2.0. Preserve the publisher notice and license
terms when redistributing. The prototype used no datasets and adds no runtime
dependency.

On the connected development host, request the exact revision with
`GET /api/models/<repository>/revision/<commit>?blobs=true` and reject a
different response revision. A downloaded LFS file must match the registry's
payload SHA-256. For an ordinary Git file, verify the registry's blob object ID
over the Git blob header and payload, then record the payload's SHA-256 for
transport. This follows the [Hub digest documentation](https://huggingface.co/docs/hub/en/local-cache)
and the [Git object format](https://git-scm.com/book/en/v2/Git-Internals-Git-Objects).
The Git blob ID is a source consistency check. A signed catalogue entry must
bind the resulting SHA-256 values to the source revision before an installation
can trust them.

The prototype checked all nine files for each real revision and rejected a
same-size mutation in both configurations. Qwen's 1,519,207,673 verified bytes
matched the embedded catalogue. SmolLM2 supplied 272,444,345 verified bytes.
A second run under a network-denying sandbox read both local snapshots from
their generated manifests and rejected both cross-model configuration
substitutions. The chosen SmolLM2 transfer set is below the current 2 GiB
import ceiling. Its published LFS digest and the transport digests computed
from verified Git blobs are recorded below. Weights and prototype files stay
outside Git.

| Path | Bytes | Transport SHA-256 |
| --- | ---: | --- |
| `README.md` | 6,772 | `4f97533ad95b1b2fea15fbc075c01b94578ebdd7c8138888fa43fa3abd530dc4` |
| `config.json` | 861 | `8eb740e8bbe4cff95ea7b4588d17a2432deb16e8075bc5828ff7ba9be94d982a` |
| `generation_config.json` | 132 | `87b916edaaab66b3899b9d0dd0752727dff6666686da0504d89ae0a6e055a013` |
| `merges.txt` | 466,391 | `0b54e8aa4e53d5383e2e4bc635a56b43f9647f7b13832d5d9ecd8f82dac4f510` |
| `model.safetensors` | 269,060,552 | `5af571cbf074e6d21a03528d2330792e532ca608f24ac70a143f6b369968ab8c` |
| `special_tokens_map.json` | 655 | `2b7379f3ae813529281a5c602bc5a11c1d4e0a99107aaa597fe936c1e813ca52` |
| `tokenizer.json` | 2,104,556 | `9ca9acddb6525a194ec8ac7a87f24fbba7232a9a15ffa1af0c1224fcd888e47c` |
| `tokenizer_config.json` | 3,764 | `4ec77d44f62efeb38d7e044a1db318f6a939438425312dfa333b8382dbad98df` |
| `vocab.json` | 800,662 | `82b84012e3add4d01d12ba14442026e49b8cbbaead1f79ecf3d919784f82dc79` |

## Integration boundary

1. Keep the existing approved catalogue read and its Qwen digest compatible
   with installations and agents that use it. Add a reviewed multi-entry
   catalogue contract. The control plane must resolve the existing
   `catalog_digest` to a verified manifest before accepting an upload.
2. Pin the resolved manifest for the life of the import. The stored import
   must keep the same file set through a catalogue refresh, a retry and a
   control restart. Stage and verify the exact manifest, then publish its
   unchanged revision and digest in the scoped model version. A contiguous
   migration must broaden the existing `model_key='qwen3-0.6b'` constraint
   before a second base model can publish.
3. Carry that identity into leased transfer, per-model verification and
   cache paths. Older agents can continue using the original pinned recipe.
   An agent that does not understand the selected format or architecture
   must refuse it before allocating a deployment.
4. Let the console select the exact revision from the multi-entry catalogue.
   Local media and a configured internal mirror use the same digest and
   exact-file checks. Mirror destinations come from deployment policy and
   use trusted TLS. Public registries remain outside the installation's
   runtime network boundary.

The prototype validates the source and transport integrity steps. Importing
and serving the second model through the product requires the shared catalogue,
verifier, schema and Agent changes above.
