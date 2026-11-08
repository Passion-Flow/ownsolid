# NVIDIA GPU telemetry

GPU-101 needs a passive view of each physical NVIDIA GPU for operators and
historical reporting. This view provides observations for later incident
handling. It does not determine device eligibility.

## Module review

[NVIDIA GPU Operator](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/25.3.2/troubleshooting.html)
separates exporter telemetry from device-plugin health and device allocation.
[Kubernetes device plugins](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/)
report unhealthy devices to kubelet, while
[NVIDIA DRA](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/25.10/dra-gpus.html)
reports allocation and device health through its own driver. These map to GPU-101
through GPU-108, with qualification tracked by GPU-109. NVIDIA also exposes
[GPU/NVSwitch topology, link state and generation-specific errors](https://docs.nvidia.com/datacenter/dcgm/latest/learn/core-services/topology-and-links.html).
Per-GPU rollups and inter-node fabric monitoring do not identify the failing
switch or link, so GPU-209 covers that missing intra-node operation.

## GPU-101 design

Use [DCGM Exporter 4.9.0 with DCGM 4.7.0](https://github.com/NVIDIA/dcgm-exporter/releases/tag/4.9.0)
as the initial source contract. Use the canonical names and SI units from its
[pinned collector list](https://github.com/NVIDIA/dcgm-exporter/blob/4.9.0/etc/default-counters.csv).
The exporter supplies the driver-facing integration. The agent can read one
bounded Prometheus text response from an explicitly configured numeric
loopback address, without following redirects or using a proxy. Use physical GPU
UUIDs, since enumeration indexes can change across reboots. The expected
exporter configuration monitors physical GPUs without workload label enrichment.
Reject ambiguous observations and exclude MIG instance, per-link and
per-workload samples from the physical GPU snapshot.

Represent missing data by an absent sample. Preserve a measured zero. The
[DCGM header](https://github.com/NVIDIA/DCGM/blob/master/dcgmlib/dcgm_structs.h)
documents numeric sentinels for unavailable and unsupported fields.
Unavailable fields, absent GPUs and scrape failures are distinct from health
incidents. Counters are cumulative readings, and consumers must account for
resets before computing rates. The exporter release notes identify changes in
units from 4.8.x, so this slice accepts the pinned canonical names only.

Direct libdcgm bindings would add a native library dependency to the agent
binary. Parsing human-oriented dcgmi output would couple the agent to a CLI
format. The existing nvidia-smi inference probe serves workload preflight and
does not supply the required DCGM observations. The text parser already used
by the node-exporter collector provides the same
[Prometheus format](https://prometheus.io/docs/instrumenting/exposition_formats/).
The new collector reuses that dependency without importing the component manager.

## Licences and delivery

Existing Go dependencies [prometheus/common](https://github.com/prometheus/common/blob/v0.72.1/LICENSE)
and [prometheus/client_model](https://github.com/prometheus/client_model/blob/v0.6.3/LICENSE)
are Apache-2.0, as are the source projects
[DCGM](https://github.com/NVIDIA/DCGM/blob/master/LICENSE) and
[DCGM Exporter](https://github.com/NVIDIA/dcgm-exporter/blob/4.9.0/LICENSE).
Apache-2.0 permits commercial and offline use and redistribution with its
licence and notice obligations. This collector does not redistribute any
NVIDIA artifacts. Before bundling a particular binary or image, the delivery
owner must pin its digest and verify its notices and the separate terms of
included NVIDIA components. No new Go dependency, model or dataset is used.

## Verification and completion

The local transport test should exercise the real HTTP client against an
isolated loopback fixture, including proxy and redirect refusal, timeouts and
bounded responses. Parser cases must include multiple GPUs, true zeros, missing
and unsupported values, invalid counters, duplicate physical observations,
mixed physical/MIG/workload series, and incompatible exposition. No case
should turn missing or malformed samples into health data.

End-to-end acceptance also needs the architecture-owned runtime and wire
integration, telemetry storage and operator views, and the delivery-owned
offline exporter configuration. On supported real NVIDIA hardware, verify the
metrics and identifiers against the pinned exporter, with ECC and NVLink
availability recorded per model. A CPU-only protocol test cannot prove that
qualification.
