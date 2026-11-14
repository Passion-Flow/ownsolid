# Run Helm control with direct site TLS

Package the current single-writer Control runtime in a Helm chart. Keep
PostgreSQL, database preparation, administrator bootstrap and the model
claim under their existing site and operator ownership. Use one Recreate
Deployment with the control runtime's existing database installation
lease.

Use the same signed OCI platform manifest digest and site Registry repository
for both the init and serving images. The installer supplies a values
overlay from the independently verified bundle. The chart keeps site
Secret references in values and stages their file contents at startup
through an unprivileged init process in the same image. Control keeps
its existing private file validation.

Route the Admin and Portal names using site Traefik TCP ingress with TLS
passthrough. The existing direct HTTPS runtime validates both names and
owns TLS and browser origins. The cluster owner supplies Traefik and
node Registry trust. The Helm profile keeps the current supported
`single-node` topology setting. Managed cell rollout and cold worker
pulls remain separate installed verification.

See [DEL-107 research](../research/offline-control-helm.md) and
[chart operator instructions](../../deploy/helm/ownsolid/README.md).
