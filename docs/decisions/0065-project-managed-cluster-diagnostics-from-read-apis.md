# Project managed cluster diagnostics from read APIs

The installer support archive can collect a managed cell through a separate
protected cluster configuration. The Control Admin API contributes recorded
cluster and member state. The Kubernetes API contributes current state for
the two named Control resources from the Helm chart. Each source can
succeed independently of the other.

Use explicit TLS trust and credentials confined to their respective origins.
Grant Kubernetes GET on the named Deployment and Service. Do not derive
readiness from Helm release history or use kubeconfig authentication
plugins. Keep fixed outcomes and counts in the archive. Response contents
that identify a site or expose configuration stay out of the projection.

The existing Compose archive and its inputs remain compatible. See
[research and verification](../research/support-diagnostics.md#managed-cluster-and-helm-diagnostics).
