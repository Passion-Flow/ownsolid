package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/postgresbackup"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const postgresRestoreCommand = "restore-kubernetes-postgres"
const postgresRestoreFilesCommand = "readback-kubernetes-postgres-restore-files"
const postgresRestoreVerifyCommand = "verify-kubernetes-postgres-restore"

type postgresRestoreArguments struct{ configuration, environment, request, policyEnvironment string }

func postgresRestorePaths(command string, args []string) (postgresRestoreArguments, error) {
	bad := errors.New("POSTGRES_RESTORE_ARGUMENT_INVALID")
	if command != postgresRestoreCommand && command != postgresRestoreFilesCommand && command != postgresRestoreVerifyCommand {
		return postgresRestoreArguments{}, bad
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	var c, e, r, p postgresMaintenanceFlag
	f.Var(&c, "config", "Protected target operator configuration")
	f.Var(&e, "env-file", "Explicit protected target operator overlay")
	f.Var(&r, "request", "Protected immutable fresh-target restore request")
	f.Var(&p, "policy-env-file", "Explicit protected restore policy overlay")
	if f.Parse(args) != nil || f.NArg() != 0 || !r.seen {
		return postgresRestoreArguments{}, bad
	}
	if command == postgresRestoreFilesCommand {
		if c.seen || e.seen {
			return postgresRestoreArguments{}, bad
		}
	} else if !c.seen {
		return postgresRestoreArguments{}, bad
	}
	for _, v := range []postgresMaintenanceFlag{c, e, r, p} {
		if v.seen && !release.PostgreSQLBackupPath(v.value) {
			return postgresRestoreArguments{}, bad
		}
	}
	return postgresRestoreArguments{c.value, e.value, r.value, p.value}, nil
}

func operatePostgresRestore(ctx context.Context, command string, a postgresRestoreArguments, r release.PostgreSQLRestoreRequest) (out any, result error) {
	if e := postgresbackup.RefuseAmbient(); e != nil {
		return nil, e
	}
	c, e := config.LoadProtectedWithEnvironment(a.configuration, a.environment)
	if e != nil {
		return nil, errors.New("POSTGRES_RESTORE_OPERATOR_CONFIGURATION_INVALID")
	}
	runtime, e := config.LoadProtectedWithEnvironment(r.RuntimeConfigurationFile, r.RuntimeEnvironmentFile)
	if e != nil {
		return nil, errors.New("POSTGRES_RESTORE_RUNTIME_CONFIGURATION_INVALID")
	}
	if e = postgresbackup.AdmitRestore(ctx, c, runtime, a.configuration, r, command == postgresRestoreCommand); e != nil {
		return nil, e
	}
	// Validate the existing canonical runtime credential producer before SQL.
	// A fresh target has no runtime role yet: no grant/preparation command runs
	// here. The existing restored-access owner is a later explicit operation.
	if _, e = databaseaccess.LoadRuntimeWithEnvironment(r.RuntimeConfigurationFile, r.RuntimeEnvironmentFile); e != nil {
		return nil, e
	}
	selected, e := openSelectedDatabase(ctx, c, command)
	if e != nil {
		return nil, e
	}
	defer func() {
		result = errors.Join(result, selected.Close())
		if result != nil {
			out = nil
		}
	}()
	if selected.postgres == nil || selected.pgLease == nil {
		return nil, errors.New("POSTGRES_RESTORE_LOCKED_CONTEXT_REQUIRED")
	}
	if command == postgresRestoreCommand {
		return postgresbackup.Restore(ctx, selected.postgres, selected.pgLease, c, runtime, a.configuration, a.environment, a.request, a.policyEnvironment, r)
	}
	if command == postgresRestoreVerifyCommand && r.EffectiveApplicationProfile() == config.MVPPostgreSQLCPUV1Profile {
		return postgresbackup.ObserveRestore(ctx, selected.postgres, selected.pgLease, c, runtime, a.configuration, a.environment, a.request, a.policyEnvironment, r)
	}
	return postgresbackup.VerifyRestore(ctx, selected.postgres, selected.pgLease, c, runtime, a.configuration, a.environment, a.request, a.policyEnvironment, r)
}

func runPostgresRestore(command string, args []string, output io.Writer) error {
	a, e := postgresRestorePaths(command, args)
	if e != nil || output == nil {
		return errors.New("POSTGRES_RESTORE_ARGUMENT_INVALID")
	}
	r, _, e := postgresbackup.LoadRestoreRequest(a.request, a.policyEnvironment)
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.Policy.OperationSeconds)*time.Second)
	defer cancel()
	var out any
	if command == postgresRestoreFilesCommand {
		out, e = postgresbackup.ReadbackRestoreFiles(ctx, r)
	} else {
		out, e = operatePostgresRestore(ctx, command, a, r)
	}
	if e != nil {
		return e
	}
	if ctx.Err() != nil {
		return errors.New("POSTGRES_RESTORE_INTERRUPTED")
	}
	if json.NewEncoder(output).Encode(out) != nil {
		return errors.New("POSTGRES_RESTORE_OUTPUT_FAILED")
	}
	return nil
}
