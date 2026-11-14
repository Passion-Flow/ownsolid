# Scoped assistant name resolution (AGT-111)

Reviewed: 2026-11-12. The first path resolves a published application in the
authenticated Portal workspace without a model. It performs no mutation.

## Sources and licences

[Kubernetes object names and IDs](https://kubernetes.io/docs/concepts/overview/working-with-objects/names/)
distinguish a scoped human name from an object's unique identity. Return IDs
from the resource owner, never manufacture one from model text. The existing
workflow writer normalizes display names to NFC and trims surrounding space;
its name-key uniqueness is a separate, broader normalization rule. Match the
visible NFC name exactly, including case. PostgreSQL's
[explicit C collation](https://www.postgresql.org/docs/18/collation.html)
keeps equality independent of a deployment's linguistic collation. Use bound
parameters, not a pattern or generated SQL.

[Go JSON strings](https://pkg.go.dev/encoding/json/v2) provide one bounded,
unambiguous quoted argument. A complete JSON string must consume the rest of
the command. Prefix normalization must not lowercase or collapse spaces in
the name. The existing command palette and exact-command references are in
[the command research](product-assistant-commands.md).

Reuse Go 1.27.3 (BSD-style), x/text v0.43.0
([BSD-3-Clause](https://raw.githubusercontent.com/golang/text/v0.43.0/LICENSE)),
pgx v5.11.0 (MIT), and PostgreSQL 18.6/pgvector 0.8.7 (PostgreSQL License).
The remaining primary licence links are in the
[budget research](product-assistant-execution-budgets.md#sources-and-licences).
These allow commercial redistribution with notices and offline use. No new
dependency, model, dataset, index or shipped service is introduced.

## Decision

Add English and Simplified Chinese `find application` command templates with
one JSON-quoted visible name. Keep the existing fixed grammar unchanged.
Resolve under the same session, workspace and publication checks as the
application list, in its owning transaction. Reuse the reviewed ListApplications
read permission. This command narrows that projection; it does not expose a
new transport or add an unreviewed API operation to the tool inventory.

The application owner returns `resolved`, `not_found` or `ambiguous`, at most
two authorized candidates, and a canonical application ID only for a unique
match. Disabled, unpublished and foreign resources yield the same empty
result as a nonexistent name. A duplicate visible name never selects the
first row, even if malformed source data bypassed normal name-key creation.
The existing workspace workflow quota is at most 1,000 and the ordinary
business deadline still bounds the scoped equality query.

Read the current publication's immutable version ID and graph digest with
the workflow ID. Future plans must bind those server results and recheck the
owner at execution; name resolution alone authorizes no effect. Keep all
writes and sensitive reads closed. Redact candidate display names using the
existing assistant policy. Store no name in run/step checkpoints or audit
metadata. Include the normalized name in the server execution fingerprint so
an execution UUID cannot be replayed for another lookup.

## Rejected paths and failure tests

- Fuzzy matching, case folding and substring parsing can choose an unintended
  resource. Use exact NFC display-name equality and an explicit no-match state.
- Client/model UUIDs, workspace selectors or arbitrary URLs cannot select scope.
- A prefix followed by free-form text admits compound instructions. Require
  one complete quoted string, valid UTF-8, the 120-character contract bound
  and no control characters. Quoted command-like names remain data.
- Do not copy the workflow owner's private name-key algorithm or create a
  second name index. Do not paginate through and accumulate the whole catalog.
- Preserve commit uncertainty and suppress all candidates after audit, current
  authority, budget or final-policy failure.

Exercise both languages through the guarded real HTTPS path and disconnected
PostgreSQL. Cover NFC, Chinese and maximum-length names, exact case, literal
wildcards and quotes, malformed/trailing JSON, no match, hidden publications,
duplicate names, other users/workspaces/tenants, revoked access, source failure,
budget exhaustion, changed-name replay, cancellation and metadata-only audit.
Reuse existing unchanged core budget evidence. Production route/configuration,
schema registration, the command UI and other resource kinds remain owner
integration requirements; AGT-111 stays unchecked.

## Acceptance and handoff

Source `21b86219` passed the complete `make check`, `make build` and `make
test` gate. Console passed 1,075 browser cases and Portal passed 389, with
their existing 28 and 9 live-environment skips and no automatic retries.
Affected application and assistant race tests and vet passed.

An isolated Linux arm64 PostgreSQL 18.6/pgvector 0.8.7 container in network
mode `none` passed all five selected repository roots, including both new
name-resolution roots, and both real HTTPS roots. The name tests exercised
exact bilingual/NFC lookup, maximum names, literal wildcards and quotes,
command-like names as data, absent/unpublished/disabled/ambiguous resources,
changed-name replay, foreign scope, membership/session revocation, budget
exhaustion, audit refusal, cancellation and final-policy suppression. The
guarded Portal HTTPS flow resolved both language prefixes to the published
UUID and refused disabled execution and a supplied cursor.

The retained repository log SHA-256 is
`ad6f5e8f81f49194dd92c4c60a356556f9ec361dc43230350d5fb6d7ba6dce0d`;
the HTTPS log is
`d5286a03b801356035a9689001cc06582cfd53e8e194bd72aab3782cd9c64a7e`.
The harness initially reported a cleanup failure because its absent-object
check expected capitalized Docker output. A separate exact-container
inspection confirmed removal; the lowercase diagnostic was retained and
the harness check corrected. No product test was rerun to conceal that
harness result. The owned temporary APFS volume was detached and removed.

The native amd64 runner twice refused a busy shared CPU lock before source
construction or containers started. This new lookup therefore has arm64
execution evidence only; earlier native budget/loop evidence does not
qualify it. Reuse the selected test roots above on the native target when
the shared window is available. No persistent test service or queue was
left running. The existing three model safety observations remain
unqualified; this deterministic lookup invokes no model.
