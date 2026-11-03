# Snapshot writeback boundaries

`Copy` is a shared file-copy mechanism for the four existing snapshot owners.
It synchronizes each complete 8 MiB large-artifact chunk so an entire large file
does not remain dirty until its final synchronization. The size is the existing
artifact transfer quantum; it is neither a customer limit nor another runtime
configuration authority. File size, admission and ownership remain with callers.

The helper receives an already admitted file, checksum writer and bounded/context
reader. It owns no path, SQL, tenant, credentials, background process or cleanup.
Byte counts and read/write/synchronization errors propagate without retry. A
failure leaves its partial destination for the domain's existing recovery rule.
Callers retain source identity/hash/size checks, exclusive creation, final file
permissions/Sync/Close, directory synchronization, audit and publication.

The chunk barrier bounds dirty data from this copy; it does not bound the whole
process, clean page cache or container RSS. Keep real resource admission and
target verification. The controlled local comparison is evidence for lower dirty
writeback, not proof that all original OOM failures are fixed.

Focused controls: `go -C services/control test ./internal/platform/snapshotio`.
Actual large-file backup/restore and the complete M17 lifecycle require their
own source-bound image and isolated target evidence.
