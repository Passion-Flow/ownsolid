package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/healthprobe"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const healthProbeCommand = healthprobe.Command

// The flag parser must not print supplied values, paths or underlying errors.
// Every flag is a single explicit authority; repeated values are refused.
type probeFlagValue struct {
	value string
	seen  bool
}

func (v *probeFlagValue) String() string { return "" }
func (v *probeFlagValue) Set(value string) error {
	if v.seen {
		return errors.New("HEALTH_PROBE_ARGUMENT_INVALID")
	}
	v.value, v.seen = value, true
	return nil
}

type probeInvocation struct {
	configuration string
	environment   string
	input         healthprobe.Input
}

func probeCanonicalPath(value string) bool {
	return value != "" && !strings.ContainsRune(value, 0) && filepath.IsAbs(value) && filepath.Clean(value) == value
}

func parseProbeInvocation(args []string) (probeInvocation, error) {
	bad := errors.New("HEALTH_PROBE_ARGUMENT_INVALID")
	flags := flag.NewFlagSet(healthProbeCommand, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	values := map[string]*probeFlagValue{}
	for _, name := range []string{"config", "env-file", "audience", "kind", "dial-address", "timeout-seconds", "ca-file"} {
		value := &probeFlagValue{}
		values[name] = value
		flags.Var(value, name, "Explicit health probe input")
	}
	if e := flags.Parse(args); e != nil || flags.NArg() != 0 {
		return probeInvocation{}, bad
	}
	for _, name := range []string{"config", "audience", "kind", "dial-address", "timeout-seconds"} {
		if !values[name].seen || values[name].value == "" {
			return probeInvocation{}, bad
		}
	}
	for _, name := range []string{"env-file", "ca-file"} {
		if values[name].seen && values[name].value == "" {
			return probeInvocation{}, bad
		}
	}
	if !probeCanonicalPath(values["config"].value) || values["env-file"].seen && !probeCanonicalPath(values["env-file"].value) {
		return probeInvocation{}, bad
	}
	seconds, e := strconv.Atoi(values["timeout-seconds"].value)
	if e != nil || strconv.Itoa(seconds) != values["timeout-seconds"].value {
		return probeInvocation{}, bad
	}
	input := healthprobe.Input{Audience: values["audience"].value, Kind: values["kind"].value,
		DialAddress: values["dial-address"].value, CAFile: values["ca-file"].value, TimeoutSeconds: seconds}
	if e := healthprobe.ValidateInput(input); e != nil {
		return probeInvocation{}, bad
	}
	return probeInvocation{configuration: values["config"].value, environment: values["env-file"].value, input: input}, nil
}

// This branch runs before the common operator dispatcher opens database/worker
// owners. Only the existing typed configuration and probe's public CA are read.
func runHealthProbeCommand(args []string, output io.Writer) error {
	invocation, e := parseProbeInvocation(args)
	if e != nil || output == nil {
		return errors.New("HEALTH_PROBE_ARGUMENT_INVALID")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	c, e := config.LoadProtectedWithEnvironment(invocation.configuration, invocation.environment)
	if e != nil {
		return errors.New("HEALTH_PROBE_CONFIGURATION_INVALID")
	}
	if e = healthprobe.RunWithMachine(ctx, c.Server, c.MachineServer, invocation.input); e != nil {
		return e
	}
	if _, e = fmt.Fprintln(output, healthprobe.Success); e != nil {
		return errors.New("HEALTH_PROBE_OUTPUT_FAILED")
	}
	return nil
}
