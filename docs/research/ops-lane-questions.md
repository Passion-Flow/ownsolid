# Operations lane integration boundaries

The lane owns `services/control/internal/modules/components`, `catalog` and its
two roadmap files. The paths in the module headers describe the finished product.
The lane can add contract and component behavior where it owns the code, and
exercise it in isolation. The following end-to-end work depends on integration
in the owning paths. Leave the boxes open while those boundaries apply.

| Items | Owning integration needed | Lane action |
| --- | --- | --- |
| OPS-101, OPS-103 | Node-agent PTY execution and revocation, a bounded stream on the outbound agent connection, role and audit integration, and a console terminal. | Preserve the outbound-only and permission boundaries. A request record alone cannot start or terminate a terminal. |
| OPS-102, OPS-107 | The cell's Kubernetes client and delegated identities, namespaced authorization and console shell or resource browser. The private-cluster discovery credential explicitly lacks exec authority. | Leave existing discovery credentials scoped to discovery. Kubernetes must authorize the impersonated user and namespace. |
| OPS-104 | An authorized server log hub with budgets, new sources and the console live viewer. | Strengthen the existing component snapshot path inside its current contract. |
| OPS-105, OPS-106, OPS-108, OPS-110, OPS-111, OPS-114 | Host-side collection or execution, fleet transport and console flows. Some facts already travel through the fleet service. | Reuse the existing facts and lifecycle contracts when their owners add these paths. Keep host reads separate from mutation authority. |
| OPS-109, OPS-113 | A durable scheduler and dispatch policy in the task owner, explicit target and command confirmation, per-node output and console recovery. | Existing component lifecycle commands are typed for node-exporter. Keep that authority scoped to the current component. |
| OPS-112 | The installer and backup owner provide the existing local backup and restore. S3 and SFTP targets, app data and a product flow require their own integration. | Preserve the current local path while the owning code gains the missing targets. |
| CAT-101, CAT-102 | Catalog service, publisher and installer, console form and dependency selection. | Build and exercise the manifest and typed binding contracts in the catalog lane. |
| CAT-103, CAT-104, CAT-105, CAT-106, CAT-108, CAT-109 | Scoped gateway and OIDC issuers, approved local deployers, quota and workload policy, operational streams and console. | Keep key issuance, identity and install authority behind their owning services. |
| CAT-107 | Approved app signing trust and installer integration with local image import. | The catalog verifies the signed OCI closure with `catalog/apps.VerifyInstallBundle`. `ImportInstallImage` checks expanded layers and DiffIDs, registers a single image via the local Docker Unix socket and confirms its exact digest. The installer owns authorization, audit and workload admission, including multi-image registration. |

The current screen plan has a node drawer and an app-catalog placeholder. The
full operator flows also need their screen specifications. Keep 1Panel strictly
a behavior reference. Use the platform's own agent connection and Kubernetes
identities for execution.

## Repository acceptance gate

The local browser harness reserves ports 4310 and 4311. Other worktrees can
occupy either port and stop `make test` before the suite starts. An alternate
preview on private ports exercised part of both browser suites. That run left
the native browser gate unverified.

Running the non-browser gate and then repeating the focused tests with a
resolved macOS temporary directory also exposed failures in the unchanged agent
packages `internal/modelpreparations` and `internal/training`. The shutdown and
revocation tests reported cancellation or changed output while verifying
ownership of the container. These tests belong to the agent owner. The OPS
increment has separate passing catalog and component evidence. The repository
gate was open in that run.

The standalone control suite also reported failures in dataset import memory,
password admission deadlines, backup restore roots and TLS file mode checks.
Those paths are outside OPS ownership. Gateway tests and the isolated i18n
rerun passed. The fixed-port and independent runs are distinct from the
PostgreSQL and VictoriaLogs component integration run.

### Same-baseline macOS reproduction

The comparison ran from an archive of main at `609d5198` and from OPS rebased
onto that commit. Both used a resolved `TMPDIR`, `GOMAXPROCS=2` and serial Go
package scheduling. Run these commands in each tree with that same environment.

```sh
export TMPDIR="$(cd "${TMPDIR:-/tmp}" && pwd -P)/" GOMAXPROCS=2
go -C services/agent test -p 1 -count=3 -json ./internal/modelpreparations ./internal/training \
  -run '^Test(ShutdownLeavesTheContainerAndTheNextProcessAdoptsIt|ARevokedAttemptStopsItsContainer)$'
go -C services/control test -p 1 -count=1 -json ./internal/modules/datasets \
  ./internal/modules/identity/security ./internal/operations/postgresbackup ./internal/platform/config \
  -run '^Test(SourceImportMemory|M01ActualHashAdmissionDeadline|PostgresRestoreMeasuredRoots(BindBeforeConfigurationOrSQL|RefuseReplacementBeforeImport)|TLSMaterialPinnedPublicAndPrivateSnapshots)$'
```

| Owner and test path | Comparison |
| --- | --- |
| `services/agent/internal/{modelpreparations,training}/static_stability_test.go` | Shutdown/adoption failed in 2 of 6 attempts on main and 1 of 6 on OPS. The errors included `context canceled` and `TRAINING_OUTPUT_CHANGED`. Revocation passed in all six attempts per tree. |
| `services/control/internal/operations/postgresbackup/restore_roots_binding_test.go` | Both measured-root tests failed on both trees with `POSTGRES_RESTORE_EXPECTED_ROOT_CHANGED`. The backup owner must validate the pinned filesystem root through the restore path. |
| `services/control/internal/platform/config/tls_material_test.go` | Both setgid subcases failed on both trees. The TLS material owner must preserve rejection and redaction for unsafe file modes on supported test filesystems. |
| Dataset source-import memory | The focused test passed on both trees. The full non-browser gate failed it on OPS. A subsequent three-run package comparison using `go -C services/control test -p 1 -count=3 -v ./internal/modules/datasets` reproduced the RSS failure once on main and passed all three attempts on OPS. |
| Password admission deadline | Passed on both trees in the controlled comparison. |

OPS has no diff in these packages. The browser gate can use the repository's
Chromium specs and built apps with two workers, private loopback preview
ports, and an external Playwright config. Keep its result separate from the
fixed-port run and the non-browser Go, Rust and web tests.


Both isolated browser runs completed. Console passed 1,031 tests and skipped
23 live-installation tests. Portal passed 372 and skipped five live-installation
tests. The gateway suite, serial web unit suites, E2E helper modules, CLI and
audit verifier also passed in separate runs.

The two measured-root tests and the TLS material suite passed on both trees
when cross-compiled with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c`
and run in a disconnected Linux amd64 container as UID/GID 65534. Use a private
tmpfs for the test's temporary directory. The restore-root contract rejects
root-owned fixture identities, so the non-root UID and GID are required for
this comparison. The native macOS non-browser gate was red with the original
workstation fixture.


### Targeted recheck after the agent fix

With `aa5e712f` present, the shutdown/adoption and revocation tests in both
agent packages each passed 10 repetitions with the resolved `TMPDIR` and
`GOMAXPROCS=2` above. The two restore-root tests and the two TLS setgid
subcases still failed on macOS. The same restore and TLS tests passed in
a disconnected Linux amd64 container as UID/GID 65534. This run used the
targeted tests, catalog checks and an isolated Docker import fixture.

### Workstation fixture groups

The native macOS restore-root and TLS setgid failures were reproducible on
`e14d11b0` with the same assertions and a scratch directory inheriting GID 0
from `/private/tmp`. The restore-root contract requires a nonzero group.
On the workstation, chmod by the unprivileged file owner also cleared the
setgid bit on a GID 0 fixture file. Setting only the lane's private test
directory to the current user's primary group preserved the setgid bit and
all four previously failing cases passed with the same code. Use a resolved
path and an isolated directory owned by a non-root user with a nonzero
primary group.

```sh
scratch=$(mktemp -d /private/tmp/ownsolid-ops-test.XXXXXXXX)
mkdir -m 0700 "$scratch/tmp"
chgrp "$(id -g)" "$scratch/tmp"
export TMPDIR="$scratch/tmp/"
```

The same restore-root and TLS test binaries passed separately in a
disconnected Linux amd64 container running as UID/GID 65534, using its
private tmpfs. The Linux evidence and corrected workstation fixture are
separate from the historical GID 0 runs.

The catalog race suite and the opt-in Docker test passed after the rebase.
The same real signed VictoriaLogs archive was imported through
`ImportInstallImage` twice in a fresh, disconnected Docker 29.1.3 store.
Readback retained the signed manifest digest and an empty tag list, with
one image in the store. The operator still owns the catalog installation
decision, audit and workload admission. OPS-104's five selected tests
passed with disposable PostgreSQL 18.6, pgvector 0.8.7 and VictoriaLogs
1.53.0, including exact settlement matching and the real log-store path.

### Rebased acceptance run

On the `e14d11b0` baseline with the private workstation fixture, `make check`,
`make build`, and every default test target passed. Go ran with
`GOMAXPROCS=2` and `GOFLAGS=-p=1`. The native browser harness used
`OWNSOLID_PLAYWRIGHT_PORT_OFFSET` on two private loopback ports and two
workers, running the two applications in sequence. Console passed 1,046
tests with 23 live-installation skips. Portal passed 387 with five
live-installation skips. The separate `make` invocation reused these
completed browser and dependency prerequisites and ran the remaining Go,
Rust and web unit recipes.

```sh
make check && make build
(cd web && pnpm --filter ./apps/console run test:browser --workers=2)
(cd web && pnpm --filter ./apps/portal run test:browser --workers=2)
make -o test-web-browser -o web-install test
```

### Native gateway gate after GW-108

After rebasing past `1b293a01`, the macOS gateway suite failed
`backend_tests::actual_connection_refusal_opens_only_its_backend_circuit`.
The same test failed when run serially from a separate archive of
`1b293a01`: it returned `upstream_timeout` where the assertion expected
`upstream_unavailable`. The gateway sources are identical in the two
checkouts. The fixture keeps its loopback port bound without listening
until the first request completes. An independent socket probe of that
state timed out on macOS and got immediate `ECONNREFUSED` in a
disconnected non-root Linux container. Closing the socket before the
probe got immediate refusal on both platforms. The exact Rust test also
passed from the same main archive in a disconnected Linux amd64
container as UID/GID 65534, with vendored locked dependencies and no
test changes. The [GW-108 research](gateway-replica-resilience.md)
records its full Rust suite on loopback-only Linux amd64.

```sh
cd services/gateway
RUST_TEST_THREADS=1 cargo test --locked --offline --lib \
  backend_tests::actual_connection_refusal_opens_only_its_backend_circuit -- --exact
```

The unchanged native assertion still blocks the required acceptance gate.
The gateway fixture belongs to the gateway lane. Keep the tested catalog
implementation local until the full gate can pass.

### Recheck after the gateway fixture repair

The exact refused-connection test passed natively on macOS after rebasing
onto `6f4e45b0`. All five selected OPS-104 tests passed with new private
PostgreSQL 18.6, pgvector 0.8.7 and VictoriaLogs 1.53.0 fixtures.
The catalog-only delivery worktree used the same catalog sources and
Go workspace entry. It passed race tests, vet, build and both offline
Docker import runs, including exact signed digest readback after retry.
The catalog research retains the original `bcb614f5` evidence and the
repeat on this baseline. The delivery scope excludes OPS-104, so its
component changes and real-store evidence remain on this lane.
