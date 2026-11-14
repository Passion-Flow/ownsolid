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

## Apply admission for full and incremental media

The read-only plan checks pending data recovery before reading candidate
media. The full upgrade currently prepares the archive first, and the
patch entry can reconstruct an archive before reaching the operation
lock. An absent or large candidate can therefore obscure the recovery
decision shown by the plan.

Read the typed site configuration once. Check the same pending restore
and coordinated backup records before staging a full archive or
reconstructing a patch. Recheck through the existing exclusive operation
lock before touching the installation. After patch reconstruction, check
again before unpacking the target. Keep the signed binding, patch
baseline, predecessor, configuration and host validation. The site
snapshot used to resolve the patch baseline also governs the apply step.
An early check is a refusal path. The lock continues to authorize later
steps and catches a record that appeared during verification.

Reuse the existing [kubeadm planning boundary](https://kubernetes.io/docs/reference/setup-tools/kubeadm/kubeadm-upgrade/)
and [GitLab recovery preparation](https://docs.gitlab.com/update/plan_your_upgrade/)
as comparisons. This change uses the same Go standard library and
installer contracts. It adds no dependency or data. Run all three native
Linux commands with protected recovery records and absent media. Assert
the exact refusal, no Docker invocation or prepared output, and the
preserved recovery records. Also verify that a clear site reaches its
existing media or signed-binding checks.

On disconnected Ubuntu 24.04 amd64, the predecessor's plan returned
the required recovery action. Its full upgrade reached the mounted
media check, and its patch returned the missing signed binding. The
updated binaries returned `INSTALL_RESTORE_PENDING` and
`INSTALL_BACKUP_RECOVERY_REQUIRED` from all three commands. No command
invoked Docker, left staging output or created an operation lock. Both
recovery records were preserved. After the records were removed, the
plan and full upgrade reached the media check and the patch reached the
signed-binding check. Only the recorder's IPv4, IPv6 and DNS probes
appeared. The authenticated upload and Console preflight remain separate
acceptance.
