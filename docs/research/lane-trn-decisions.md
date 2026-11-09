# Training lane decisions

## TRN-101 to TRN-103 product boundaries

| Item | Existing path and boundary |
| --- | --- |
| TRN-101 | The versioned training HTTP API already covers create, cancel, recover, attempts, checkpoints, metrics and files. The operator CLI has no training commands. The S-501 and S-502 flows must invoke those same operations. The lane leaves the existing contract in place and records the CLI and web handoff. |
| TRN-102 | The worker loads an exact local approved Qwen3 snapshot with remote code disabled, and the Agent and controller validate the approved model and LoRA recipe. General imported causal models and QLoRA require model admission, Agent dispatch, method-aware validation and a pinned offline bitsandbytes build. Replacing only the Python model type check would still leave the new mode unreachable and the output unacceptable. |
| TRN-103 | The current result and checkpoint contracts require adapter files and frozen base parameters. Full-parameter SFT needs an agreed model artifact type and a compatible Agent and registry path. Sending full weights as an adapter would misrepresent their identity and bypass artifact checks. |

The existing lifecycle contract remains the authority. The relevant upstream
paths are [Kubeflow lifecycle](https://trainer.kubeflow.org/en/latest/user-guides/trainjob-lifecycle.html),
[PEFT training](https://huggingface.co/docs/transformers/main/peft), and
[Transformers quantization](https://huggingface.co/docs/transformers/quantization/bitsandbytes).
The next independently testable piece is the TRN-104 worker seal. The lane
keeps the three product items unticked.

## TRN-104 to TRN-111 remaining product gates

| Item | Verified surface or implementation boundary |
| --- | --- |
| TRN-104 | The worker seals complete state before emission. The controller already verifies accepted, same-job checkpoint receipts before delivery. A real interrupted run still needs the NVIDIA runtime image, Agent and an S-502 recovery flow. |
| TRN-105 | The job retains its model/catalog and dataset/manifest identity, typed settings and seed. Runtime bindings retain package versions and image identity. Dispatch uses current hardware observations for eligibility; that mutable observation is not the attempt's historical hardware. Persist hardware and the lock digest at the attempt admission boundary and expose them with the existing scoped run, rather than copying today's node inventory into old experiments. |
| TRN-106 | Metrics are already persisted as scoped, cursor-paginated training events. S-502 must plot events by attempt and compare runs with their original input and recipe records. A second metric collector would duplicate the existing event owner. |
| TRN-107 | The accepted adapter snapshot has workspace and attempt authority. The serving and evaluation owners must consume that exact identity for deployment and comparable baseline/candidate results. The training service cannot declare the comparison passed. |
| TRN-108 | Both dependency locks are present. The current deployment tree lacks the training image referenced by the inherited guide. Release packaging and the Agent's installed image selection must agree on the same worker source and image identity before this can run. |
| TRN-109 | Notebook admission, interactive access and workload isolation belong with scheduler, Agent and browser owners. An unrestricted local Python process would not supply the notebook access or isolation contract. |
| TRN-110 | The CPU server has no NVIDIA device. The current ownsolid run requires an admitted image and an available NVIDIA host. The inherited RTX 3090 result remains separate. |
| TRN-111 | Wait for TRN-105. The experiment record must bind actual processed tokens to the model and attempt hardware. See [training compute record](training-compute-record.md) for the distinct measured and estimated quantities. |

[Kubeflow progress](https://trainer.kubeflow.org/en/latest/user-guides/trainjob-progress.html)
and [MLflow tracking](https://mlflow.org/docs/latest/tracking/) separate status
and artifact storage from the run comparison UI. Retain ownsolid's existing
event and artifact authorities. No shared contracts change in the worker seal.

## TRN-106 metrics readback

The existing TLS contract test
`TestActualTLSDualEntranceTrainingIntentHistoryMetricsRecoveryAndCurrentRoles`
passed against disposable PostgreSQL and the local dataset processor on
Linux with only loopback. It records test metrics through the admitted
agent path, reads the next scoped page from the Portal and refuses a
cursor from the metrics collection on the attempts collection and an
unknown attempt. This qualifies server-side ingestion and pagination.

S-502 owns polling and comparison across runs. It can reuse the scoped
metrics endpoint and the accepted experiment projection for each selected
attempt's frozen inputs and recipe. Plot by optimizer step and distinguish
different attempts by attempt ID and event sequence. Keep TRN-106 open
until that user flow is wired and verified. A new collector or metrics
store would duplicate the existing event authority.
