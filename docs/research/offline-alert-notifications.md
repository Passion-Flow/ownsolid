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
No new library, relay, model or data asset is shipped.

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
