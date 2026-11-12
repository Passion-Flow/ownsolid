# Backup repository path admission (REL-103)

The installer requires backup output outside `install_root`, so removing an
installation does not also remove its recovery point. The shared admission
serves manual and scheduled logical backups, upgrade recovery points and the
PITR repository. Its current lexical prefix check misses a symlink in an output
ancestor and an installation configured through a different symlink spelling.

[Go 1.27.3 filepath](https://pkg.go.dev/path/filepath@go1.27.3#EvalSymlinks)
distinguishes lexical cleaning from resolving links. Resolve the existing
output and installation root before repeating the containment comparison.
Keep the existing lexical rejection and final-component symlink refusal. A
genuinely external output remains valid when its parent has another spelling.
An unresolvable root fails clearly. If the installation root does not yet
exist, retain the existing lexical admission: an existing output cannot be its
descendant, and callers still perform their own installation admission.

[pgBackRest's repository configuration](https://pgbackrest.org/configuration.html#section-repository)
similarly keeps repository storage and PostgreSQL data paths explicit and
requires retained backup dependencies for recovery. It is a reference, not a
new dependency. A string that looks external does not establish independent
storage. This check covers stable symlink aliases in the host namespace;
operators still keep paths and mounts fixed during operations and select
storage for their required disk and site failure domains. It does not claim
bind-mount discovery or protection against privileged concurrent remounts.

No dependency, model, dataset or copied upstream code is added. The Go standard
library uses [BSD-3-Clause](https://go.dev/LICENSE), permitting commercial use,
offline operation and redistribution with its notices. Existing database-aware
`pg_dump`/`pg_restore` and native PostgreSQL PITR mechanisms remain unchanged.

Reproduce output-parent aliases to the installation and a nested model
directory, a differently spelled installation root and a direct alias of that
root. Refuse every resolved internal output before Docker or publication.
Accept a resolved external output and preserve existing missing-root behavior.
Cover a symlink loop with a stable actionable error. Run filesystem regressions
on Linux amd64 and the install package tests; these checks do not replace a
disconnected logical backup/restore drill or close the cross-store item.

The actual installer-package regressions ran on Linux amd64 as UID/GID 65534
inside private network, PID and mount namespaces. The original implementation
admitted all four internal aliases and the looping root; its run exited 1 as
expected. The corrected binary passed all three selected roots without skips
in 0.066 seconds. The namespace had only its down loopback interface and no
routes. The Mac install-package suite also passed in 29.215 seconds, followed
by vet and a Linux amd64 cross-build. Native tests used only owned temporary
directories and joined their children before removing the fixture. They did
not start Docker, PostgreSQL, a service or a host port, or qualify a new packet
recorder or installed backup flow.
