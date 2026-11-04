# Private node identity and process lock

[state.go](state.go) owns the persisted node identity and exclusive agent lock. Identity contains the enrolled node, protected credential, control origin and protocol/profile binding. It is operational state, not a tracked configuration example or a workload result.

Directory requires a canonical absolute, caller-owned private directory and rejects symlink resolution changes. ProtectedFile checks a bounded regular private file and compares the opened inode with the inspected one. Load strictly decodes identity. Lock uses a private no-follow file and a nonblocking exclusive flock; a second active agent is an error.

SaveNew creates and syncs a private temporary inode, links it exclusively as identity.json and syncs the containing directory. It refuses an existing identity rather than overwriting it. A failed save or directory sync is not successful enrollment; retain the original outcome for the runtime's reconciliation path.

Do not extend this package into a shared credential manager or move account/keychain state. Synchronize [runtime](../runtime/README.md), enrollment outcome handling and actual file/lock failure tests when changing identity format or durability semantics. Focused check: `go -C services/agent test ./internal/state`. Tests must preserve real private-file/ownership/consumer assumptions; no cleanup of another process's lock or identity is allowed.

The identity records the site and cell returned at enrollment. Load and SaveNew refuse an
identity without them, and the client addresses that cell on every node path (ADR 0006).
