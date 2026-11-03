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

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/composeprofile"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/scope"
)

const composePrivateConfigurationCommand = "compose-private-configuration"

func runComposePrivateConfigurationCommand(args []string, input io.Reader, output io.Writer) error {
	bad := errors.New("OFFLINE_COMPOSE_PRIVATE_ARGUMENT_INVALID")
	flags := flag.NewFlagSet(composePrivateConfigurationCommand, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var action, environment composePortFlagValue
	flags.Var(&action, "action", "install, inspect, or explicit recover of this exact immutable intent")
	flags.Var(&environment, "env-file", "Optional protected explicit private installer environment")
	if flags.Parse(args) != nil || flags.NArg() != 0 || !action.seen || (action.value != "install" && action.value != "inspect" && action.value != "recover") || environment.seen && !composePortCanonicalPath(environment.value) || input == nil || output == nil {
		return bad
	}
	raw, e := io.ReadAll(io.LimitReader(input, composeprofile.PrivateConfigurationMaximumRequestBytes+1))
	if e != nil {
		return bad
	}
	var overlay []byte
	if environment.seen {
		overlay, e = bundle.ReadProtectedConfiguration(environment.value)
		if e != nil {
			return errors.New("OFFLINE_COMPOSE_PRIVATE_ENVIRONMENT_UNAVAILABLE")
		}
	}
	p, e := composeprofile.LoadPrivateConfiguration(raw, overlay)
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.TimeoutSeconds)*time.Second)
	defer cancel()
	result, e := scope.RunComposePrivateConfiguration(ctx, p, action.value)
	if e != nil {
		return e
	}
	if e = json.NewEncoder(output).Encode(result); e != nil {
		return errors.New("OFFLINE_COMPOSE_PRIVATE_RESULT_WRITE_FAILED")
	}
	return nil
}
