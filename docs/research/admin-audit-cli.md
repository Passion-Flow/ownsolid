# Admin audit queries from the CLI (DEV-102)

[CloudTrail lookup-events](https://docs.aws.amazon.com/awscloudtrail/latest/userguide/view-cloudtrail-events-cli.html)
uses a bounded management event page and requires the original filter with its
continuation token. [gcloud logging read](https://docs.cloud.google.com/sdk/gcloud/reference/logging/read)
provides ordered output, a time range through its filters, and bounded reads.

Use the existing installation-wide
[Admin audit contract](../../api/openapi/v1/identity/openapi.json) and the
[registered dispatcher](../../services/control/internal/modules/identity/http/handler.go).
Reuse the Admin session, local CA and response handling from the gateway
history CLI. The audit path is global. The configured site, cell and workspace
do not select an audit scope. The dispatcher and storage own Admin permission,
valid filter values, the time window, and cursor binding.

Use dedicated flags for the eight documented filters. Forward their values to
the server and preserve its problem codes. In particular, the server owns its
action catalogue and deployment-specific time window policy. The local CLI
shares the existing 100-row and 512-byte cursor bounds with gateway history.
Return one page with the opaque continuation and preserve all published fields
in structured output. An explicit assistant attribution flag sends the
[existing header](audit-actor-contract.md). Table output identifies a human
by email and shows the separate verified assistant identity when present.

Reading every page would make an interactive incident query depend on the
installation's full audit volume. A client-side workspace selector or raw
SQL would bypass the current API boundary. Keep the server's filtered,
authorized projection and cursor, including the pinned audit window.

The existing CLI dependencies and test stack are documented in
[Gateway usage CLI licenses](gateway-usage-cli.md). The BSD-3-Clause Go
toolchain, Apache-2.0 Cobra and mousetrap, BSD-3-Clause pflag and Go
extensions, and MIT/Apache-2.0 YAML allow commercial use, redistribution
with notices, and offline use. PostgreSQL and pgvector use the PostgreSQL
License, pgx uses MIT, and the fixture runner uses Python under the PSF
License. No dependency, model or dataset is added.

Use the compiled CLI with real Admin HTTPS and PostgreSQL. Verify exact
request and action filters, continuation across pages and identical
unchanged retries, an empty result, wrong filter reuse, server refusals,
and expired sessions. Exercise verified assistant attribution and the
separate subject. Check local malformed page handling and absence of
partial stdout on failures.

The Linux amd64 run used PostgreSQL 18.6 and pgvector 0.8.7 in a network
namespace with only loopback. The compiled CLI read exact request and
human action filters, continued and replayed a page, rejected a changed
filter and a revoked session, and kept the verified assistant separate
from the user subject. An unrelated site, cell and workspace in the
local CLI configuration did not affect the global query. The existing
Admin audit HTTP test and gateway CLI workflow passed in the same run.

Run `tools/cli/testdata/run-audit-tls.sh` with the pinned Go and Python
toolchains and `PROJECT_ONE_TEST_DSN` pointed at disposable PostgreSQL.
