//go:build cgo && odbc_manager

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/damengbackup"
	"strings"
	"syscall"
)

// The root dispatcher forwards here before constructing selected serving state.
// One owner configuration builds one retained kernel inside RunOperator.
func runDamengRecoveryCommand(command string, args []string, output io.Writer) error {
	arguments, e := damengRecoveryPaths(command, args)
	if e != nil || output == nil {
		return errors.New("DAMENG_RECOVERY_ARGUMENT_INVALID")
	}
	request, e := damengbackup.LoadOperatorRequest(arguments.request, arguments.environment)
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result, e := damengbackup.RunOperator(ctx, command, request)
	if e != nil {
		return damengRecoveryPublicError(e)
	}
	if ctx.Err() != nil {
		return errors.New("DAMENG_RECOVERY_INTERRUPTED")
	}
	if json.NewEncoder(output).Encode(result) != nil {
		return errors.New("DAMENG_RECOVERY_OUTPUT_FAILED")
	}
	return nil
}

// Only closed owner error codes may cross the command output boundary. Wrapped
// filesystem/provider errors and joined diagnostics remain private.
func damengRecoveryPublicError(e error) error {
	code := e.Error()
	valid := strings.HasPrefix(code, "DAMENG_RECOVERY_") && len(code) <= 128
	for _, r := range code {
		if r != '_' && (r < 'A' || r > 'Z') {
			valid = false
			break
		}
	}
	if valid {
		return errors.New(code)
	}
	return errors.New("DAMENG_RECOVERY_OPERATION_FAILED")
}
