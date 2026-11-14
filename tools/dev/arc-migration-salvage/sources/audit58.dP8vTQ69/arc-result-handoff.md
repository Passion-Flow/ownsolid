
### Terminal component lease proof

A disposable overlay folded the post-callback PostgreSQL component
lease check into the terminal `UPDATE` predicate. The state, original
operation, current lease digest and database deadline still match the
locked task. The audit write path and other result owners were unchanged.

On 50 and 3,000 seeded peers the first result used 41 statements on
each fleet, compared with 43 before the overlay. The 16-statement gate
still failed. The same real mTLS fixture forced both an expired deadline
and a changed lease digest inside the callback transaction. Each request
returned `AGENT_LEASE_STALE` and preserved the task, component state and
audit head. Replay still returned the same result. Keep the prototype
separate from the activated migration and evaluate the remaining Audit
and result budgets without changing their declarations.
