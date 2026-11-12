package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/install"
)

func runFleetHistory(args []string, out io.Writer) error {
	const help = "usage: ownsolid-install fleet-history validate|apply --config <protected fleet-history.yaml>"
	if len(args) == 0 || args[0] != "validate" && args[0] != "apply" {
		return errors.New(help)
	}
	flags := flag.NewFlagSet("fleet-history", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("config", "", "protected private fleet-history deployment configuration")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || *path == "" {
		return errors.New(help)
	}
	if args[0] == "validate" {
		if err := install.ValidateFleetHistory(*path); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "FLEET_HISTORY_CONFIG_VALID")
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return install.ApplyFleetHistory(ctx, *path, install.ExecRunner{}, out)
}
