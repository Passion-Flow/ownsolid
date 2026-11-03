# Shared real TLS HTTP fixture

This test-only owner drives the existing control dispatcher with synthetic data
and canonical response schemas. It supplies no production listener, identity,
provider success, SQL database or target qualification. Production code must not
import it. Constructor/source presence alone does not establish verification. Actual scoped
results belong to the coordinator's retained execution receipts. Historical
receipts stay bound to their original sources and are never rewritten by fixture
repair.

## Entrances, trust and API

`NewTLSServer(t, handler) -> *httptest.Server` generates a process-private
self-signed synthetic CA/server certificate with exactly
`portal.example.invalid` and `admin.example.invalid` DNS SANs. It installs this
certificate and the real TLS policy before starting an owned loopback listener.
There is no private-key file, system/account trust installation, ambient proxy,
external DNS dependency, fixed global registry, fake Request.TLS or disabled
certificate/hostname verification. The running TLS configuration is never patched.
The server's `URL` retains its actual loopback address for resource inventory.
It is not the advertised browser origin.

`PortalOrigin` is `https://portal.example.invalid` and `AdminOrigin` is
`https://admin.example.invalid`. These are fixed synthetic fixture identities,
not product deployment defaults. The client dials exactly these DNS authorities
on effective port 443 to this one listener's captured loopback address and port.
It preserves the selected URL, Host, SNI and certificate reference identity.
The distinct physical port is an explicit test-network dial mapping, not a public
origin rewrite. Unknown hosts, ports, HTTP URLs and non-loopback listeners fail;
no fallback DNS, socket or proxy is consulted.

`New(t, server, audience, schemas)` retains the existing Client API. `Call` and
`Write` use the selected actual DNS URL and Origin. Each Client owns a private
transport that admits only its selected audience, including redirect targets;
Portal never automatically follows an Admin redirect. Native Go TLS derives SNI
from the URL, with no fixed portal/admin ServerName override. `Redirect` observes
only the first real response, updates its cookie map and never follows the
callback URL. Client cookies are process-local synthetic fixtures and do not
establish browser Cookie/SameSite behavior. Existing response schema, CSRF,
Cookie collection and error assertions remain in their current owners.

The constructor's `server.Client()` admits both fixed DNS origins for related
raw upload/download/machine requests. Use an explicit `PortalOrigin` or
`AdminOrigin` URL with that client; do not request `server.URL` and overwrite only
Host. It trusts only this fixture's certificate. `server.Certificate().Raw` is the
public synthetic CA/server certificate for an independently owned test client;
never change the running certificate or install it into a shared trust store.
Provider TLS doubles and their independent origins/CA contracts stay separate.

## Lifetime, bounds and source ownership

The fixture captures the actual loopback listener before starting consumers.
Each private transport has bounded dial/TLS-handshake/request/header/idle waits;
all clients close idle connections through `t.Cleanup`, and the server registers
its own Close. Existing explicit server.Close calls remain safe/idempotent.
Server/client TLS state, cookies and private keys never become public product
configuration. These test bounds do not change runtime HTTP policy.

[tls-callsites.json](tls-callsites.json) freezes the original 45eac9f3 source
fingerprints, 35 caller files, 37 shared-control constructor replacements and
9 related raw DNS URLs in 8 of those files. Only the selected functions changed;
provider constructors, the raw slow-socket training test, independent custom
clients and new transport SQL tests belong to their existing owners. An unchanged
historical fixture remains a compatibility obligation, not proof that the current
SNI checks should be weakened.

## Focused local acceptance source

Five new `TestTLSFixture*` roots cover real private CA/DNS SAN/SNI, exact target
refusal, cross-audience redirects, ordinary untrusted CA/wrong-peer failure,
first-response cookie issue/clear, configuration guards and owned-listener close.
They serve explicit synthetic probes, never successful product authentication.
The coordinator runs these new tests sequentially under the existing resource,
external cache and source/evidence inventory controls; this source task runs none.
For the admitted environment, the focused proposal is:

```sh
go -C services/control test -p=1 -parallel=1 -timeout=30s -run '^TestTLSFixture' ./internal/testing/httpfixture
```

The calling packages still require their declared integration tags, isolated SQL,
assets and other actual prerequisites. Compiling those sources does not execute
their SQL/browser or target journeys. The complete deployment-transport matrix,
mode changes, provider validation and real browser trust remain separate gates.

## Explicit deferred private-client compatibility

These independent families still use their own client/constructor behavior and
were not silently rewritten in this shared-client batch:

- `modules/identity/http/{handler_test.go,policy_test.go,license_integration_test.go,
  node_reads_repository_integration_test.go,privateclusters_native_integration_test.go,
  node_enrollment_repository_integration_test.go}` and consumers of their private
  clients, including `license_reductions_test.go`.
- `modules/sso/{application_integration_test.go,browser_integration_test.go}`,
  independent SSO callback/browser/provider fixtures, and all provider TLS doubles.
- The raw slow-body socket test in `modules/training/upload_http_test.go` and
  independent training transfer/derived-transfer clients.

Paths above are relative to `services/control/internal`. Each owning follow-up
must preserve real DNS/IP URL, appropriate SAN/private CA, actual SNI and exact
socket mapping; it must not forge Request.TLS, bypass verification, mutate a live
server or relabel old evidence as a new pass. Mixed private/shared Client tests
that supply a legacy certificate now fail fixture admission explicitly until
that owner is repaired and its current gate is executed.
