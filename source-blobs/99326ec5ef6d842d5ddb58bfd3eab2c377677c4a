# Network and storage module review

The 2026-11-09 review retains the S3 and OCI Distribution protocol
boundaries. [Amazon S3 multipart uploads](https://docs.aws.amazon.com/AmazonS3/latest/userguide/mpuoverview.html)
document part ordering and verification, and distinguish ETags from full
object digests. [Conditional writes](https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html)
prevent replacing an object after another writer wins. Preserve the module's
private scoped keys, full byte readback and separate publication step.

[OCI Distribution](https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md)
defines uploads and digest-addressed reads. [CNCF Distribution's deployment
guide](https://distribution.github.io/distribution/about/deploying/) supports an
in-site TLS registry loaded from local images. An upstream pull-through cache
would still have an external runtime dependency. Keep the repository's scoped
pin and grant checks on top of private distribution.

[P1 and P2 already cover network telemetry, egress policy, and storage
interfaces](../product/roadmap/network-and-storage.md). Retained objects can
still become unreadable after ingest, even when their key and database receipt
remain valid. As an analogue, [Ceph deep scrub](https://docs.ceph.com/en/latest/architecture/scalability-high-availability/)
compares stored bytes with checksums to surface integrity problems. NET-208 adds
paced, scoped readback against ownsolid's expected artifact digest, and recovery
only from an independently verified copy. This check belongs above individual
storage implementations because ownsolid owns the intended digest and access
scope. It does not introduce a Ceph dependency.
