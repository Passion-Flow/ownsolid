package scope

import (
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Populated only by the original restore owner's protected execution/completion
// getter and three completed native stages. It is not a public DTO or authority
// obtained by decoding a caller-provided reference.
type mvpNativeRestoredBackupOriginal struct {
	Request    wire.PostgreSQLRestoreRequest            `json:"-"`
	Writer     engine.DatabaseTask                      `json:"-"`
	Completion engine.NativePostgreSQLRestoreCompletion `json:"-"`
	Reference  wire.NativePostgreSQLRestoreReference    `json:"-"`
}
