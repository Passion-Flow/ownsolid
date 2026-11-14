# Component result terminal guard prototype

The initial task read locks the exact task through the owner callback. It
validates current state, the credential digest, typed operation and result.
Keep those checks and the completed-result replay. The terminal write can
check the same operation, the same lease digest, running state and
`deadline_at > clock_timestamp()` immediately after the callback.
[PostgreSQL UPDATE](https://www.postgresql.org/docs/18/sql-update.html)
returns a count of rows meeting the predicate. The
[clock contract](https://www.postgresql.org/docs/18/functions-datetime.html#FUNCTIONS-DATETIME-CURRENT)
uses actual time rather than transaction-start time. If no row changed,
the caller returns the existing stale-lease error and the transaction
rolls back the owner's writes. Retain the separate reauthorization for
other dialects and result kinds, especially derived model authority.
Leave audit write scope and custody unchanged.

Verify the real component mTLS result and replay on 50 and 3,000 peers
without changing the 16-statement budget. Inject expiry or digest
replacement after the component owner writes and require the existing
refusal with unchanged task, component and audit state. The prototype
uses the existing Go, PostgreSQL, pgx and test tooling documented in
agent-claim-authority. It adds no runtime dependency, dataset or model.
