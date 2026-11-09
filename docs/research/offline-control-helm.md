# Install control with Helm from the signed site Registry (DEL-107)

The current Compose installer configures the admitted `single-node` control
topology. The [versioned topology contract](../../api/configuration/v1/control-topology/schema.json)
also names larger sizes, and the control binary refuses them until their
capabilities are ready. Start with one control writer on a prepared Linux amd64
RKE2 cluster. Use a site PostgreSQL installation already migrated and
bootstrapped with the existing control operator commands, a persistent model
claim and a site SMTP relay. Do not run migrations or issue administrator
credentials as a side effect of Helm apply.

## Upstream and the existing contracts

- [Helm charts](https://helm.sh/docs/topics/charts/) apply the local
  `values.schema.json` to merged values for lint, template and install. A
  local chart with no dependencies and a schema with only local references
  needs no chart repository or schema server. [Helm 4.3.1](https://github.com/helm/helm/releases/tag/v4.3.1)
  supports the pinned Kubernetes minor under the [version policy](https://helm.sh/docs/topics/version_skew/).
- [Kubernetes image identity](https://kubernetes.io/docs/concepts/containers/images/)
  distinguishes image digests from mutable tags. Its image pull Secret
  reference is scoped to the workload namespace. [RKE2 registry configuration](https://docs.rke2.io/install/private_registry)
  keeps private CA trust on the nodes. Use the registry authority explicitly
  so resolution never starts from a public image name. The [signed Registry](signed-registry-bootstrap.md)
  already defines `ownsolid/release/<asset-id>@sha256:...`. Use the manifest
  identity, not the image config hash or a Docker alias.
- [Kubernetes projected volumes](https://kubernetes.io/docs/concepts/storage/projected-volumes/)
  allow a named ConfigMap and Secrets to share a volume. The
  [AtomicWriter](https://github.com/kubernetes/kubernetes/blob/v1.36.6/pkg/volume/util/atomic_writer.go)
  publishes a generation through `..data` and uses symlinks for visible
  files. The actual control configuration and TLS readers require
  owner-only regular files, and the protected configuration reader also
  requires its directory to be private. Use a non-root init process in the
  same signed control image to read one projected generation and install
  the allowlisted files as mode 0600 into a private memory volume. The main
  process gets a read-only mount of that volume. Preserve the control
  readers and do not enable alpha projected-file ownership features.
- [Kubernetes Ingress TLS](https://kubernetes.io/docs/concepts/services-networking/ingress/#tls)
  describes termination at the ingress. [Traefik's TCP IngressRoute](https://doc.traefik.io/traefik/reference/routing-configuration/kubernetes/crd/tcp/ingressroutetcp/)
  permits exact SNI matching with TLS passthrough. Keep control's
  [direct HTTPS transport](../../api/configuration/v1/control-serving-expectation/schema.json).
  The site certificate covers the two distinct Admin and Portal DNS
  names. Neither an ingress-issued certificate nor a forwarded-header
  trust exception is needed. RKE2 supplies Traefik in its
  [offline core archive](https://docs.rke2.io/release-notes/v1.36.X).
  ADR 0051's managed server currently disables ingress. The chart consumes
  an existing site-private Traefik entry point and CRD. The cluster owner
  controls ingress enablement and credentials.

## Licences and custody

The Helm binary is [Apache-2.0](https://github.com/helm/helm/blob/v4.3.1/LICENSE),
Kubernetes is [Apache-2.0](https://github.com/kubernetes/kubernetes/blob/v1.36.6/LICENSE),
and Traefik Proxy is [MIT](https://github.com/traefik/traefik/blob/v3.7.16/LICENSE.md).
PostgreSQL uses the [PostgreSQL License](https://www.postgresql.org/about/licence/)
and pgvector also uses the
[PostgreSQL License](https://github.com/pgvector/pgvector/blob/v0.8.7/LICENSE).
The init command uses the existing signed control image's Debian `/bin/sh`
([dash, BSD-style](https://salsa.debian.org/debian/dash/-/blob/debian/0.5.12-2/debian/copyright))
and `install` ([GNU coreutils, GPL-3.0-or-later](https://github.com/coreutils/coreutils/blob/v9.1/COPYING)).
This does not add a new Go module, container image, model or dataset.
These upstream licences permit commercial and offline use subject to their
terms. Redistributing the selected artefacts requires their actual notices
and source obligations, including GPL source correspondence for coreutils.
The production inventory remains DEL-110.

## Admission and failure checks

- The installer projects a Helm values overlay from independently verified
  release bytes. The chart takes the private IPv4 Registry authority and
  the signed control digest. It derives the same reference for init and
  serving. Reject a missing lock, tag, malformed digest, public registry,
  unknown value, missing Secret name and identical browser hosts.
- Kubernetes must refuse to create the serving container when a required
  Secret or key is absent. The init command rejects an empty input. The
  control binary checks the actual TLS key pair and both DNS SANs before
  startup, then readiness verifies the CA and admin hostname using the
  existing health probe. Check the projection, private file mode and
  wrong certificate on the native runtime.
- Use the existing PVC and PostgreSQL instead of coupling their lifecycle
  to a Helm release. A single `Recreate` Deployment and the database
  installation lease avoid concurrent writers. A working chart install
  must pull on a cold worker, reach both entrances with the site CA, and
  preserve application data after replacing the Pod. The harness must
  record and refuse any unexpected external IPv4, IPv6 or DNS traffic.
- Before that run, validate the packaged chart, bad inputs and rendered
  resource wiring with the exact Helm CLI in an isolated network.
  Rendering, cold Registry pulls from DEL-108 and actual Helm installation
  are separate evidence.
