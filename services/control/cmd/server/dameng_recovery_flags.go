package main

import (
	"errors"
	"flag"
	"io"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/damengbackup"
)

type damengRecoveryArguments struct{ request, environment string }
type damengRecoveryFlag struct {
	value string
	seen  bool
}

func (f *damengRecoveryFlag) String() string { return "REDACTED" }
func (f *damengRecoveryFlag) Set(v string) error {
	if f.seen || !damengbackup.OperatorRequestPath(v) {
		return errors.New("DAMENG_RECOVERY_ARGUMENT_INVALID")
	}
	f.value = v
	f.seen = true
	return nil
}
func damengRecoveryPaths(command string, args []string) (damengRecoveryArguments, error) {
	bad := errors.New("DAMENG_RECOVERY_ARGUMENT_INVALID")
	if !damengbackup.OperatorCommand(command) {
		return damengRecoveryArguments{}, bad
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var request, environment damengRecoveryFlag
	flags.Var(&request, "request", "Protected versioned native recovery request")
	flags.Var(&environment, "env-file", "Protected whole-request replacement mapping")
	if flags.Parse(args) != nil || flags.NArg() != 0 || !request.seen {
		return damengRecoveryArguments{}, bad
	}
	return damengRecoveryArguments{request.value, environment.value}, nil
}

func damengRecoveryCommand(command string) bool { return damengbackup.OperatorCommand(command) }
