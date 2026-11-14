
## Disconnected privilege probe

A disposable PostgreSQL 18.6 container applied the 58 checked-in migration
inputs from the verified source archive. The current serving predicate accepted
the ordinary serving grants and rejected the narrow test login. The test login
could read the granted managed-node table and received permission errors for a
user-table delete and direct audit-chain-head access. With its sole session
occupied, PostgreSQL rejected a second connection to that role while the
ordinary serving role still connected. The test used Unix sockets with
networking disabled. It establishes the existing admission dependency and
independent connection limits. Qualifying the complete gateway grant set still
requires the normal TLS gateway and all enabled workload profiles.
