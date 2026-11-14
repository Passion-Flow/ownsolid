# Gateway keys

Research behind GW-113, the Gateway and keys page: screen S-206 of the
[build plan](../product/plan/README.md). Administrators issue, rotate and revoke the keys that call a
workspace's models, and read each key's calls.

## What exists

The `distribution` module (`api/openapi/v1/distribution`) owns keys. A key belongs to one
workspace and calls exactly one model alias; it is owned by the person who issued it and stays
valid only while that person's account, session epoch and workspace membership hold. Its secret
(`pk1.<id>.<token>`) is returned once at issue and stored only as a MAC. Issue takes a label, the
alias, a lifetime (default `distribution.key_ttl_seconds`, at most `key_max_ttl_seconds`) and a
reason, and refuses an alias that is not available now. Revoke takes a reason and is idempotent.
Both are audited. Lists are cursor pages, newest first; an administrator sees every key of the
workspace, anyone else only their own.

Every call the gateway admits is a row in `gateway_requests` with its key, alias, deployment, state
and, once reported, its outcome (status, error code, tokens, elapsed time).

Three things S-206 needs were missing:

- **Rotation.** No operation replaces a key's secret.
- **A key's calls.** The request list could not be narrowed to one key, and no index served it.
- **A running gateway.** The installer starts no model gateway, so on an installed product no call
  is ever admitted. GW-114 records the gap; the page reads whatever calls the server holds.

## How others do it

- **Rotation with an overlap.** LiteLLM's [`POST /key/{key}/regenerate`](https://docs.litellm.ai/docs/proxy/virtual_keys)
  takes a `grace_period` such as `"24h"`: "Both old and new keys work until the grace period
  elapses"; without one the old key stops at once. [Portkey](https://portkey.ai/docs/changelog/2026/april.md)
  rotates on demand or on a schedule; [Bifrost](https://docs.getbifrost.ai/api-reference/governance/rotate-virtual-key.md)
  has a rotate endpoint. Scheduled rotation is GW-115, added by the module review.
- **What a key row shows.** OpenAI's [project API keys](https://developers.openai.com/api/reference/resources/organization/subresources/projects/subresources/api_keys)
  carry a name, a redacted value, the owner, `created_at` and `last_used_at`; permissions are All,
  Restricted or Read only (restricted keys are GW-116). LiteLLM keys carry `key_alias`, `models`,
  `expires`, `spend`, `rpm_limit`, `tpm_limit` and `max_budget`.
- **Limits.** Portkey and LiteLLM put request and token rate limits and budgets on each key, and a
  key over its limit is refused with 429. ownsolid has no per-key limit yet (GW-106), so the page's
  *Over limit* tile is shown as not ready with that reason
  ([ADR 0036](../decisions/0036-show-every-option-and-fetch-models-offline.md)).
- **Per-key logs.** LiteLLM stores each call's key hash in its spend logs and reports spend per key
  through `/key/info`; the call log itself is per request.

## Licences

No new dependency. The page uses the console's React, TanStack and Lucide packages already in the
lock file.

## Decisions

**Rotation keeps everything but the secret.** `POST .../keys/{id}/rotate` with an
`Idempotency-Key`, `overlap_seconds` (0 to 604800, seven days) and a reason issues a successor with
the same workspace, alias, label, owner, session epoch and membership authority, and the same
lifetime the old key was given, capped by `key_max_ttl_seconds`. The old key then stops after the
overlap, or at once when it is 0, which revokes it with the reason. The successor names the key it
replaced (`rotated_from`) and the old key names its successor (`replaced_by`).

- Ownership stays with the owner, as LiteLLM's regenerate keeps a key's properties: an
  administrator rotating someone's key hands them a new secret for the same access, and the key
  still dies with the owner's membership. The new secret is shown once to whoever rotated it.
- A key has at most one successor, held by a unique index, so two people rotating at once get one
  successor and one `API_KEY_ALREADY_ROTATED`. The same request repeated returns the successor
  without its secret, like issue.
- Only an active key rotates; anything else answers `API_KEY_NOT_ACTIVE`. Rotation does not
  re-check the alias: a key rotated during an outage keeps its scope.
- The owner's active-key limit does not apply to a successor, because the key it replaces stops
  within at most seven days and cannot be rotated again.
- PostgreSQL only. MySQL and Dameng have no column for the successor; there rotation refuses with
  `API_KEY_ROTATION_UNAVAILABLE` before any write, and keys read
  with no successor, as ADR 0019 and ADR 0025 did for their chains (CMP-201, CMP-202).
- Rejected: revoke and issue from the browser. It is two writes, so a failure between them leaves
  the caller with no key, and the new key would lose the owner.

**A key's calls.** The request list takes `api_key_id`; the cursor is bound to it, so a cursor from
one key's log cannot page another's. A new index on `(tenant, workspace, key, admitted_at, id)`
serves it. The drawer reads 20 calls at a time with *Load more*.

**The page.** One workspace at a time, from the switcher, like Model services. Columns: name,
prefix, scope (the model alias), owner, last used, expiry. Tiles: *Expiring soon* (active, ending
within seven days, the longest overlap a rotation can give), *Unused* (active, never used) and
*Over limit* (not ready, GW-106). Issue has five fields: name, model, lifetime, a limit that is not
ready (GW-106) and the reason; it shows the secret once with a copy button and a warning that it
cannot be shown again. Rotate states the overlap in words. Revoke asks for the key's name to be
typed and names what stops: calls to the alias with this key, from now on.

## Failure modes and tests

- Two rotations at once: an integration test runs them concurrently on PostgreSQL 18.6 and expects
  one successor and one `API_KEY_ALREADY_ROTATED`.
- A replayed rotation returns the successor without its secret; a different body with the same
  key answers `IDEMPOTENCY_CONFLICT`.
- Overlap 0 revokes; a positive overlap moves the expiry and never extends it.
- The successor authenticates and the old key keeps working until the overlap ends, through the
  real admission path.
- A non-owner who is not an administrator cannot rotate, list or read another person's key or its
  calls; another workspace's key is not found.
- The request filter rejects a malformed key and a cursor from another key.
- Browser flows with a mocked control plane cover issue with the secret shown once, rotate, revoke,
  the tiles, the empty and failure states, the drawer's call log and paging, permissions and both
  languages; a live run on an installation covers issue, rotate and revoke against a real server.

## Done when

An administrator, from the console, issues a key for a model in a workspace and copies its secret
once; sees it in the list with its scope, owner and expiry; filters by the tiles; rotates it with
an overlap and gets one new secret while the old key keeps working until the overlap ends; revokes
it by typing its name; and opens any key to read only that key's calls. Every write is audited,
enforced on the server, retryable after a failure, and works with no network.
