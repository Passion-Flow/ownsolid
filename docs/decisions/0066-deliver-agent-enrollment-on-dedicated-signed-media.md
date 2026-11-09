# Deliver Agent enrollment on dedicated signed media

Use the existing offline release verifier and public trust format for a
small Linux amd64 Agent inventory. Bind the native binary, service unit,
notices and SPDX to the signed manifest. The installer needs only this
media and the site's protected configuration, CA and enrollment token.

The Linux installer runs enrollment as the reserved non-root Agent account
before enabling the signed service. Its protected token copy marks a
possibly dispatched enrollment until the Agent validates its completed
identity or explicitly refuses an unusable token. Preserve unresolved
state. Replays of a completed identity can proceed without a token.

See the [release handoff and verification](../research/agent-offline-enrollment.md#signed-host-delivery).
