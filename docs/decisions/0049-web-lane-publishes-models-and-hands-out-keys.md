# 0049: The web lane publishes models to the gateway and hands keys to people

Status: accepted.

## Context

Lane B finished the list in [ADR 0044](0044-web-lane-follows-the-build-plan.md) with GW-113, the Gateway and keys
page. Its live run showed what keeps that page from being used on an installation:

- No page publishes a deployment to the gateway. A key calls an alias, and the run had to provision the gateway
  instance and publish the alias through the API (GW-117).
- The installer starts no model gateway, so an installation admits no call (GW-114).

The build plan's third stage hands the platform to the people who use it. Its key screen, S-306 "My API keys",
needs the same server GW-113 completed: rotation and a key's call log answer the portal's paths too.

## Decision

Lane B continues, in order, with:

1. **GW-117** publishing a deployment to the gateway from its page: provision the node's gateway instance and name
   the alias, so a key can be issued without the API.
2. **POR-106** the portal's API keys, S-306: a person issues, rotates and revokes their own keys and reads their own
   calls, on the endpoints GW-113 extended.
3. **GW-114** the installer delivers and starts the Rust gateway on its entrance, so an installation serves calls.
   It touches `deploy/` and `services/gateway`; lane C's list is finished, and the agent paths lane A works in stay
   untouched.
4. **POR-104** the portal's model catalogue, S-304: which models the person's workspace may call.

UX-131 keeps closing page by page, as ADR 0044 set out.

## Consequences

- After GW-114, the live runs of GW-113 and POR-106 can replace the fixture that stands in for the gateway with the
  real process.
- The playground (POR-105) needs a running gateway and follows GW-114 in a later list.
