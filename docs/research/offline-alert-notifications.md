# Site-local alert notifications (OBS-105)

[Alertmanager receivers](https://prometheus.io/docs/alerting/latest/configuration/#email_config)
deliver mail directly to a configured SMTP relay, with certificate verification
against the site's CA. The [webhook receiver](https://prometheus.io/docs/alerting/latest/configuration/#webhook_config)
reads the internal destination from a protected file and can turn redirects off.
Both work over an isolated network. Upstream cloud notification integrations
would require third-party connectivity. WeCom, DingTalk and Feishu need
separately qualified customer-managed relay paths.

Use Alertmanager 0.34.2 under its
[Apache-2.0 license](https://github.com/prometheus/alertmanager/blob/v0.34.2/LICENSE).
Commercial use, offline use and redistribution are permitted with the license
and notices. The relay fixture reuses the repository's SMTP sink. It creates a
disposable certificate with OpenSSL 3 ([Apache-2.0](https://www.openssl.org/source/license.html))
and uses curl ([curl license](https://curl.se/docs/copyright.html)), Python's
[standard library](https://docs.python.org/3/license.html) and Linux test tools.
The SMTP example ships no new library, relay, model or data asset.

Require a verified TLS connection to the customer SMTP relay. Use the CA file
and an authentication password file when the relay requires authentication.
Restrict both mail and webhook egress to the site network. An absent relay,
bad certificate or failed delivery must remain a failed delivery. The local
smoke test uses a real Alertmanager and TLS SMTP sink, sends an alert through
the API and inspects the received message. The local webhook flow is verified
in [the rack routing test](offline-alert-routing.md). The full item still
requires ownsolid configuration, offline distribution and the named
vendor relay paths.

## Verified path

On Linux amd64, Alertmanager 0.34.2 loaded the example and delivered an
API-submitted alert over implicit TLS to the repository's SMTP sink.
The certificate was trusted through the configured CA file. The test
verified the recipient and alert subject in the message. Only loopback
was available. Both processes stopped after the test.

## Site-local Matrix relay

[Alertmanager's webhook v4](https://prometheus.io/docs/alerting/latest/configuration/#webhook_config)
provides a group key, group status and alert fingerprints. Keep grouping,
inhibition, and silences in Alertmanager. Its
[webhook implementation](https://github.com/prometheus/alertmanager/blob/v0.34.2/notify/webhook/webhook.go)
handles bounded retries for retryable HTTP failures. Authenticate each configured
tenant and receiver over TLS using a distinct file-backed bearer credential.
Require the authenticated tenant on the group, common labels and every alert.
The private operator's Alertmanager supplies trusted scope labels. Do not expose
its alert or silence APIs to tenants.

Use the [Matrix room send API](https://spec.matrix.org/v1.17/client-server-api/#put_matrixclientv3roomsroomidsendeventtypetxnid)
against a customer-owned Matrix homeserver. A fixed TLS origin, tenant-specific
bot device and private unencrypted room make the destination independent of
payload data. Disable federation, public access and push gateways on the
site homeserver. A Matrix client inside the intranet reads room history.
The [transaction ID contract](https://spec.matrix.org/v1.17/client-server-api/#transaction-identifiers)
makes retransmission safe for the same device and HTTP path. Generate the ID
from the scoped group key, group status and sorted alert fingerprint, start
time and status. Ignore refreshed firing deadlines, annotations, and source
URLs. An actual resolution and a new start time are distinct events. Identical
firing reminders collapse by design. Preserve the token/device and homeserver
transaction history across recovery. Changing the device or deleting its
transaction history ends that deduplication guarantee.

Render one plain-text message per webhook group from bounded topology,
severity and alert-name labels. Refuse incomplete batches, mixed tenant
labels, unknown receiver or invalid authentication. Treat transport errors,
rate limits, server errors and missing acknowledgements as retryable and
let Alertmanager resend. Refuse a redirect or permanent Matrix rejection
without changing the destination. Return success only after a Matrix event
ID. Use a private site CA and literal private IP origins, no system proxy or
online fallback. Credentials are files. Record safe delivery outcome codes
and avoid persisting alert data in ownsolid SQL.

[Tuwunel 1.9.4](https://github.com/matrix-construct/tuwunel/releases/tag/v1.9.4)
is an independently deployed test homeserver under
[Apache-2.0](https://github.com/matrix-construct/tuwunel/blob/v1.9.4/LICENSE).
The fixture uses the published Linux x86-64 v1 default binary artifact
with SHA-256 `0c6fad8c69aaf8d8e6cc0c58a6d1f407c0009aafe53517b4d7784367a092664b`.
Commercial use and offline deployment are permitted. Redistribution
requires the license and notices for upstream and bundled dependencies.
This slice adds no bundled homeserver, runtime library, model, or dataset.
The relay uses the [Go standard library](https://go.dev/LICENSE) and the
repository's existing JSON contract. Gotify's
[POST contract](https://gotify.net/api-docs) lacks a client transaction ID.
Cloud vendor bots need independent in-site relay contracts. Matrix covers
only the local chat path and does not qualify WeCom, DingTalk or Feishu.

Use a real homeserver and Alertmanager in a loopback-only namespace. Assert
that a persisted Matrix event whose response is lost is retried under the
same transaction ID and appears once, that a new lifecycle event appears
separately, and that an Alertmanager silence prevents sending to that
tenant without affecting another tenant. Deny wrong tokens, mismatched
tenant labels and cross-room reads. Installation must deliver independent
credentials, scoped routing, private CA, network policy and bot/room
provisioning. Console integration must own audited tenant authorization
and silence management. OBS-105 remains open.

A loopback prototype started the checksum-verified Tuwunel binary and
registered a synthetic account through its local administration endpoint.
Two authenticated room sends with the same transaction ID returned the
same event ID. The homeserver stopped after the probe.

### Verified Matrix path

On Linux amd64, the pinned Alertmanager 0.34.2 and Tuwunel 1.9.4 ran
with the actual relay executable in a network namespace with only
loopback. Both relay hops used independently verified TLS. A test proxy
discarded the first successful Matrix response after the homeserver wrote
the event. Alertmanager retried the 503 with the same group, and Matrix
returned the same event ID for the same transaction. The room contained
one firing message for the 40-alert group. A resolution and a recurrence
each produced a different event. An API-created silence applied only
to its tenant; the other tenant still received its maintenance probe.
A mismatched credential, mixed-tenant batch and cross-room history read
were refused. The Alertmanager, relay and homeserver processes stopped
and their temporary namespace was removed.

## Site-local Feishu custom-bot relay

The [Feishu custom-bot guide](https://open.feishu.cn/document/client-docs/bot-v3/add-custom-bot)
defines the v2 hook path, text payload, signed Unix timestamp, HMAC-SHA256
over an empty message using timestamp and secret as the key, Base64 encoding,
and the modern response code. Use that wire format and the documented
20 KB body bound. The signature authenticates the time and secret.
Private TLS protects the message and both credential files.

The custom-bot contract provides no durable idempotency receipt. The
customer relay must explicitly advertise the ownsolid v1 capability on
OPTIONS to the same private hook, then deduplicate POST by its
Idempotency-Key and return the matching key only after durable acceptance.
The [HTTPAPI working draft](https://datatracker.ietf.org/doc/draft-ietf-httpapi-idempotency-key-header/)
describes the key and receiver-side retention pattern. This is a private
relay extension. A plain HTTP 200 or Feishu code zero without the matching
receipt remains retryable. The endpoint must retain delivery state across
restarts and reject a changed message under the same key. A relay that
forwards to a non-idempotent vendor API owns any ambiguous downstream
outcome and must reconcile it before acknowledging durable delivery.
Do not use a local in-memory deduplication cache or redirect a request
to a vendor URL.

Use a protected hook ID and signing secret per tenant, one private literal
HTTPS origin and a configured CA. Reuse Alertmanager authentication,
grouping, silences and occurrence identity. Every retry sends the same
semantic key and recomputes its signature with the current timestamp.
Retry network failures, HTTP 408, 429, server errors, and documented
Feishu code 11232. Treat permanent refusals as failed deliveries. Check
the real Alertmanager path against a controlled HTTPS endpoint with a
durable receipt fixture, including a lost receipt, resolution, wrong
tenant, bad capability and no public route. No vendor endpoint is part
of the qualification. The implementation uses only the existing Go
standard library and internal packages. No vendor SDK, model or data
asset is added.

### Verified Feishu relay contract

On Linux amd64 with only loopback and no default route, Alertmanager 0.34.2
delivered firing and resolved groups through the compiled relay to a
controlled HTTPS receiver. The receiver independently verified the signature
and persisted a tenant-scoped idempotency key and message before simulating
a lost receipt. The next delivery used the same key and left one stored
firing message. The test also exercised a tenant silence, delivery for a
different tenant, and denial of a cross-tenant webhook credential. Unit
tests cover missing capability, missing or mismatched receipts, rate
limits, signature rejection and redirects. The controlled endpoint
qualifies the site relay contract. A vendor-side connection and packaged
installer and Console flows remain unqualified.

## Site-local DingTalk signed webhook compatibility

The [DingTalk robot send reference](https://developer.alibaba.com/docs/api.htm?apiId=37246)
defines POST /robot/send, the access_token query parameter, the text body
with msgtype and text.content, and a numeric errcode in the receipt.
[DingTalk's signing example](https://dingtalk.apifox.cn/doc-3550006) uses
Unix milliseconds and HMAC-SHA256 with the signing secret as the key and
timestamp, a newline and the secret as the message. Base64-encode the
MAC and URL-encode it as sign. The [DingTalk error contract](https://s.apifox.cn/apidoc/docs-site/467052/api-140275634)
identifies -1 and 410100 as retryable and 310000 as a security rejection.
[Alibaba Cloud's alert integration](https://help.aliyun.com/zh/sls/create-a-webhook)
also accepts signing secrets for private group integrations. The
[DingTalk developer guide](https://open-dingtalk.github.io/developerpedia/docs/learn/bot/webhook/overview/)
flags retirement of the legacy custom-robot entry point. The chosen
contract is signed webhook compatibility at a customer-owned site relay.
Direct cloud webhook delivery and application OAuth would require outside
connectivity and are outside the offline boundary. A legacy wire-format
fixture cannot qualify a currently provisioned vendor-side robot.
The site profile bounds the JSON body at 16 KiB.

Reuse the Feishu durable-v1 transport and shared tenant authorization.
Probe OPTIONS /robot/send without query credentials. Send the signed POST
only after the receiver declares capability. Use separate protected files
for the Alertmanager bearer, access token and signing secret. The HTTPS
origin is a private IP with a pinned site CA. Because the compatibility
wire format contains credentials and a signature in the query, the site
must suppress query logging and tracing at the receiving listener and any
reverse proxy. The relay reports only bounded outcome codes and scope.
The controlled receiver returns numeric errcode zero and echoes the
quoted Idempotency-Key only after committing the semantic payload.
Changing timestamps or signatures do not change the transaction identity.
Site receivers must reconcile ambiguous downstream delivery before retrying
an operation that the downstream system cannot deduplicate.

Unit tests check an independent signing vector, request encoding, retryable
and permanent outcomes, missing or repeated receipts, redirects and
certificate rejection. The native Alertmanager fixture checks exact text,
resolution, silence, scope, and retry after a persisted message loses its
receipt. Everything runs on a loopback-only Linux namespace. A vendor-side
connection and installer/Console wiring are separate qualification steps.
No SDK, model or dataset is introduced. The existing Go standard library
uses the [Go BSD license](https://go.dev/LICENSE), and the existing
[Alertmanager release](https://github.com/prometheus/alertmanager/blob/v0.34.2/LICENSE)
uses Apache-2.0. Both permit commercial use, offline operation and
redistribution subject to their terms.

### Verified DingTalk relay contract

On Linux amd64 with only loopback and no default route, Alertmanager 0.34.2
delivered the group through the compiled DingTalk relay. A controlled HTTPS
receiver independently verified the query signature and message structure,
then synchronized the semantic message and key to disk. The first response
returned numeric zero without the receipt header. Alertmanager retried, and
the receiver acknowledged the same key with one stored firing message.
The test observed a resolution, tenant-specific silencing, and refusal of
a cross-tenant webhook credential. Unit checks verified an independent
signature vector, missing and ambiguous receipts, redirect refusal and
untrusted TLS rejection before any HTTP request reached the receiver.
The shared Feishu and Matrix integration tests passed on the same host.
The vendor-side path remains unqualified.
