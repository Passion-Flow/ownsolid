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
