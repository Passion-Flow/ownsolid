# Separate Console and Portal listeners

The default serves both browser hostnames through one configured browser socket.
To put Console and Portal in separate firewall zones, add
`server.portal_listen_address` to the complete protected control configuration.
The existing `server.listen_address` then serves Console only; the new socket
serves Portal only. Requests naming the other entrance are refused on each socket,
including assets and health probes. Do not use a redirect to cross this boundary.

For example, merge these public address fields into the existing valid snapshot:

```yaml
server:
  listen_address: 10.20.0.10:8443
  portal_listen_address: 10.30.0.10:9443
  admin_origin: https://admin.example.invalid:8443
  portal_origin: https://portal.example.invalid:9443
```

These example addresses are placeholders, not defaults. Keep the existing
transport mode, protected TLS file references, assets and other required fields.
Bind addresses select local interfaces; advertised origins select the addresses
clients actually reach. They may differ behind a configured trusted proxy. Direct
HTTPS uses the existing browser key pair, whose certificate must cover both
declared names. No private-key contents belong in YAML or environment values.

The existing protected `serve --env-file` can set
`PROJECT_ONE_PORTAL_LISTEN_ADDRESS` explicitly. Ambient environment is ignored.
The [commented mapping](../../services/control/config/examples/control/transport.env.example)
describes precedence. An independently invalid YAML snapshot fails even if its
override would be valid. Empty values cannot remove a selected bind.

Upgrade the serving binary before selecting the new field. When using
`verify-serving-transport`, add the same `portal_listen_address` to the existing
version-1 expected-server JSON. The original eight fields remain required. Old
expectations assert that the Portal bind is absent, so a stale expectation refuses
split serving. Do not bypass this verifier or replace its failure with success.
Restart through the existing service supervisor to apply the selected snapshot;
there is no live rebinding. If any socket cannot bind, startup releases its other
owned sockets and accepts no requests. Correct the configuration or conflict and
retry the same startup. It never closes a separately owned process's socket.

To return to shared serving, select a new valid protected snapshot with
`portal_listen_address` omitted, omit it from expected intent and restart. Address
changes can affect sessions and SSO callbacks; reconcile those through the existing
address-change procedure before switching advertised origins. Merely changing a
bind does not alter the configured public origin.

The optional `machine_server` remains the separate direct-HTTPS entrance for nodes
and model-gateway control traffic. Agent gateway replicas retain their independent
typed listener. The model inference gateway is a separate process. Complete
installer projection, native installation and real-browser qualification of all
four entrances remain open under ARC-110. Current implementation and actual runs
are recorded in [the research note](../research/entrance-listeners.md) and the
[product status](../product/status.md); this configuration guide is not evidence
that those remaining acceptance gates passed.
