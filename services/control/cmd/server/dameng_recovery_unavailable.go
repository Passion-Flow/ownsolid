//go:build !cgo || !odbc_manager

package main

import (
	"errors"
	"io"
)

func runDamengRecoveryCommand(command string, args []string, output io.Writer) error {
	if _, e := damengRecoveryPaths(command, args); e != nil {
		return e
	}
	if output == nil {
		return errors.New("DAMENG_RECOVERY_ARGUMENT_INVALID")
	}
	return errors.New("DAMENG_RECOVERY_NATIVE_BUILD_REQUIRED")
}
