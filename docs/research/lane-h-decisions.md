# Lane H decisions

## DEL-101 production bundle boundary

Keep DEL-101 open. The current production factory requires the old milestone
inventory, the installer lacks a verified bundle entry point, and the release
has no Helm chart or release-qualified plug-in to package. Reuse the existing
factory when a current production profile and those assets are available.
See [offline delivery research](offline-bundle.md).

Rejected labeling a fixture profile as a release, changing the shared release
contract from this lane, and creating a parallel signing format. Any of those
could create an installable-looking file that violates the existing verifier or
omits a required capability. If the inherited profile can be populated with the
actual current inputs, it can be evaluated without waiting for a replacement.

## DEL-102 preflight policy

Keep existing configuration files valid. A typed policy enables the full host
checks on apply, and a read-only command lets an operator check before changing
anything. Local host inspection and a private resolver selected by the operator
fit isolated sites. Reject implicit system DNS, online time queries, and remote
Docker for host preflight. Requiring these new policy inputs on every existing
apply would block upgrades or labs that resolve browser origins on clients.
The cost of opt-in is that an installation using an old configuration continues
with its original Docker and Compose checks until an operator enables the policy.
