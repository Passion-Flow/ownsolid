# Initial monitoring configuration

Production uses monitoring.LoadEnvironment with exactly
PROJECT_ONE_MONITORING_CONFIG_FILE. The protected regular local JSON contains no
secret/provider address. Load starts from monitoring.Defaults, then strict JSON
rejects duplicates/unknown/wrong types and Config.Validate checks finite ordered
buckets and all ranges. Each field/unit/default/restart effect is documented in
monitoring.schema.json. Only the parent global typed boundary reads the deployment
input, mounts it read-only in the original Compose/Helm deployment and injects it.
No module scatters os.Getenv, loads a remote URL or creates a public metric listener.
Changing a policy requires control restart; never merge counters across epochs.

Create one matching monitoringmetrics.New(config.HTTP, originalSlogLogger) at
control startup. The constructor keeps a private immutable bucket copy; monitoring
New rejects a mismatched policy. StructuredLogging=false disables only those log
events, while counters/auth remain active. The original JSON slog sink is supplied
by the parent; no external logger, alert sink or daemon is introduced.

The quality-only capacity.json has no customer endpoint or PID. Every field is
required. capacity.schema.json, capacity_configuration.py and owning tagged Go
boundary reject unknown/duplicate/missing/wrong type/invalid/range/cross-field
values. Literal stop_on_error=false works; the string "false" fails. Selection is
explicit --workload-configuration > exact one-key --workload-env-file > tracked
capacity.json. The commented capacity.env.example documents that quality mapping;
it does not add a production server environment authority or Compose service.

The wrapper injects two private test-only paths, never operator credentials:
PROJECT_ONE_MONITORING_WORKLOAD_FILE points to exact retained validated nonsecret
input; PROJECT_ONE_MONITORING_REPORT_FILE is a new exclusive0600 output under the
private evidence directory. No arbitrary remote target or existing process input
exists. PROJECT_ONE_MONITORING_PROCESS_CHILD is solely an exact new test-binary
child selector injected by the owning test, not deployment configuration. Parent
sets TMPDIR to an owned external scratch for all executable pure/fixture tests.

Actual host/Docker-VM/DB/Go peak resource policy remains the existing centrally
admitted shared profile with30percent floor. This small workload is not a capacity
claim; full request facts, samples/coverage/error totals, effective profile, recovery,
process runtime accounting, original source/raw hashes and cleanup are retained.
Host RSS/available host memory remain separate outer resource observations.

## Mainline configuration and license delivery

Omit global `monitoring_config_file` to disable assembly. To enable it, create an
owned0700 directory and a0600 regular JSON file from monitoring.json, select its
canonical absolute path through YAML or merge monitoring.env.example into the
existing protected `serve --env-file`. The explicit env-file overrides valid YAML;
ambient variables do not. Null, unknown, duplicate and wrong-typed policies fail.
Restart applies all fields and starts a new process epoch.

For Compose, pass compose.env.example as the deployment interpolation `--env-file`
and overlay deploy/compose/monitoring/compose.yaml; provision the host directory
first. This file selects read-only mounts/command only. Keep the global protected
configuration bind and original serving policy file. For Helm, include the JSON
basename in secrets.files and the merged policy basename in
configuration.policyEnvironmentFile, with references under privateMountPath/private.
The original materializer creates owned0700/0600 input files. No public listener,
new service or telemetry volume is created. Template rendering is not installation.

Control embeds pinned MIT httpsnoop. Both control Dockerfile variants and the
original source-bound image builder retain services/control/third_party/httpsnoop
LICENSE.txt and NOTICE.md at /opt/projectone/third-party/httpsnoop. Standalone
control delivery must include those two files; offline release manifests must
reference the exact retained license asset from the control image/binary. The
existing release contract rejects missing license references; no complete offline
package or installed image is claimed by source changes.
