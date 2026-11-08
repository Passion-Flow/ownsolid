# DAT-106 dataset worker exit during Linux memory sampling

A repeated dataset clone workflow failed during initial publication and
clone inspection with `DATASET_PROCESSOR_MEMORY_MONITOR_FAILED`. On a
disk-backed disposable PostgreSQL fixture, instrumented samples showed
`State: R`, no `VmRSS`, and decimal task flags 4194316, which include
`PF_EXITING`. A separate fixture on a small temporary volume had entered
PostgreSQL recovery. Keep that failure separate from processor behavior.

[Linux procfs](https://github.com/torvalds/linux/blob/master/fs/proc/base.c)
returns ENOENT when a process status path has vanished. Its status reader can
return ESRCH when the process has been reaped since the descriptor was opened.
A Linux amd64 probe opened the status of a real CPython child, reaped it,
then read the descriptor and received ESRCH.

Linux [formats RSS only while the task has an address space](https://github.com/torvalds/linux/blob/v6.8/fs/proc/array.c).
The [exit path](https://github.com/torvalds/linux/blob/v6.8/kernel/exit.c)
sets [PF_EXITING](https://raw.githubusercontent.com/torvalds/linux/v6.8/include/linux/sched.h)
before releasing the address space and notifying the parent. The task can
still report a nonterminal state at that point. A Linux amd64 prototype
observed a real CPython child in state R with no RSS and PF_EXITING set,
then collected a successful wait. The
[proc stat contract](https://man7.org/linux/man-pages/man5/proc_pid_stat.5.html)
exposes this kernel bit in field 9. Linux 5.15 and 6.8 use the same bit.

Treat both missing-process results as eligible for bounded reconciliation
when a signal-zero check on the same PID also returns ESRCH. For a valid
status with no RSS and a nonterminal state, check the same PID's kernel flags
before permitting reconciliation. Keep missing flags, permission failures,
malformed RSS and unconfirmed live samples as monitor errors. Accepting a
missing sample as zero RSS would hide a live worker whose resource use
could no longer be checked. The controller still requires the owned Cmd.Wait
result and verifies the exit code, artifact integrity and processor result.

Verify the real descriptor race by opening the status of a CPython child
before reaping it and reading afterward. Verify the observed flags, a live
task with the exit bit clear, malformed flags and process names that contain
parentheses. Repeat dataset publication, clone and republication on real
PostgreSQL with a small number of CPU workers and external networking disabled.

The change uses the existing [Go standard library](https://go.dev/LICENSE),
which permits commercial use, offline operation and redistribution with its
notice. It adds no package, model, dataset or runtime service.
