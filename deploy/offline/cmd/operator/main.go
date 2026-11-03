// Command operator inspects and prepares a signed release in a new isolated scope.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/prepare"
)

func run(args []string) error {
	if len(args) < 1 {
		return errors.New("OFFLINE_OPERATOR_COMMAND_REQUIRED")
	}
	if args[0] == composePortValuesCommand {
		return runComposePortValuesCommand(args[1:], os.Stdout)
	}
	if args[0] == composeMachineValuesCommand {
		return runComposeMachineValuesCommand(args[1:], os.Stdout)
	}
	if args[0] == composePrivateConfigurationCommand {
		return runComposePrivateConfigurationCommand(args[1:], os.Stdin, os.Stdout)
	}
	if !prepare.Supported(args[0]) {
		return errors.New("OFFLINE_OPERATOR_COMMAND_INVALID")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	name := flags.String("config", "", "Protected explicit JSON operator configuration")
	environment := flags.String("env-file", "", "Optional protected explicit operator environment mapping")
	operation := flags.String("operation-file", "", "Protected explicit operation JSON")
	operationEnvironment := flags.String("operation-env-file", "", "Optional protected explicit operation environment mapping")
	var binding scopeBinding
	flags.Var(&binding, "bound-scope-id", "Expected existing isolated scope identity for selected TLS commands")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 {
		return errors.New("OFFLINE_OPERATOR_ARGUMENT_INVALID")
	}
	if e := binding.validateCommand(args[0]); e != nil {
		return e
	}
	operationCommand := args[0] == "inspect-compatible-restore-target" || prepare.RuntimeAbsenceRecoveryCommand(args[0]) || prepare.CumulativeUpgradeCommand(args[0]) || prepare.ProcessRecoveryCommand(args[0]) || prepare.JournalCapacityCommand(args[0]) || prepare.RestoredTLSCommand(args[0]) || prepare.BackupCommand(args[0]) || prepare.RestoreSourceCommand(args[0]) || prepare.RestoreReservationCommand(args[0]) || prepare.RestoreSQLCommand(args[0]) || prepare.RestoreDatabaseCommand(args[0]) || prepare.RestoreStoresCommand(args[0])
	if operationCommand != (*operation != "") || !operationCommand && *operationEnvironment != "" {
		return errors.New("OFFLINE_OPERATOR_OPERATION_ARGUMENT_INVALID")
	}
	raw, e := bundle.ReadProtectedConfiguration(*name)
	if e != nil {
		return e
	}
	var env []byte
	if *environment != "" {
		env, e = bundle.ReadProtectedConfiguration(*environment)
		if e != nil {
			return e
		}
	}
	c, e := configuration.Load(raw, env)
	if e != nil {
		return e
	}
	c.BoundScopeID = binding.value
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutSeconds)*time.Second)
	defer cancel()
	var result any
	if operationCommand {
		rawOperation, re := bundle.ReadProtectedConfiguration(*operation)
		if re != nil {
			return re
		}
		var opEnv []byte
		if *operationEnvironment != "" {
			opEnv, re = bundle.ReadProtectedConfiguration(*operationEnvironment)
			if re != nil {
				return re
			}
		}
		if args[0] == "inspect-compatible-restore-target" {
			request, re := configuration.LoadRestoreRequest(rawOperation, opEnv)
			if re != nil {
				return re
			}
			result, e = prepare.RunCompatibleRestoreTarget(ctx, c, request)
		} else if prepare.RuntimeAbsenceRecoveryCommand(args[0]) {
			request, re := configuration.LoadRuntimeAbsenceRecoveryRequest(rawOperation, opEnv)
			if re != nil {
				return re
			}
			result, e = prepare.RunRuntimeAbsenceRecovery(ctx, c, request)
		} else if prepare.CumulativeUpgradeCommand(args[0]) {
			request, re := configuration.LoadCumulativeUpgradeRequest(rawOperation, opEnv)
			if re != nil {
				return re
			}
			result, e = prepare.RunCumulativeUpgrade(ctx, args[0], c, request)
		} else if prepare.ProcessRecoveryCommand(args[0]) {
			request, re := configuration.LoadProcessRecoveryRequest(rawOperation, opEnv)
			if re != nil {
				return re
			}
			result, e = prepare.RunProcessRecovery(ctx, args[0], c, request)
		} else if prepare.JournalCapacityCommand(args[0]) {
			request, re := configuration.LoadJournalCapacityRequest(rawOperation, opEnv)
			if re != nil {
				return re
			}
			result, e = prepare.RunJournalCapacity(ctx, c, request)
		} else if prepare.RestoredTLSCommand(args[0]) {
			request, re := configuration.LoadTLSGenerationRequest(rawOperation, opEnv)
			if re != nil {
				return re
			}
			result, e = prepare.RunRestoredTLS(ctx, args[0], c, request)
		} else if prepare.RestoreReservationCommand(args[0]) || prepare.RestoreSQLCommand(args[0]) || prepare.RestoreDatabaseCommand(args[0]) || prepare.RestoreStoresCommand(args[0]) {
			request, re := configuration.LoadRestoreRequest(rawOperation, opEnv)
			if re != nil {
				return re
			}
			if prepare.RestoreStoresCommand(args[0]) {
				result, e = prepare.RunRestoreStores(ctx, args[0], c, request)
			} else if prepare.RestoreDatabaseCommand(args[0]) {
				result, e = prepare.RunRestoreDatabase(ctx, args[0], c, request)
			} else if prepare.RestoreSQLCommand(args[0]) {
				result, e = prepare.RunRestoreSQL(ctx, args[0], c, request)
			} else {
				result, e = prepare.RunRestoreReservation(ctx, args[0], c, request)
			}
		} else if prepare.RestoreSourceCommand(args[0]) {
			request, re := configuration.LoadRestoreSourceRequest(rawOperation, opEnv)
			if re != nil {
				return re
			}
			if args[0] == "inspect-restore-target" {
				result, e = prepare.RunRestoreTarget(ctx, c, request)
			} else {
				result, e = prepare.RunRestoreSource(ctx, c, request)
			}
		} else {
			request, re := configuration.LoadBackupRequest(rawOperation, opEnv)
			if re != nil {
				return re
			}
			result, e = prepare.RunBackup(ctx, args[0], c, request)
		}
	} else {
		result, e = prepare.Run(ctx, args[0], c)
	}
	if e != nil {
		return e
	}
	if e = json.NewEncoder(os.Stdout).Encode(result); e != nil {
		return errors.New("OFFLINE_OPERATOR_RESULT_WRITE_FAILED")
	}
	return nil
}
func main() {
	if e := run(os.Args[1:]); e != nil {
		code := bundle.ErrorCode(e)
		if e.Error() == "COMPOSE_PORT_VALUES_INVALID" || e.Error() == "COMPOSE_PORT_ENVIRONMENT_INVALID" || e.Error() == "COMPOSE_MACHINE_VALUES_INVALID" || e.Error() == "COMPOSE_MACHINE_ENVIRONMENT_INVALID" {
			code = e.Error()
		} else if (strings.HasPrefix(e.Error(), "OFFLINE_OPERATOR_") || strings.HasPrefix(e.Error(), "OFFLINE_SCOPE_") || strings.HasPrefix(e.Error(), "OFFLINE_ENGINE_") || strings.HasPrefix(e.Error(), "OFFLINE_COMPOSE_")) && len(e.Error()) <= 96 && !strings.ContainsAny(e.Error(), "\r\n /:") {
			code = e.Error()
		}
		fmt.Fprintln(os.Stderr, code)
		os.Exit(1)
	}
}
