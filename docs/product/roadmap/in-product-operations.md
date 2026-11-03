# In-product operations

Customers do every operation inside ownsolid instead of logging in elsewhere. This module
covers the operations capabilities of 1Panel and Rancher, built natively.

1Panel is a feature reference only. It is never embedded, forked, shipped or offered as
an app, no code is copied, and implementers work from public documentation and observed
behavior, not its source.

- Owns: `services/agent` host operations, `services/control` operations APIs, console pages.
- Depends on: identity-and-access, audit-and-compliance, fleet-and-infrastructure.
- Security: every terminal and file action runs through the outbound agent connection,
  never through inbound SSH, under the user's permissions, and is audited.

## P1 Enterprise release

- [ ] OPS-101 Host web terminal through the agent, no inbound SSH; per-role enablement (P1)
- [ ] OPS-102 Kubernetes shell with user impersonation, and container exec (P1)
- [ ] OPS-103 Session recording with replay, force-terminate and idle timeout (P1)
- [ ] OPS-104 Live logs for platform services, pods, containers and node services through a server-side log hub with per-user limits (P1)
- [ ] OPS-105 Scoped file manager for nodes and volumes: browse, upload, download, edit with diff, audited (P1)
- [ ] OPS-106 Containers, images, volumes, networks and Compose projects on hosts (P1)
- [ ] OPS-107 Kubernetes resource browser with YAML view and edit, diff, validation and events (P1)
- [ ] OPS-108 Processes, services, ports, disks, mounts, kernel and driver versions per node (P1)
- [ ] OPS-109 Scheduled tasks with history and output (P1)
- [ ] OPS-110 Firewall rules view (P1)
- [ ] OPS-111 SSH key and access view per node (P1)
- [ ] OPS-112 Backups of platform and app data to S3, SFTP and local targets, with restore (P1) - Catalog: F204, F507
- [ ] OPS-113 Batch commands across many nodes with staged rollout, stop on failure and per-node output (P1)
- [ ] OPS-114 System status per node: CPU, memory, disk, network, GPU live charts (P1)

## P2 Fleet operations

- [ ] OPS-201 Firewall and SSH configuration changes through policy (P2)
- [ ] OPS-202 Database consoles for PostgreSQL, MySQL and Redis (P2)
- [ ] OPS-203 Command-level host capture with eBPF; just-in-time elevation; moderated sessions (P2)
- [ ] OPS-204 Certificate management for platform and app endpoints (P2)
- [ ] OPS-205 Disk and mount management (P2)
- [ ] OPS-206 Remote file transfer between nodes and object storage (P2)

## P4 Breadth

- [ ] OPS-401 Virtual machines (KVM) on hosts (P4)
- [ ] OPS-402 Web hosting features: websites, reverse proxy, PHP runtimes, FTP, antivirus, WAF (P4, may be dropped)
