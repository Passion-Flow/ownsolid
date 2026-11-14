# Effective serving transport check

The [machine extension](../../../../../docs/engineering/deployment-transport/machine-serving-check.md)
adds `ParseExpectedMachine` and `VerifyWithMachine`. Complete four-field intent
uses the existing versioned machine-server schema/example, strict64KiB JSON and
the existing typed validation. A nil machine intent asserts no dedicated group;
`Verify` retains that explicit absence behavior. Both browser and machine intent
must match exact presence/strings before either TLS pair is read. The checker
then calls the existing `Config.TLSConfig` and `Config.MachineTLSConfig` owners.
It never copies a browser success into machine qualification or silently skips
configured machine TLS. The optional CLI `--expected-machine-json` is public
intent, with no environment/default authority or input rewriting.


This operation compares explicit public deployment intent with the complete
typed control configuration before serving starts. [check.go](check.go) exports
`Command`, `Success`, `MaximumExpectedBytes`, `Expected`, `ParseExpected`,
`Expected.Validate` and `Verify(config.Config, Expected)`. The early CLI owner is
[cmd/server](../../../cmd/server/README.md); the exact command and wire contract
are in the [frozen specification](../../../../../docs/engineering/deployment-transport/serving-check.md).

`ParseExpected` accepts at most 64 KiB of strict version 1 JSON with all eight
required fields. Unknown, duplicate, null, incorrectly typed, missing and trailing
values fail with a fixed error. Validation reuses
`config.ServerConfiguration.ValidateTransport`, requires actual direct HTTPS
file references and an explicit proxy array, and performs no I/O. Empty arrays
retain their wire meaning while the existing runtime's no-proxy modes use nil.
There are no defaults, ambient environment reads or alternate runtime settings.

`Verify` revalidates the full `Config` and expectation, then compares effective
mode, exact listener/origin/reference strings and the complete canonical proxy
prefix set. Set ordering has no authority; neither configuration nor intent is
rewritten or sorted. Every mismatch returns before opening TLS material. After
a match it calls the sole `Config.TLSConfig` owner, including the nil-TLS result
for explicit HTTP and trusted-proxy HTTPS. The TLS owner alone reads pinned
certificate/key snapshots and verifies the actual pair, validity and both DNS/IP
SANs. This operation supplies no separate parser, reader, trust bypass or TLS
success adapter. CA-chain/client trust remains a separate health/browser check.

The operation opens no database, listener, provider or worker and writes no
configuration or receipt. It returns only
`SERVING_TRANSPORT_EXPECTATION_INVALID`,
`SERVING_TRANSPORT_CONFIGURATION_INVALID` or `SERVING_TRANSPORT_MISMATCH`; CLI
argument/output codes belong to its command owner. Correct the selected intent,
configuration or material and explicitly rerun after refusal. No retry, fallback,
downgrade or mutation is implied. File/metadata syscalls still need the caller's
external supervision; the checker does not promise filesystem cancellation.

[check_test.go](check_test.go) contains the three frozen operation roots:
`TestServingCheckStrictExpectedTransportContract`,
`TestServingCheckMatchesEffectiveConfiguration` and
`TestServingCheckRejectsMismatchedFieldsAndUnsafeMaterial`. They use complete
synthetic configuration with unavailable runtime credentials, generated actual
certificate/key pairs, all modes, legacy effective-direct equivalence, complete
prefix membership, exact-field mismatches and unsafe material refusal. Actual
replacement/FIFO/ctime controls remain with the TLS owner's tests. These controls
do not establish database readiness, License validity, installed Compose/Helm,
browser trust or a complete deployment profile.

The central admitted queue runs the focused package with the current TLS-owner
source, then the affected CLI/owner checks and actual native/Linux builds:

```sh
go -C services/control test -race ./internal/operations/servingcheck
go -C services/control vet ./internal/operations/servingcheck
```

Actual acceptance is retained by the central source-frozen quality queue and its
independent reader. Rendering, parsing or a command success alone is not installed
deployment qualification.
