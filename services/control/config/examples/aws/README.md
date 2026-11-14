# Operator AWS discovery configuration

`policy.env.example` maps every numeric/boolean policy to the existing explicit
private `control serve --env-file` loader. The optional protected connections file
maps the remaining adjustable settings as one typed array. This file overrides
`aws.connections` in the main YAML; unknown/duplicate env keys, unknown JSON fields,
invalid types, empty required values and contradictory bounds are rejected. Ambient
`AWS_*`, EC2 instance metadata, local SDK profiles and shell substitution are never
used. Disabling discovery preserves retained SQL observations and offline functions.
All changes require restart; no application rebuild is required.

Each connection owns: `name` (unique lower-case identifier, 1..48 characters),
`partition` (`aws`, `aws-cn`, `aws-us-gov`), `account_id` (exact 12 decimal digits),
`regions` (1..100 unique region identifiers matching the partition),
`workspace_ids` (1..1000 canonical nonzero UUIDs authorized for this connection),
and `credentials_file` (absolute canonical protected reference). Account and
Workspace values in the example are deliberately synthetic; replace them with
reviewed deployment inputs before enabling. A provider endpoint is derived from
its named protocol/partition and permitted region; user-provided endpoint URLs and
TLS verification bypasses are not supported. STS confirms the credential's account and principal ARN partition.
The account must grant `ec2:DescribeInstances`; STS `GetCallerIdentity` obtains the
actual caller identity. No write/cloud provisioning API exists in this adapter.

Provision the credential file manually or through an authorized secret manager,
owned by the serving UID, mode 0600, inside an owned 0700 directory without symlink
parents or hard links. Required JSON fields: `access_key_id` and
`secret_access_key` (strings). Temporary credentials additionally require
`session_token` and `expires_at` (RFC3339 time). Do not place credential contents in
a tracked sample, command argument, browser field or log. The file is reopened and
revalidated for each import, so credential rotation can use an atomic private file
replacement; temporary credentials are never refreshed by an unconfigured provider.
Connection/policy changes themselves require restart.

Compose uses the existing `deploy/compose/base/workspace-policy.yaml` overlay,
`PROJECT_ONE_POLICY_ENVIRONMENT_BASENAME` and existing `PROJECT_ONE_CONFIG_ROOT`
private bind. Keep connections and credentials inside the same private bind with
matching container paths. Do not create an anonymous credential volume or relocate
Docker storage. Helm uses `configuration.policyEnvironmentFile` plus the existing
`secrets.files` projection for the env file, connection file and credential files;
use `configuration.privateMountPath` consistently in references. The existing
projection enforces UID/mode and its verified receipt; retain and refresh that
receipt through the supported provisioning workflow when rotating credentials.

Initial defaults: one concurrent import per process; request 10s; overall discovery 25s; 3 attempts; exponential delay
base 250ms; 100 instances/page; 100 pages; 4MiB/response; 10,000 resources and 10,000
successful receipts per Workspace. The HTTP request budget must cover discovery.
Limits stop the import atomically, retaining previous inventory. Missing rows remain
retained for historical inspection and count toward capacity; raise a reviewed bound
or retain/export and plan an audited retention migration rather than deleting receipt
history ad hoc. Discovery has no timer, worker, daemon or startup network request.

The minimal reviewed IAM policy is [readonly-policy.json](readonly-policy.json).
It permits only EC2 DescribeInstances. STS GetCallerIdentity returns identity even
without an IAM Allow; an explicit STS deny is not a useful denial acceptance case.
The [customer procedure](../../../docs/milestones/m27-aws/customer-readonly.md)
contains one-shot HTTP/readback and separate target qualification requirements.
No policy is installed or account accessed by supplying this example.

The AWS page uses the existing validated public request_timeout_milliseconds for
both reads and imports (runtime.http.request_seconds / PROJECT_ONE_HTTP_REQUEST_SECONDS).
Browser timeout keeps the original key and enables explicit receipt checking;
scope cancellation ignores old results. It does not automatically resend a POST.
