# ARC lane boundaries

## Kubernetes delivery and scale

ARC-104 has signed managed API acceptance documented in
[Kubernetes cells](kubernetes-cells.md#signed-release-and-managed-api-acceptance).
Its complete Console creation, installation and retry path still crosses delivery
and web ownership. Keep the item open. ARC-105 retains its separate combined
1,000-node managed-cell acceptance. ARC-106 publishes only the topology actually
measured and keeps the single-control baseline distinct from future cell results;
it does not accept ARC-105 or imply a multi-cell capacity result.

## Gateway listener and rebalancing

ARC-107 is accepted on dedicated gateways: automatic age and GOAWAY, authenticated
replacement, natural transport retirement and preferred return passed the
unchanged native 500-agent lifecycle twice, including strict terminal counts and
persisted cursors. The shared listener retains legacy behavior.
[The research](agent-gateway-rebalance.md) records scope and mixed-version behavior.

## Agent call cost

ARC-108's shared cost test is owned by the training module. Extending it requires
that module's integration fixtures and the component, inference, preparation and
Registry owners. ARC-109 can reduce duplicate checks for node-owned claims here.
Its delegated event and upload paths need work in training and preparation.
[The research](agent-claim-authority.md) records the transaction boundary.

## Remaining P1 dependencies

ARC-110's split-browser runtime and native three-socket composition are qualified;
the complete four-entrance installed and actual-browser flow remains open.
ARC-111 has native restricted-role prototypes, but production role activation and
the separate gateway's training artifact-owner failure remain open.
ARC-112 depends on [FLT-101 inventory](fleet-inventory.md).
ARC-113 needs installer changes. ARC-114 needs console and alerting. ARC-115,
ARC-116 and FLT-111 need the installed and managed cluster from ARC-104.

FLT-102 and FLT-104 through FLT-108 need persisted operator controls and console
flows. FLT-103 needs REL device collectors. FLT-109 needs the component owner.
FLT-110 needs a signed artifact path and host service update and rollback, which
cross delivery ownership. FLT-112 and FLT-115 need console and public API work.
The current lane can supply node changes as these dependencies become available.
