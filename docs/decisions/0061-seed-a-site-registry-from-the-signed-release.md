# Seed a site Registry from the signed release

Use the exact Linux amd64 Docker Official Image of Distribution 3.1.2 as an
optional signed asset of the current bundle. Reuse the factory's OCI archive
validation and selected runtime descriptors. Write the signed config, layers
and manifest bytes directly to the Distribution API and verify the digest
and size of each descriptor before advertising image references.

Seed through a loopback-only writer with a fresh credential, then serve the
verified data through a separate TLS endpoint bound to the site's private
IPv4 address. Use a read-only storage mount and Distribution's read-only
configuration. Nodes pull with a distinct site credential and trust the
site CA. Bind the install root and container to the signed manifest and
site settings so a retry can verify the same data and safely resume.

Treat release distribution and workload authorization as separate
responsibilities. The site Registry serves a release repository per signed
image asset. Application tenant grants and RKE2, Helm and Agent consumption
stay with their owners. The factory and installer preserve admission for
existing signed single-node releases.

See [DEL-108 research](../research/signed-registry-bootstrap.md) and
[operator instructions](../../deploy/offline/README.md#bootstrap-a-site-registry).
