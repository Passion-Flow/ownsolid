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
	composePortValuesCommand   = "compose-port-values"
	composePortArgumentInvalid = "OFFLINE_COMPOSE_PORT_ARGUMENT_INVALID"
	composePortInputInvalid    = "OFFLINE_COMPOSE_PORT_INPUT_INVALID"
	composePortOutputFailed    = "OFFLINE_COMPOSE_PORT_OUTPUT_FAILED"
)

type composePortFlagValue struct {
	value string
	seen  bool
}

func (v *composePortFlagValue) String() string { return "" }
func (v *composePortFlagValue) Set(value string) error {
	if v.seen {
		return errors.New(composePortArgumentInvalid)
	}
	v.value, v.seen = value, true
	return nil
}

type composePortInvocation struct {
	configuration string
	environment   string
}

func composePortCanonicalPath(value string) bool {
	return value != "" && filepath.IsAbs(value) && filepath.Clean(value) == value && filepath.Base(value) != string(filepath.Separator) && !strings.ContainsFunc(value, unicode.IsControl)
}

// All supplied flags are checked before either protected source is accessed.
// Parsing never prints paths, supplied values or underlying flag errors.
func parseComposePortInvocation(args []string) (composePortInvocation, error) {
	bad := errors.New(composePortArgumentInvalid)
	flags := flag.NewFlagSet(composePortValuesCommand, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var configuration, environment composePortFlagValue
	flags.Var(&configuration, "config", "Protected explicit port JSON")
	flags.Var(&environment, "env-file", "Optional protected three-key port environment")
	if flags.Parse(args) != nil || flags.NArg() != 0 || !configuration.seen || !composePortCanonicalPath(configuration.value) || environment.seen && !composePortCanonicalPath(environment.value) {
		return composePortInvocation{}, bad
	}
	return composePortInvocation{configuration: configuration.value, environment: environment.value}, nil
}

func composePortProjectionFailure(e error) error {
	if e != nil && (e.Error() == "COMPOSE_PORT_VALUES_INVALID" || e.Error() == "COMPOSE_PORT_ENVIRONMENT_INVALID") {
		return errors.New(e.Error())
	}
	return errors.New("COMPOSE_PORT_VALUES_INVALID")
}

// This early command opens only explicit protected public-value sources. It
// never constructs signed preparation, Engine, SQL, runtime or operation owners.
func runComposePortValuesCommand(args []string, output io.Writer) error {
	invocation, e := parseComposePortInvocation(args)
	if e != nil || output == nil {
		return errors.New(composePortArgumentInvalid)
	}
	raw, e := bundle.ReadProtectedConfiguration(invocation.configuration)
	if e != nil {
		return errors.New(composePortInputInvalid)
	}
	var environment []byte
	if invocation.environment != "" {
		environment, e = bundle.ReadProtectedConfiguration(invocation.environment)
		if e != nil {
			return errors.New(composePortInputInvalid)
		}
	}
	parameters, e := composeprofile.Load(raw, environment)
	if e != nil {
		return composePortProjectionFailure(e)
	}
	values, e := parameters.Values()
	if e != nil || len(values) == 0 {
		return composePortProjectionFailure(e)
	}
	n, e := output.Write(values)
	if e != nil || n != len(values) {
		return errors.New(composePortOutputFailed)
	}
	return nil
}
