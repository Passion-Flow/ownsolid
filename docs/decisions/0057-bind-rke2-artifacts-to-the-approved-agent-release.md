# Bind RKE2 artifacts to the approved Agent release

Extend the current Linux amd64 signed inventory with an explicit RKE2 group.
Use the Agent contract's approved filenames, sizes and hashes for the binary
and two upstream image archives. Pin the original amd64 checksum file too.
The signed prerequisite names the approved version. A cell operator can
require that group in the protected verification policy.

Use the existing factory, Ed25519 signature and preparation workflow. Treat
the upstream image sets as immutable files for the Agent's node-local RKE2
import. Keep single-node Docker image validation independent of RKE2 and
keep scale fixtures outside this inventory.

See the [delivery research](../research/rke2-signed-delivery.md).
