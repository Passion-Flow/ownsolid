# Offline operator CLI (DEV-102)

## Operator workflow

Operators need the Admin entrance over SSH in installations where only the private
control plane is reachable. [kubectl get][kubectl] uses a table by default and
offers JSON and YAML from the same request. [OpenShift oc][oc] demonstrates a
login command that establishes a session for subsequent commands. ownsolid will
ship that pattern as a local Go binary, with examples in the root, group and leaf
help and with a version command that works without configuration.

Use the existing Admin contracts for [identity][identity], [nodes][nodes],
[models][models] and [deployments][deployments]. Login and logout obtain a fresh
CSRF token and its context cookie before the POST, and send the configured origin.
Persist only the Admin session cookie value. The server owns expiry, authorization,
tenant isolation and audit. Use an in-memory cookie jar for each invocation. Do
not follow redirects or use environment proxies. A configured PEM file supplies
an explicit trust pool for Go's TLS verification. Linux can use local system
roots by default. Require the PEM on macOS for HTTPS because [Go delegates
system trust on macOS][darwin-trust] to a verifier that [can fetch missing
certificates][apple-trust]. An explicitly configured
HTTP origin uses the server's separate plaintext cookie namespace. Normalize the
origin using the scheme, host and port [origin rules][origin].

## Scope and output

- List nodes through all server cursor pages. Find a node by exact name in the
  results of the documented name-prefix filter, or use a UUID. Reject ambiguous
  names and fetch the actual detail endpoint after resolving the ID.
- List models and deployments in a configured workspace, or the current
  workspace returned by `GET /api/v1/me`. Their APIs return server-bounded lists
  without cursors. Deployment records have UUIDs and model IDs, with no independent
  name field. A deployment UUID directly selects its record. A name means an
  exact model name and selects the deployment only if there is exactly one
  matching model and one retained deployment for it in the selected workspace.
  Read the server's existing public runtime lookup limit. If a full page could
  conceal a duplicate, or the selection is ambiguous, direct the operator to a
  deployment UUID.
- Preserve the API resource fields in JSON and YAML and use selected columns in
  tables. Escape control characters in human output. Write actionable failures
  and prompts to stderr so automation can parse stdout.
- Use `--server`, then `OWNSOLID_SERVER`, then the local JSON config. Follow the
  [XDG base directory specification][xdg], with `OWNSOLID_CONFIG_DIR` as an explicit
  override. Use an origin-specific credential file in a private directory. Open
  credentials without following final symlinks, require owner-only mode 0600,
  atomically replace the file, and serialize login and logout for that origin.
  Preserve the credential on a retryable logout failure. Read passwords from a
  masked terminal prompt or an explicitly supplied private file.

## Dependency review

| Runtime dependency | Pinned version | License |
| --- | --- | --- |
| [Go standard library][go] | Repository Go toolchain | BSD-3-Clause |
| [Cobra][cobra-license] | 1.10.2 | Apache-2.0 |
| [pflag][pflag] (Cobra parser) | 1.0.9 | BSD-3-Clause |
| [go-yaml v3][yaml] | 3.0.5 | Apache-2.0 and MIT for the libyaml-derived files |
| [x/term][term] | 0.47.0 | BSD-3-Clause |
| [x/sys][sys] | 0.49.0 | BSD-3-Clause |

These licenses permit commercial use, redistribution and offline execution.
Distribute their license texts and applicable notices with the binary. No model
or dataset is needed. Cobra's [command implementation][cobra-command] and
[module manifest][cobra-mod] separate argument parsing, completion and help from
application execution. Its execution path runs callbacks provided by the binary
and does not perform an update check, download or telemetry request. Its
Windows-only `mousetrap` module is Apache-2.0 and is outside the Linux and macOS
builds. Cobra's documentation tooling is not imported. YAML conversion, local
terminal I/O and file operations are in-process. The CLI sends HTTP requests
only to the selected Admin origin and requires no external runtime service.

The standard `flag` package would require maintaining subcommand dispatch and
persistent flags ourselves. A YAML serializer avoids hand-rolled quoting and
implicit scalar parsing. A server API token or a client-generated session would
introduce a second authentication flow and change authorization semantics.
Shelling out to curl and saving a complete cookie jar would add a runtime
dependency and persist the CSRF context unnecessarily.

## Verification criteria

- Exercise all eight commands. Check root and leaf help on the compiled binary.
- Cover table alignment, JSON and YAML, empty results, cursor iteration and
  exact or ambiguous resource selection against HTTP fixtures.
- Check flag, environment and file precedence and ensure connection errors
  report the configured origin and a recovery action.
- Assert mode 0600 and origin isolation, refusal of insecure files, CSRF and
  cookie handling, redirect refusal, permission failures and logout retry.
- Run the CLI flow on a network with only the test Admin service reachable.
  Verify repository build, checks and tests before marking the item complete.

[kubectl]: https://kubernetes.io/docs/reference/kubectl/generated/kubectl_get/
[oc]: https://docs.redhat.com/en/documentation/openshift_container_platform/4.18/html/cli_tools/openshift-cli-oc
[identity]: ../../api/openapi/v1/identity/openapi.json
[nodes]: ../../api/openapi/v1/nodes/openapi.json
[models]: ../../api/openapi/v1/models/openapi.json
[deployments]: ../../api/openapi/v1/deployments/openapi.json
[origin]: https://www.rfc-editor.org/rfc/rfc6454
[xdg]: https://specifications.freedesktop.org/basedir/latest/
[go]: https://go.dev/LICENSE
[cobra-license]: https://github.com/spf13/cobra/blob/v1.10.2/LICENSE.txt
[cobra-command]: https://github.com/spf13/cobra/blob/v1.10.2/command.go
[cobra-mod]: https://github.com/spf13/cobra/blob/v1.10.2/go.mod
[pflag]: https://github.com/spf13/pflag/blob/v1.0.9/LICENSE
[yaml]: https://github.com/yaml/go-yaml/blob/v3.0.5/LICENSE
[term]: https://go.googlesource.com/term/+/refs/tags/v0.47.0/LICENSE
[sys]: https://go.googlesource.com/sys/+/refs/tags/v0.49.0/LICENSE
[darwin-trust]: https://go.dev/src/crypto/x509/root_darwin.go
[apple-trust]: https://developer.apple.com/documentation/security/sectrustsetnetworkfetchallowed(_:_:)

## Integration evidence

`TestLaneDRealCLI` ran the compiled Linux arm64 CLI as separate processes against
the real Admin HTTP dispatcher over verified TLS and PostgreSQL 18 with pgvector
0.8.7. The disposable Docker container used `--network none` and had no network
route beyond loopback. The fixture enrolled a synthetic node, inserted approved
model metadata and created a deployment through the service. It verified both
help pages, local version, login, both node operations, model list, both
deployment operations by name and UUID, and logout. A read after logout failed
and PostgreSQL held the logout audit event. The test covers the control-plane
workflow with synthetic inventory and deployment records.
