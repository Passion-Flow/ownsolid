package main

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/bootstrap"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/storeinitialization"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/damengserving"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlmigrate"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// This is a capability invariant. Candidate source files do not activate it.
const completeMySQLSchemaSteps = 265

type selectedDatabase struct {
	native    damengserving.Runtime
	postgres  *pgxpool.Pool
	pgLease   *pgxpool.Conn
	mysql     *mysqlstore.Store
	myLease   *sql.Conn
	store     transaction.Store
	catalog   mysqlmigrate.Catalog
	options   mysqlmigrate.Options
	lock      string
	verify    chan struct{}
	workers   <-chan struct{}
	cleanup   int
	leaseLost atomic.Bool // Irreversible, including a timed-out verification-slot wait.
}

func migrationOptions(c config.Config, catalog mysqlmigrate.Catalog) mysqlmigrate.Options {
	p := c.MySQLMigrationPolicy()
	return mysqlmigrate.Options{LockWaitSeconds: p.LockWaitSeconds, StepSeconds: p.StepSeconds, TotalSeconds: p.TotalSeconds, CleanupSeconds: p.CleanupSeconds, MaximumSteps: catalog.Steps(), MaximumMetadataBytes: p.MaximumMetadataBytes}
}

func mysqlMigrationCommand(command string) bool {
	return command == "migrate" || command == "reconcile-mysql-migrations" || command == databaseaccess.MigrationCommand
}

func validateSelectedCommand(c config.Config, command string) error {
	if e := c.ValidateDatabaseSelection(); e != nil {
		return e
	}
	if c.DatabaseEngine() == config.DamengDatabaseEngine {
		if command != "serve" && command != "check-schema" {
			return errors.New("DAMENG_SERVING_COMMAND_UNSUPPORTED")
		}
		return nil
	}
	if c.DatabaseEngine() != config.MySQLDatabaseEngine {
		if mysqlNativeCommand(command) || databaseaccess.MySQLRestoredCommand(command) && command != "prepare-restored-runtime-access" {
			return errors.New("MYSQL_COMMAND_REQUIRES_MYSQL_SELECTION")
		}
		if command == "reconcile-mysql-migrations" || command == "verify-mysql-installation" || command == databaseaccess.ReconcileMySQLCommand || command == databaseaccess.ResumeMySQLCommand || command == bootstrap.PrepareMySQLPacketCommand || command == storeinitialization.CheckModelCommand || command == storeinitialization.RecordModelCommand {
			return errors.New("MYSQL_COMMAND_REQUIRES_MYSQL_SELECTION")
		}
		return nil
	}
	return nil
}

func (d *selectedDatabase) mysqlOperator(ctx context.Context, c config.Config) (*mysqloperator.Context, error) {
	if d == nil || d.mysql == nil || d.myLease == nil || d.postgres != nil || d.pgLease != nil {
		return nil, errors.New("MYSQL_OPERATOR_CONTEXT_REQUIRED")
	}
	return mysqloperator.New(ctx, d.mysql, d.myLease, c, d.catalog, d.options)
}

func requireCompleteMySQLSchema(steps int) error {
	if steps != completeMySQLSchemaSteps {
		return errors.New("MYSQL_COMPLETE_SCHEMA_NOT_ACTIVATED")
	}
	return nil
}

func openSelectedDatabase(ctx context.Context, c config.Config, command string) (*selectedDatabase, error) {
	if e := validateSelectedCommand(c, command); e != nil {
		return nil, e
	}
	d := &selectedDatabase{cleanup: c.MySQLMigrationPolicy().CleanupSeconds, verify: make(chan struct{}, 1)}
	if c.DatabaseEngine() == config.DamengDatabaseEngine {
		minimumConnections := 2 // One pinned installation lease plus one business/readback connection.
		if c.MongoDB.Enabled {
			minimumConnections = max(minimumConnections, c.MongoDB.MaximumConcurrent+2)
		}
		var e error
		d.native, e = damengserving.OpenProtected(ctx, c.Database.Dameng.RuntimeConfigurationFile, c.Database.Dameng.SchemaConfigurationFile, minimumConnections)
		if d.native != nil {
			d.cleanup = d.native.CleanupSeconds()
			d.store = d.native.Repository()
		}
		if e != nil {
			return d, e
		}
		if d.native == nil || d.store == nil || d.store.Dialect() != transaction.Dameng {
			return d, errors.New("DAMENG_SERVING_OWNER_REQUIRED")
		}
		return d, nil
	}
	if c.DatabaseEngine() == config.PostgreSQLDatabaseEngine {
		if command != "migrate" && c.Database.MaxOpenConnections < 2 {
			return nil, errors.New("INSTALLATION_POOL_REQUIRES_TWO_CONNECTIONS")
		}
		pool, e := database.Open(ctx, c)
		if e != nil {
			return nil, e
		}
		d.postgres = pool
		d.store, e = transaction.FromPostgreSQL(pool)
		if e == nil {
			d.pgLease, e = pool.Acquire(ctx)
		}
		if e != nil {
			_ = d.Close()
			return nil, errors.New("OPERATOR_LOCK_UNAVAILABLE")
		}
		var acquired bool
		if e = d.pgLease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&acquired); e != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.Seconds(d.cleanup))
			_ = d.pgLease.Hijack().Close(cleanup)
			cancel()
			d.pgLease = nil
			_ = d.Close()
			return nil, errors.New("INSTALLATION_LOCK_OUTCOME_UNKNOWN")
		}
		if !acquired {
			d.pgLease.Release()
			d.pgLease = nil
			_ = d.Close()
			return nil, errors.New("INSTALLATION_PROCESS_ALREADY_RUNNING")
		}
		return d, nil
	}
	catalog, e := mysqlmigrate.DefaultCatalog()
	if e != nil {
		return nil, e
	}
	if !mysqlMigrationCommand(command) && command != "check-schema" && command != "verify-mysql-installation" {
		if e = requireCompleteMySQLSchema(catalog.Steps()); e != nil {
			return nil, e
		}
	}
	input, e := mysqlstore.Load(c.Database.MySQLConfigurationFile, "")
	if e != nil {
		return nil, e
	}
	if !mysqlMigrationCommand(command) && input.Policy.MaximumOpenConnections < 2 {
		return nil, errors.New("INSTALLATION_POOL_REQUIRES_TWO_CONNECTIONS")
	}
	d.catalog = catalog
	d.options = migrationOptions(c, catalog)
	d.mysql, _, e = mysqlstore.Open(ctx, input)
	if e != nil {
		return nil, e
	}
	d.store, e = transaction.FromMySQL(d.mysql)
	if e != nil {
		_ = d.Close()
		return nil, e
	}
	if mysqlMigrationCommand(command) {
		return d, nil
	}
	d.myLease, e = d.mysql.Pool().Conn(ctx)
	if e != nil {
		_ = d.Close()
		return nil, errors.New("OPERATOR_LOCK_UNAVAILABLE")
	}
	var actual string
	if e = d.myLease.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&actual); e != nil || actual != input.Database {
		_ = d.Close()
		return nil, errors.New("MYSQL_SESSION_INVARIANT_INVALID")
	}
	d.lock, e = mysqlmigrate.LockName(actual)
	if e != nil {
		_ = d.Close()
		return nil, e
	}
	var acquired sql.NullInt64
	if e = d.myLease.QueryRowContext(ctx, "SELECT GET_LOCK(?,0)", d.lock).Scan(&acquired); e != nil || !acquired.Valid {
		d.discardMySQLLease()
		_ = d.Close()
		return nil, errors.New("INSTALLATION_LOCK_OUTCOME_UNKNOWN")
	}
	if acquired.Int64 != 0 && acquired.Int64 != 1 {
		d.discardMySQLLease()
		_ = d.Close()
		return nil, errors.New("INSTALLATION_LOCK_OUTCOME_UNKNOWN")
	}
	if acquired.Int64 != 1 {
		_ = d.myLease.Close()
		d.myLease = nil
		d.lock = ""
		_ = d.Close()
		return nil, errors.New("INSTALLATION_PROCESS_ALREADY_RUNNING")
	}
	return d, nil
}

func (d *selectedDatabase) identity(c config.Config) *identity.Service {
	s := &identity.Service{DB: d.postgres, Config: c}
	// Native and MySQL services borrow the selected repository and have no
	// PostgreSQL pool that could accidentally become an alternate path.
	if d.mysql != nil || d.native != nil {
		s.Storage = d.store
	}
	return s
}

func (d *selectedDatabase) Verify(ctx context.Context) error {
	if d == nil || d.store == nil {
		return errors.New("SELECTED_DATABASE_REQUIRED")
	}
	select {
	case d.verify <- struct{}{}:
		defer func() { <-d.verify }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if d.leaseLost.Load() {
		return errors.New("INSTALLATION_LOCK_LOST")
	}
	if d.native != nil {
		return d.native.Verify(ctx)
	}
	if d.postgres != nil {
		if e := d.verifyPostgreSQLLeaseWithin(ctx); e != nil {
			return e
		}
		return database.Check(ctx, d.postgres)
	}
	_, e := mysqlmigrate.VerifyRuntimeLedger(ctx, d.myLease, d.catalog, d.options)
	return e
}

func (d *selectedDatabase) VerifyOperator(ctx context.Context) error {
	if d == nil || d.mysql == nil {
		return errors.New("MYSQL_COMMAND_REQUIRES_MYSQL_SELECTION")
	}
	_, e := mysqlmigrate.VerifyInstallationSession(ctx, d.myLease, d.catalog, d.options)
	return e
}

func (d *selectedDatabase) discardMySQLLease() {
	if d.myLease != nil {
		_ = d.myLease.Raw(func(any) error { return sqldriver.ErrBadConn })
		_ = d.myLease.Close()
		d.myLease = nil
	}
	d.lock = ""
}

// Close refuses to release authority while owned workers remain unjoined. It can
// be called again after their tracked shutdown completes; no success is invented.
func (d *selectedDatabase) Close() error {
	if d == nil {
		return nil
	}
	if d.workers != nil {
		select {
		case <-d.workers:
		default:
			return errors.New("CONTROL_WORKERS_NOT_JOINED")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), config.Seconds(d.cleanup))
	defer cancel()
	var result error
	if d.native != nil {
		if e := d.native.Close(ctx); e != nil {
			return e
		}
		d.native = nil
		d.store = nil
		return nil
	}
	if d.pgLease != nil {
		var released bool
		if e := d.pgLease.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID).Scan(&released); e != nil || !released {
			_ = d.pgLease.Hijack().Close(ctx)
			result = errors.New("INSTALLATION_LOCK_RELEASE_UNCONFIRMED")
		} else {
			d.pgLease.Release()
		}
		d.pgLease = nil
	}
	if d.myLease != nil {
		if d.lock != "" {
			var released sql.NullInt64
			if e := d.myLease.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", d.lock).Scan(&released); e != nil || !released.Valid || released.Int64 != 1 {
				d.discardMySQLLease()
				result = errors.New("INSTALLATION_LOCK_RELEASE_UNCONFIRMED")
			}
		}
		if d.myLease != nil {
			result = errors.Join(result, d.myLease.Close())
			d.myLease = nil
		}
		d.lock = ""
	}
	if d.postgres != nil {
		d.postgres.Close()
		d.postgres = nil
	}
	if d.mysql != nil {
		result = errors.Join(result, d.mysql.Close())
		d.mysql = nil
	}
	return result
}
