# Replayable offline Agent enrollment (FLT-102)

Reviewed 2026-11-09. This slice covers the read-only Agent on a host with
protected configuration and a prepared state directory. The
[S-104 browser verification](nodes-page.md#csv-enrollment-s-104-flt-102)
issued 48 tokens and joined one Linux amd64 Agent. Its host separately copied
`/opt/agent` and `/opt/ca.pem`. The Console then created directories, wrote the
one-use token to a private file, wrote `agent.json`, ran `agent enroll`, and
started `agent run`. The token was omitted from the displayed commands. The
current [signed release manifest](../../api/release/v1/manifest.go) binds the
hash and size of each approved asset. Its [RKE2 input group](../../api/release/v1/kubernetes_delivery.go)
is a separate delivery path. The base bundle and [systemd recipes](../../deploy/systemd/)
currently provide no standard signed Linux amd64 Agent installer and service.

## Upstream and approach

[Kubeadm join](https://kubernetes.io/docs/reference/setup-tools/kubeadm/kubeadm-join/)
authenticates discovery with a trusted CA and uses a bootstrap credential to
obtain an identity bound to a locally created key. [RKE2 secure tokens](https://docs.rke2.io/security/token#secure)
also verify the cluster before presenting a credential. ownsolid already has
verified HTTPS, a protected token file, one-use server-side consumption and
local CSR generation. Reuse these mechanisms. No certificate bypass, bundled
secret, download, dependency, model or dataset is added. The [existing Go
runtime and standard library](https://go.dev/LICENSE) are BSD-3-Clause and
allow commercial use, redistribution with notices and offline execution.
Kubernetes and RKE2 are references only. The fixture uses an ephemeral CA and
synthetic tokens. The existing [PostgreSQL licence](https://www.postgresql.org/about/licence/)
permits commercial use, offline use and redistribution under its notice terms.

The installer can call `agent enroll --if-needed --config <protected-config>`.
With an empty state directory, use normal enrollment, including trusted TLS
before sending the token and the existing atomic, private identity publication.
With a completed local identity, require the same origin, protocol and profile
as `run`, a valid node ID and scoped identity, and structurally valid local
certificate state when present. An older read-only identity without a
certificate or an explicit profile can be reused. An issued enrollment
certificate must name the node and match the returned site and cell. The replay is local. It does
not contact Control, read a spent token or replace state. It checks the local
configuration and trust file for errors. Certificate trust against the server
is established on initial enrollment and on the run connection. An existing
`run` process need not stop to recognize its immutable identity. Concurrent
first enrollments still serialize under the existing state lock.

Keep bare `agent enroll` strict for existing automation and keep the new
installer mode read-only. Replaying a network enrollment or passing a token on
the command line was rejected because the token is single-use and a prior
request may have committed after losing its reply. Recognizing a certificate
key or an identity filename by itself was rejected because either can be left
behind by an incomplete or corrupt attempt. Keep uncertain commit and local
publication failure distinct from a valid replay. The installer must stop and
preserve state on either error. Reconcile against the authorized token and node
record before starting a new enrollment attempt.

## Acceptance and release handoff

- From a clean Linux host, one invocation with protected configuration and a
  usable token must publish a private scoped identity. A repeat invocation
  after the token file is absent must return success without another enrollment
  request or any state change. A running process holding the state lock must
  continue serving the same identity.
- A different configured origin, invalid identity or corrupt local certificate
  must fail without new enrollment. A fresh host with the wrong CA sends no
  token and publishes no identity. An expired token produces the existing
  refusal and no identity. A pre-certificate v1 identity stays usable by the
  old and new Agent paths. Keep authorization and protocol fields unchanged.
- Delivery owns the signed Linux amd64 Agent executable, installation assets,
  unit and verifier admission. A local `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`
  Agent build can supply the executable for the new manifest entry once the
  release owner binds its digest, architecture, notices and source identity.
  The installer should verify signed media before copying the binary and unit,
  install the site CA from a trusted local source, create canonical private
  directories and write the protected configuration. Provide the token via a
  private file or an input channel that writes that file with mode 0600.
- Invoke the new mode and use its exit status as the gate to enable and start
  `agent run --config <protected-config>`. Require a successful scoped heartbeat
  and token consumption in the offline end-to-end test. Console should expose
  the packaged command only after the signed installer and unit are available.
  Keep FLT-102 open until that packaged flow runs from the Console instructions
  on a clean disconnected Linux amd64 host.

## Verified Agent boundary

`TestActualAgentEnrollmentCommand` ran the Linux amd64 Agent executable under
local emulation in a container with networking disabled. Its real Nodes HTTPS
handler and PostgreSQL 18.6 with pgvector 0.8.7 used only loopback. An unrelated
CA failed the TLS handshake before the token reached the handler. An expired
token was refused by Control. Neither refusal consumed a token or published
identity. A valid token was consumed exactly once, the identity and certificate
files had mode 0600, and `agent run` sent a scoped heartbeat using that state.
The same enrollment command then succeeded with the token file removed, while
`run` was active, without another enrollment request or a change to either
identity file. The existing-state CLI test also verified reuse of a scoped
pre-certificate v1 identity and refusal of a changed origin or invalid key.

To repeat the command test, build a Linux Agent and set
`OWNSOLID_TEST_AGENT_BINARY` to its absolute path. On Linux, use the isolated
PostgreSQL repository fixture variables in the [development environment](../development/environment.md)
and run `go -C services/control test -p 1 -tags repository_integration
./internal/modules/nodes -run '^TestActualAgentEnrollmentCommand$' -count=1 -v`.
This test covers the command boundary. The signed installer and systemd flow
remain separate acceptance gates.
