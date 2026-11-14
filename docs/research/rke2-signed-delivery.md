# Signed RKE2 delivery (DEL-101, ARC-104)

The control artifact store and Agent already agree on one approved release in
[agent v1](../../api/agent/v1/kubernetes.go). The signed Compose profile has
an Ed25519 manifest, size and SHA-256 checks, notice references, an SPDX
inventory and protected preparation. Its image kind describes a single
OCI platform manifest and config, so it cannot describe an upstream RKE2
multi-image archive.

[RKE2 air-gap installation](https://docs.rke2.io/install/airgap) documents
transferring its architecture-specific binary tarball and keeping its
upstream image archives intact for node-local import. [K3s](https://docs.k3s.io/installation/airgap)
uses the same preload pattern. The RKE2 private registry alternative needs
another site service and node configuration. It remains ARC-116. The
[Kubernetes cell decision](../decisions/0051-kubernetes-cells-with-rke2.md)
already pins the three files and defines Agent ownership of node installation.

Reuse the current signed profile and add a reserved optional RKE2 group.
Reserve the inventory identities and paths and require a signed prerequisite
of the exact approved version. Admit only the three files pinned in the Agent
contract plus the original upstream checksum list, with the approved byte
counts and digests. A site preparing RKE2 opts into the existing verifier with
a policy that requires the group. Existing single-node packages can omit it.
Keep the byte-exact image sets separate from Docker's single-image import.
A separate envelope would add release trust and verification logic. Accepting
generic optional assets would allow a signed, incomplete group or a signer
pin inconsistent with what the Agent can install.

## Pinned upstream bytes

[RKE2 v1.36.6+rke2r1](https://github.com/rancher/rke2/releases/tag/v1.36.6%2Brke2r1)
publishes the [original amd64 checksums](https://github.com/rancher/rke2/releases/download/v1.36.6%2Brke2r1/sha256sum-amd64.txt).

| File | Bytes | SHA-256 |
| --- | ---: | --- |
| rke2.linux-amd64.tar.gz | 41496182 | e111ac6333ddb2b819d133a413914d4456dd2195a3d694865cb7acda5bf81ae4 |
| rke2-images-core.linux-amd64.tar.zst | 567518406 | 58c153e94944039fc05b8e23de9e8f489df049519e55dd33e5790bf5ae19a217 |
| rke2-images-canal.linux-amd64.tar.zst | 241211711 | 40a09eb8460e27e79dc13247517445c9d94461ef5078ef9e4d7fe1f95aced8b0 |
| sha256sum-amd64.txt | 3558 | 2a9ae019e47798ab98691f26b2b588a68ca2387cc6c3c3c50efd32b89ac21980 |

The upstream [core](https://github.com/rancher/rke2/releases/download/v1.36.6%2Brke2r1/rke2-images-core.linux-amd64.txt)
and [Canal](https://github.com/rancher/rke2/releases/download/v1.36.6%2Brke2r1/rke2-images-canal.linux-amd64.txt)
image lists also match the upstream checksum file. They are research inputs
for component accounting. The signed group preserves the original binary
tarball, image sets and checksum file.

## Licence and production boundary

The binary tarball carries the upstream [RKE2 Apache-2.0
license and notices](https://github.com/rancher/rke2/blob/v1.36.6%2Brke2r1/LICENSE)
at `share/rke2/LICENSE.txt`. The upstream checksums are release metadata
distributed by that project.

The image sets' top-level components are covered by the source projects'
licences:

| Image components | Upstream licence |
| --- | --- |
| RKE2 runtime and cloud provider | [Apache-2.0](https://github.com/rancher/rke2/blob/v1.36.6%2Brke2r1/LICENSE) |
| Kubernetes, pause and node-local DNS cache | [Apache-2.0 for Kubernetes](https://github.com/kubernetes/kubernetes/blob/v1.36.6/LICENSE) and [Apache-2.0 for Kubernetes DNS](https://github.com/kubernetes/dns/blob/master/LICENSE) |
| CoreDNS and etcd | [Apache-2.0 for CoreDNS](https://github.com/coredns/coredns/blob/v1.14.7/LICENSE) and [Apache-2.0 for etcd](https://github.com/etcd-io/etcd/blob/main/LICENSE) |
| Cluster Autoscaler, addon-resizer, metrics-server and snapshot controller | [Apache-2.0 for autoscaler and addon-resizer](https://github.com/kubernetes/autoscaler/blob/master/LICENSE), [Apache-2.0 for metrics-server](https://github.com/kubernetes-sigs/metrics-server/blob/master/LICENSE) and [Apache-2.0 for external-snapshotter](https://github.com/kubernetes-csi/external-snapshotter/blob/master/LICENSE) |
| Klipper Helm and Klipper LB | [Apache-2.0 for Klipper Helm](https://github.com/k3s-io/klipper-helm/blob/master/LICENSE) and [Apache-2.0 for Klipper LB](https://github.com/k3s-io/klipper-lb/blob/master/LICENSE) |
| Canal's Calico and Flannel | [Apache-2.0 for Calico](https://github.com/projectcalico/calico/blob/v3.32.2/LICENSE) and [Apache-2.0 for Flannel](https://github.com/flannel-io/flannel/blob/v0.28.10/LICENSE) |
| Traefik | [MIT](https://github.com/traefik/traefik/blob/v3.7/LICENSE.md) |

These licences permit commercial and offline use and redistribution under
their terms. Retain the full archive notices and record
each distributed image's components, base packages, licences and applicable
source obligations in the production SPDX and notice inventory. The
fixture verifies that the signed release binds the archive bytes and notice
references. It does not establish the production component-level inventory.
This path adds no library, model or dataset. KWOK and the Felix test AddOn
are scale test inputs outside the group.

## Failure and acceptance

- Signed inventories missing any group file, naming a different version,
  architecture or path, or overriding an upstream pin are refused.
- The site requirement refuses a valid single-node-only manifest. An
  unrelated trust root, changed asset bytes and a missing archive entry are
  refused before preparing an artifact directory.
- Exercise the actual installer with all four upstream files and an
  ephemeral test signer on Linux amd64 with no uplink and an egress probe.
  Compare the prepared output against the pinned checksums and run the
  packaged executable. Record any further runtime work separately.
- Document the control and Agent handoff with the minimum protected directory
  and key-file settings. Managed cell creation and the delayed Calico DNS
  check remain ARC-104 acceptance gates.
