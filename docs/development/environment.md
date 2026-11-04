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

## Telemetry store integration tests

The telemetry client has tests against the real stores, behind a build tag:

```sh
docker run -d --name vl -p 127.0.0.1:59428:9428 victoriametrics/victoria-logs:v1.53.0
docker run -d --name vm -p 127.0.0.1:58428:8428 victoriametrics/victoria-metrics:v1.155.0
OWNSOLID_TEST_LOGS_ADDRESS=http://127.0.0.1:59428 OWNSOLID_TEST_METRICS_ADDRESS=http://127.0.0.1:58428 \
  go -C services/control test -tags telemetry_integration ./internal/platform/telemetrystore/
```

Component tests against PostgreSQL also need the pinned `node_exporter` archives, 1.12.1
and 1.12.0, under `$PROJECT_ONE_STATE_ROOT/dependencies/components/node-exporter/<version>/linux-amd64/`.
Without them the tests stop at `COMPONENT_ASSET_UNAVAILABLE` before reaching their subject.

## End-to-end install

`tests/e2e/run-single-node.sh` runs the real installer in a Docker-in-Docker container
started with `--network none`, so the installer, its Docker daemon and every workload have
no route anywhere. Its default route is a TUN interface held by `egress-recorder`
([ADR 0008](../decisions/0008-zero-egress-harness.md)): every packet that tries to leave,
including DNS queries, is logged and refused, and the run fails listing each attempt.
It needs a Linux amd64 host with Docker and takes about two minutes.

```sh
make image-control DOCKER='ssh ownsolid-cpu docker' CONTROL_IMAGE=ownsolid/control:e2e
make e2e-binaries
scp bin/e2e/* tests/e2e/run-single-node.sh ownsolid-cpu:/root/e2e/
ssh ownsolid-cpu 'cd /root/e2e && bash run-single-node.sh /root/e2e ownsolid/control:e2e \
  pgvector/pgvector:0.8.7-pg18-bookworm victoriametrics/victoria-metrics:v1.155.0 \
  victoriametrics/victoria-logs:v1.53.0 docker:29.1.3-dind'
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

The certificate's public key is pinned, so the browser still refuses any other certificate.
The spec ignores the Mac's system proxy, which cannot reach a forwarded port.

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
