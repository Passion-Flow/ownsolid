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

An optional model drill uses the repository's pinned
[Qwen3-0.6B catalogue](../../api/agent/v1/model-catalog.json) and
[upstream Apache-2.0 licence](https://huggingface.co/Qwen/Qwen3-0.6B/blob/c1899de289a04d12100db370d81485cdf75e47ca/LICENSE).
The licence allows commercial use, redistribution and offline use under
its terms. Before staging, compare all nine files with the pinned sizes
and SHA-256 hashes. Import the USTAR archive through the same API as
Admin. After recovery, sign in, retrieve the ready model through that
API and hash every restored file on the target. This verifies model
readability from the recovered store. Inference is a separate runtime
flow.

## Linux amd64 acceptance

`TestSignedRestoreIsolatedDocker` passed in 875.53 seconds on Ubuntu 24.04
amd64 on `ownsolid-cpu` with Docker 29.1.3, Compose 2.40.3 and PostgreSQL
18.6 at schema 57. The temporary-signed single-node fixture
`lane-del-rel104-test` used archive SHA-256
`9692284eab0ea4f4ef3f67003a6f7fbfa514dbfd4b7b7ccabb6254278dc8fd37`
and manifest SHA-256
`8668b3d5e96199eac01584d1d691702a2219bedde414904b190a7ddf43720186`.
The source and both recovery sites used those same release bytes.

The private Docker daemon ran in an isolated Linux mount, network and
PID namespace. IPv4 and IPv6 default routes terminated at the recorder's
`egress0` interface. The recorder's two JSON lines were TCP to
`192.0.2.1:9` from the test host and TCP to `192.0.2.2:9` from control.
No other packet was recorded during installation, model import, backup,
the rejection cases, rollback or restore.

The source published the pinned Qwen3-0.6B archive and took both verified
logical backups. The legacy installer was built from `756269b3`. The
current encrypted backup carried the exact signed manifest digest. Each
backup's scratch restore verified 118 tables, 19 installation files
and nine model files. Between sites, the test stopped the preceding
Compose project and removed all four images from the private daemon.

The empty target rejected a truncated release, a corrupt database dump,
a different release digest, an unrelated age identity and modified
ciphertext. It had no bound release, Compose files, keys, PostgreSQL data,
containers or images after these checks. When control start was refused
after the PostgreSQL exchange, the original unbootstrapped database was
put back and the writer stayed stopped. The subsequent retry reported
`RESTORE_COMPLETE`, as did the encrypted restore on a separate empty
target.

Both targets served Admin and Portal over HTTPS and accepted the
recovered administrator's login. The test compared tenant, workspace
and administrator identity and the credential bytes. The Admin model
detail returned the ready model, and all nine files at revision
`c1899de289a04d12100db370d81485cdf75e47ca` matched their hashes on both
targets (1,519,207,673 bytes). This qualification covers one test release
and the model store read path. The production release gate stays open
until the exact production payload runs through the drill for every
release.
