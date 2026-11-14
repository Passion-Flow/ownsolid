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

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/postgresbackup"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const postgresRestoreRootsInspectCommand = release.PostgreSQLRestoreRootsInspectCommand

type postgresRestoreRootsInspectionArguments struct{ configuration, environment, request, policyEnvironment string }

func postgresRestoreRootsInspectionPaths(command string, args []string) (postgresRestoreRootsInspectionArguments, error) {
	bad := errors.New("POSTGRES_RESTORE_ROOTS_INSPECTION_ARGUMENT_INVALID")
	if command != postgresRestoreRootsInspectCommand {
		return postgresRestoreRootsInspectionArguments{}, bad
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	var c, e, r, p postgresMaintenanceFlag
	f.Var(&c, "config", "Protected original target runtime configuration")
	f.Var(&e, "env-file", "Explicit protected original runtime overlay")
	f.Var(&r, "request", "Protected immutable root-inspection request")
	f.Var(&p, "policy-env-file", "Explicit protected inspection policy overlay")
	if f.Parse(args) != nil || f.NArg() != 0 || !c.seen || !r.seen {
		return postgresRestoreRootsInspectionArguments{}, bad
	}
	for _, v := range []postgresMaintenanceFlag{c, e, r, p} {
		if v.seen && !release.PostgreSQLBackupPath(v.value) {
			return postgresRestoreRootsInspectionArguments{}, bad
		}
	}
	return postgresRestoreRootsInspectionArguments{c.value, e.value, r.value, p.value}, nil
}

func runPostgresRestoreRootsInspection(command string, args []string, output io.Writer) error {
	a, e := postgresRestoreRootsInspectionPaths(command, args)
	if e != nil || output == nil {
		return errors.New("POSTGRES_RESTORE_ROOTS_INSPECTION_ARGUMENT_INVALID")
	}
	r, _, e := postgresbackup.LoadRestoreRootsInspectionRequest(a.request, a.policyEnvironment)
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	out, e := postgresbackup.InspectRestoreRoots(ctx, a.configuration, a.environment, a.request, a.policyEnvironment, r)
	if e != nil {
		return e
	}
	if ctx.Err() != nil || !out.ValidFor(r) {
		return errors.New("POSTGRES_RESTORE_ROOTS_INSPECTION_OUTPUT_INVALID")
	}
	if json.NewEncoder(output).Encode(out) != nil {
		return errors.New("POSTGRES_RESTORE_ROOTS_INSPECTION_OUTPUT_FAILED")
	}
	return nil
}
