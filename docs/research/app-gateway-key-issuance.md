# Application gateway key issuance (CAT-103)

The signed application release identifies a model slot and its protocol.
The installer selects a workspace model alias and obtains an
installation-scoped reference from the gateway and secret owners.

Kubernetes uses workload service accounts and bound credentials to scope
authority to an application identity. Open Service Broker separates the
service binding from resource provisioning and supports revocation per
binding. These patterns keep the installation's credential lifecycle with
the authority that issues and admits it.
[Kubernetes service accounts](https://kubernetes.io/docs/concepts/security/service-accounts/),
[Open Service Broker bindings](https://github.com/cloudfoundry/servicebroker/blob/master/spec.md#binding).

The existing distribution `MintKey` path creates a key for one alias
under an authenticated user's workspace membership and auth epoch. Its
initial response can contain the one-time secret. `WithTransientKey`
uses that authority for one accepted operation and revokes the
credential when the callback completes. `RotateKey` retains the
predecessor's user and alias authority. These lifecycles cannot supply
a durable application installation credential.
[Key issuance](../../services/control/internal/modules/distribution/keys.go),
[Transient operations](../../services/control/internal/modules/distribution/transient.go),
[Rotation](../../services/control/internal/modules/distribution/rotation.go).

The gateway owner needs the authorized installation identity, tenant and
workspace, signed release pin, dependency slot, chosen alias UUID and
required model protocol. Issuance and rotation also need the operation
idempotency key and audit request ID. Recheck the alias and workspace on
the server. Send the credential through the secret owner and hand the
installer only its opaque reference. The gateway must admit that
credential only for the recorded installation's alias and workspace.
The catalog can validate the returned `ResourceGrant` and `SecretRef`
with `VerifyInstallBindings`. DEL owns file projection and workload launch.
[Signed binding contract](app-dependency-bindings.md).

Acceptance needs a real disconnected installation that can call its
approved alias. Exercise a foreign alias and workspace, a mismatched
protocol, an unauthorized actor, retry after an interrupted issuance,
automatic rotation, and revocation after uninstall. Verify stable
idempotency and audit attribution without credential bytes. Work in
the catalog resumes once the gateway and secret owners can return a
revocable reference for a durable installation identity.

Kubernetes and Open Service Broker are Apache-2.0 references. Their
licenses permit commercial use and offline redistribution with the
applicable notices. This research adds no dependency, model or dataset.
[Kubernetes license](https://github.com/kubernetes/kubernetes/blob/master/LICENSE),
[Open Service Broker license](https://github.com/cloudfoundry/servicebroker/blob/master/LICENSE).
