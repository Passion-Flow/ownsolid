# Cell scheduler adapters

SCH-107 installs Kueue and Volcano in each cell. The first adapter boundary
reads observations without giving either scheduler authority over the ownsolid
quota tree. [Kueue 0.20 installation](https://kueue.sigs.k8s.io/v0.20/docs/getting-started/installation/)
supports the Kubernetes version of the current RKE2 cell. Its
[v1beta2 Workload API](https://kueue.sigs.k8s.io/docs/reference/kueue.v1beta2/#workloadstatus)
distinguishes Admitted, PodsReady and Finished. The
[Volcano Job API](https://github.com/volcano-sh/apis/blob/master/pkg/apis/batch/v1alpha1/job.go)
and [Job status guide](https://volcano.sh/docs/concepts/volcanojob/#status)
distinguish pending, running and lifecycle phases. Volcano Running meets
minAvailable, so the read model must not use it alone as gang readiness.
[Kubernetes API concepts](https://kubernetes.io/docs/reference/using-api/api-concepts/)
describe UID identity and the semantics of an object's resourceVersion.

Read namespaced objects by the resource's original UID from an authenticated
cell API. Dial one operator-configured private or loopback address and verify
the explicit TLS name against the cell CA. Scope the service account to get
only the relevant scheduler objects in the allowed namespaces. Use the existing
offline bundle reader for protected configuration and credentials. Never
follow redirects, accept a new UID under the same name or treat a failed read
as evidence of termination. Opaque resource versions are for identity and
reconciliation, never clock or arithmetic ordering. The adapter returns a
scheduler fact separately from runtime state and does not release quota.

Watching and writing objects require a durable reconciliation cursor and
transactional ownership identity. Comparing only names risks applying an old
attempt's state to a new job. Adding the Kubernetes client suite solely for
two reads adds a dependency tree and does not simplify the identity checks.
Pin installer manifests, images and CRD versions as part of the offline
install flow. Use one scheduler owner per workload so independent controllers
do not both govern the same pods.

The adapter uses Go's [BSD-3-Clause standard library](https://go.dev/LICENSE)
and the existing ownsolid bundle code. Kubernetes, Kueue and Volcano are
Apache-2.0, compatible with commercial offline redistribution when required
notices are packaged. The adapter adds no model, dataset or runtime download.

Test real TLS transport, untrusted CA, redirects, forbidden requests, replaced
UIDs, terminating objects, stale conditions and the two schedulers' distinct
readiness and terminal states. Full acceptance needs signed offline installation
of both controllers, real admission and cancellation through the proper owner,
and egress-isolated tests against RKE2.

## Rotating the protected bearer file

Kubernetes issues time-bound ServiceAccount tokens and expects clients to
reload the rotated credentials. The
[ServiceAccount guide](https://kubernetes.io/docs/tasks/configure-pod-container/configure-service-account/#launch-a-pod-using-service-account-token-projection)
describes kubelet renewal. The upstream
[client-go transport](https://github.com/kubernetes/client-go/blob/master/transport/token_source.go)
periodically reloads its file and invalidates its cache on an authorization
failure.

This adapter uses one operator-owned protected file on the Control host.
Validate it at startup and reload the small file before every bounded
scheduler observation through the existing protected reader. Credential
provisioning supplies a replacement regular file by atomic rename in the
same directory. Keep the TLS transport and authenticated destination
unchanged across rotation. If the path becomes unreadable, malformed,
insecure, or changes while it is read, fail locally and retry a later
observation after the file is valid. Avoid a background cache or fallback
credential. At the current observation boundary they add timing state
and can keep sending a revoked token after a file is withdrawn.

Test real loopback TLS with a server that accepts only the current token.
Rotate the protected file without recreating the adapter. Reject an
insecure replacement before making a network request and recover when
the operator installs the next valid file. The installation owner still
provisions a least-privilege ServiceAccount and supplies its rotating
credential. The existing protected-file contract requires a regular
file, so direct kubelet projected symlinks need a separately verified
installation path. client-go is an Apache-2.0 design reference. The
implementation uses the existing standard library and bundle reader,
with no new dependency or license obligations.

## Offline controller installation and upgrade boundary

The [Kueue 0.20 installation guide](https://kueue.sigs.k8s.io/v0.20/docs/getting-started/installation/)
currently names release 0.20.5 and recommends Kubernetes 1.34 or newer. It uses
server-side apply and waits for controller availability. Its internal webhook
certificate management avoids requiring an additional certificate controller.
The guide warns that unscoped management of Jobs can intercept distribution
maintenance Jobs. Select only ownsolid workload namespaces and supported
frameworks; leave RKE2 and other system workloads outside both scheduler owners.

[Volcano installation](https://volcano.sh/docs/gettingstarted/installation/)
includes YAML and Helm paths, but its generic Kubernetes minimum does not prove
compatibility with the selected RKE2 release. The immutable
[1.16.0 chart](https://github.com/volcano-sh/volcano/blob/v1.16.0/installer/helm/chart/volcano/Chart.yaml)
contains a local jobflow subchart. Its
[values](https://github.com/volcano-sh/volcano/blob/v1.16.0/installer/helm/chart/volcano/values.yaml)
select a public registry and Always pull policy. The source-tag
[Kueue chart values](https://github.com/kubernetes-sigs/kueue/blob/v0.20.5/charts/kueue/values.yaml)
also contain development image defaults. Neither unrendered source chart is an
approved offline payload. These are candidate references, not installed or
qualified ownsolid controller versions.

Prepare exact release artifacts outside the installation, render them against
the selected Kubernetes version and inventory every image, init container, hook,
CRD and chart dependency. Pin image digests and verify all bytes through the
existing signed offline delivery mechanism. Resolve images only from preloaded
node content or an explicitly configured intranet registry. Installation must
not fetch manifests, charts, dependencies, public registry layers or certificates.
[RKE2 air-gap guidance](https://docs.rke2.io/install/airgap) distinguishes image
preloading from private registry delivery; scheduler images must reach the cell's
containerd, not merely the development host's Docker cache.

Kueue [0.20.5](https://github.com/kubernetes-sigs/kueue/blob/v0.20.5/LICENSE) and
Volcano [1.16.0](https://github.com/volcano-sh/volcano/blob/v1.16.0/LICENSE) use
Apache-2.0, allowing commercial modification and offline redistribution with its
license, notice and modification obligations. That does not establish every
image layer or subchart's license. The exact delivered inventory needs its own
license/notices review. This research adds no dependency, model or dataset and
copies no upstream code.

Reuse the registered cell cluster and its authenticated lifecycle owner. Keep
installation authority separate from namespaced observation credentials. Persist
an idempotent desired installation revision and each applied object's identity;
retain unknown outcomes for reconciliation. CRD acceptance, webhook readiness,
controller rollout and effective RBAC are separate checks. A Deployment becoming
available does not prove workload admission or an offline image restart. Do not
uninstall CRDs or delete existing workloads as automatic rollback. Upgrades need
release-note review, served/stored API compatibility and a recovery path before
changing persisted controller objects.

Acceptance must use the selected RKE2 version with both controllers installed:
admit and cancel one real workload through each owner; refuse unauthorized and
foreign namespace access; reconcile a replaced UID; restart controllers with no
external route and an empty Pod image cache; retry a partial installation and
an uncertain apply; verify webhooks do not intercept maintenance workloads.
Exercise incompatible CRDs, missing images, certificate rotation and failed
upgrades without dropping active workloads or releasing logical holds.
Production installation tasks, registered-cluster credentials and these offline
runs remain open. The observation reader and this research do not close SCH-107.
