package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

const MVPPostgreSQLCPUV1Profile = "mvp-postgresql-cpu-v1"
const MVPPostgreSQLCPUV1SchemaVersion = 29
const MVPPostgreSQLCPUV1TableCount = 108

func orderedMVPPostgreSQLCPUV1() ([]migration, error) {
	list, err := orderedProfile("releases")
	if err != nil || len(list) != 22 {
		return nil, errors.New("MVP_POSTGRESQL_PREFIX_INVALID")
	}
	entries, err := migrations.Files.ReadDir("postgresql/" + MVPPostgreSQLCPUV1Profile)
	if err != nil {
		return nil, errors.New("MIGRATION_ASSET_MISSING")
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			return nil, errors.New("MVP_POSTGRESQL_LINEAGE_INVALID")
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		version, err := strconv.Atoi(prefix)
		if !ok || err != nil || version != len(list)+1 {
			return nil, errors.New("MIGRATION_SEQUENCE_INVALID")
		}
		raw, err := migrations.Files.ReadFile("postgresql/" + MVPPostgreSQLCPUV1Profile + "/" + entry.Name())
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(raw)
		list = append(list, migration{version, hex.EncodeToString(digest[:]), raw})
	}
	if len(list) != MVPPostgreSQLCPUV1SchemaVersion {
		return nil, errors.New("MVP_POSTGRESQL_LINEAGE_INVALID")
	}
	return list, nil
}

// CheckMVPPostgreSQLCPUV1Within verifies the independent complete lineage and
// full native catalog on the caller's original borrowed transaction/session.
func CheckMVPPostgreSQLCPUV1Within(ctx context.Context, q transaction.Executor) error {
	if ctx == nil || q == nil || q.Dialect() != transaction.PostgreSQL {
		return errors.New("SCHEMA_INCOMPATIBLE")
	}
	if err := CheckKnowledgeVectorWithin(ctx, q); err != nil {
		return err
	}
	list, err := orderedMVPPostgreSQLCPUV1()
	if err != nil {
		return err
	}
	rows, err := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT version,checksum FROM schema_migrations ORDER BY version"))
	if err != nil {
		return errors.New("SCHEMA_UNAVAILABLE")
	}
	err = checkMigrationRows(rows, list)
	rows.Close()
	if err != nil {
		return err
	}
	if err = checkMVPPostgreSQLCPUV1CatalogWithin(ctx, q); err != nil {
		return err
	}
	return checkMVPPostgreSQLCPUV1CurrentPrivileges(ctx, q, true)
}

func migrateMVPPostgreSQLCPUV1(ctx context.Context, connection MigrationConnection) error {
	if ctx == nil || connection == nil {
		return errors.New("MIGRATION_CONNECTION_REQUIRED")
	}
	list, err := orderedMVPPostgreSQLCPUV1()
	if err != nil {
		return err
	}
	tx, err := connection.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationTransactionLockID); err != nil {
		return err
	}
	q := transaction.PostgreSQLTransaction(tx)
	if err = CheckKnowledgeVectorWithin(ctx, q); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(version integer PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP)"); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, "SELECT version,checksum FROM schema_migrations ORDER BY version")
	if err != nil {
		return err
	}
	applied := 0
	for rows.Next() {
		var version int
		var checksum string
		if err = rows.Scan(&version, &checksum); err != nil {
			rows.Close()
			return err
		}
		if applied >= len(list) || version != list[applied].Version || checksum != list[applied].Checksum {
			rows.Close()
			return errors.New("MIGRATION_CHECKSUM_MISMATCH")
		}
		applied++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// No implicit conversion from platform27 or an incomplete precursor.
	if applied != 0 && applied != 22 && applied != len(list) {
		return errors.New("MVP_POSTGRESQL_PRECURSOR_REQUIRED")
	}
	if applied == 22 {
		if err = CheckAccountProfileWithin(ctx, q, "releases"); err != nil {
			return err
		}
	}
	for _, m := range list[applied:] {
		if _, err = tx.Exec(ctx, string(m.SQL)); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO schema_migrations(version,checksum) VALUES($1,$2)", m.Version, m.Checksum); err != nil {
			return err
		}
	}
	if err = CheckMVPPostgreSQLCPUV1Within(ctx, q); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		if errors.Is(err, pgx.ErrTxCommitRollback) {
			return &transaction.Error{Kind: transaction.CommitRolledBack}
		}
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return nil
}
