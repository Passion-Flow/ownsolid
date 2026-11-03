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

## P2 Fleet operations

- [ ] AUD-201 MLPS level 3 evidence pack (P2)
- [ ] AUD-202 SM2, SM3 and SM4 integration with the customer's cryptography services (P2) - Catalog: F525
- [ ] AUD-203 Content safety: keyword libraries with offline updates and output labeling (P2)
- [ ] AUD-204 Compliance certificate and qualification records (P2) - Catalog: F526

Catalog rows absorbed: F007, F082, F442, F488, F512 to F527 (compliance parts), F525, F526.
