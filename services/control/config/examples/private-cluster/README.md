# Private cluster configuration

The example is secret-free and deliberately unbound: example.invalid endpoints,
synthetic UUIDs and nonexistent protected files are placeholders, never observed
cluster identities or working credentials. Replace every identity from actual
operator/API observations before admission. No image digest is supplied; the
real signed image proof must exist. The server never accepts these inputs from a
Portal/Admin request. LoadEnvironment accepts exactly the documented env-file key;
Load merges only omitted safe policy defaults and rejects duplicate/unknown/null
fields. Policy durations use seconds, memory MiB, CPU millicores and sizes bytes;
the schema documents ranges/defaults. operation_seconds>=request_seconds.

CA/token/proof/config files must meet the existing bundle protected-file owner,
regular-file, canonical path, no-symlink and0600 rules. No ambient kubeconfig,
exec plugin, proxy or TLS bypass is supported. Explicit HTTPS DNS/IP and IPv6 SAN
verification apply; every resolved IP must be within full private/loopback CIDRs.
The bearer token must be the actual named scoped control ServiceAccount identity
and pass current API authorization. It is never sent to the probe Pod.

Factory verification fields reuse the existing actual manufacturer authority and
budgets; allow_test_fixture=false is mandatory production behavior. New policies
and files apply on component reconstruction/restart; a changed fingerprint fences
persisted execution until explicit current-Admin same-scope rebind. The original control loader maps the optional protected component reference
through private_cluster_config_file and its documented serve --env-file key.
Compose/Helm protected mount integration remains an explicit mainline delivery
obligation; the presence of this example does not establish installed deployment.

The existing policy.maximum_body_bytes also bounds each complete public JSON data
envelope plus newline. Public maximum_response_bytes projects that exact selected
value (zero when disabled), without a second setting. List collection accounts
encoded items/commas before retaining more data; an exact final cursor/envelope
check rejects overflow. Lowering this budget or raising page_size may make a
retained large page unreadable: increase the protected budget within its validated
range or reduce page_size, then reconstruct/restart the same artifact. Do not
truncate stored observations or clear an uncertain request to make a read pass.
The strict browser decoder/stream limit and actual SQL recovery checks remain
separate acceptance obligations until their recorded gates pass.


Protected JSON fields must use their exact documented names at every nesting
level; case aliases and aliases beside a canonical field are refused. Bare HTTPS
origins exclude even empty `?`/`#` suffixes. CA files contain only PEM certificates
and whitespace; each certificate must be a currently valid signing CA. Replacing
a token or CA changes the existing credential fingerprint and requires explicit
same-scope rebind under current business authority. An expired or RBAC-rejected
credential never establishes absence or releases retained capacity; rotate the
protected operator-provisioned credential and reconcile through current authority.
