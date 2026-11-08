# ARC lane boundaries

## Kubernetes delivery and scale

ARC-104 has the agent installer and the control orchestration from commits
`27dc24c` and `f98a160`. Its offline release assets and the cluster creation and retry
screens require delivery and web changes outside the ARC lane. Keep the item open
until both paths are available and an offline install reaches Kubernetes Ready.
ARC-105 depends on that managed cell. ARC-106 will use measured cell results and
distinguish them from the single-control results in [the P0 run](fleet-load-test.md).

## Gateway listener and rebalancing

ARC-107's HTTP/2 age and GOAWAY belong to the control HTTP listener and gateway
lifecycle. The ARC lane can implement and verify client rebalancing with the
existing v1 transport. [The research](agent-gateway-rebalance.md) records the
remaining listener gate and the mixed-version behavior.

## Agent call cost

ARC-108's shared cost test is owned by the training module. Extending it requires
that module's integration fixtures and the component, inference, preparation and
Registry owners. ARC-109 can reduce duplicate checks for node-owned claims here.
Its delegated event and upload paths need work in training and preparation.
[The research](agent-claim-authority.md) records the transaction boundary.
