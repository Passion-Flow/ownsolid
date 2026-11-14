# Run catalog Compose from a closed model

A verified Compose file can refer to files, credentials and executable
providers that the signed digest does not cover. The first installer
profile admits only self-contained, disconnected services. Reject extra
inputs before giving bytes to Compose, and reject fields introduced or
changed during normalization.

The installer stages and launches from bounded memory buffers. It records
the source and model digests at admission, rechecks source identity before
startup, and passes the checked model as the sole stdin input to a pinned
Compose executable. Source staging, the Compose process context and
authorization each have separate owners. The existing authorized install
flow must bind the release pin and model digests to the same scoped
operation before invoking this boundary.

See the [installer boundary and acceptance](../research/catalog-compose-staging.md).
