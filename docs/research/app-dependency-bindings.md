# Application dependency bindings (CAT-102)

A release declares what it needs. The installer chooses resources visible in its
workspace and receives scoped access from the resource and credential owners.
The same declaration must work for local Helm and Compose workloads.

The [Open Service Broker API](https://github.com/cloudfoundry/servicebroker/blob/master/spec.md#binding)
separates provisioning from binding and recommends credentials revocable per
binding. The [Kubernetes Service Binding specification](https://servicebinding.io/spec/core/1.1.0/#workload-projection)
uses stable binding names and projects files into a workload.
[Kubernetes Secrets](https://kubernetes.io/docs/concepts/configuration/secret/)
and [Compose secrets](https://docs.docker.com/compose/how-tos/use-secrets/)
support read-only file delivery to selected containers.

Use explicit required slots keyed by lower-case identifiers, with closed kind
and protocol pairs. Model slots identify chat, embeddings or rerank. Database
slots identify PostgreSQL, MySQL or Redis. Storage slots identify S3-compatible
object storage. These are contract values. An installer exposes a provider only
when its owning service can resolve and grant that type in the isolated site.
Existing manifests can omit the dependency list.

The declaration carries no resource, endpoint, credential or file location.
Resolve selected resources in the authenticated workspace through their owners.
The credential owner must issue a reference bound to that exact resource,
dependency slot, tenant, workspace and application installation. Before
projection, check the entire set of resolved records against the declaration.
Return a complete ordered plan or an error, without partial allocation. Resolve
and recheck the plan against the owning records before mounting any credential.
The installer owns the mount roots, file permissions, per-container scope and
intranet endpoint policy.

Free-form URLs, credentials in environment variables and arbitrary host-file
paths allow package inputs to bypass resource selection or leak secrets.
Kubernetes-specific binding objects alone would leave Compose without the same
contract. Use internal references with canonical UUID spelling
([RFC 9562](https://www.rfc-editor.org/rfc/rfc9562.html)) and project files through
the engine's native mechanism during installation.

Test a local manifest with model, database and storage slots, validate its
bindings independent of map order, and refuse unknown, absent, cross-workspace,
cross-tenant, cross-installation, mistyped and reused references. The contract
has no network or secret-file reads. Full acceptance requires server-side
selection, durable reservation and lifecycle, successful file projection and a
disconnected app run through the console. Rotation and provider grants remain
owned by the corresponding services.

This increment uses only the [BSD-3-Clause Go standard library](https://go.dev/LICENSE).
The [Service Binding](https://github.com/servicebinding/spec/blob/main/LICENSE)
and [Open Service Broker](https://github.com/cloudfoundry/servicebroker/blob/master/LICENSE)
Apache-2.0 specifications are references. These licenses allow commercial and
offline use and redistribution subject to their notices. No broker, controller,
additional dependency, model or dataset ships with this increment.
