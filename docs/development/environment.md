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

`ownsolid-cpu` has no Docker or Go. Build test binaries on the workstation with
`GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c` and run them there. Offline packages
need a non-root user and a mounted temporary directory.

## Database integration tests

These tests need PostgreSQL 18.6 with pgvector 0.8.7, for example the image
`pgvector/pgvector:0.8.7-pg18-bookworm`, and a loopback database whose name ends in
`_test`. Run them serially: `go test -p 1`, because packages share the migration lock.
Set `PROJECT_ONE_TEST_DSN`. Add `-tags repository_integration` with
`PROJECT_ONE_REPOSITORY_ENGINE=postgresql`, `PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE`
and `PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA` for the repository tests, and use `umask 077`.

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
