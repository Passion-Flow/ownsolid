# Private-cluster HTTP contracts

This owner now declares the existing 25 authenticated operations (15 Admin,10
Portal), closed required profile/connection/run/observation/proof/mutation DTOs,
and80canonical safe module/configuration error statuses. Admin uses its required
Workspace query; Portal uses the original Workspace path. Existing current session,
Workspace, Origin/CSRF, whole License and fixed safety recovery remain server-owned.
An API declaration and generated types do not grant authority or prove execution.

`tools/contracts/http/generate_privateclusters.py` owns OpenAPI, browser schema,
Go status projection and the public runtime policy. This now includes the actual
maximum_response_bytes from the same protected maximum_body_bytes owner, with
zero bytes/time when disabled. Array/range
bounds derive from owning configuration; credentials, endpoints, paths and
historical execution/session authority stay private. Ordinary Run no longer emits
its internal remote-state document; dedicated proof has the explicit projection.
SQL remote_state serialization and reconciliation still retain that internal data.

```sh
python3 -B tools/contracts/http/generate_privateclusters.py --check
```

The shared ordered generator now includes this owner; use pinned offline
openapi-typescript to regenerate privatecluster-types.ts. Public policy parsing
continues to validate literal false, unknown fields, units and request budgets.
Backend aggregate envelope/item/cursor limits now have pure encoding tests.
Full scoped timed-abort client, actual SQL/browser response bounds, real entrance
and API/browser recovery acceptance remain pending in the
[M28 HTTP contract](../../../../docs/milestones/m28-private-cluster/http-contract.md).
No shape/generator/compile check establishes installed database or cluster behavior.

Actual current contract controls pass the two native JSON/status methods, five
isolated order regressions, whole TypeScript and pinned type regeneration. The
shared Problem union includes128codes; Go module/configuration status projection
has80codes. Historical cookie declarations inherit the current Identity HTTPS
baseline; they do not qualify the pending full selectable transport matrix.
