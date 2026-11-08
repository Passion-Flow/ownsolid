# Restore from the signed release and backup

An empty single-node site has no installed release binding. Restore it using
the independently verified release and the verified logical backup in one
operation. Install the exact image identities from the release and put back
the source keys before preparing the database. Keep the checked PostgreSQL
database exchange and its interruption record as the promotion mechanism.

Record the signed manifest digest in new backups taken against a bound
release. A legacy backup without that field is admitted only when its
control and PostgreSQL image IDs and database schema match the signed
release. Retain the prior writer state on rollback. A failed first restore
must leave the original unbootstrapped database in place with the writer
stopped. Bind the interruption record to the installed configuration so
recovery works after the temporary release extraction is removed.

See [REL-104 research](../research/signed-release-recovery.md).
