# Bind model use to verified catalogues

## Context

Model import acknowledges publisher terms. Deployment needs a separate
server-side check against the installation's use and the exact model
version it is about to schedule. Existing deployments must remain
inspectable and stoppable.

## Decision

Models owns the reviewed terms bound to the verified catalogue digest.
Consumers supply authenticated scope through their existing model lookup
and a typed, installation-owned use. The first serving operation is
internal deployment. An installation without a new declaration retains
on-site commercial behavior for versions with reviewed compatible terms.
Explicitly undeclared use and versions without reviewed terms refuse new
launches. Readback, stop and removal do not require a new license decision.

Keep the edition license, artifact use authorization and runtime admission
separate. Never infer permission for a derivative from its base license
label alone. The same ownership boundary can serve training and scientific
runs when their queue and execution transactions adopt it.

## Consequences

No persistence migration or change to immutable catalogue bytes is required
for the first record. Shared configuration must supply the declaration
before startup. Public reasons need the shared problem registry and
generated consumers. Already committed deployment intents replay their
original results. A new launch rechecks current policy.
