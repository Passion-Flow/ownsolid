# Registry component configuration

Control selector, closed HTTP routes, actual agent invocation, PostgreSQL24 and
same-transaction deployment action ownership are integrated with declared local
checks. Actual signed-image/Registry/browser qualification remains pending.
Omitting a configured component requires empty Registry state; startup refuses
retained pins. Fresh actions also refuse retained Registry task intent without
the real owner. Restore the protected component configuration to recover; do not
delete history/references or disable authority to bypass this refusal.
Both owning LoadEnvironment functions consume only their respective component key
from the deployment's parsed `.env`/`--env-file` input. Parent must partition the map,
retain its existing precedence and invoke Load before constructing the component.
No code reads process environment, downloads configuration or stores credentials
in command-line arguments. A component JSON change needs restart, not recompilation.

The control example deliberately has empty required independent release digests.
Obtain both actual signed release archive/manifest hashes from the release receipt;
blank, zero placeholder, wrong trust, fixture permission or unmounted source fails.
The credential reference selects a protected0600 local JSON file containing only
`username` and `password`; supply actual Distribution credentials out of Git.
CA/trust files are protected0600 regular files. Private root directories are0700.
No real endpoint, password, certificate or container digest is supplied here.

Control settings:
- `endpoint`: required HTTPS origin only, with DNS/IP SAN and explicit port where
  needed. No path/query/userinfo; addresses must fall in `allowed_cidrs`. Private CA
  validation is mandatory. No proxy, public fallback, insecure TLS or auth redirect.
- `ca_file` / `credential_file`: required protected server-only files. Rotation needs
  restart. Actual endpoint+CA identity is pinned per row; a new backend cannot adopt
  an old remote pin. Local verified offline snapshots retain their original facts.
- `store_root`: required dedicated durable0700 CAS root; cannot overlap prepared
  release root. `prepared_release_root` and both `release_*_sha256` are required
  operator-selected immutable release inputs. No HTTP path/URL/digest override.
- `verification`: unchanged typed offlinefactory verification contract. All its
  limits, mount and signature/license trust checks remain in force. Production
  `allow_test_fixture=false` is fixed admission, not a disable switch.
- `maximum_archive_bytes` / `maximum_expanded_bytes`: defaults64GiB each, allowed
  1..2^50 bytes. `maximum_blob_bytes`: default32GiB, positive and <=expanded limit.
- `maximum_retained_bytes`: default128GiB, at least twice the archive limit. Both
  archive and blob copies count toward retained admission. `maximum_pins` default64;
  `maximum_references` default4096 per retained reference kind/tenant, including released history; positive bounded inventories. Deleted tombstones
  retain provenance; archive/blob copies are removed only after committed eligibility.
- `maximum_concurrent_imports`: default1, positive <=maximum_pins. This source uses
  one process admission channel and real transaction/intent fences. Parent must not
  mount multiple independent import workers as if they shared that RAM budget.
- `minimum_headroom_percent`: default30, allowed30..90, never a security bypass.
  Admission includes archive/blob coexistence and expanded content; parent separately
  admits Registry daemon/logical export/recovery peak storage and host memory/CPU.
- `operation_seconds`: default3600, 1..86400. `request_seconds`: default120, positive
  <=operation. `grant_seconds`: default900, positive <=operation, still bounded by
  actual task lease/deadline at every read; expiry never releases a deployment ref.
- `page_size`: default50, positive <=maximum_pins. Keysets use exact canonical IDs;
  timestamps are actual database UTC microseconds. No OFFSET scan or stale pool.

Agent settings: required private `root`, exact actual `docker_data_root` observed
from the protected Unix Docker `/info`, original verification contract, archive/
expanded limits64GiB, Docker JSON response cap1MiB (must be <=verification.maximum_metadata_bytes), operation3600seconds and minimum
headroom30percent. JSON numbers must be positive; values/ranges follow the Go
Validate owner. Invalid/empty/unknown/null/duplicate keys fail. Before a load, the
same expected Docker root must exist as a real directory and match actual `/info`;
Statfs admits actual additional transport+expanded bytes above30percent. A VM or
containerized daemon whose actual backing filesystem is unavailable cannot be
qualified by guessing a host path; parent must supply/verify an actual supported
storage profile. No global Docker setting or daemon is changed by this adapter.

Compose/Helm integration seam: mount component JSON/CA/credential/trust read-only,
CAS/agent root read-write on the declared verified storage, and inject only the
corresponding protected config-file path. Parent existing control/agent deployment
owns these mounts and env/CLI parsing. Distribution is a pre-provisioned internal
endpoint with its management listener restricted to the control network; do not
publish it through Portal or assign long-lived Registry secrets to nodes. Actual
Distribution binary/image identity must come from the centrally verified fixture;
this example provides no invented image digest or floating `latest` dependency.


Dedicated central validation inputs are validation.json/validation.schema.json and
validation.env.example. registry_validation.load_policy owns these fields; only
registry_runtime and registry_node_runtime wrappers consume this override, leaving
shared validation policy untouched. Precedence is explicit --env-file values over
--validation-configuration JSON, then the dedicated example selected by default.
Unknown/duplicate/null/empty/bool/out-of-range values fail. No process environment
read or global setting is added. See the milestone runbook for resource admission,
source-only status and separate actual SQL/Distribution/Docker/readback commands.

## Central control selector and deployment mounts

The platform registryconfig package is the single owner of the control JSON type,
loader, defaults and validation; imageregistry aliases it. Control Decode resolves
`image_registry_config_file` once into an internal snapshot. Omit both selector
and env key to disable. Present empty/null/relative/noncanonical references fail.
The explicit protected serving --env-file overrides valid YAML using
`PROJECT_ONE_REGISTRY_CONFIG_FILE`; ambient process environment is ignored. A
resolved component body is excluded from Config JSON diagnostics. CAS and prepared
release roots cannot overlap active model/dataset/training/preparation roots.

Use `component.env.example` for the control policy input. Merge selected component
keys into one protected policy file. The agent's existing LoadEnvironment is a
component API; integration into the agent command's env-file/runtime invocation
remains pending and is not implied by the control selector.

Compose: apply `deploy/compose/registry/compose.yaml` after the base and needed
product-store overlays, selecting `compose.env.example` bind inputs. The base
private mount contains the selected JSON, private CA/credential/trust references
and merged policy file. Dedicated parent host/container paths map to JSON verification.mount_root; CAS
store_root is a private child. Prepared host/container paths map to the separate
prepared_release_root child and are read-only. The verifier requires the parent
to be a real mount; two sibling mounts do not establish that fact. Existing
sources are mandatory; create_host_path=false. No Registry daemon/image is started
or obtained. An existing private endpoint with verified TLS is a prerequisite.

Helm: supply both storage.registry-cas and storage.registry-prepared, with distinct
existingClaim. registry-cas.mountPath equals verification.mount_root; its private
CAS child equals store_root. registry-prepared.mountPath equals the strict
prepared_release_root child. Only this named pair may nest. The prepared PVC and
mount are always read-only. Add the policy/JSON/CA/credential/trust basenames to
secrets.files and select configuration.policyEnvironmentFile. Neither slot creates
claims or changes their ownership. Global GC, release source replacement and
restored authority invalidation still require their explicit workflows. Rendered
manifests do not establish installed CSI, correct permissions, service readiness,
a complete offline package, browser behavior or target qualification.

`helm.values.example.json` is a merge overlay over the existing chart/base values.
For Helm, policy/JSON CA, credential and trust references must use the selected
`configuration.privateMountPath/private/<basename>` produced by materialization.
Compose uses its base private mount directly. These selected paths are explicit
deployment input, never silently rewritten. The example includes public path and
claim names only; provision actual private files/claims and signed release first.
