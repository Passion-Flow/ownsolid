# Task-event component configuration

These examples target the sole `taskbus.Configuration` boundary, now installed
as optional root `task_events` YAML and the protected serving --env-file overlay.
Enabled serving requires actual root tenant/schema/broker admission; full root
qualification remains separately recorded. Every enabled setting is
explicit; the example is synthetic and contains no credential material. The same
built artifact can receive another valid configuration without source changes.

`DecodeConfiguration` reads bounded strict JSON. `DecodeYAMLConfiguration` and
`ValidateYAMLNode` reject aliases, duplicate/unknown/case fields, nulls, YAML boolean
aliases and noncanonical integer spelling. Disabled input contains only explicit
`enabled: false`; enabled input supplies all 31 fields. `ApplyEnvironment` accepts
an explicit already-unquoted map with only PROJECT_ONE_TASK_EVENTS_* keys and
validates the resulting complete typed object; it never reads ambient variables.
The root protected env-file parser rejects duplicate raw keys before constructing
that map and applies all task-event overrides together. Explicit file values
override independently valid component YAML. Ambient environment is ignored.

All fields and their English constraints/restart effects are adjacent to their
example values. Cross-field relationships are enforced by runtime validation.
Broker storage headroom, exact stream/durable configuration, actual credentials,
private CA/mTLS, namespace ACL and real negative tests are separate mandatory
admission gates. A valid source configuration is not a running or qualified broker.

The publisher and consumer NKey seed files are separate protected references;
neither a seed nor key bytes are a URL, CLI flag, tracked variable, log field or
browser value. Secret readers additionally check canonical path/no symlink, owner,
private permissions, link count and bounded stable descriptor identity before use.
Changing policy requires a controlled restart; a differing broker configuration
is refused rather than updated or silently adopted.

Tests are source-authored for nondefault JSON/YAML/map input, false behavior, IPv6
and DNS/localhost/private IP, unknown/duplicate/null/case fields, unsafe endpoints,
secrets, integer/retention/ACK boundaries and redacted formatting. Original source-lane checks did not run a broker or qualification. Current root
controls and actual native broker cases have separately retained receipts.


Root omission and `task_events: {enabled: false}` disable the component. Indent the
complete standalone example under task_events to enable through root YAML, or
merge deployment.env.example keys into the single protected root policy file.
Do not overlay false onto retained enabled fields: remove those fields explicitly.
JSON/format diagnostics redact this private component. Invalid base input cannot
be repaired by environment; file permission/ownership failures remain failures.

For Compose, use the existing base plus task-events/compose.yaml and a protected
copy of compose.env.example. Its readonly bind projects only existing0700/0600
credential inputs; create_host_path:false refuses absent sources. No broker is
started, downloaded or provisioned by this overlay. The same merged policy file
uses the existing control --env-file command. Validate the exact bind topology and
all typed path references before any service start.

For Helm, configuration.policyEnvironmentFile selects the merged policy basename
in secrets.files. Add all five credential basenames to that existing projected
Secret and use typed absolute references under configuration.privateMountPath/
private. Keep every filename distinct and within existing projection limits.
Root control configuration is supplied through the existing ConfigMap projection;
these31 runtime keys do not create a second Helm authority. Restart/rollout and
exact broker admission are required. Rendering is not installation qualification.
