package postgresbackup

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
)

type backupSchema struct{ version, tables int }

// The request selects a real lineage, not an arbitrary version/table-count
// pair. Historical omitted requests retain exact releases22/86 semantics.
func selectedBackupSchema(profile string) (backupSchema, error) {
	if profile != database.LineageName {
		return backupSchema{}, errors.New("POSTGRES_BACKUP_APPLICATION_PROFILE_INVALID")
	}
	return backupSchema{database.SchemaVersion, database.TableCount}, nil
}
func matchesDatabaseProfile(profile string, data DatabaseReadback) bool {
	spec, e := selectedBackupSchema(profile)
	return e == nil && data.SchemaVersion == spec.version && data.Tables == spec.tables
}

// Reuse the original principal/grant authority on this one selected pool and
// held installation session. An explicit runtime env file is part of the same
// typed input; no second credential parser or runtime connection is opened.
func verifyMVPPrincipal(ctx context.Context, pool *pgxpool.Pool, lease *pgxpool.Conn, r release.PostgreSQLBackupRequest) error {
	if r.EffectiveApplicationProfile() != database.LineageName {
		return nil
	}
	if ctx == nil || pool == nil || lease == nil {
		return errors.New("POSTGRES_BACKUP_LOCKED_CONTEXT_REQUIRED")
	}
	input, e := databaseaccess.LoadRuntimeWithEnvironment(r.RuntimeConfigurationFile, r.RuntimeEnvironmentFile)
	if e != nil {
		return e
	}
	return databaseaccess.VerifyRuntimePrincipal(ctx, pool, lease, input)
}
