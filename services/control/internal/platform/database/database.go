package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

// InstallationLockID serializes the server and local installation operators.
const InstallationLockID int64 = 713090002
const migrationTransactionLockID int64 = 713090001

// MigrationConnection lets a scoped operator keep DDL on the exact session whose
// identity and installation lock it verified. Ordinary test setup may use a pool.
type MigrationConnection interface {
	Begin(context.Context) (pgx.Tx, error)
}

func Open(ctx context.Context, c config.Config) (*pgxpool.Pool, error) {
	if e := c.ValidateDatabaseLockTimeout(); e != nil {
		return nil, e
	}
	d, e := config.Secret(c.Database.DSNFile)
	if e != nil {
		return nil, e
	}
	pc, e := pgxpool.ParseConfig(strings.TrimSpace(string(d)))
	if e != nil {
		return nil, errors.New("DATABASE_CONFIG_INVALID")
	}
	pc.MaxConns = c.Database.MaxOpenConnections
	pc.MinConns = c.Database.MaxIdleConnections
	pc.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pc.ConnConfig.RuntimeParams["statement_timeout"] = strconv.Itoa(c.Database.StatementTimeoutSeconds * 1000)
	pc.ConnConfig.RuntimeParams["lock_timeout"] = strconv.Itoa(c.DatabaseLockTimeoutMilliseconds())
	p, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		return nil, errors.New("DATABASE_UNAVAILABLE")
	}
	if e = p.Ping(ctx); e != nil {
		p.Close()
		return nil, connectionError(e)
	}
	return p, nil
}

type migration struct {
	Version  int
	Checksum string
	SQL      []byte
}

// LineageName names the one PostgreSQL lineage in offline delivery contracts
// inherited from project-one, which still carry it as application_profile.
const LineageName = "mvp-postgresql-cpu-v1"

// CheckPrefixWithin reports how many lineage migrations the ledger holds. The
// ledger must be an exact prefix of the lineage, with matching checksums.
func CheckPrefixWithin(ctx context.Context, q transaction.Executor) (int, error) {
	if ctx == nil || q == nil || q.Dialect() != transaction.PostgreSQL {
		return 0, errors.New("SCHEMA_INCOMPATIBLE")
	}
	list, e := ordered()
	if e != nil {
		return 0, e
	}
	rows, e := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT version,checksum FROM schema_migrations ORDER BY version"))
	if e != nil {
		return 0, errors.New("SCHEMA_UNAVAILABLE")
	}
	defer rows.Close()
	applied := 0
	for rows.Next() {
		var version int
		var checksum string
		if rows.Scan(&version, &checksum) != nil {
			return 0, errors.New("SCHEMA_UNAVAILABLE")
		}
		if applied >= len(list) || version != list[applied].Version || checksum != list[applied].Checksum {
			return 0, errors.New("SCHEMA_INCOMPATIBLE")
		}
		applied++
	}
	if rows.Err() != nil {
		return 0, errors.New("SCHEMA_UNAVAILABLE")
	}
	return applied, nil
}

// SchemaVersion is the number of migrations in the one PostgreSQL lineage.
const SchemaVersion = 44

// TableCount is the number of public tables the complete lineage creates.
const TableCount = 115

// ordered reads the one migration lineage. File names carry a contiguous
// version prefix; the checksum covers the exact file bytes.
func ordered() ([]migration, error) {
	entries, e := migrations.Files.ReadDir("postgresql")
	if e != nil {
		return nil, errors.New("MIGRATION_ASSET_MISSING")
	}
	list := []migration{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		version, e := strconv.Atoi(prefix)
		if !ok || e != nil || version != len(list)+1 {
			return nil, errors.New("MIGRATION_SEQUENCE_INVALID")
		}
		b, e := migrations.Files.ReadFile("postgresql/" + entry.Name())
		if e != nil {
			return nil, e
		}
		h := sha256.Sum256(b)
		list = append(list, migration{version, hex.EncodeToString(h[:]), b})
	}
	if len(list) != SchemaVersion {
		return nil, errors.New("MIGRATION_ASSET_MISSING")
	}
	return list, nil
}

// Migrate applies the one ordered PostgreSQL lineage under the migration lock.
// The applied ledger must be an exact prefix of the lineage; anything else is
// rejected instead of being reinterpreted. The finished schema must match the
// expected catalog and grants before the transaction commits.
func Migrate(ctx context.Context, connection MigrationConnection) error {
	if ctx == nil || connection == nil {
		return errors.New("MIGRATION_CONNECTION_REQUIRED")
	}
	list, e := ordered()
	if e != nil {
		return e
	}
	tx, e := connection.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationTransactionLockID); e != nil {
		return e
	}
	q := transaction.PostgreSQLTransaction(tx)
	if e = CheckKnowledgeVectorWithin(ctx, q); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(version integer PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP)"); e != nil {
		return e
	}
	rows, e := tx.Query(ctx, "SELECT version,checksum FROM schema_migrations ORDER BY version")
	if e != nil {
		return e
	}
	applied := 0
	for rows.Next() {
		var version int
		var checksum string
		if e = rows.Scan(&version, &checksum); e != nil {
			rows.Close()
			return e
		}
		if applied >= len(list) {
			rows.Close()
			return errors.New("SCHEMA_INCOMPATIBLE")
		}
		if version != list[applied].Version || checksum != list[applied].Checksum {
			rows.Close()
			return errors.New("MIGRATION_CHECKSUM_MISMATCH")
		}
		applied++
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, m := range list[applied:] {
		if _, e = tx.Exec(ctx, string(m.SQL)); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, "INSERT INTO schema_migrations(version,checksum) VALUES($1,$2)", m.Version, m.Checksum); e != nil {
			return e
		}
	}
	if e = CheckWithin(ctx, q); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		if errors.Is(e, pgx.ErrTxCommitRollback) {
			return &transaction.Error{Kind: transaction.CommitRolledBack}
		}
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return nil
}

// Check verifies the complete lineage, catalog and grants on a pool.
func Check(ctx context.Context, p *pgxpool.Pool) error {
	if ctx == nil || p == nil {
		return errors.New("SCHEMA_UNAVAILABLE")
	}
	q, e := transaction.FromPostgreSQL(p)
	if e != nil {
		return e
	}
	return CheckWithin(ctx, q)
}

// CheckWithin verifies the complete lineage, catalog and grants on a borrowed
// PostgreSQL executor, so restore and maintenance bind the check to their own
// transaction. Operator principals are accepted; serving admission uses
// CheckRuntimeWithin.
func CheckWithin(ctx context.Context, q transaction.Executor) error {
	if ctx == nil || q == nil || q.Dialect() != transaction.PostgreSQL {
		return errors.New("SCHEMA_INCOMPATIBLE")
	}
	if e := CheckKnowledgeVectorWithin(ctx, q); e != nil {
		return e
	}
	list, e := ordered()
	if e != nil {
		return e
	}
	rows, e := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT version,checksum FROM schema_migrations ORDER BY version"))
	if e != nil {
		return errors.New("SCHEMA_UNAVAILABLE")
	}
	e = checkMigrationRows(rows, list)
	rows.Close()
	if e != nil {
		return e
	}
	if e = checkCatalogWithin(ctx, q); e != nil {
		return e
	}
	return checkCurrentPrivileges(ctx, q, true)
}

func checkMigrationRows(rows transaction.Rows, list []migration) error {
	count := 0
	for rows.Next() {
		var v int
		var h string
		if rows.Scan(&v, &h) != nil {
			return errors.New("SCHEMA_UNAVAILABLE")
		}
		if count >= len(list) || v != list[count].Version || h != list[count].Checksum {
			return errors.New("SCHEMA_INCOMPATIBLE")
		}
		count++
	}
	if rows.Err() != nil {
		return errors.New("SCHEMA_UNAVAILABLE")
	}
	if count != len(list) {
		return errors.New("SCHEMA_INCOMPATIBLE")
	}
	return nil
}
func connectionError(e error) error {
	var pg *pgconn.PgError
	if errors.As(e, &pg) && (pg.Code == "28P01" || pg.Code == "28000") {
		return errors.New("DATABASE_AUTHENTICATION_FAILED")
	}
	var dns *net.DNSError
	if errors.As(e, &dns) {
		return errors.New("DATABASE_DNS_UNRESOLVED")
	}
	var network *net.OpError
	if errors.As(e, &network) {
		return errors.New("DATABASE_NETWORK_UNREACHABLE")
	}
	return errors.New("DATABASE_UNAVAILABLE")
}
