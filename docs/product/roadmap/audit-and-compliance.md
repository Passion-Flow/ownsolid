# Audit and compliance

One audit trail from user to GPU, tamper evident, retained as regulations require, and
evidence packs for security assessments.

- Owns: `services/control/internal/modules/audit`, `licensing`.
- Depends on: identity-and-access, architecture-and-scale.

## P1 Enterprise release

- [ ] AUD-101 Unified audit log for people, services and the product agent (P1) - Catalog: F007
- [ ] AUD-102 Hash-chained audit records with an offline verifier (P1)
- [ ] AUD-103 One trace from user through app, gateway, model version and GPU, exportable (P1) - Catalog: F082
- [ ] AUD-104 Retention settings with a six-month minimum for network logs (P1)
- [ ] AUD-105 Admins cannot delete or alter operation logs (P1)
- [ ] AUD-106 Read-only auditor role and audit search (P1) - Catalog: F442
- [ ] AUD-107 Controlled data export with approval and record (P1) - Catalog: F488
- [ ] AUD-108 Diagnostic read access audited and redacted (P1)
- [ ] AUD-109 Encryption at rest for the platform database, object storage and backups with keys from an external KMS (KMIP, Vault-compatible transit, cloud KMS), envelope encryption, key rotation, and a TLS 1.3-only policy option (P1)

## P2 Fleet operations

- [ ] AUD-201 MLPS level 3 evidence pack (P2)
- [ ] AUD-202 SM2, SM3 and SM4 integration with the customer's cryptography services (P2) - Catalog: F525
- [ ] AUD-203 Content safety: keyword libraries with offline updates and output labeling (P2)
- [ ] AUD-204 Compliance certificate and qualification records (P2) - Catalog: F526
- [ ] AUD-205 GxP validation support pack per release: intended use, risk assessment, requirement-to-test traceability, installation and operational qualification protocols with executed records, for the customer's computerized system validation (P2)
- [ ] AUD-206 Electronic records and signatures for 21 CFR Part 11 and EU GMP Annex 11: signer name, time and meaning shown, re-authentication, signatures bound to their records, and audit trail review (P2)
- [ ] AUD-207 Health and personal data safeguards: classify projects and data sets holding such data, minimum-necessary access, access reports, and storage pinned to a region (P2)

Catalog rows absorbed: F007, F082, F442, F488, F512 to F527 (compliance parts), F525, F526.
