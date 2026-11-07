# Model services

Research behind SRV-111, the Model services pages: screens S-203 (deploy a model), S-204 (the list)
and S-205 (a deployment's own page) of the [build plan](../product/plan/README.md).

## What exists

The control plane serves one model on one whole GPU of one node, through the `deployments` module
(`api/openapi/v1/deployments`):

- **Create** takes a node, a GPU and a model. It checks the model is ready, the node is online,
  runs Ubuntu 24.04 on amd64 with the recipe's host memory, has a fresh GPU observation, and that the
  GPU meets the approved recipe (driver 580 or newer, compute capability 7.5, MIG off, 8 GiB). A node
  serves one deployment and a GPU belongs to one deployment, so either already in use refuses with
  `INFERENCE_RESOURCE_BUSY`. Creating queues a node task; the deployment is running only when the
  agent reports it ready with a verified generation.
- **Actions** start (`deploy`), stop, remove, check (`observe`) and verify a deployment, each guarded
  by its revision, so two people acting at once cannot both win.
- **GPU observation** is a node task (`POST .../nodes/{id}/inference/observations`); its result is what
  every check above reads, and it goes stale after the online window.

What does not exist yet: replicas, scaling and rolling updates (SRV-105, SRV-106), a choice of engine
(SRV-101), per-deployment traffic metrics (OBS-101, GW-107) and logs (OBS-102), deployment names
(SRV-112), revision history and rollback (SRV-113), and the read-only root and egress settings as
choices (SRV-104, DEL-003). The pages must show each of these as not ready, with its reason, rather
than hide it ([ADR 0036](../decisions/0036-show-every-option-and-fetch-models-offline.md)).

## How others do it

- **Placement and cost before the commit.** [GPUStack](https://docs.gpustack.ai/latest/user-guide/model-deployment-management/)
  "automatically computes resource requirements for the model instances from model metadata and
  schedules them to available workers", with an automatic mode (spread or binpack) and a manual GPU
  selector. It does not show a preview of the choice before deploying. The build plan asks for more:
  the recommendation in words and what it takes, before the commit.
- **Rollouts.** A [Kubernetes Deployment](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/)
  reports progress per replica (ready, up to date, available), a progress deadline, and rolls back to a
  recorded revision. [KServe](https://kserve.github.io/website/docs/model-serving/predictive-inference/rollout-strategies/canary)
  splits traffic between the last good revision and the new one by `canaryTrafficPercent`, and a
  rollback pins all traffic to the previous revision. Both need replicas and revisions the server lacks
  today; they arrive with SRV-105 and SRV-113, and SRV-207 (canary) was added by this review.
- **Traffic.** [vLLM's metrics](https://docs.vllm.ai/en/latest/usage/metrics.html) give request
  latency (`vllm:e2e_request_latency_seconds`, time to first token), throughput
  (`vllm:prompt_tokens`, `vllm:generation_tokens`) and load (`vllm:num_requests_running`,
  `vllm:num_requests_waiting`). The Traffic tab and the list's requests and p95 columns read these once
  OBS-101 collects them; until then they say so.
- **Sharing a GPU.** NVIDIA's [time-slicing](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/gpu-sharing.html)
  gives "no memory or fault-isolation between replicas", and MIG does. The approved recipe refuses MIG
  and takes a whole GPU; GPU-106 owns sharing, so the deploy flow states a whole GPU is taken.

## Licences

No new dependency. The pages use the console's existing React, TanStack and Lucide packages.

## The placement preview

A new read, `GET .../admin/deployments/placement?model_id=`, answers where a model can be deployed
now. It reads the nodes of the model's workspace that most recently reported their GPUs (at most one
lookup page, 100 by default), and judges every GPU with **the same functions the create path runs**,
in the same order: node facts, freshness, the probe's own error, the node already serving, then each
GPU against the recipe. It recommends the eligible GPU with the most free memory, the most recently
observed node winning a tie. It reserves nothing; the create checks again.

- **Rejected: judging in the browser.** The page would copy the recipe's thresholds and drift from
  the server the first time either changed. Refactoring the preflight into pure checks shared by both
  paths makes drift impossible, and an integration test proves that what the preview recommends the
  create accepts, and what it refuses the create refuses with the same code.
- **Rejected: only a node picker.** It hides why a GPU cannot be used until after the commit, which is
  what the build plan rules out.
- **Bound.** The query sorts the workspace's observed nodes by time without an index. The table holds
  one row per GPU node, so that is acceptable at one workspace's size; the preview names the bound and
  a node outside it can still be deployed to by name.

## The pages

- **Routes.** `/services` (S-204), `/services/new` (S-203), `/services/$deploymentId` (S-205). The
  navigation's *Model services* opens the list; `d` opens the flow.
- **Scope.** Deployments belong to a workspace and follow the workspace switcher, as models do.
- **Names.** A deployment has no name yet (SRV-112), so it is called by its model on its node.
- **Tiles.** Healthy (running, ready, fresh), degraded (failed, unknown, stale, or holding a GPU that
  needs recovery), changing (a task is pending or running, or loading), stopped (stopped or not
  installed). Each filters the list, and the filter lives in the address.
- **Deploy flow.** Choose a model, read the recommendation in words, open the advanced fold to choose
  another node or GPU; engine, replicas, autoscaling, read-only root and egress are shown there as not
  ready with the item that brings them. The cost is stated before the commit: the GPU taken whole, its
  memory, and how many eligible GPUs remain. When nothing is eligible, each node's reason is shown,
  and a stale or missing observation offers to read the node's GPUs again. Deploying opens the
  deployment's page, where the task's progress shows and survives leaving the page.
- **Stop and remove** confirm by typing the model's name, and name the gateway aliases that send
  traffic to the deployment and the keys bound to them, which stop getting answers. Start, check and
  verify are reversible and act at once.
- **Detail.** Overview (the one replica, its node and GPU, health, the last task), Traffic and Logs
  (not ready), Configuration (model, recipe, image, revision), Activity. Scale and Roll back are shown
  and not ready.

## Failure modes and how the tests cover them

| Failure | Coverage |
| --- | --- |
| The preview and the create disagree | integration: recommendation accepted, every refusal code equal |
| A node's observation is stale or missing | integration: `INFERENCE_PREFLIGHT_REQUIRED`; browser flow: reading the GPUs again |
| A node already serves a deployment | integration: `INFERENCE_RESOURCE_BUSY` on the node, nothing recommended |
| A node of another workspace | integration: never offered |
| A non-administrator or the portal | integration and TLS: refused, 404 from the portal |
| Two people act on the same deployment | browser flow: `VERSION_CONFLICT` explained, the page reloads the revision |
| The create's answer is lost | browser flow: the same idempotency key on retry, one deployment |
| No model in the workspace | browser flow: sent to the Model library |
| An agent reports a failure code | browser flow: the code explained in both languages |

## What done means

- From the console, with the network disconnected from the internet, a person opens Model services,
  starts the deploy flow from a model, sees the recommendation and its cost or every reason it cannot
  run, deploys, follows the task, and stops and removes the deployment, typing its name.
- On an installation without an NVIDIA GPU (lane B's test install), the same run reaches the flow's
  refusal, explained, which is as far as that environment allows.
