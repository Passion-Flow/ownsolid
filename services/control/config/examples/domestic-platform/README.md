# Native connector and standalone calling-layer inputs

## Native schema operator format1

`schema.example.json`, `schema.schema.json` and `schema.env.example` describe
`dameng.SchemaConfiguration` for the separately invoked `dameng-schema` command.
Every JSON field is required; unknown, duplicate, null, wrong-type/false integer
and contradictory values fail. No defaults or ambient policy values are read.
Protected inputs retain the same canonical0700-parent/0600-single-link contract.
The explicit env-file has one reference key and its complete adjacent comments;
it overrides an already valid `--schema-config` reference for this invocation.
Change inputs by closing old Stores and starting a new invocation; no live reload.

| Field | Required type, units, allowed values and mapping |
| --- | --- |
| format_version | Integer1, fixed operator format. |
| schema | Uppercase SQL identifier1..63characters; no SYS prefix; existing schema and migration owner username. |
| runtime_user | Same identifier rules; distinct pre-existing runtime username; no account/password creation. |
| lock_wait_seconds | Integer1..60seconds for native DBMS_LOCK.REQUEST. |
| step_seconds | Integer1..120seconds per SQL operation or bounded metadata pass. |
| total_seconds | Integer at least lock_wait_seconds+step_seconds, at most1800seconds; invocation cap. |
| cleanup_seconds | Integer1..30seconds for independent advisory-lock cleanup. Native Store cleanup additionally retains its connector cleanup bound. Neither force-frees native work. |
| maximum_metadata_rows | Integer512..10001; must exactly equal each connector's policy.maximum_rows, enforced during native eager materialization. |
| maximum_metadata_bytes | Integer16384..1048576bytes per metadata text cell; must exactly equal policy.maximum_cell_bytes. Native policy.maximum_result_bytes independently bounds aggregate materialized results. |

`schema-owner-connector.example.json` and `schema-runtime-connector.example.json`
use the existing `ConnectionConfiguration` schema with matching metadata caps,
SQL/column budgets and step-compatible native timeouts. They are illustrative
private references requiring the actual lawful native inventory, UID and socket.
Both use the same native directory/manifest/socket; only the username/password
reference differs. The operation chooses the connector role, never an ambient
setting. `--config`/`--env-file` load the owner except for `--mode check-runtime`;
grant alone also loads `--runtime-config`/`--runtime-env-file`. The schema operator
requires native DDL_AUTO_COMMIT1, case sensitivity1 and runtime GRANT_SCHEMA0;
it validates rather than edits those server parameters. Exact dictionary profile
support is still pending native verification. No actual secret appears here.

The optional operator is not a serving deployment selector, so no inactive
Compose/Helm environment switch is introduced. Complete runtime/deployment
mapping remains a shared integration gate. See the [command guide](../../../services/control/cmd/dameng-schema/README.md)
and [foundation contract](../../../docs/milestones/m29-domestic-platform/schema-foundation-contract.md).

## Local connector format1

`connector.example.json`, `connector.schema.json` and `connector.env.example`
describe `database/dameng.ConnectionConfiguration`. This is the separately invoked
Linux native connector/checker, not `control.database.engine`. No Compose/Helm or
server-selector mapping is added before complete platform integration exists.
`--config` is required. An explicit protected `--env-file` can replace that valid
configuration reference using only the documented
`PROJECT_ONE_DAMENG_CONNECTOR_CONFIGURATION_FILE` assignment. No ambient policy
environment is read. All fields are required, with no hidden defaults; change
them by reopening/restarting after the old Store has definitely closed.

| Field | Type, units, required behavior and owner |
| --- | --- |
| format_version / transport | Fixed protocol values1 / linux_unixsocket; no TLS or TCP alias/fallback. |
| native_manifest_file | Canonical protected JSON reference; actual native inventory and observed identity, not a credential. |
| native_configuration_directory | Canonical private0700 directory containing protected comments-only dm_svc.conf, odbc.ini and odbcinst.ini. |
| socket_file / socket_owner_uid | Canonical Linux socket path <=107bytes and existing same effective UID integer1..2147483647; private0700 parent and no group/other socket access. No new OS account is created. |
| user / schema | Explicit uppercase SQL identifiers1..63characters; no arbitrary statement or DSN text. SYS/SYSDBA/SYSSSO/SYSAUDITOR users are refused; actual ISDBA must be false. |
| password_file | Canonical protected secret-file reference, never inline password. Exact UTF8 bytes with no newline; reread/hash-bound per connection. |
| operation_seconds / transaction_seconds | Integer1..120seconds; request/transaction context cap. Native call cancellation must still join and is not a forced-free deadline. |
| cleanup_seconds | Integer1..30seconds for a Close wait; pending native ownership is reported, not discarded. |
| policy | All11 policy fields from the manager table below except connection_file/format_version. Connector minimums: cell256bytes, columns6, result2048bytes, SQL1024bytes for fixed session verification. Other bounds and cross-field constraints remain unchanged. |

The native runtime environment has no independent policy values: the Go loader
derives DM_SVC_PATH/ODBCSYSINI from native_configuration_directory, ODBCINI from its
odbc.ini, ODBCINSTINI is the fixed filename `odbcinst.ini`, and
LD_LIBRARY_PATH comes from the manifest's library_directory. The
[launcher](../../../tools/quality/odbc/connector.py) requests that exact mapping
from the checker and execs it with an empty inherited environment. Runtime checks
it again and refuses LD_PRELOAD/LD_AUDIT. No system/native configuration is edited.

`native-manifest.template.json` is deliberately invalid until a lawful actual
installation supplies its architecture, exact filenames, lengths, SHA256 values,
license/terms file and observed SQL_DRIVER_VER/SQL_DBMS_NAME/SQL_DBMS_VER. There is
no fabricated proprietary pin. `native-manifest.schema.json` describes the closed
format:64files maximum,1GiB/file,2GiBtotal, exact flat inventory, private0700 directory
and0600 single-link files. Pin the actual driver/support files copied under their
supplied terms. Every non-license inventory entry must be a same-architecture ELF64 little-endian
shared library. The loader checks actual PT_DYNAMIC/PT_LOAD tables, bounded
DT_STRTAB strings and the DT_NEEDED closure before loading any driver. Each
non-base dependency must have its exact name in the same private inventory;
license files cannot satisfy a library dependency. The only implicit base ABI
names are libc.so.6, libm.so.6, libpthread.so.0, libdl.so.2, librt.so.1 and the
selected architecture's glibc loader (ld-linux-x86-64.so.2 or
ld-linux-aarch64.so.1). Private files cannot shadow any of these names. libstdc++, libgcc_s, libnsl and other support libraries
need explicit lawful pinned private copies. Their exact target compatibility and
base OS dependencies still require the native target receipt.

If present, SONAME must equal the inventoried filename. In manifest format 1,
RPATH/RUNPATH may contain only the single $ORIGIN or ${ORIGIN} directory; absolute, relative and parent
search paths, dynamic filters/audit/config objects, duplicate string tags and
malformed/unbounded loader metadata fail. The private flat-directory profile has
no switch to bypass these guards and never patches a vendor binary. Required
non-library sidecar inputs or incompatible loader behavior remain explicit
unsupported prerequisites until a reviewed native input contract implements them.
Static ELF inspection does not establish runtime dlopen behavior, native API
compatibility, licensing rights or the security of an actual DM session. Public
unixODBC linking also requires its separate build receipt.

The example UID, paths and SQL identifiers are illustrative deployment inputs;
replace them with the existing Linux fixture's actual identity. The supplied
terms must permit the chosen local use/copies; a listed license file is not legal
approval. No download, license acceptance, service start or target qualification
is performed by these examples. See the [local contract](../../../docs/milestones/m29-domestic-platform/local-connector-contract.md).

## Original standalone manager format2

`odbc-manager.example.json` and its closed schema describe strict format2
`database/dameng.ODBCConfig`. The library constructor or `LoadODBCConfig` owns all
values. This is not an active control database selector, an installed driver or a
deployment profile. No unused environment DSN or automatic ambient configuration
is declared. Active serving integration must add the complete owning runtime/env/
Compose/Helm mapping before it can claim deployment support.

Every field is required. Unknown, duplicate, null, wrong-type and contradictory
values fail; booleans cannot stand for integers. Secret connection text lives in
a separately protected file, never this example. All policy input and connection
references require canonical absolute paths, effective-UID-owned 0700 parents and
0600 single-link regular files with pinned parent/file replacement checks.

| Field | Type / range / purpose |
| --- | --- |
| format_version | Integer 2; versioned calling-layer contract. |
| connection_file | Required canonical protected file reference; no inline DSN, credential or guessed vendor TLS option. Reread on every new physical connection. |
| login_seconds | Integer 1..30seconds; native login timeout, set before connection and checked after successful connection. |
| connection_seconds | Integer 1..120seconds; native connection timeout with exact readback. Driver enforcement remains unqualified. |
| query_seconds | Integer 1..120seconds; per-statement timeout with exact readback. Native cancellation always joins before releasing referenced memory. |
| maximum_parameter_bytes | Integer 1..1048576bytes; logical aggregate binding budget. |
| maximum_cell_bytes | Integer 16..1048576bytes; individual materialized cell budget. |
| maximum_rows | Integer 1..10000; eager result row limit. |
| maximum_columns | Integer 1..128; eager result column limit. |
| maximum_result_bytes | Integer maximum_cell_bytes..16777216 bytes; logical result budget, not a hard RSS cap. |
| maximum_sql_bytes | Integer 1..65536bytes; admitted SQL text bound. |
| maximum_open_connections | Integer 1..4; logical database/sql pool limit. |
| maximum_physical_connections | Integer maximum_open_connections..4; independent physical reservation limit including failed/retained/cleaning resources. Never raise this to hide cleanup debt. |

There are no omitted deployment defaults or reload thread. Replace policy through
a new validated Manager after the old Manager is definitely closed. Merely closing
the borrowed sql.DB does not release the retained native owner. A context deadline
ends a caller wait, not an unreturned native call; retain the Manager and explicitly
join/retry cleanup. See the [library owner](../../../services/control/internal/platform/database/dameng/README.md),
[contract](../../../docs/milestones/m29-domestic-platform/manager-repair-contract.md)
and [local gate](../../../tools/quality/odbc/README.md). Public-manager controls do
not qualify proprietary DM TLS, SQL, cancellation or the complete target platform.

## Explicit bounded long values

`connector-long-values.example.json` selects connector format **2**, mapping to
standalone manager format **3** (`odbc-manager-long-values.example.json`). Legacy
connector format1/manager format2 still forbid all three added fields. New versions
require every added field; unknown, duplicate, null, boolean/string-as-integer,
missing and contradictory input fails before native admission. There are no new
ambient environment variables or independent defaults. The existing protected
`PROJECT_ONE_DAMENG_CONNECTOR_CONFIGURATION_FILE` reference / explicit
`--env-file` selects the entire validated JSON, including these values. Its valid
base `--config` is still required; no inactive serving/Compose/Helm key is added.

| Field under connector policy / standalone root | Required nonsecret type, units, bounds and mapping |
| --- | --- |
| maximum_long_value_bytes | Integer bytes, at least maximum_cell_bytes and at most67108864. Bounds each text value in UTF-8 and each binary value in bytes; native text transfers use UTF-16. Maps to ConnectionPolicy.MaximumLongValueBytes / ODBCConfig.MaximumLongValueBytes. No default. |
| transfer_chunk_bytes | Integer native bytes4096..262144, multiple of4, at most maximum_parameter_bytes. Maps to TransferChunkBytes. Text read chunks reserve2bytes for a terminator; writes never split a surrogate pair. Descriptors, inline bindings and one active transfer scratch must fit the original aggregate parameter budget. No default. |
| maximum_retained_bytes | Integer bytes, at least4*maximum_long_value_bytes+2*transfer_chunk_bytes, at most536870912. Maps to MaximumRetainedBytes. Shared by all manager connections, retained statement bindings/scratch and eager results until Rows.Close. Conservative allocation admission, not process RSS or application heap enforcement. No default. |

All three values belong to the same owning configuration, contain no secret
references and take effect only on a new validated Store/Manager after the old
owner has definitely closed; no live reload or application rebuild is required.
The example8MiB long-value /64MiB retained profile is illustrative, not admission
of every M08 policy or simultaneous result. Derive actual limits from the selected
domain policy and projections before mutation. Insufficient capacity fails without
a business retry; full driver/vendor memory still belongs in host resource admission.

The original1MiB parameter,1MiB scalar cell,16MiB ordinary-result,10000-row and
four-physical-connection ceilings remain. Large string/binary bindings use actual
SQLParamData/SQLPutData; only native LONGVARCHAR/WLONGVARCHAR/LONGVARBINARY columns
use repeated SQLGetData. Their payload is bounded separately; ordinary results
still need paging. Native LONG identification, empty/NULL behavior, cancellation
and maximum-policy workloads require the actual target. See the
[wave6 implementation and qualification handoff](../../../docs/execution/mvp/parallel-wave6-domestic.md).

## Explicit vendor relative-loader profile

Native manifest format 1 retains its strict origin-only search rules.
Format 2 requires `loader_profile=dm_relative_release_v1` and a
`library_directory` whose final component is `bin`. Both the older observed
`./:../release` list and the current 2026.07 exact
`./:../release:$ORIGIN:$ORIGIN/dependencies` list are supported only in this profile.
These values enter only through the existing protected manifest file reference;
no independent environment override or live reload is added. A change requires
closing the old Store and launching a new process with the selected metadata.

| Manifest field | Required behavior and mapping |
| --- | --- |
| `format_version` | Required integer 1 or 2; no default. Version 1 retains the original field set. |
| `loader_profile` | Required string `dm_relative_release_v1` in version 2; absent in version 1. Unknown, empty, null and wrong-type values refuse. This fixed contract selects the checked vendor topology. |
| `library_directory` | Required canonical absolute path to the private `bin` directory in version 2; it supplies both the child cwd and `LD_LIBRARY_PATH`. No secret is embedded. |

The actual process working directory must equal that private `bin` directory.
Its parent, `bin`, `parent/release` and `bin/dependencies` must be real directories
owned by the same UID with mode `0700`. The two secondary lookup directories must
be empty. All libraries and the declared terms file remain exact pinned regular
files with mode `0600` in `bin`. `dependencies` is a reserved directory name,
never a file inventory entry. System directories, arbitrary relative paths,
extra colon entries and binary patches are refused. `libstdc++` and `libgcc_s`
remain private inventory requirements. Only the selected architecture's standard
glibc loader is an implicit OS ABI dependency; private files cannot shadow either
architecture's loader name.

The two operator commands expose `--print-launch-settings` with exactly
`format_version` (1), `environment` (the original five loader entries), and
`working_directory` (empty for manifest 1, derived `bin` for manifest 2). Launchers
use that typed child working directory. The old `--print-launch-environment` mode
refuses manifest 2 so an older launcher cannot silently omit the cwd requirement.
No library function calls `os.Chdir`. Use `native-manifest-relative.template.json`
only after filling actual observed identity, file hashes and lawful terms. The
template is deliberately not a valid runtime inventory. Actual loading, session
identity and ABI behavior require separate native qualification.

## Monitoring lookahead capacity

The connector, manager and schema metadata row ceilings are 10001, including one
lookahead row for a maximum 10000-sample domain request. Selected runtime and
schema row caps must still match. The ordinary 16 MiB result bound, per-cell
limits and all long-value and retained-memory admission stay unchanged. A
10001-row response with 4096-byte CLOB outcomes cannot be qualified through the
ordinary 16 MiB path. `connector-monitoring.example.json` selects the existing
long-value transport and 256 MiB retained admission. It is an illustrative
capacity input, not proof that the actual driver reports native long types or
that all worst-case rows fit. The actual typed CLOB, memory and cleanup profile
must pass. Domain history page 101 and business sample 10000 limits are unchanged.

### Exact unixODBC installer alias

The actual DM client dynamically requests `libodbcinst.so`. When a private
manifest 2 inventory supplies this public installer library, the only permitted
SONAME exception is `libodbcinst.so` alongside `libodbcinst.so.2`, with equal byte
length and SHA256. Both are separate real files with mode 0600, one link and the
same private owner. Neither is a license file. Every original ELF, layout and
hash check still applies; symlinks, hard links, other SONAME aliases and version
1 alias admission refuse. This is a fixed native dependency contract, not a
runtime alias setting. Public-manager license/source records remain required
outside this flat library inventory; vendor redistribution is separate.

The retained discovery03 failure reported a missing unversioned installer
library. A separate discovery04 metadata-only candidate supplied the exact pair,
passed static checks and advanced to a different native connection failure.
These observations establish the dependency requirement, not successful SQL or
application admission. Full native identity, session and runtime grants remain
independently verified by `Open` and the serving/custody owners.
