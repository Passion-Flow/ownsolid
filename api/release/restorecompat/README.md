# Exact restore compatibility authorization

This independent package defines and signs/verifies a domain-separated Ed25519
authorization for one reviewed source backup and target signed release/scope.
It imports existing v1 identifiers without changing their serialization or
the release factory signature domain. Operator owns protected trust/file/plan
admission and all effects; issuer owns private signing-key custody. No policy
disable, network, filesystem mutation or effect retry is implemented here.
See the [restore contract](../../../docs/milestones/m17-offline-compose/restore-compatibility.md).
