# Prepaid balance authority

TEN-109 needs a verifiable source for each balance increase and a durable
transaction for each debit. OpenMeter records grants for a specific customer
entitlement and applies usage to eligible grants. Stripe distinguishes credit
grants from invoice adjustments and cash balances. Those systems retain an
explicit grant record before applying usage.
[OpenMeter grants](https://openmeter.io/docs/billing/entitlements/grant)
[Stripe billing credits](https://docs.stripe.com/billing/subscriptions/usage-based/billing-credits)

The current ownsolid contracts and installation catalog have usage events,
rate cards for internal estimates and the platform license verifier. They
have no top-up authority, approved offline grant format, credit ledger or
balance debit transaction. A workspace owner or platform administrator
read session grants access to metering reports. The platform license
verifies product entitlement. Neither supplies an authorized credit
increase or a settled source for a debit.

Before adding writes, the commerce owner must supply an authenticated
grant source or verifiable offline authorization with an issuer, unique
grant identity, authorized tenant or workspace, amount, currency and
validity rules. The database owner must integrate the approved durable
balance and immutable operation records with exact scope, replay and audit
semantics. The workload owner must supply an exact charge or reservation
identity from verified usage and any pending usage policy. Those inputs
must be retained across uncertain commits. The ledger and balance
change must commit atomically in the selected repository.
[PostgreSQL transaction atomicity](https://www.postgresql.org/docs/18/tutorial-transactions.html)

Then prove unauthorized and foreign grants leave the balance untouched,
same grant replay after a lost reply writes once, changed grant payload
conflicts, and concurrent debits cannot overdraw a balance. Verify that a
failed operation rolls back its balance and audit together. No new runtime
dependency, signing key, model or dataset is needed for this boundary
review. The referenced systems are design examples.
