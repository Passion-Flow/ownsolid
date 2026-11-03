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

const postgresBackupCommand = "backup-kubernetes-postgres"
const postgresBackupReadbackCommand = "readback-kubernetes-postgres-backup"
const postgresBackupVerifyCommand = "verify-kubernetes-postgres-backup"

type postgresBackupArguments struct{ configuration, environment, request, policyEnvironment, manifest string }

func postgresBackupPaths(command string, args []string) (postgresBackupArguments, error) {
	bad := errors.New("POSTGRES_BACKUP_ARGUMENT_INVALID")
	if command != postgresBackupCommand && command != postgresBackupReadbackCommand && command != postgresBackupVerifyCommand {
		return postgresBackupArguments{}, bad
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	var c, e, r, p, m postgresMaintenanceFlag
	f.Var(&c, "config", "Protected operator configuration")
	f.Var(&e, "env-file", "Explicit protected operator configuration overlay")
	f.Var(&r, "request", "Protected immutable matched-backup request")
	f.Var(&p, "policy-env-file", "Explicit protected backup policy overlay")
	f.Var(&m, "manifest-sha256", "Expected candidate manifest digest")
	if f.Parse(args) != nil || f.NArg() != 0 || !r.seen {
		return postgresBackupArguments{}, bad
	}
	if command == postgresBackupCommand {
		if !c.seen || m.seen {
			return postgresBackupArguments{}, bad
		}
	} else {
		if c.seen || e.seen || !m.seen || !release.ValidSHA256(m.value) {
			return postgresBackupArguments{}, bad
		}
	}
	for _, v := range []postgresMaintenanceFlag{c, e, r, p} {
		if v.seen && !release.PostgreSQLBackupPath(v.value) {
			return postgresBackupArguments{}, bad
		}
	}
	return postgresBackupArguments{c.value, e.value, r.value, p.value, m.value}, nil
}

func createPostgresBackup(ctx context.Context, a postgresBackupArguments, r release.PostgreSQLBackupRequest) (out postgresbackup.Completion, result error) {
	if e := postgresbackup.RefuseAmbient(); e != nil {
		return out, e
	}
	c, e := config.LoadProtectedWithEnvironment(a.configuration, a.environment)
	if e != nil {
		return out, errors.New("POSTGRES_BACKUP_OPERATOR_CONFIGURATION_INVALID")
	}
	runtime, e := config.LoadProtectedWithEnvironment(r.RuntimeConfigurationFile, r.RuntimeEnvironmentFile)
	if e != nil {
		return out, errors.New("POSTGRES_BACKUP_RUNTIME_CONFIGURATION_INVALID")
	}
	if c.DatabaseEngine() != config.PostgreSQLDatabaseEngine || runtime.DatabaseEngine() != config.PostgreSQLDatabaseEngine || c.Server.Profile() != r.EffectiveApplicationProfile() || runtime.Server.Profile() != r.EffectiveApplicationProfile() {
		return out, errors.New("POSTGRES_BACKUP_RELEASES_CONFIGURATION_REQUIRED")
	}
	if e = postgresbackup.AdmitConnection(c.Database.DSNFile, r); e != nil {
		return out, e
	}
	// Admit the exact protected runtime role through its existing grant/password
	// authority; no second preparation path, grant or credential change.
	input, e := databaseaccess.LoadRuntimeWithEnvironment(r.RuntimeConfigurationFile, r.RuntimeEnvironmentFile)
	if e != nil {
		return out, e
	}
	selected, e := openSelectedDatabase(ctx, c, postgresBackupCommand)
	if e != nil {
		return out, e
	}
	defer func() {
		result = errors.Join(result, selected.Close())
		if result != nil {
			out = postgresbackup.Completion{}
		}
	}()
	if selected.postgres == nil || selected.pgLease == nil {
		return out, errors.New("POSTGRES_BACKUP_LOCKED_CONTEXT_REQUIRED")
	}
	if c.MVPPostgreSQLCPUV1Only() {
		e = databaseaccess.VerifyMVPPostgreSQLCPUV1Principal(ctx, selected.postgres, selected.pgLease, input)
	} else {
		_, e = databaseaccess.Check(ctx, selected.postgres, selected.pgLease, input)
	}
	if e != nil {
		return out, e
	}
	return postgresbackup.Backup(ctx, selected.postgres, selected.pgLease, c, runtime, a.configuration, a.request, a.policyEnvironment, r)
}

func runPostgresBackup(command string, args []string, output io.Writer) error {
	a, e := postgresBackupPaths(command, args)
	if e != nil || output == nil {
		return errors.New("POSTGRES_BACKUP_ARGUMENT_INVALID")
	}
	r, _, e := postgresbackup.LoadRequest(a.request, a.policyEnvironment)
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.Policy.OperationSeconds)*time.Second)
	defer cancel()
	var result any
	if command == postgresBackupCommand {
		result, e = createPostgresBackup(ctx, a, r)
	} else if command == postgresBackupVerifyCommand && r.EffectiveApplicationProfile() == config.MVPPostgreSQLCPUV1Profile {
		result, e = postgresbackup.ObserveBackup(ctx, r, a.manifest)
	} else {
		result, e = postgresbackup.Verify(ctx, r, a.manifest, command == postgresBackupVerifyCommand)
	}
	if e != nil {
		return e
	}
	if ctx.Err() != nil {
		return errors.New("POSTGRES_BACKUP_INTERRUPTED")
	}
	if json.NewEncoder(output).Encode(result) != nil {
		return errors.New("POSTGRES_BACKUP_OUTPUT_FAILED")
	}
	return nil
}
