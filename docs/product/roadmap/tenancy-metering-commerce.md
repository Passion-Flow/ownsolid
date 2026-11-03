# Tenancy, metering and commerce

Tenants and organizations, usage records, chargeback for enterprises, and commerce for
compute-center operators who resell capacity. The ownsolid platform license is separate
from how a customer bills its own tenants.

- Owns: `services/control/internal/modules/workspaces`, `licensing` (verification), metering (new).
- Depends on: identity-and-access, gateway, scheduling-and-quota.

## P1 Enterprise release

- [ ] TEN-101 Hierarchy: platform, tenant, department tree, project or workspace, with scope inheritance (P1) - Catalog: F466, F468
- [ ] TEN-102 Tenant lifecycle: create, suspend, resume, archive, delete with verified cleanup (P1) - Catalog: F467, F574
- [ ] TEN-103 Members, guests and cross-workspace sharing (P1) - Catalog: F469, F472
- [ ] TEN-104 Development, test and production environment separation (P1) - Catalog: F470
- [ ] TEN-105 Isolation across database, cache, object storage, queues and logs, with cross-tenant tests (P1) - Catalog: F474 to F476
- [ ] TEN-106 Usage ledger for GPU hours, tokens, storage and requests per tenant and project (P1) - Catalog: F231
- [ ] TEN-107 Rate cards, chargeback and showback reports (P1) - Catalog: F439
- [ ] TEN-108 Budgets with alerts and enforcement (P1) - Catalog: F429
- [ ] TEN-109 Prepaid credit and offline top-up (P1)

## P2 Fleet operations

- [ ] TEN-201 Operator storefront: product catalog of GPU instances and services (P2)
- [ ] TEN-202 Orders, invoices, vouchers and refunds (P2)
- [ ] TEN-203 White label per reseller (P2) - Catalog: F060
- [ ] TEN-204 GPU instances for tenants with SSH or Jupyter access (P2)
- [ ] TEN-205 Customer tickets with SLA levels (P2)
- [ ] TEN-206 Tenant export, migration and restore (P2) - Catalog: F477
- [ ] TEN-207 Ownership transfer on staff departure (P2) - Catalog: F471
- [ ] TEN-208 Service catalog with availability explanations (P2) - Catalog: F566

## P3 Multi-site

- [ ] TEN-301 National compute-network federation and cross-site settlement (P3)
- [ ] TEN-302 Supplier billing between sites and partners (P3)

Catalog rows absorbed: F060, F231, F429, F439, F466 to F477, F566, F574.
