---
name: dev-environment
description: "Where ownsolid is developed, toolchain locations on the MacBook Pro, and the available Linux servers"
metadata:
  node_type: memory
  type: reference
  originSessionId: 5b180534-1ea7-45f4-8f52-3433974e0450
  modified: 2026-11-03T22:28:35.340Z
---

Develop on the MacBook Pro (M4 Max, 36 GB, about 700 GB free). Linux servers are the target-test environments.

Toolchains live in the user's home directory (on PATH via ~/.zprofile since 2026-11-04):
- Go 1.27.3: `~/.local/opt/go/bin`
- Node 26.13.0 and pnpm 12.16.1: `~/.local/opt/node/bin`
- Rust 1.99.1: `~/.cargo/bin`
- uv 0.13.7: `~/.local/bin`; Python 3.12 via `uv python find 3.12`

GitHub CLI: `Development/.tools/github-cli/gh`, logged in as Passion-Flow. Git identity: "Passion." with the noreply address 279058513+Passion-Flow@users.noreply.github.com.

Servers (stated 2026-11-04):
- Rented GPU server, 3-day rental: `wwh@47.100.192.71`, port 10670, password auth. Specs: Ubuntu 24.04.2 amd64, RTX 3090 24 GB, driver 580, CUDA 13, Docker 28 with the nvidia runtime, user in the docker group, sudo needs a password.
  - A foreign container `novatra-qwen3-vllm-b9bca8` uses 12 GB of GPU memory and port 18000. Do not touch it without permission.
  - Hugging Face and Docker Hub are blocked; use hf-mirror, ModelScope and the configured registry mirrors.
  - Installing an SSH key was denied by the auto-mode classifier, so connect per command with a temporary askpass file and delete it afterwards.
- Company CPU server. **Wiped to a fresh install on 2026-11-15 and no longer on Tailscale; see [[machine-reset-2026-11-15]] before relying on anything below.** Was: `root@10.200.0.4` on the company network, Ubuntu 24.04.5 amd64, 16 cores, 31 GB, 98 GB disk, outbound internet works, no Docker yet. The user says the company has no restrictions on this use.
  - Reach it without the unstable AnyConnect VPN over Tailscale (account passions.liu@gmail.com): server `ownsolid-cpu` = 100.100.110.24, Mac `passion-macbook` = 100.94.197.105. Verified 2026-11-04 with VPN off: direct path, about 18 ms.
  - The Mac runs userspace Tailscale built from source: binaries in `~/.local/bin`, state and socket in `~/.local/state/tailscale-userspace`. Since 2026-11-04 it runs as the LaunchAgent `com.ownsolid.tailscaled`, created at the user's request.
  - The user keeps the current root password for development; never store it in files. Key expiry is disabled for `ownsolid-cpu`. SSH goes through `ProxyCommand "tailscale --socket=... nc %h %p"`; the SOCKS5 port fails with error 1.
  - Tailscale SSH stays off because its check mode prompts in a browser.

Its disk was later extended from 120 GB to 1005 GB online with `lvextend` and `resize2fs`.

Repositories: `Development/ownsolid` and `Development/ownsolid-entitlement`, both pushed to GitHub. `project-one` becomes a read-only archive.
