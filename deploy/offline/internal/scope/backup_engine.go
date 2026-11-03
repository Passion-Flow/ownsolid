package scope

import (
	"context"
	"io"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

// The production implementation is the bounded Unix Engine client. Tagged
// integration tests can wrap exact real effects to exercise response loss.
type backupEngine interface {
	storageEngine
	databaseTaskActionEngine
	RemoveBackupVolume(context.Context, engine.Ownership) error
	CreatePostgresBackupTask(context.Context, engine.PostgresBackupTask) (engine.RuntimeContainer, error)
	InspectPostgresBackupTask(context.Context, engine.PostgresBackupTask) (engine.RuntimeContainer, bool, error)
	StartPostgresBackupTask(context.Context, engine.PostgresBackupTask, string, []engine.ConfigurationFile, int) (engine.ResourceAdmission, error)
	StopPostgresBackupTask(context.Context, engine.PostgresBackupTask, string, int) error
	RemovePostgresBackupTask(context.Context, engine.PostgresBackupTask, string) error
	StreamPostgresBackupArchive(context.Context, engine.PostgresBackupTask, string, int64, int, io.Writer) (engine.PostgresBackupArchive, error)
	StreamStoreBackupArchive(context.Context, engine.DatabaseTask, string, int64, int, int, io.Writer) (engine.StoreBackupArchive, error)
}
