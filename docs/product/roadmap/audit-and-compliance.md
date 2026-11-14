# Audit and compliance

Module reviewed: 2026-11-07, against GitLab, GitHub and Kubernetes audit documentation ([research](../../research/audit-trail-page.md)). The review added AUD-111.

One audit trail from user to GPU, tamper evident, retained as regulations require, and
evidence packs for security assessments.

- Owns: `services/control/internal/modules/audit`, `licensing`.
- Depends on: identity-and-access, architecture-and-scale.

PostgreSQL audit integrity coverage begins when the hash-chain migration activates.
Earlier records remain readable outside that coverage. Verification against a database
owner or superuser requires a checkpoint captured before the suspected rewrite and kept
under the auditor's separate control. See the
[checkpoint capture and offline verification procedure](../../../tools/audit-verify/README.md#coverage-and-custody).

## P1 Enterprise release

- [ ] AUD-101 Unified audit log for people, services and the product agent (P1) - Catalog: F007
  - Scoped PostgreSQL commands write a distinct assistant actor and user subject.
    The [offline disclosure](../../../tools/audit-verify/README.md#verify-a-disclosed-assistant-actor)
    verifies both against the existing chain. The
    [opt-in Admin audit response](../../research/audit-list-assistant-projection.md#admin-http-representation)
    shows the verified actor. The [AGT-107 correlation fixture](../../research/product-assistant-audit.md#acceptance)
    read both Admin representations and confirmed that refused requests wrote no
    event. Production assistant registration, workspace-free navigation, confirmed
    writes and service-account identity (IAM-109) remain open.
- [x] AUD-102 Hash-chained audit records with an offline verifier (P1) - Evidence: [verification record](../../research/lane-e-decisions.md#repository-verification-on-a-shared-workstation) at c4f3a69. PostgreSQL 18.6 wrote 1,000 concurrent events and the disconnected verifier checked 1,003 exported events across two months.
- [ ] AUD-103 One trace from user through app, gateway, model version and GPU, exportable (P1) - Catalog: F082
- [ ] AUD-104 Retention settings with a six-month minimum for network logs (P1)
- [x] AUD-105 Admins cannot delete or alter operation logs (P1) - Evidence: [PostgreSQL authority and acceptance](../../research/audit-immutability.md) at 2410daa. The serving role could append but was refused updates, deletes and truncation.
- [ ] AUD-106 Read-only auditor role and audit search (P1) - Catalog: F442
- [ ] AUD-107 Controlled data export with approval and record (P1) - Catalog: F488
- [ ] AUD-108 Diagnostic read access audited and redacted (P1)
  - [Diagnostic consumer inventory](../../research/audit-diagnostic-access.md)
    preserves the implemented license export and identifies the identity and
    offline support collection access-accounting call sites still required.
  - [Exact workspace append contract](../../research/audit-diagnostic-access.md#scoped-append-contract)
    binds the installed tenant, authorized workspace and current local site/cell.
    IAM consumer pairing and the remaining diagnostic entry points stay open.
  - [Scoped PostgreSQL qualification](../../research/audit-diagnostic-access.md#scoped-postgresql-qualification)
    verifies the append under real scope, permission, transaction and concurrency
    failures. The actual workspace read pairing remains open.
- [ ] AUD-109 Encryption at rest for the platform database, object storage and backups with keys from an external KMS (KMIP, Vault-compatible transit, cloud KMS), envelope encryption, key rotation, and a TLS 1.3-only policy option (P1)
- [x] AUD-110 Audit trail page: events with filters by action, outcome, time and request, event details, shareable links (P1) - Evidence: 2c51002; real Chromium on an offline install on `ownsolid-cpu` read a real sign-in event with the person's address, filtered by family, followed it to its request and opened it from its own link, three runs in a row, 2026-11-07
- [ ] AUD-111 Stream audit events to an external SIEM over syslog or a webhook, with counters for events sent and dropped (P1)
  - [Operator TLS delivery](../../../tools/audit-verify/README.md#forward-a-verified-window-to-site-local-syslog)
    forwards verified windows with tenant scope, redaction and a durable
    cursor. Trusted service startup and approved site SIEM acceptance
    remain open.

## P2 Fleet operations

- [ ] AUD-201 MLPS level 3 evidence pack (P2)
- [ ] AUD-202 SM2, SM3 and SM4 integration with the customer's cryptography services (P2) - Catalog: F525
- [ ] AUD-203 Content safety: keyword libraries with offline updates and output labeling (P2)
- [ ] AUD-204 Compliance certificate and qualification records (P2) - Catalog: F526
- [ ] AUD-205 GxP validation support pack per release: intended use, risk assessment, requirement-to-test traceability, installation and operational qualification protocols with executed records, for the customer's computerized system validation (P2)
- [ ] AUD-206 Electronic records and signatures for 21 CFR Part 11 and EU GMP Annex 11: signer name, time and meaning shown, re-authentication, signatures bound to their records, and audit trail review (P2)
- [ ] AUD-207 Health and personal data safeguards: classify projects and data sets holding such data, minimum-necessary access, access reports, and storage pinned to a region (P2)

Catalog rows absorbed: F007, F082, F442, F488, F512 to F527 (compliance parts), F525, F526.
