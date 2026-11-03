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

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/postgresbackup"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const postgresTargetInitializeCommand = release.PostgreSQLRestoreTargetInitializeCommand
const postgresTargetInspectCommand = release.PostgreSQLRestoreTargetInspectCommand

type postgresTargetInitializationArguments struct{ configuration, environment, request, policyEnvironment string }

func postgresTargetInitializationPaths(command string, args []string) (postgresTargetInitializationArguments, error) {
	bad := errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_ARGUMENT_INVALID")
	if command != postgresTargetInitializeCommand && command != postgresTargetInspectCommand {
		return postgresTargetInitializationArguments{}, bad
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	var c, e, r, p postgresMaintenanceFlag
	f.Var(&c, "config", "Protected original target operator configuration")
	f.Var(&e, "env-file", "Explicit protected original operator overlay")
	f.Var(&r, "request", "Protected immutable target initialization or discovery request")
	f.Var(&p, "policy-env-file", "Explicit protected four-field initialization policy overlay")
	if f.Parse(args) != nil || f.NArg() != 0 || !c.seen || !r.seen {
		return postgresTargetInitializationArguments{}, bad
	}
	for _, v := range []postgresMaintenanceFlag{c, e, r, p} {
		if v.seen && !release.PostgreSQLBackupPath(v.value) {
			return postgresTargetInitializationArguments{}, bad
		}
	}
	return postgresTargetInitializationArguments{c.value, e.value, r.value, p.value}, nil
}

func operatePostgresTargetInitialization(ctx context.Context, command string, a postgresTargetInitializationArguments, r release.PostgreSQLRestoreTargetInitializationRequest) (out release.PostgreSQLRestoreTargetInitializationObservation, result error) {
	if command != postgresTargetInitializeCommand && command != postgresTargetInspectCommand {
		return out, errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_ARGUMENT_INVALID")
	}
	if e := postgresbackup.RefuseAmbient(); e != nil {
		return out, e
	}
	c, e := config.LoadProtectedWithEnvironment(a.configuration, a.environment)
	if e != nil {
		return out, errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_CONFIG_INVALID")
	}
	inspection := command == postgresTargetInspectCommand
	if e = postgresbackup.AdmitRestoreTargetInitialization(ctx, c, a.configuration, a.environment, a.request, a.policyEnvironment, r, inspection); e != nil {
		return out, e
	}
	selected, e := openSelectedDatabase(ctx, c, command)
	if e != nil {
		return out, e
	}
	defer func() {
		result = errors.Join(result, selected.Close())
		if result != nil {
			out = release.PostgreSQLRestoreTargetInitializationObservation{}
		}
	}()
	if selected.postgres == nil || selected.pgLease == nil {
		return out, errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_CONTEXT_REQUIRED")
	}
	// No selected.Verify, runtime role preparation, bootstrap or business owner
	// runs: the selected target must still be pristine before native restoration.
	if inspection {
		return postgresbackup.InspectRestoreTargetInitialization(ctx, selected.postgres, selected.pgLease, c, a.configuration, a.environment, a.request, a.policyEnvironment, r)
	}
	return postgresbackup.InitializeRestoreTarget(ctx, selected.postgres, selected.pgLease, c, a.configuration, a.environment, a.request, a.policyEnvironment, r)
}

func runPostgresTargetInitialization(command string, args []string, output io.Writer) error {
	a, e := postgresTargetInitializationPaths(command, args)
	if e != nil || output == nil {
		return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_ARGUMENT_INVALID")
	}
	r, _, e := postgresbackup.LoadRestoreTargetInitializationRequest(a.request, a.policyEnvironment)
	if e != nil {
		return e
	}
	if command == postgresTargetInitializeCommand && !r.Valid() {
		return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_PIN_REQUIRED")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.Policy.OperationSeconds)*time.Second)
	defer cancel()
	out, e := operatePostgresTargetInitialization(ctx, command, a, r)
	if e != nil {
		return e
	}
	if ctx.Err() != nil {
		return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_INTERRUPTED")
	}
	if !out.ValidFor(r, command == postgresTargetInspectCommand) {
		return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_OUTPUT_INVALID")
	}
	if json.NewEncoder(output).Encode(out) != nil {
		return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_OUTPUT_FAILED")
	}
	return nil
}
