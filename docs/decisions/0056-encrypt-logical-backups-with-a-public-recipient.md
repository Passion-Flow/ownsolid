# Encrypt logical backups with a public recipient

Wrap the installer’s verified logical backup in a streaming age archive.
Accept an explicit native X25519 public recipient file at backup time and an
explicit private identity file at restore time. The recovery operator holds
the identity outside the installation and the archive directory. Keep the
identity off the backup host whenever recovery operations permit.

Stage cleartext inside the root-private installation workspace. Publish the
encrypted file in a separate private directory using a synced, no-replace
link. During restore, authenticate the entire stream and validate the existing
backup manifest before starting the database restore. The recorded database
exchange still governs promotion and interruption recovery.

See [REL-110 research](../research/encrypted-backups.md).
