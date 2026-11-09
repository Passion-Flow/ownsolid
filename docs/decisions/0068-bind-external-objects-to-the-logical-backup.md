# Bind external objects to the logical backup

Keep object recovery in a distinct backup format. Its final manifest
binds the existing logical backup inventory to a checked object inventory,
independent copy confirmation, source cluster and scoped operation. The
restore boundary requires the manifest digest and binding from the
coordinator's trusted durable state. Verify bytes again before the
coordinator may stage recovery.

Local backup and restore keep their current format. Provider exclusion,
copy confirmation and durable recovery are prerequisites for enabling
the external path. The local checkpoint does not acquire or release a
fence.

See the [object backup boundary](../research/object-backup-fence.md#installer-checkpoint-boundary).
