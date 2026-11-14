# Native verified health probe

This operation owns one read-only GET of the configured Portal, Admin or dedicated machine health
endpoint. [probe.go](probe.go) exports `Command`, `Success`, typed `Input`,
`ValidateInput`, `Run(context.Context, config.ServerConfiguration, Input)` and
`RunWithMachine(context.Context, config.ServerConfiguration, *config.MachineServerConfiguration, Input)`.
It has no database, migration, bootstrap, worker, serving-asset or credential
owner. The CLI under [cmd/server](../../../cmd/server/README.md) selects this
operation before opening those runtime owners.

The existing serving configuration selects the exact browser origin and
transport mode. Input selects `portal`/`admin`/`machine`, `live`/`ready`, an explicit
canonical numeric `IP:port` socket target, timeout 1..60 seconds, and an optional
protected CA-file reference. `ValidateInput` performs no I/O; `Run` additionally
validates the server's existing transport structure and requires a CA for HTTPS
or forbids one for explicit HTTP. There is no alternate environment loader,
arbitrary health URL or default dial target. Deployment mapping and the complete
flag contract remain in the [frozen contract](../../../../../docs/engineering/deployment-transport/health-probe.md).

`Run` preserves the browser API by delegating to `RunWithMachine` with a nil
machine pointer. Explicit `machine` audience requires the complete dedicated
typed group, distinct exact origin/listener and valid browser transport before
trust-file access or dialing; absence returns
`HEALTH_PROBE_CONFIGURATION_INVALID` without falling back to Admin. A dedicated
machine origin is always HTTPS and requires a private CA even while browser mode
is HTTP. A machine TLS certificate/key pair remains the serving owner's input;
the probe validates its typed reference structure and verifies the actual remote
certificate using the explicit protected CA, without opening serving private keys.
All audiences then share the identical TLS, numeric dial, one-request, strict
response and timeout path. No new environment key, trust bypass or default target
is introduced. The CLI owner passes both protected configuration owners; its
acceptance remains separate. See the
[machine health contract](../../../../../docs/engineering/deployment-transport/machine-health.md).

The private transport preserves the configured URL Host/reference identity and
normal DNS/IP certificate verification/SNI while dialing the exact input socket.
One dial guard and one GET, no ambient proxy, redirects, cookies, credentials,
forwarding headers, automatic replay, system trust fallback or TLS downgrade are
allowed. A proxy profile probes the actual HTTPS terminator; the probe itself
never manufactures that terminator's backend forwarding protocol.

[private.go](private.go) reads a certificate-only PEM bundle of at most 1 MiB from
an absolute canonical single-link 0600 file under an owned 0700 parent. A held
`os.Root`, no-follow nonblocking descriptor, inode/name/parent/owner/mode/size and
modification/change-time snapshots reject unsafe or replaced inputs. Darwin and
Linux change-time fields are checked without importing a SQL operator package.
No shared trust or input files are changed. The response must be HTTP200 with
JSON media type, no-store, a request ID and exactly the bounded 1 KiB
`{"data":{"status":"ok"}}` envelope; duplicates, unknown/null/trailing data and
login/HTML/status substitutes fail. All returned errors are fixed redacted codes.

After a failure, correct the selected configuration, trust, endpoint or runtime
dependency and invoke explicitly again. Earlier cancellation/deadlines win; the
operation closes response and private transport resources on every outcome.
It does not schedule itself or retry an uncertain response. Input changes take
effect per invocation and require no rebuild of the same target executable.

The ten existing roots in [probe_test.go](probe_test.go) and
[private_test.go](private_test.go) map to HP01–HP08: real owned loopback
HTTP/TLS/TLS-proxy, DNS/localhost/IPv4/IPv6 references, TLS refusal, single-request
and ambient-proxy controls, strict response, timeout/cancellation, pre-dial
input/configuration refusal, private trust and real file-snapshot mutation.
Their health responders and certificates are explicitly synthetic. They do not
establish product readiness, browser compatibility, full serve lifecycle,
installed Compose/Kubernetes or target qualification. CLI HP09/HP10, actual
build/test HP11 and installed HP12 evidence retain their separate owners.

[machine_test.go](machine_test.go) adds the three exact MP01/MP02 roots:
`TestMachineHealthProbeActualTLSReferences`,
`TestMachineHealthProbeRefusesUnsafeTargets` and
`TestMachineHealthProbeSingleRequestAndCancellation`. They reuse the existing
explicitly synthetic responder/private CA/owned-listener helpers and exercise
real local TLS with DNS/localhost/IPv4/IPv6 identities, distinct advertised/dial
targets, both health kinds, HTTP/direct/proxy browser coexistence, pre-dial refusal,
wrong CA/SAN/expired certificates, plaintext refusal, strict responses,
redirect/proxy/replay refusal and cancellation/deadlines. These are probe-mechanic
controls; they do not execute the product readiness Handler, SQL, CLI, installed
probe scheduling or browser trust. Those owners retain MP03–MP07. The underlying
private trust reader and historical tests remain unchanged.

Executable focused checks, to be run by the admitted central queue:

```sh
go -C services/control test -race ./internal/operations/healthprobe
go -C services/control vet ./internal/operations/healthprobe
```

The selected machine extension and two directly affected browser/input roots pass
their native gate; actual production SQL composition is accepted separately.
See [actual evidence and limits](../../../../../docs/execution/mvp/machine-health.md).
The broad commands above are reproduction procedures, not a claim that every
historical root was replayed. Installed scheduling/browser trust remain pending.
