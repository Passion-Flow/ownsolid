# Recover a signed release on an empty target (REL-104)

The signed installer uses an independently trusted release to load the exact
Linux amd64 images and bind their Compose file. The ordinary restore command
uses an installed binding when site YAML omits those inputs. A new target
has no binding. Applying the release first would bootstrap unrelated keys,
which the restore correctly refuses.

Use one restore entry point that takes the signed bundle, its verification
policy, site YAML and a checked backup. Reuse release verification and
tagless image import. Reuse the existing logical dump verification,
protected file restore and recorded database exchange. Admit the backup
before writing a binding or loading images. Newly written signed logical
backups record the signed manifest digest. Version 1 backups without it
can be recovered when both image digests and the database schema match
the verified release. They have less release identity evidence. Keep the
operator-only recovery identity outside both the target and archive.
Applying then overwriting freshly generated secrets would mix two
installations. A separate database import would duplicate the checked
exchange and its rollback logic.

[PostgreSQL logical backups](https://www.postgresql.org/docs/18/backup-dump.html)
and [pg_restore](https://www.postgresql.org/docs/18/app-pgrestore.html)
describe the portable logical path. [GitLab recovery](https://docs.gitlab.com/administration/backup_restore/restore_gitlab/)
requires a compatible release and separately restored configuration keys.
The existing [age v1 format](https://age-encryption.org/v1) authenticates
encrypted backup bytes. This change adds no library, model or dataset.
PostgreSQL uses the [PostgreSQL License](https://www.postgresql.org/about/licence/).
The existing [encrypted backup inventory](encrypted-backups.md#licensing)
documents commercial use, redistribution and offline use of the Go
dependencies. A staged model needs its existing release and catalogue
licence evidence before it can be used in the drill.

## Failure and acceptance

- Run a source and empty targets serially under one private Linux amd64
  Compose daemon. Use the same byte-exact temporary-signed release. Take
  and verify a legacy logical and an encrypted backup. Confirm the
  original administrator, tenant data and both HTTPS entrances on the
  targets. If a release-qualified model is staged, check its restored
  files and availability and report that scope separately.
- Reject a corrupt or wrong-key input, an untrusted bundle, and a backup
  for a different release before loading images or preparing database
  state. The encrypted check can allocate private staging under the
  installation lock. It must clear the staging and leave the empty
  target without a release binding, keys or services.
- Inject an error at the real writer start on a fresh target after the
  database exchange. The original empty database remains intact and the
  writer stays stopped. Retrying from the same signed inputs should
  recover the target. Use the existing restore record if the exchange
  itself is interrupted. Existing services retain the installed release
  and their original database when admission fails.
- Prove both host and running workload probes reach the egress recorder
  and fail the drill on any other attempt. Record the tested package
  hash and remove only the fixture's services and data. Keep the
  production, every-release gate open until the exact production
  payload and release pipeline are qualified.
