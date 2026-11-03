# Native cumulative MVP PostgreSQL requests

The two examples select the exact `mvp-postgresql-cpu-v1` lineage29/108. Every
identifier/hash is synthetic; materialize them from the actual selected signed
release, maintenance plan and installed source. `application_profile` is a
required explicit field for this profile. Omitted legacy requests still mean
releases22 and cannot auto-detect the new lineage.

Use the existing fully commented [backup policy env mapping](../kubernetes/postgres-backup.env.example)
or [restore policy env mapping](../kubernetes/postgres-restore.env.example) only
through `--policy-env-file`. Those existing typed policies own paths, exact
native tool hashes/build, deadlines, byte/file bounds and free-byte reserve.
Application selection is request/configuration input, not a policy env override.
The runtime env file is an explicit optional request reference and must stay
inside the backed-up private configuration projection. DSNs/secrets are protected
references in the existing typed Control configuration, outside that projection.

The MVP native integration uses the original registry operator exporter/importer
for an additional `artifacts/imageregistry` payload. It requires the complete typed
registry configuration, protected credential/CA/trust projection and a separate
fresh target CAS; it does not add a fifth domain store. Do not remove integration
configuration or rows to bypass a missing payload. Follow the [profile runbook](../../../docs/runbooks/postgresql/mvp-postgresql-cpu-v1-native-recovery.md)
for ownership, native readback and pending execution boundaries.
