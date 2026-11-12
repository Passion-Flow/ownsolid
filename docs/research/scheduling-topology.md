# Topology inventory and placement constraints

SCH-106 owns a trustworthy topology inventory and its admission constraints.
The fixed architecture keeps Kueue/Volcano behind cell adapters. Logical quota
is not proof of a physically compatible placement.

## Primary sources and their boundaries

[Kueue topology-aware scheduling](https://kueue.sigs.k8s.io/docs/concepts/topology_aware_scheduling/)
represents network domains through ordered node labels. A rack label need not be
globally unique: its parent path distinguishes the domain. Required and preferred
constraints have different admission semantics. A node label does not describe
device-local PCIe or NVLink connectivity.

[Volcano network topology](https://volcano.sh/docs/keyfeatures/networktopologyaware/)
uses a HyperNode domain tree and hard/soft job constraints. Its domain tree must
not contain cycles or multiple parents. Discovery can consume authoritative
fabric information rather than guessing from node names. A logical domain tree
is not an inventory of every physical network path.

[Kubernetes Topology Manager](https://kubernetes.io/docs/tasks/administer-cluster/topology-manager/)
coordinates NUMA hints on one node. Policy can reject a Pod even after scheduling;
a strict single-NUMA requirement cannot silently become best effort. This local
admission boundary differs from cross-node fabric placement.

[NVIDIA DCGM topology and links](https://docs.nvidia.com/datacenter/dcgm/latest/learn/core-services/topology-and-links.html)
separates known CPU/PCIe/NVLink relationships, current port state and active
diagnostics. Topology does not reserve hardware, certify bandwidth or prove link
health. The [topology API](https://docs.nvidia.com/datacenter/dcgm/latest/dcgm-api/dcgm-api-topology.html)
also requires callers to exclude devices already in use when selecting a group.

These references establish the inventory/adapter separation. They do not select
a new controller, driver or collector version. Exact controller features must be
qualified against the signed, pinned offline assets before installation.

## Chosen approach and rejected alternatives

Retain a versioned inventory bound to authenticated site/cell/node identity and
the registered cluster. Separate device-local relationships and physical fabric
links from derived scheduler domain trees. Use stable device identity and explicit
parent/endpoint references, not GPU array indices as global IDs. Preserve CPU
NUMA affinity, PCIe paths, NVLink relationships, rail membership and leaf/spine
connections with source, observation time, freshness and known/unknown status.

An inventory collector supplies hardware facts; a fabric source or authorized
operator supplies network domains. Administrative import must be validated,
versioned, permission checked and audited. Scope comes from server authority.
Collectors need bounded calls, verified TLS and protected credential files.

Translate only representable constraints into the installed adapter. Retain the
full physical graph: a multi-rail physical network can have valid alternate paths,
while the derived HyperNode tree must be acyclic. Reject an invalid projection
instead of deleting physical edges to invent a tree. Required constraints wait
with fixed guidance when data is missing, stale, unsupported or incompatible.
Preferred constraints may relax only as explicitly authorized by policy.

The workload owner revalidates the inventory/policy versions and actual physical
holds at dispatch. An observation or successful quota reservation does not assign
devices. Do not infer topology from hostnames, GPU model strings or total counts;
treat unknown links as healthy; equate link count with usable bandwidth; or mix
independent observations into an apparently atomic capacity sample. Arbitrary
client labels cannot replace an authorized inventory.

## Existing contracts and dependency boundary

The current agent Facts in api/agent/v1/protocol.go carries host totals and an
observation time, not the required per-device and fabric graph. The scheduling
celladapter Observation contains object version, state and blocker only. Neither
is a current topology inventory producer. This lane does not edit the Agent or
silently expand a shared protocol. The owning collectors and registered-cell
authority must provide the facts before a production inventory reader is built.

This research adds no dependency, model or dataset. Kueue and Volcano remain
[Apache-2.0](https://github.com/kubernetes-sigs/kueue/blob/main/LICENSE)
[references](https://github.com/volcano-sh/apis/blob/master/LICENSE).
[DCGM core](https://github.com/NVIDIA/DCGM/blob/master/LICENSE) is Apache-2.0;
that does not establish redistribution rights for a complete driver or binary
bundle. Verify each selected asset's exact license/notices, commercial
redistribution and air-gapped use before including it. No remote collector,
online lookup or automatic runtime download is introduced.

## Failure coverage and finished behavior

Test repeated local rack names under different parents, missing endpoints,
duplicate device IDs, invalid logical-tree cycles/multiple parents, valid physical
alternate paths, unsupported or down links, stale/future samples, node replacement,
foreign site/cell, concurrent inventory edits, revoked authority and hard versus
preferred constraints. Test every replica group's complete physical fit and owner
rollback; a partial gang placement must not publish success or release a hold.

The finished UI lets an authorized operator inspect/import current topology,
see the source and freshness, correct a rejected inventory and retry. A workload
user submits a topology constraint and receives the actual placed domain or fixed
waiting guidance without another tenant's inventory. State, audit, permissions,
English/Chinese, failure/retry and the installed offline cell flow are required.
Simulated graph tests and a controller condition alone cannot close SCH-106.
