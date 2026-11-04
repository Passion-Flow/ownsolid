package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/postgresmaintenance"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const postgresMaintenanceCheckCommand = "check-kubernetes-maintenance"

type postgresMaintenanceFlag struct {
	value string
	seen  bool
}

func (v *postgresMaintenanceFlag) String() string { return "" }
func (v *postgresMaintenanceFlag) Set(value string) error {
	if v.seen {
		return errors.New("POSTGRES_MAINTENANCE_ARGUMENT_INVALID")
	}
	v.value, v.seen = value, true
	return nil
}

func postgresMaintenancePaths(args []string) (string, string, error) {
	invalid := errors.New("POSTGRES_MAINTENANCE_ARGUMENT_INVALID")
	flags := flag.NewFlagSet(postgresMaintenanceCheckCommand, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var configuration, environment postgresMaintenanceFlag
	flags.Var(&configuration, "config", "Explicit protected existing configuration")
	flags.Var(&environment, "env-file", "Explicit protected existing policy overlay")
	if flags.Parse(args) != nil || flags.NArg() != 0 || !configuration.seen {
		return "", "", invalid
	}
	for _, v := range []postgresMaintenanceFlag{configuration, environment} {
		if v.seen && (v.value == "" || !filepath.IsAbs(v.value) || filepath.Clean(v.value) != v.value || strings.ContainsAny(v.value, "\x00\r\n")) {
			return "", "", invalid
		}
	}
	return configuration.value, environment.value, nil
}

func observePostgresMaintenance(ctx context.Context, c config.Config) (out postgresmaintenance.Observation, result error) {
	if c.DatabaseEngine() != config.PostgreSQLDatabaseEngine {
		return out, errors.New("POSTGRES_MAINTENANCE_RELEASES_CONTEXT_REQUIRED")
	}
	selected, e := openSelectedDatabase(ctx, c, postgresMaintenanceCheckCommand)
	if e != nil {
		return out, e
	}
	defer func() {
		result = errors.Join(result, selected.Close())
		if result != nil {
			out = postgresmaintenance.Observation{}
		}
	}()
	if selected.pgLease == nil || selected.postgres == nil {
		return out, errors.New("POSTGRES_MAINTENANCE_RELEASES_CONTEXT_REQUIRED")
	}
	tx, e := selected.pgLease.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if e != nil {
		return out, errors.New("POSTGRES_MAINTENANCE_READ_ONLY_TRANSACTION_UNAVAILABLE")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.Seconds(selected.cleanup))
		defer cancel()
		if e := tx.Rollback(cleanup); e != nil && !errors.Is(e, pgx.ErrTxClosed) {
			result = errors.Join(result, errors.New("POSTGRES_MAINTENANCE_TRANSACTION_CLOSE_UNCONFIRMED"))
		}
	}()
	out, e = postgresmaintenance.CheckWithin(ctx, transaction.PostgreSQLTransaction(tx))
	if e != nil {
		return postgresmaintenance.Observation{}, e
	}
	if ctx.Err() != nil {
		return postgresmaintenance.Observation{}, errors.New("POSTGRES_MAINTENANCE_INTERRUPTED")
	}
	// The result leaves this function only after both cleanup defers have run.
	out.CurrentLockHeld = false
	out.Status = "historical_locked_quiescence_observation"
	return out, nil
}

func runPostgresMaintenanceCheck(args []string, output io.Writer) error {
	path, environment, e := postgresMaintenancePaths(args)
	if e != nil || output == nil {
		return errors.New("POSTGRES_MAINTENANCE_ARGUMENT_INVALID")
	}
	c, e := config.LoadProtectedWithEnvironment(path, environment)
	if e != nil {
		return errors.New("POSTGRES_MAINTENANCE_CONFIGURATION_INVALID")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(c.Policy().HTTP.StartupSeconds))
	defer cancel()
	result, e := observePostgresMaintenance(ctx, c)
	if e != nil {
		return e
	}
	if json.NewEncoder(output).Encode(result) != nil {
		return errors.New("POSTGRES_MAINTENANCE_OUTPUT_FAILED")
	}
	return nil
}
