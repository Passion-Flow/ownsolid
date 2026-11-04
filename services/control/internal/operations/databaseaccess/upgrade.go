package databaseaccess

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const UpgradeCommand = "upgrade-schema"
const UpgradeInspectCommand = "inspect-schema-upgrade"
const UpgradeRequired = "SCHEMA_UPGRADE_REQUIRED"
const UpgradeGrantsPending = "SCHEMA_COMMITTED_GRANTS_PENDING"
const UpgradeComplete = "SCHEMA_UPGRADE_COMPLETE"

type runtimePrincipal struct {
	oid      uint32
	verifier string
}

func readRuntimePrincipal(ctx context.Context, q queryer, input RuntimeInput) (runtimePrincipal, error) {
	var identity runtimePrincipal
	if err := q.QueryRow(ctx, "SELECT oid,rolpassword FROM pg_authid WHERE rolname=$1", input.connection.ConnConfig.User).Scan(&identity.oid, &identity.verifier); err != nil {
		return identity, errors.New("UPGRADE_EXISTING_RUNTIME_ROLE_REQUIRED")
	}
	return identity, nil
}

// InspectUpgrade is read-only on the caller's held installation-lock session.
// It admits an accepted installation whose ledger is a prefix of the lineage,
// with an existing, non-elevated runtime principal whose credential matches.
// It reports whether the schema, only the grants, or nothing remains to be done.
func InspectUpgrade(ctx context.Context, p *pgxpool.Pool, lease *pgxpool.Conn, input RuntimeInput) (string, error) {
	target, err := lockedTarget(ctx, p, lease, input)
	if err != nil {
		return "", err
	}
	tx, err := lease.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return "", errors.New("UPGRADE_INSPECTION_UNAVAILABLE")
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	q := transaction.PostgreSQLTransaction(tx)
	applied, err := database.CheckPrefixWithin(ctx, q)
	if err != nil || applied == 0 {
		return "", errors.New("UPGRADE_ACCEPTED_LINEAGE_REQUIRED")
	}
	complete := applied == database.SchemaVersion
	if complete {
		if err = database.CheckWithin(ctx, q); err != nil {
			return "", errors.New("UPGRADE_ACCEPTED_LINEAGE_REQUIRED")
		}
	}
	var bootstrapped bool
	if err = tx.QueryRow(ctx, "SELECT bootstrap_complete FROM installation_state WHERE id=1").Scan(&bootstrapped); err != nil || !bootstrapped {
		return "", errors.New("UPGRADE_ACCEPTED_INSTALLATION_REQUIRED")
	}
	if _, exists, e := inspectRoleIdentity(ctx, tx, target, input); e != nil {
		return "", e
	} else if !exists {
		return "", errors.New("UPGRADE_EXISTING_RUNTIME_ROLE_REQUIRED")
	}
	if !complete {
		return UpgradeRequired, nil
	}
	if exists, e := inspectRole(ctx, tx, target, input); e == nil && exists {
		return UpgradeComplete, nil
	}
	return UpgradeGrantsPending, nil
}

type upgradeSteps struct {
	migrate func(context.Context, database.MigrationConnection) error
	commit  func(context.Context, pgx.Tx) error
}

// Upgrade appends the remaining lineage in one transaction, then grants the
// existing runtime principal exactly the application tables in a second
// atomic transaction. It never recreates or rekeys the principal, changes
// applied checksums or bootstraps. After an unknown commit it returns no
// success; a later invocation inspects first and never replays committed DDL.
func Upgrade(ctx context.Context, p *pgxpool.Pool, lease *pgxpool.Conn, input RuntimeInput) (string, error) {
	return upgrade(ctx, p, lease, input, upgradeSteps{database.Migrate, func(ctx context.Context, tx pgx.Tx) error { return tx.Commit(ctx) }})
}

func upgrade(ctx context.Context, p *pgxpool.Pool, lease *pgxpool.Conn, input RuntimeInput, steps upgradeSteps) (string, error) {
	state, err := InspectUpgrade(ctx, p, lease, input)
	if err != nil {
		return "", err
	}
	target, err := lockedTarget(ctx, p, lease, input)
	if err != nil {
		return "", err
	}
	before, err := readRuntimePrincipal(ctx, lease, input)
	if err != nil {
		return "", err
	}
	if state == UpgradeRequired {
		if err = steps.migrate(ctx, lease); err != nil {
			if outcome, ok := transaction.CommitOutcome(err); ok {
				return "", errors.New(string(outcome))
			}
			return "", errors.New("UPGRADE_MIGRATION_FAILED")
		}
		if state, err = InspectUpgrade(ctx, p, lease, input); err != nil {
			return "", err
		}
	}
	if state == UpgradeGrantsPending {
		tx, err := lease.Begin(ctx)
		if err != nil {
			return "", errors.New("UPGRADE_GRANT_TRANSACTION_UNAVAILABLE")
		}
		defer tx.Rollback(context.WithoutCancel(ctx))
		current, e := readRuntimePrincipal(ctx, tx, input)
		if e != nil || current != before {
			return "", errors.New("UPGRADE_RUNTIME_IDENTITY_CHANGED")
		}
		role := pgx.Identifier{input.connection.ConnConfig.User}.Sanitize()
		for _, name := range postgreSQLTables() {
			if _, err = tx.Exec(ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON TABLE "+pgx.Identifier{target.Schema, name}.Sanitize()+" TO "+role); err != nil {
				return "", errors.New("UPGRADE_GRANT_FAILED")
			}
		}
		if exists, e := inspectRole(ctx, tx, target, input); e != nil || !exists {
			return "", errors.New("UPGRADE_GRANT_CONFLICT")
		}
		if err = steps.commit(ctx, tx); err != nil {
			if errors.Is(err, pgx.ErrTxCommitRollback) {
				return "", errors.New(string(transaction.CommitRolledBack))
			}
			return "", errors.New(string(transaction.CommitUnknown))
		}
	}
	after, err := readRuntimePrincipal(ctx, lease, input)
	if err != nil || after != before {
		return "", errors.New("UPGRADE_RUNTIME_IDENTITY_CHANGED")
	}
	if state, err = InspectUpgrade(ctx, p, lease, input); err != nil {
		return "", err
	}
	if state != UpgradeComplete {
		return "", errors.New("UPGRADE_INCOMPLETE")
	}
	if err = checkRuntimeConnection(ctx, target, input); err != nil {
		return "", err
	}
	return UpgradeComplete, nil
}

// VerifyRuntimePrincipal checks the existing exact DML-only runtime principal
// on the original locked operator session. It never opens another pool, logs
// credentials, changes grants or attempts a runtime login.
func VerifyRuntimePrincipal(ctx context.Context, p *pgxpool.Pool, lease *pgxpool.Conn, input RuntimeInput) error {
	target, err := lockedTarget(ctx, p, lease, input)
	if err != nil {
		return err
	}
	exists, err := inspectRole(ctx, lease, target, input)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("UPGRADE_EXISTING_RUNTIME_ROLE_REQUIRED")
	}
	return nil
}
