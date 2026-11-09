# Share the upgrade preflight decision

DEL-113 will present the same admission state in the Console and the
installer CLI. [kubeadm upgrade plan](https://kubernetes.io/docs/reference/setup-tools/kubeadm/kubeadm-upgrade/)
checks the current deployment before applying an upgrade and can use an
explicit offline version. [GitLab's pre-upgrade procedure](https://docs.gitlab.com/update/plan_your_upgrade/)
separates checking the required stops and recovery points from running the
upgrade. The ownsolid installer already emits a JSON plan and performs the
real mutation through `upgrade-bundle`. Keep that contract.

A protected pending restore or coordinated backup prevents the mutating
installer from taking its operation lock. The current plan checks only an
upgrade record. It can report an upgrade plan while an interrupted restore
or fenced backup requires recovery. Reuse the existing data recovery gate
before opening candidate media and check it again under the planner's
existing shared installation lock. This lets recovery proceed during a
large candidate verification and detects a record published in the meantime.
Use the gate's original stable errors and action. A symlink or inaccessible
record remains pending, as on the write path. An absent record does not
authorize media or a new deployment by itself. The existing signed media,
installed binding, and live schema checks still apply. The change adds no
dependency, model or dataset and reuses the existing Go standard library
under its [BSD 3-Clause licence](https://go.dev/LICENSE).

Verify the normal native `upgrade-plan` command with held recovery
records and missing media in an isolated Linux namespace. It must emit
the recovery error without invoking Docker or reading the candidate.
With those records removed, the media verifier must run as before.
The existing signed upgrade test also exercises the same gates with a
real signed candidate. The Console upload, authenticated API and shared
preflight screen are separate owner dependencies and still need installed
acceptance.

## Disconnected command check

On 2026-11-09, the native Linux amd64 installer ran on `ownsolid-cpu`
as an unprivileged user in a disconnected namespace. With a pending
restore and absent media, the original binary reported only the media
verification failure. The updated binary reported `INSTALL_RESTORE_PENDING`
and `INSTALL_BACKUP_RECOVERY_REQUIRED` with their respective records.
Neither case emitted a plan, invoked Docker or created an operation lock.
The records stayed in place. After removing the fixture records, the
same command returned the media verification failure again.

The recorder saw only the IPv4, IPv6 and DNS probes. This fixture exercised
the native CLI refusal and admission order with absent media. The
signed upgrade Compose test retains coverage for a valid candidate.
