# Published alias trust download contract (GW-125, DEV-101)

The [registered Distribution route](../../services/control/internal/modules/distribution/http/human.go)
adds /ca to the cell-scoped Admin and Portal alias detail path. Reuse the
existing session schemes and workspace and alias identifiers. The operation
only accepts these path parameters. [Gateway trust delivery](gateway-delivery.md#downloading-caller-trust-gw-125)
records the scoped source of the CA, activation preconditions, and its
certificate-only validation.

[RFC 7468](https://www.rfc-editor.org/rfc/rfc7468.html#section-5) defines
the PEM certificate encoding. [curl TLS verification](https://curl.se/docs/sslcerts.html)
uses a selected CA file and verifies the advertised endpoint name.
Advertise the registered application/octet-stream attachment with the
fixed model-gateway-ca.pem name, byte length, SHA-256, no-store and request
correlation. Match the existing safe Problem schema and authority error.
The client saves the exact public PEM bytes. A new media type or JSON
envelope would disagree with the shared attachment writer.

Verify both real paths with PostgreSQL and local TLS, including repeatable
identical bytes, a signed gateway endpoint, rejection of an incorrect
name, out-of-scope access and invalid trust material. Verify that generated
clients keep the exact attachment and decode GATEWAY_AUTHORITY_UNAVAILABLE.
The Web owner can use the versioned contract to connect the existing
endpoint sample to the download.

The existing [Gateway delivery licenses](gateway-delivery.md#downloading-caller-trust-gw-125)
and [SDK generator licenses](sdk.md#licenses) permit commercial and offline
use and redistribution with notices. No dependency, model, or dataset is
added.

## Disconnected verification

The Linux amd64 fixture used the registered HTTPS dispatcher and
PostgreSQL 18.6 to validate statuses, media type, header schemas and
problem bodies for both entrances. The returned PEM verified the
agent-activated test endpoint and rejected a wrong hostname. Invalid
trust, foreign authority and inactive aliases returned their documented
problems. Pinned Go and Python clients encoded the same scoped paths,
retained the exact PEM bytes and decoded the authority error.
