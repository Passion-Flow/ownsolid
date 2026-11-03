package databaseaccess

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const MVPPostgreSQLCPUV1UpgradeCommand = "upgrade-mvp-postgresql-cpu-v1"
const MVPPostgreSQLCPUV1InspectCommand = "inspect-mvp-postgresql-cpu-v1"
const MVPPostgreSQLCPUV1UpgradeRequired = "MVP_POSTGRESQL_UPGRADE_REQUIRED"
const MVPPostgreSQLCPUV1GrantsPending = "MVP_POSTGRESQL_SCHEMA_COMMITTED_GRANTS_PENDING"
const MVPPostgreSQLCPUV1UpgradeComplete = "MVP_POSTGRESQL_UPGRADE_COMPLETE"

type mvpPrincipal struct {
	oid      uint32
	verifier string
}

func readMVPPrincipal(ctx context.Context, q queryer, input RuntimeInput) (mvpPrincipal, error) {
	var identity mvpPrincipal
	if err := q.QueryRow(ctx, "SELECT oid,rolpassword FROM pg_authid WHERE rolname=$1", input.connection.ConnConfig.User).Scan(&identity.oid, &identity.verifier); err != nil {
		return identity, errors.New("MVP_POSTGRESQL_EXISTING_RUNTIME_ROLE_REQUIRED")
	}
	return identity, nil
}

// InspectMVPPostgreSQLCPUV1 is read-only. It admits only the exact accepted
// account22 precursor or exact new catalog, with either the complete old or
// complete new grant set. Partial grants, foreign ledgers and elevated roles fail.
// It keeps the caller's original held installation-lock session throughout.
func InspectMVPPostgreSQLCPUV1(ctx context.Context, p *pgxpool.Pool, lease *pgxpool.Conn, input RuntimeInput) (string, error) {
	if !input.configuration.MVPPostgreSQLCPUV1Only() || input.configuration.ValidateApplicationProfile() != nil {
		return "", errors.New("MVP_POSTGRESQL_UPGRADE_PROFILE_REQUIRED")
	}
	target, err := lockedTarget(ctx, p, lease, input)
	if err != nil {
		return "", err
	}
	tx, err := lease.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return "", errors.New("MVP_POSTGRESQL_INSPECTION_UNAVAILABLE")
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	q := transaction.PostgreSQLTransaction(tx)
	complete := database.CheckMVPPostgreSQLCPUV1Within(ctx, q) == nil
	if !complete {
		if err = database.CheckAccountProfileWithin(ctx, q, "releases"); err != nil {
			return "", errors.New("MVP_POSTGRESQL_PRECURSOR_OR_TARGET_REQUIRED")
		}
	}
	var bootstrapped bool
	if err = tx.QueryRow(ctx, "SELECT bootstrap_complete FROM installation_state WHERE id=1").Scan(&bootstrapped); err != nil || !bootstrapped {
		return "", errors.New("MVP_POSTGRESQL_ACCEPTED_INSTALLATION_REQUIRED")
	}
	if complete {
		if exists, e := inspectRole(ctx, tx, target, input); e == nil && exists {
			return MVPPostgreSQLCPUV1UpgradeComplete, nil
		}
	}
	old := input
	oldProfile := "releases"
	old.configuration.Server.ApplicationProfile = &oldProfile
	exists, err := inspectRole(ctx, tx, target, old)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", errors.New("MVP_POSTGRESQL_EXISTING_RUNTIME_ROLE_REQUIRED")
	}
	if complete {
		return MVPPostgreSQLCPUV1GrantsPending, nil
	}
	return MVPPostgreSQLCPUV1UpgradeRequired, nil
}

type mvpUpgradeSteps struct {
	migrate func(context.Context, database.MigrationConnection, ...string) error
	commit  func(context.Context, pgx.Tx) error
}

// UpgradeMVPPostgreSQLCPUV1 appends the complete schema in one transaction, then
// atomically grants exactly the 22 new application tables. It never recreates or
// rekeys a principal, changes old checksums, invalidates accounts or bootstraps.
// After any unknown commit, this invocation returns no success. A later explicit
// invocation first inspects the original state and never replays committed DDL.
func UpgradeMVPPostgreSQLCPUV1(ctx context.Context, p *pgxpool.Pool, lease *pgxpool.Conn, input RuntimeInput) (string, error) {
	return upgradeMVPPostgreSQLCPUV1(ctx, p, lease, input, mvpUpgradeSteps{database.Migrate, func(ctx context.Context, tx pgx.Tx) error { return tx.Commit(ctx) }})
}
func upgradeMVPPostgreSQLCPUV1(ctx context.Context, p *pgxpool.Pool, lease *pgxpool.Conn, input RuntimeInput, steps mvpUpgradeSteps) (string, error) {
	state, err := InspectMVPPostgreSQLCPUV1(ctx, p, lease, input)
	if err != nil {
		return "", err
	}
	target, err := lockedTarget(ctx, p, lease, input)
	if err != nil {
		return "", err
	}
	before, err := readMVPPrincipal(ctx, lease, input)
	if err != nil {
		return "", err
	}
	if state == MVPPostgreSQLCPUV1UpgradeRequired {
		if err = steps.migrate(ctx, lease, database.MVPPostgreSQLCPUV1Profile); err != nil {
			if outcome, ok := transaction.CommitOutcome(err); ok {
				return "", errors.New(string(outcome))
			}
			return "", errors.New("MVP_POSTGRESQL_UPGRADE_MIGRATION_FAILED")
		}
		// Re-observe schema and exact old grants before the separate atomic phase.
		state, err = InspectMVPPostgreSQLCPUV1(ctx, p, lease, input)
		if err != nil {
			return "", err
		}
	}
	if state == MVPPostgreSQLCPUV1GrantsPending {
		tx, err := lease.Begin(ctx)
		if err != nil {
			return "", errors.New("MVP_POSTGRESQL_GRANT_TRANSACTION_UNAVAILABLE")
		}
		defer tx.Rollback(context.WithoutCancel(ctx))
		current, e := readMVPPrincipal(ctx, tx, input)
		if e != nil || current != before {
			return "", errors.New("MVP_POSTGRESQL_RUNTIME_IDENTITY_CHANGED")
		}
		role := pgx.Identifier{input.connection.ConnConfig.User}.Sanitize()
		for _, name := range postgreSQLVariantTables {
			if _, err = tx.Exec(ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON TABLE "+pgx.Identifier{target.Schema, name}.Sanitize()+" TO "+role); err != nil {
				return "", errors.New("MVP_POSTGRESQL_UPGRADE_GRANT_FAILED")
			}
		}
		if exists, e := inspectRole(ctx, tx, target, input); e != nil || !exists {
			return "", errors.New("MVP_POSTGRESQL_UPGRADE_GRANT_CONFLICT")
		}
		if err = steps.commit(ctx, tx); err != nil {
			if errors.Is(err, pgx.ErrTxCommitRollback) {
				return "", errors.New(string(transaction.CommitRolledBack))
			}
			return "", errors.New(string(transaction.CommitUnknown))
		}
	}
	after, err := readMVPPrincipal(ctx, lease, input)
	if err != nil || after != before {
		return "", errors.New("MVP_POSTGRESQL_RUNTIME_IDENTITY_CHANGED")
	}
	state, err = InspectMVPPostgreSQLCPUV1(ctx, p, lease, input)
	if err != nil {
		return "", err
	}
	if state != MVPPostgreSQLCPUV1UpgradeComplete {
		return "", errors.New("MVP_POSTGRESQL_UPGRADE_INCOMPLETE")
	}
	if err = checkRuntimeConnection(ctx, target, input); err != nil {
		return "", err
	}
	return MVPPostgreSQLCPUV1UpgradeComplete, nil
}

// VerifyMVPPostgreSQLCPUV1Principal checks the existing exact DML-only runtime
// principal on the original locked operator session. It never opens another pool,
// logs credentials, changes grants or attempts a runtime login. Native snapshot
// callers retain ownership of their held session and separately checked lineage.
func VerifyMVPPostgreSQLCPUV1Principal(ctx context.Context, p *pgxpool.Pool, lease *pgxpool.Conn, input RuntimeInput) error {
	if !input.configuration.MVPPostgreSQLCPUV1Only() || input.configuration.ValidateApplicationProfile() != nil {
		return errors.New("MVP_POSTGRESQL_UPGRADE_PROFILE_REQUIRED")
	}
	target, err := lockedTarget(ctx, p, lease, input)
	if err != nil {
		return err
	}
	exists, err := inspectRole(ctx, lease, target, input)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("MVP_POSTGRESQL_EXISTING_RUNTIME_ROLE_REQUIRED")
	}
	return nil
}
