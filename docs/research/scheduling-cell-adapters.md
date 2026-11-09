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
