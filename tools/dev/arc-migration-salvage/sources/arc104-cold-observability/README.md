# ARC-104 cold run observability

This package contains a verified small reproducer, logs and a compiled
Linux amd64 diagnostic cold kit. The tracked Control and PostgreSQL source
trees are unchanged. The cold kit has been compiled and checked, and its
installation and activation still need a Linux cold run.

## Reproduce the small test

With the cached PostgreSQL 18.6 arm64 image, Go dependencies and Docker
Desktop available, run the following commands from this directory.

```sh
bash reproduce.sh /absolute/path/to/lane-arc
python3 verify-traces.py "$(cat latest-run)"
```

The test uses one disposable PostgreSQL and one short-lived collector
sentinel, both with no Docker network. It seeds a synthetic joining member
by SQL and calls the real member handler over loopback TLS. The test
records the SQL backend PID, remaining request time, query and commit
outcomes, and PostgreSQL lock and connection events. The child case
kills an isolated member handler process and records the client's
transport error and the observed process exit. The collector sentinel
tests Docker lifecycle evidence separately.

## Diagnostic cold package

Rebuild with `bash build-cold.sh /absolute/path/to/lane-arc`. Both binaries
use their recorded Git sources. The Control build overlays the exact
original fixture source in a separate scratch export. The generated
diagnostic binary is intended for a read-only mount over the executable
in the disposable Control container after the signed installer completes.
The signed image and archive identities remain recorded. The resulting
cold experiment is tracked separately from signed runtime acceptance.

On an admitted `ownsolid-cpu` window, place a fresh copy of the contents of
`cold-kit` in its own private directory below
`/root/lane-arc104-registry.JFfDHO`. It reuses the earlier signed media,
Registry installer and Agent binaries from that runner. Run
`sha256sum -c SHA256SUMS` and then `bash run-session.sh` from the new
directory. The runner admits once, checks the actual container scopes,
and allocates new preparation and capture roots. It collects PostgreSQL
and Control logs before teardown, records Docker lifecycle events and
retains the wrapper status. HTTP 409 remains a failure. The collector
emits only selected lifecycle actions and excludes exec arguments.

Host admission rejected the diagnostic cold run twice. Full I/O PSI
avg10 was 33.95% at 03:32 UTC. At 03:41 UTC it was 0.23%, and the
gate found an active Go build. No cold run was launched with this kit.
