# AUD-105 operation log immutability

## Authority boundary

Platform administrators use the non-owner PostgreSQL serving principal. Their
audit API reads events. An application bug or compromised administrative
session should still be unable to update or delete those events using the
serving principal. The separate database operator owns maintenance and DDL.
An auditor validates that operator's actions with an independently held
AUD-102 checkpoint. PostgreSQL ownership and superuser privileges inherently
permit modifying or destroying an object.
[PostgreSQL privileges](https://www.postgresql.org/docs/18/ddl-priv.html)

CloudTrail separately protects log integrity with chained digest files. Its
validation model supports independent custody of verification material.
[AWS CloudTrail integrity validation](https://docs.aws.amazon.com/awscloudtrail/latest/userguide/cloudtrail-log-file-validation-intro.html)

## Chosen approach

Add a security-invoker BEFORE UPDATE OR DELETE row trigger to the PostgreSQL
audit parent. Deny mutation with SQLSTATE 42501 unless the effective database
role owns the affected table. PostgreSQL clones row triggers into existing
and future partitions. Reject non-owner TRUNCATE on the parent with a
statement trigger as well. Runtime admission already withholds the TRUNCATE
privilege and ownership. The additional trigger also protects direct mutation
if the general CRUD grants are broader than the table's policy. The serving
role receives no direct partition grants. PostgreSQL clones row triggers,
while a parent statement trigger protects operations naming the parent.
[PostgreSQL CREATE TRIGGER](https://www.postgresql.org/docs/18/sql-createtrigger.html)

The chain append trigger runs as its owner, so it can assign the new row's
hashes in the writer's transaction. Use the effective current user rather
than session_user for the ownership check. The partition maintenance function
can continue dropping whole expired partitions under its existing authority.
The service can invoke that separate retention function. Direct possession
of database credentials therefore still carries the existing maintenance
capability. The application audit interface cannot invoke it. A failed guard
must abort the statement and preserve the original row.
[PostgreSQL current user](https://www.postgresql.org/docs/18/functions-info.html),
[PostgreSQL partitioning](https://www.postgresql.org/docs/18/ddl-partitioning.html)

Revoking CRUD grants outright would be narrower but requires changing the
shared provisioning and upgrade contract that currently grants CRUD uniformly
to every application table. The trigger adds the policy at the audit storage
boundary. Relying on the HTTP handler alone would leave database writes
possible. An unconditional owner denial would block the existing chain writer
or require an authority signal the serving role could forge.

The new SQL uses existing PostgreSQL functionality under the
[PostgreSQL License](https://www.postgresql.org/about/licence/). The tests use
the existing pgx and UUID dependencies under MIT and BSD-3-Clause as recorded
in [the audit chain note](audit-chain.md). Commercial use, redistribution with
license notices and offline use are allowed. No new dependency, model or
data set is required.

## Acceptance

On PostgreSQL, use a non-owner with the same audit table grants as the runtime.
It can append a chain record and read the audit row. Updates to content or
hash columns, deletion and truncation fail without changing the row or tail.
Test direct writes to a partition and to a partition created after migration.
Verify the owner can run the existing partition retention path and that an
offline verifier still accepts the unaffected surviving sequence. Confirm the
migration runs over an existing chained installation and preserves its
records.
