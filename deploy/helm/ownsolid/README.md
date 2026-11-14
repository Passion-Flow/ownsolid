# Offline Helm installation

This chart runs one ownsolid control writer on a prepared Linux amd64
Kubernetes 1.36 cluster. It uses the existing single-node control runtime
and its independent PostgreSQL operator commands. Use Helm 4.3.1 from
verified local media.

## Prepare the site

- Provide a private Traefik entry point on TCP 443, its
  `traefik.io/v1alpha1` `IngressRouteTCP` CRD and an IngressClass selected by
  the site's Traefik provider. Point two distinct private DNS names at the
  entry point. Configure that listener and its private address in the
  cluster's site configuration. The chart routes the two exact SNI names
  and Control terminates TLS. ADR 0051's managed RKE2 server disables
  ingress by default. The cluster operator owns ingress enablement.
- Use the signed site Registry installation and give every eligible RKE2
  node the site Registry CA. Use the explicit private endpoint and the
  RKE2 private registry settings, including fallback denial where mirrors
  are configured. The node needs access to that endpoint before installing
  the chart.
- Prepare PostgreSQL with pgvector 0.8.7 and the schema for the selected
  release. Run the existing `migrate`, `prepare-runtime-access` and
  `bootstrap-admin-file` operator commands using protected operator files
  and the signed Control executable. Follow the bootstrap recovery
  procedure if its first credential output already exists. Serve with the
  restricted database role. The Chart manages the Control Deployment.
  It leaves SQL migration and administrative credentials with the operator.
- Provide an existing model PVC in the release namespace. In the volume
  root, prepare a `models` directory owned by the Control UID/GID with
  mode 0700. For Kubernetes' standard fsGroup handling, the parent volume
  root must have the Control GID and mode 2770. Use a volume driver that
  preserves the private subdirectory when the Pod mounts the claim. The
  init process verifies that subdirectory before serving. Reserve the claim
  for this installation and keep it across upgrades.
- Provide a TLS-enabled SMTP relay within the site.

The runtime Secret contains these file keys. Use an immutable Secret or a
new name when changing the contents so the Deployment takes a new snapshot.

| Key | File contents |
| --- | --- |
| `runtime-dsn` | PostgreSQL connection URI for the restricted runtime role with `sslmode=verify-full` and `sslrootcert=/run/ownsolid/private/postgres-ca.pem` |
| `postgres-ca.pem` | Site trust for the PostgreSQL certificate |
| `auth-hmac` | The installation's existing authentication key |
| `mail-keyring.json` | The installation's existing mail keyring |
| `license-trust.json` | Public ownsolid license verification trust |
| `browser-ca.pem` | Site trust for Control's HTTPS health probe |
| `smtp-ca.pem` | Site trust for the SMTP relay |
| `smtp-password` | Relay password file when `smtp.username` is configured |

Create the named TLS Secret from a certificate chain covering both DNS
names and its matching private key. Create the pull Secret from a protected
Docker authentication file for the same Registry authority. Create both
in the release namespace.

```sh
kubectl -n "$NAMESPACE" create secret generic "$PULL_SECRET" \\
  --type=kubernetes.io/dockerconfigjson --from-file=.dockerconfigjson="$REGISTRY_CREDENTIAL_FILE"
kubectl -n "$NAMESPACE" create secret generic "$RUNTIME_SECRET" \\
  --from-file=runtime-dsn="$RUNTIME_DSN" --from-file=postgres-ca.pem="$POSTGRES_CA" \\
  --from-file=auth-hmac="$AUTH_HMAC" --from-file=mail-keyring.json="$MAIL_KEYRING" \\
  --from-file=license-trust.json="$LICENSE_TRUST" --from-file=browser-ca.pem="$BROWSER_CA" \\
  --from-file=smtp-ca.pem="$SMTP_CA"
kubectl -n "$NAMESPACE" create secret tls "$TLS_SECRET" --cert "$TLS_CHAIN" --key "$TLS_KEY"
```

Use the same names in `registry.pullSecret`, `control.runtimeSecret` and
`ingress.tlsSecret`. For an authenticated SMTP relay, also supply the
`smtp-password` key from its file and configure `smtp.username`. Do not
put any of these file contents in Helm values. The initial placement uses
a namespace-local pull credential, no workload API token and one private
runtime file snapshot per Pod.

## Install the signed release

Package the local chart during release manufacture and carry the package
as a signed `chart` asset with its notice and release SPDX entries. On the
site host, use `prepare-bundle` to obtain the verified chart package on
the mounted release volume.

Create a site values file based on [values.yaml](values.yaml). Supply site
references, DNS names and resources there. Generate the image overlay from
the same independently verified archive used to seed the Registry.

```sh
ownsolid-install helm-images --bundle "$RELEASE_ARCHIVE" --verification "$VERIFICATION" \\
  --registry "$REGISTRY_AUTHORITY" > "$VERIFIED_IMAGE_VALUES"

helm upgrade --install "$RELEASE" "$VERIFIED_CHART_PACKAGE" --namespace "$NAMESPACE" \\
  --values "$SITE_VALUES" --values "$VERIFIED_IMAGE_VALUES" --wait --timeout "$INSTALL_TIMEOUT"
```

The chart joins the private Registry authority to the fixed
`ownsolid/release/control-image` repository and the signed platform manifest
digest. It uses that same immutable reference for the init and serving
containers. The chart configures direct HTTPS on port 8443 inside the Pod.
Traefik forwards TLS records on port 443 based on the two SNI names. Clients
trust the site's browser CA and reach Admin and Portal at the two exact
configured names. With TCP passthrough Control sees the proxy's peer
address. This profile does not configure forwarded client addresses.

If the Pod is waiting for its image, inspect its events for Registry trust
or credentials. If it stays in init, check the Secret keys and the model
PVC directory permissions. After supplying corrected file inputs, update
the referenced Secret names in values and rerun Helm. Reconcile database
schema and runtime grants through the existing operator commands before
changing the Control image across schema versions.

## Verify without starting a cluster

The [Linux render drill](../test-render-egress.sh) tests Helm lint, the packaged
chart, failure cases, the non-root projected-file handoff and the native
Control certificate checks with only an egress recorder route. Run it with a
fresh private directory, verified Helm and Linux amd64 Control and installer
test binaries:

```sh
bash deploy/helm/test-render-egress.sh "$PRIVATE_WORK_DIR" "$HELM" "$CONTROL" \\
  "$INSTALLER_TEST" "$EGRESS_RECORDER"
```

This drill prepares the native runtime configuration. The cluster drill
must additionally install from the actual Registry on a cold RKE2 worker,
check both entrances over site TLS and verify persisted data after Pod
replacement.
