# Scheduling topology sources and producer contract

SCH-106 keeps inventory and admission in ownsolid, with Kueue and Volcano
as cell placement engines. This note defines missing producer inputs.
It does not establish an installed topology capability.

## Proven upstream boundaries

[Kueue TAS](https://kueue.sigs.k8s.io/v0.20/docs/concepts/topology_aware_scheduling/)
uses hierarchical node labels and required or preferred placement. Labels
can project an authenticated rack inventory but do not prove GPU link health.

[Volcano network topology](https://volcano.sh/docs/userguide/user_guide_how_to_use_network_topology_aware_scheduling/)
uses HyperNode domains, hard or soft constraints and discovery providers.
Its projection still needs an actual fabric provider or authorized inventory.

[NVML](https://developer.nvidia.com/management-library-nvml) is the local
NVIDIA device API. [NVLink methods](https://docs.nvidia.com/deploy/nvml-api/620/api/group__NvLink.html)
provide state, remote PCI information and endpoint type, with unsupported
and device-lost results. Use APIs available in the delivered driver; the
documentation version is not a driver upgrade. The
[official Go binding](https://github.com/NVIDIA/go-nvml) loads the local
driver library and resolves versioned symbols.

The [Linux PCI ABI](https://github.com/torvalds/linux/blob/master/Documentation/ABI/testing/sysfs-bus-pci)
provides NUMA affinity; -1 means unknown. NVIDIA warns that
[nvidia-smi output](https://docs.nvidia.com/deploy/nvidia-smi/index.html)
can change across releases and recommends NVML for maintained tooling.

## Current source and proposed fields

The installed `api/agent/v1/inference.go:NVIDIAGPU` carries UUID, name,
driver, memory, compute capability, MIG mode and observation time.
`nodes.ObservedInventory` binds those observations to server-owned node,
tenant, Workspace, site and cell identities. Neither carries PCI, NUMA,
NVLink or fabric topology.

The existing DCGM health collector validates PCI identity labels and emits
per-GPU NVLink counters; see [GPU telemetry](gpu-telemetry.md).
These observations do not identify peer endpoints or the fabric graph.
Reuse measured identities when the owner exposes them through its scoped
inventory contract rather than treating counters as connectivity.

ARC and the hardware owner must extend that authenticated source, retaining
existing node credentials and observation validation. The following table
is a proposed integration contract, not a new wire type.

| Input | Required meaning and source |
| --- | --- |
| Server binding | Registered node, site/cell and cluster identity, checked by the existing owner rather than trusted from report fields |
| Observation identity | Owner revision, collection interval and server acceptance time; distinguish stale, reordered and mixed-incarnation reports |
| Device identity | Existing physical GPU UUID and normalized PCI domain, bus, device and function; preserve parent/partition distinction |
| Local affinity | Optional NUMA node and PCI ancestor identities from the local device tree; unknown remains absent |
| NVLink edge | Local GPU/link index, remote identity/type, observed state and per-field result; unsupported queries and unresolved switches are not healthy edges |
| HCA attachment | Stable adapter/port identity and PCI attachment from the hardware provider, with unavailable status preserved |
| Fabric inventory | Authorized rail, leaf and spine identities, ports and connectivity from the fabric provider or operator import, retaining source and revision |
| Projection binding | Exact cell controller object UID and source revision; replacement cannot acknowledge an old placement |

GPU-local facts cannot identify a remote leaf or spine. Hostname prefixes,
GPU ordinals and equal labels cannot supply missing connectivity.
Multiple rails require explicit edges rather than one invented parent
chain. These field requirements are ownsolid's inference from the upstream
boundaries above.

## Approach and rejected alternatives

Use typed NVML observations and read-only PCI affinity. Keep device and
fabric sources distinct until their authenticated identities can be joined.
Preserve unknowns and failures per field. Refuse a required constraint that
the pinned controller cannot represent. Preferred fallback needs an explicit
owner policy.

The hardware owner must first qualify a pinned probe outside product code,
including missing-library and unsupported-query behavior. Qualify the Go
binding's CGO and dynamic-loader packaging against static Agent delivery
before selecting a dependency. This lane does not change Agent build flags.

Reject a free-form CLI matrix as the primary format, guessed fabric links,
synthetic production graphs and a hierarchy-only model that discards device
links or rails. A separate placement scheduler would duplicate the agreed
Kueue/Volcano boundary.

## Licensing and offline delivery

This note adds no dependency, model or dataset.

| Candidate or reference | Commercial use, redistribution and offline boundary |
| --- | --- |
| [Kueue](https://github.com/kubernetes-sigs/kueue/blob/main/LICENSE), [Volcano](https://github.com/volcano-sh/volcano/blob/master/LICENSE) | Apache-2.0 permits commercial use and redistribution with its notices and conditions. Offline delivery must include pinned controllers, manifests and dependencies. |
| [go-nvml](https://github.com/NVIDIA/go-nvml/blob/main/LICENSE) | Apache-2.0 permits commercial use and redistribution with its notices and conditions. Its local driver binary has separate terms. No binding is selected here. |
| NVIDIA driver/NVML binary | The exact component license is a release-owner prerequisite. NVIDIA publishes [component manifests and licenses](https://docs.nvidia.com/datacenter/tesla/driver-installation-guide/tarballs-and-zip-archive-deliverables.html) and requires checking each component before redistribution. Binding licensing does not establish binary redistribution rights. |
| Linux PCI ABI | Read the installed OS interface; no kernel source or binary is vendored by this proposal. |
| Existing Go | The BSD-3-Clause toolchain remains unchanged. No runtime download is added. |

The finished collector must run with external networking unavailable and
approved libraries already installed, without online driver or controller
lookup.

## Acceptance before SCH-106 completes

- The real authenticated producer persists one scoped observation, survives
  restart and refuses foreign nodes/cells and replaced sources.
- Tests distinguish unknown NUMA, unsupported NVLink, lost devices, partial
  reads, unresolved endpoints and an explicitly down link.
- Reordered reports, reboot/hotplug and mixed collection intervals cannot
  overwrite a newer coherent graph or manufacture connectivity.
- Installed Kueue and Volcano enforce required constraints and configured
  preferred fallback, including controller replacement and topology changes
  while work is queued.
- The main UI flow shows absent, failed and stale observations under current
  permissions and runs disconnected with the qualified probe and signed assets.

No probe, hardware observation, controller installation or user flow has
been run by this note. Producer and packaging qualification remain required
before authoritative topology implementation.
