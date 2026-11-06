# Development environment

Where ownsolid is built and tested. Credentials never appear in this repository; ask the
project owner for them when a task needs a server.

## Workstation

The project owner's MacBook Pro (Apple M4 Max, 36 GB) is the main development machine.
Toolchains are installed in the home directory and are on `PATH` through `~/.zprofile`:

| Tool | Version | Location |
| --- | --- | --- |
| Go | 1.27.3 | `~/.local/opt/go/bin` |
| Node.js and pnpm | 26.13.0 and 12.16.1 | `~/.local/opt/node/bin` |
| Rust | 1.99.1 | `~/.cargo/bin` |
| uv and Python | 0.13.7 and 3.12 | `~/.local/bin`; `uv python find 3.12` |
| GitHub CLI | logged in as Passion-Flow | `../.tools/github-cli/gh` relative to this repository |

`make check`, `make build` and `make test` include the web workspace. Browser tests need
Chromium once per machine: `cd web/apps/console && pnpm exec playwright install chromium`.
The console and portal use ports 4310 and 4311 for `vite dev` and `vite preview`.

macOS links `/var` to `/private/var`, and some tests reject symlinked temporary paths;
`make test` exports a resolved `TMPDIR`. Docker Desktop is available for local
containers.

Object staging refuses writes that would leave less than 30% of its volume free, and its
tests stage in `TMPDIR`. When the workstation's disk is fuller than that (it was at 80% on
2026-11-06), `make test` fails in `internal/platform/objectstore` with `OBJECT_STORAGE_FULL`
and stops before the later modules. Free space, or run that package on a scratch volume and
the remaining modules, gateway and web tests by hand:

```sh
dev=$(hdiutil attach -nomount ram://1048576 | awk '{print $1}')
diskutil erasevolume HFS+ ownsolid-tmp "$dev"
(cd services/control && TMPDIR=/Volumes/ownsolid-tmp/ go test -count=1 ./internal/platform/objectstore/)
hdiutil detach "$dev"
```

## Test servers

| Server | Purpose | Access |
| --- | --- | --- |
| Company CPU server `ownsolid-cpu` | Permanent Linux amd64 test environment: Ubuntu 24.04, 16 cores, 31 GB, 98 GB disk, outbound internet | Tailscale address `100.100.110.24`, user `root` |
| Rented GPU servers | Short-term real GPU checks | Provided by the owner when needed |

The company server is reached through Tailscale instead of the company VPN. The Mac
runs userspace Tailscale built from source:

- Binaries in `~/.local/bin`, state in `~/.local/state/tailscale-userspace`.
- `~/.ssh/config` defines `Host ownsolid-cpu` with a `ProxyCommand` through
  `tailscale --socket=<state>/tailscaled.sock nc %h %p`. Use `ssh ownsolid-cpu`.
- The daemon runs as the launch agent `com.ownsolid.tailscaled`
  (`~/Library/LaunchAgents/com.ownsolid.tailscaled.plist`). It starts at login and
  restarts if it exits. Check it with `launchctl print gui/$(id -u)/com.ownsolid.tailscaled`.
- Browsers cannot reach tunnel addresses directly; use SSH port forwarding.
- `ssh ownsolid-cpu` logs in with the key `~/.ssh/ownsolid_cpu_ed25519`, set in `~/.ssh/config`.

`ownsolid-cpu` runs Docker 29.1.3 and Compose 2.40.3 from the Ubuntu packages and has no
Go. Docker Hub pulls from it work but can stall or drop, so retry them; `timeout` around a
pull keeps a stalled one from blocking a session. Build test binaries on the workstation with
`GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c` and run them there. Offline packages
need a non-root user and a mounted temporary directory.

## Database integration tests

These tests need PostgreSQL 18.6 with pgvector 0.8.7, for example the image
`pgvector/pgvector:0.8.7-pg18-bookworm`, and a loopback database whose name ends in
`_test`. Run them serially: `go test -p 1`, because packages share the migration lock.
Set `PROJECT_ONE_TEST_DSN`. Add `-tags repository_integration` with
`PROJECT_ONE_REPOSITORY_ENGINE=postgresql`, `PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE`
and `PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA` for the repository tests, and use `umask 077`.

## Agent call cost test

`make test-agent-cost` measures every agent call against its declared cost on a small and
a large fleet ([ADR 0020](../decisions/0020-agent-call-cost-rule.md)) and takes about ten
seconds. It needs a superuser in `PROJECT_ONE_TEST_DSN`, because it loads `auto_explain`,
and `PROJECT_ONE_DATA_PYTHON` set to a Python 3.12 binary as a real path. Its log prints
statements, blocks and rows of each exchange. When a change raises a cost on purpose, raise
the declaration in `api/agent/v1/calls.go` in the same change and say why.

## Free disk space

`internal/platform/objectstore` refuses to stage below 30% free on the filesystem of `TMPDIR`, so
its tests fail with `OBJECT_STORAGE_FULL` on a fuller disk and `make test` stops there. Free space,
or run that package with `TMPDIR` on a RAM disk and the rest of `make test` as usual.

## Telemetry store integration tests

The telemetry client has tests against the real stores, behind a build tag:

```sh
docker run -d --name vl -p 127.0.0.1:59428:9428 victoriametrics/victoria-logs:v1.53.0
docker run -d --name vm -p 127.0.0.1:58428:8428 victoriametrics/victoria-metrics:v1.155.0
OWNSOLID_TEST_LOGS_ADDRESS=http://127.0.0.1:59428 OWNSOLID_TEST_METRICS_ADDRESS=http://127.0.0.1:58428 \
  go -C services/control test -tags telemetry_integration ./internal/platform/telemetrystore/
```

`TestComponentLogsThroughARealStore` in `internal/modules/components` uses the same tag and
also needs the PostgreSQL settings above: settled tasks write through the queue to the real
log store, and the reader confirms each record against PostgreSQL.

Component tests against PostgreSQL also need the pinned `node_exporter` archives, 1.12.1
and 1.12.0, under `$PROJECT_ONE_STATE_ROOT/dependencies/components/node-exporter/<version>/linux-amd64/`.
Without them the tests stop at `COMPONENT_ASSET_UNAVAILABLE` before reaching their subject.

## Liveness replica integration tests

The store gateways share sightings through has tests against three real NATS servers with TLS
and an NKey, started with Docker by the test itself, which also removes them:

```sh
go -C services/control test -tags liveness_integration ./internal/platform/liveness/
```

It takes about 40 seconds, needs `nats:2.12.1-alpine` loaded, and stops one server to show that
the bucket and the service survive it. Docker must be able to mount `TMPDIR`.

## End-to-end install

`tests/e2e/run-single-node.sh` runs the real installer in a Docker-in-Docker container
started with `--network none`, so the installer, its Docker daemon and every workload have
no route anywhere. Its default route is a TUN interface held by `egress-recorder`
([ADR 0008](../decisions/0008-zero-egress-harness.md)): every packet that tries to leave,
including DNS queries, is logged and refused, and the run fails listing each attempt.
The same run starts the agent's egress guard test on that isolated Docker host
([ADR 0017](../decisions/0017-workload-egress-guard.md)): a probe container on a plain
bridge must reach the recorder, and the same probe on the agent's guarded inference
network must leave no trace. It needs a Linux amd64 host with Docker.

```sh
make image-control DOCKER='ssh ownsolid-cpu docker' CONTROL_IMAGE=ownsolid/control:e2e
make e2e-binaries
scp bin/e2e/* tests/e2e/run-single-node.sh ownsolid-cpu:/root/e2e/
ssh ownsolid-cpu 'cd /root/e2e && bash run-single-node.sh /root/e2e ownsolid/control:e2e \
  pgvector/pgvector:0.8.7-pg18-bookworm victoriametrics/victoria-metrics:v1.155.0 \
  victoriametrics/victoria-logs:v1.53.0 docker:29.1.3-dind'
```

The run includes a simulated fleet ([agent simulator](../../tools/dev/simulator/README.md)):
the test raises the fleet limits with `apply`, mints join tokens, enrolls
`OWNSOLID_E2E_SIMULATED_AGENTS` agents (default 2,000) from one process and runs them for four
minutes through a disconnect, a partition, lost replies, lost cursors and a restart while it runs
tasks on every twentieth node, then restores the default limits. That adds about five minutes;
a whole run took 13 minutes on `ownsolid-cpu`.

When the host's disk is nearly full, keep the run in memory: `OWNSOLID_E2E_SCRATCH=/dev/shm`
puts the image archive there and `OWNSOLID_E2E_DOCKER_TMPFS=12g` gives the isolated Docker
daemon a tmpfs for its storage. `ownsolid-cpu` has the memory for both. If the control image
cannot be built there for lack of space, build it on the workstation and load it over SSH:

```sh
make image-control DOCKER='env DOCKER_DEFAULT_PLATFORM=linux/amd64 docker' CONTROL_IMAGE=ownsolid/control:e2e
docker save ownsolid/control:e2e | ssh ownsolid-cpu docker load
```

`make test` skips this test without those inputs.

## Browser test against an installation

`web/apps/console/e2e/live.spec.ts` signs in and out in real Chromium, through an
installation's own TLS, cookies, CSRF checks and Content-Security-Policy. Install on
`ownsolid-cpu` with a `project`, `install_root`, `network.https_port` and
`network.state_subnet` of your own, copy `operator/admin-credential` to a private file,
forward the port with `ssh -N -L <port>:127.0.0.1:<port> ownsolid-cpu`, then:

```sh
spki=$(openssl s_client -connect 127.0.0.1:<port> -servername <admin host> </dev/null 2>/dev/null \
  | openssl x509 -pubkey -noout | openssl pkey -pubin -outform der \
  | openssl dgst -sha256 -binary | base64)
cd web/apps/console
OWNSOLID_LIVE_ORIGIN=https://<admin host>:<port> OWNSOLID_LIVE_EMAIL=<administrator> \
  OWNSOLID_LIVE_PASSWORD_FILE=<credential file> OWNSOLID_LIVE_SPKI="$spki" \
  OWNSOLID_LIVE_HOST_RULES='MAP <admin host> 127.0.0.1' pnpm exec playwright test live
```

`live-tasks.spec.ts` needs a real agent as well: it stops the agent so that a task is known to wait,
leaves the page, starts the agent and checks the console tells how the task ended. Enroll an agent with
a `read_only` join token, run it in a container on the host network so the host needs no changes, and
name how to stop and start it:

```sh
ssh ownsolid-cpu docker run -d --name <agent> --network host --add-host <admin host>:127.0.0.1 \
  -v <directory>:<directory> -v <agent binary>:/usr/local/bin/agent:ro debian:bookworm-slim \
  agent run --config <directory>/agent.json
OWNSOLID_LIVE_AGENT_STOP='ssh ownsolid-cpu docker stop -t 5 <agent>' \
  OWNSOLID_LIVE_AGENT_START='ssh ownsolid-cpu docker start <agent>' \
  <the variables above> pnpm exec playwright test live-tasks
```

`live-nodes.spec.ts` adds a node from the console and runs the commands the page shows, as written, on a
disposable host: a container on the host network with `sudo` as a pass-through, the agent binary at
`/opt/agent` and the installation's CA at `/opt/ca.pem`. Name a shell on it, which reads its script from
standard input. A license must be imported, or the agent's enrollment is refused:

```sh
ssh ownsolid-cpu docker run -d --name <host> --network host --add-host <admin host>:127.0.0.1 \
  -v <agent binary>:/opt/agent:ro -v <install root>/config/ca.pem:/opt/ca.pem:ro \
  debian:bookworm-slim sleep infinity
ssh ownsolid-cpu docker exec <host> sh -c 'printf "#!/bin/sh\nexec \"\$@\"\n" > /usr/local/bin/sudo; chmod 755 /usr/local/bin/sudo'
OWNSOLID_LIVE_NODE_SHELL='ssh ownsolid-cpu docker exec -i <host> sh -s' \
  <the variables above> pnpm exec playwright test live-nodes
```

`live-users.spec.ts` invites someone, reads the invitation from a mail server, accepts it in a second
browser, and disables and enables the new account. It needs an SMTP server with TLS whose messages it
can read. `tools/dev/smtp_sink.py`, a test sink that stores each message as a file, runs on the installation's state network
under the SMTP host name; point the install configuration at it (`port: 465`, `tls_mode:
implicit_tls`, `ca_file` for its own test CA, made with RSA keys and SHA-256, since TLS clients refuse
EC keys with explicit parameters), apply, and name a command that prints every stored message:

```sh
ssh ownsolid-cpu docker run -d --name <sink> --network <project>_state --ip <free address> \
  --network-alias <smtp host> -v <sink directory>:/sink python:3.12.15-slim-bookworm \
  python3 /sink/smtp_sink.py /sink/out /sink/sink.pem /sink/sink.key
OWNSOLID_LIVE_MAIL_COMMAND='ssh ownsolid-cpu "cat <sink directory>/out/*.eml 2>/dev/null || true"' \
  <the variables above, with the portal host in the host rules> pnpm exec playwright test live-users
```

The certificate's public key is pinned, so the browser still refuses any other certificate.
The spec ignores the Mac's system proxy, which cannot reach a forwarded port.
The spec signs in once with a wrong password, so a few runs in a row against the same
account trip the installation's sign-in rate limit; the failure then says how many
seconds to wait.

The spec also renames the account, changes its password and changes it back; a failure in
between leaves the changed password on the account. It checks the page shows no providers
unless `OWNSOLID_LIVE_PROVIDER_NAME` is set. With that, `OWNSOLID_LIVE_PROVIDER_ORIGIN`,
`OWNSOLID_LIVE_PROVIDER_USER` and `OWNSOLID_LIVE_PROVIDER_PASSWORD_FILE` it links and unlinks a
real Gitea. `ownsolid-install` cannot enable single sign-on yet (IAM-113), so that run adds
an `sso` section to the installation's `control.yaml` by hand and restarts control; `apply`
removes it again. The issuer is Gitea's root URL with no trailing slash, the release is the
one `GiteaTestedRelease` names, and a live run spends about four of the ten sign-ins allowed
in 15 minutes. Add the provider's public key to `OWNSOLID_LIVE_SPKI` (comma-separated) and
its host to `OWNSOLID_LIVE_HOST_RULES`.

`live-license.spec.ts` refuses real signed files and imports a real renewal, so each run adds one
to the license history. It needs test files signed with the key the installation trusts.
`OWNSOLID_LIVE_LICENSE_COMMAND` reads the license state as JSON on standard input and prints a JSON
object with `renewal`, `otherDeployment`, `otherIssuer`, `changed` and `addsNoTime`. Keep that
command out of the repository, which holds no signing code: write it against `license.Issue` in a
scratch module and run it where the test key lives.

`live-overview.spec.ts` checks the overview's figures against the API and follows a real offline node
to its drawer. Stop a joined node's agent so that it goes offline, leave the node active, and run it
with `OWNSOLID_LIVE_OFFLINE_NODE=true` and the variables above.

`live-idle.spec.ts` waits out a real idle timeout with the page's poll running, so it needs a
short one. Add this to the installation's `control.yaml`, restart control, and run it with
`OWNSOLID_LIVE_IDLE_SECONDS=60 OWNSOLID_LIVE_WARNING_SECONDS=30`. It takes about three minutes,
and `apply` removes the section again:

```yaml
runtime:
    identity:
        admin_idle_seconds: 60
        session_touch_seconds: 15
        session_warning_seconds: 30
```

The tunnel can drop to about 20 KB/s, and pages then time out. Run the spec on
`ownsolid-cpu` instead, with `--network host` in a `node` container that installs
`@playwright/test` and `playwright install --with-deps chromium`. The server cannot pull
`mcr.microsoft.com/playwright`: the connection is reset.

## Factory tests on Linux

`make test-offline HOST=ownsolid-cpu` builds the Linux test binaries here and runs them on
that host as `nobody` with a mounted tmpfs. It takes a few minutes.

## Network notes for China

Hugging Face and Docker Hub are blocked from mainland servers. Use hf-mirror or
ModelScope for model files and configured registry mirrors for images. BuildKit can hang
resolving base-image metadata through mirrors; pre-pull the base image or use the
classic builder. Product installations never download at runtime; this applies only to
development machines.

## Real-GPU results to repeat on ownsolid

On 2026-11-04, an RTX 3090 (Ubuntu 24.04, driver 580, CUDA 13) ran the inherited code:
the pinned vLLM 0.31 image served Qwen3-0.6B with the agent's hardening flags, offline
LoRA training produced a working adapter, and the adapter scored 4 of 4 on held-out
prompts against 0 of 4 for the base model. The gaps found are roadmap items DEL-003,
MOD-104 and SRV-104.
