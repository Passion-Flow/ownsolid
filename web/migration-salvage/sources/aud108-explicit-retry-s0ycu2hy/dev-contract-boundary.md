# Qualified-source contract boundary

Source inspected: c864fbb10294fa36bdbf4256fe1601f8358ad1f8.

The current implemented identity audit reader appends within the read transaction. Its real wire integration tests observe HTTP409/STORAGE_COMMIT_OUTCOME_UNKNOWN, no data and no Retry-After. In the qualified OpenAPI operation, responses409 and x-error-codes409 are absent. The shared Problem code enum already contains STORAGE_COMMIT_OUTCOME_UNKNOWN.

The minimum DEV-owned follow-up is the existing409 Problem response/error metadata and accurate audited-read/recovery wording. No new response projection, event field, endpoint, exact-action enum or idempotency contract is needed by this Web consumer. The Web parser already preserves that server code and request ID.

This lane did not import cf8d1c10 or edit OpenAPI/generators. Regenerate and compare actual Web inputs only after DEV publishes its qualified commit. The new UI draft is not covered by the older1134/410 browser result and is awaiting its own gates.
