package main

import (
	"errors"
	"flag"
	"io"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/composeprofile"
)

const (
	composeMachineValuesCommand   = "compose-machine-values"
	composeMachineArgumentInvalid = "OFFLINE_COMPOSE_MACHINE_ARGUMENT_INVALID"
	composeMachineInputInvalid    = "OFFLINE_COMPOSE_MACHINE_INPUT_INVALID"
	composeMachineOutputFailed    = "OFFLINE_COMPOSE_MACHINE_OUTPUT_FAILED"
)

type composeMachineFlagValue struct {
	value string
	seen  bool
}

func (v *composeMachineFlagValue) String() string { return "" }
func (v *composeMachineFlagValue) Set(value string) error {
	if v.seen {
		return errors.New(composeMachineArgumentInvalid)
	}
	v.value, v.seen = value, true
	return nil
}

type composeMachineInvocation struct {
	configuration string
	environment   string
}

func composeMachineCanonicalPath(value string) bool {
	return value != "" && filepath.IsAbs(value) && filepath.Clean(value) == value && filepath.Base(value) != string(filepath.Separator) && !strings.ContainsFunc(value, unicode.IsControl)
}

// Validate all unique flags and canonical path references before accessing any
// file. Never print the supplied values, paths or underlying parser errors.
func parseComposeMachineInvocation(args []string) (composeMachineInvocation, error) {
	bad := errors.New(composeMachineArgumentInvalid)
	flags := flag.NewFlagSet(composeMachineValuesCommand, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var configuration, environment composeMachineFlagValue
	flags.Var(&configuration, "config", "Protected explicit public machine profile JSON")
	flags.Var(&environment, "env-file", "Optional protected fourteen-key machine profile environment")
	if flags.Parse(args) != nil || flags.NArg() != 0 || !configuration.seen || !composeMachineCanonicalPath(configuration.value) || environment.seen && !composeMachineCanonicalPath(environment.value) {
		return composeMachineInvocation{}, bad
	}
	return composeMachineInvocation{configuration: configuration.value, environment: environment.value}, nil
}

func composeMachineProjectionFailure(e error) error {
	if e != nil && (e.Error() == "COMPOSE_MACHINE_VALUES_INVALID" || e.Error() == "COMPOSE_MACHINE_ENVIRONMENT_INVALID") {
		return errors.New(e.Error())
	}
	return errors.New("COMPOSE_MACHINE_VALUES_INVALID")
}

// The public profile renderer is independent of signed release preparation,
// Engine, runtime configuration, mounted credentials, SQL and service owners.
func runComposeMachineValuesCommand(args []string, output io.Writer) error {
	invocation, e := parseComposeMachineInvocation(args)
	if e != nil || output == nil {
		return errors.New(composeMachineArgumentInvalid)
	}
	raw, e := bundle.ReadProtectedConfiguration(invocation.configuration)
	if e != nil {
		return errors.New(composeMachineInputInvalid)
	}
	var environment []byte
	if invocation.environment != "" {
		environment, e = bundle.ReadProtectedConfiguration(invocation.environment)
		if e != nil {
			return errors.New(composeMachineInputInvalid)
		}
	}
	profile, e := composeprofile.LoadMachine(raw, environment)
	if e != nil {
		return composeMachineProjectionFailure(e)
	}
	values, e := profile.Values()
	if e != nil || len(values) == 0 {
		return composeMachineProjectionFailure(e)
	}
	n, e := output.Write(values)
	if e != nil || n != len(values) {
		return errors.New(composeMachineOutputFailed)
	}
	return nil
}
