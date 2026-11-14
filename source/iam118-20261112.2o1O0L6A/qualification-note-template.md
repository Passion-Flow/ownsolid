## Qualification boundary

The schema 59 candidate passed `make check` and `make build`. The full test
stages passed with standard toolchain `umask 022`. The continuation used
`make -o test-web-browser test`: its browser stage reuses the exact unchanged
Web, OpenAPI, code-generator, capability and Makefile inputs already tested in the first
run. Those real Chromium suites passed 1,075 Console and 389 Portal cases,
with 37 installation-dependent cases skipped. Go, gateway and Web unit stages
ran in the continuation; no assertion or integration test was disabled.

The earlier full run under `umask 077` stopped in the unchanged delivery test
`TestRestorePutsBackAMissingModelBeforeTheDatabaseChanges`. Its helper's
`os.WriteFile(...,0444)` creates an original `0400` file with that umask;
restore preserves the manifest mode, while the assertion expects `0444`.
Changing only the ambient umask to `022` passed the targeted control. Delivery
owns making the fixture's permissions deterministic. The original failure is
retained and is not relabeled as a passing run.

All nine diagnostic PostgreSQL HTTPS cases passed on schema 59, including
known foreign tenant records and unchanged intent postimages. They cover
persisted minimized identity and session/binding preimages, exact pending
retry, current Admin session, CSRF/scope refusals, provider denial and fresh
retry, audit rollback, parallel replay, actor revocation, expiry and scoped
cleanup, connector changes, and canceled exchange with terminal secret erasure.
The SSO and SSO HTTP unit packages also passed. Candidate SQL stays unnumbered.

`TestConnectorSignInTestActualGiteaHTTPS` passed through verified private TLS,
actual Gitea password authentication, consent and S256 code exchange. It
compared the stable subject/login with an independent actual provider read
and verified no local binding, session creation or login-cookie rotation.
`TestSSOActualGiteaRecoveryAndDeviceRevocationHTTPS` also passed. Fixtures used
PostgreSQL 18.6, pgvector 0.8.7 and pinned Gitea 28.1.0 in network-none Linux
namespaces on the workstation, with no public host port or external provider.
The Gitea amd64 image used workstation emulation. Explicit private ownership
of the copied test program permits the non-root fixture runner; the earlier
pre-execution permission failure is retained separately.

The qualified runtime/test input is @INPUT@. Durable evidence is under
`iam118-20261112.2o1O0L6A`, with the first `gate-source59` browser results,
`qualification-source59` native results, the corrected actual-provider retry,
and `gate-resume-current3` native requalification and continuation commands and raw exit codes. Owned
fixtures were removed and the scratch volume detached. Verification text
was updated after the gate; product source did not change.

This is not native Linux amd64 installation, formal migration/restore
admission or rendered IAM-118 UI acceptance. The complete item remains open
until the shared production boundaries above are admitted.
