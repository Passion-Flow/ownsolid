# S3 artifact contract (NET-101)

The inherited artifactstores API implements private object staging and
model and dataset ingress. The control plane assigns tenant and workspace
keys and retains the expected SHA-256, object size and revision. The
[Amazon S3 multipart contract](https://docs.aws.amazon.com/AmazonS3/latest/userguide/mpuoverview.html)
orders parts by part number and identifies them with ETags. A multipart
ETag alone does not establish a SHA-256 digest. Keep the service's bounded
full readback and conditional [If-Match access](https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-requests.html)
before treating a completed object as an accepted artifact.

The existing adapter uses the
[AWS SDK for Go v2](https://github.com/aws/aws-sdk-go-v2/blob/v1.47.3/LICENSE.txt)
and admits a private SeaweedFS S3 profile. Both are Apache-2.0
([SeaweedFS license](https://github.com/seaweedfs/seaweedfs/blob/master/LICENSE)).
Commercial and offline use and redistribution are permitted with the
required license and notices. The
[SeaweedFS S3 conformance suite](https://github.com/seaweedfs/seaweedfs/tree/master/test/s3/compatibility)
exercises upstream protocol compatibility. Ownsolid additionally requires
the configured private bucket and explicit refusal checks, rather than
assuming any S3 endpoint is interchangeable. No dependency or dataset was
added.

The acceptance path must qualify the private provider over TLS, including
multipart rejection, lost responses, no-credential and cross-scope refusal,
full digest verification, retry and object consumption by each named owner.
The existing object storage integration tests require an isolated SeaweedFS
profile, four private management mTLS endpoints and selected PostgreSQL.
Checkpoint and backup publication use owners outside artifactstores.
Extending the existing model and dataset ingress without binding those
owners would leave the S3 contract incomplete. NET-101 stays open.
