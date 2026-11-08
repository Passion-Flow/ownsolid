# In-site OCI registry transport (NET-102)

[OCI Distribution 1.1.1](https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#pushing-blobs)
uses a Location from the upload initiation response for the next request.
[HTTP Semantics section 10.2.2](https://www.ietf.org/rfc/rfc9110.html#section-10.2.2)
resolves a relative Location against the request target URI. A private registry
or its in-site front end can return a path relative to the upload collection.
Resolving that value against the site origin drops the repository prefix and
refuses an otherwise valid upload.

Use the HTTP client's existing response Location resolver and retain the same
origin, repository upload prefix, single path segment, and allowed query
checks. Do not follow a redirect or trust a server-supplied external URL.
Continue verifying the digest and exact size after successful upload.

[CNCF Distribution v3.1.2](https://github.com/distribution/distribution/releases/tag/v3.1.2)
is a pinned test dependency under its [Apache-2.0 license](https://github.com/distribution/distribution/blob/v3.1.2/LICENSE).
It permits commercial use, redistribution with license and notices, and offline
operation. The existing Go HTTPS client and fixture remain in use. No runtime
library, model or dataset is added.

The native test rewrites only the representation of an actual Location from a
TLS-authenticated Distribution server, then verifies the uploaded bytes on that
same server. Separate controls cover foreign redirects, cross-origin upload
locations, missing content and digest errors. The full item also needs its own
offline installation and user flow.

## Verified path

On Linux amd64 in a loopback-only network namespace, Distribution v3.1.2
passed authenticated blob and manifest roundtrip, the path-relative Location
case, digest reads after changing a tag, and restart and garbage collection.
The same run passed the existing refusal controls, including missing or wrong
credentials, a foreign upload Location and an untrusted certificate.
