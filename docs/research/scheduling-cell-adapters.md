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
