package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/install"
)

const pitrUsage = "       ownsolid-install pitr backup --config <install.yaml> --id <base> [--parent <base>]\n" +
	"       ownsolid-install pitr recover --config <install.yaml> --id <recovery> --base <base> --target <UTC RFC3339>\n" +
	"       ownsolid-install pitr prune --config <install.yaml> --keep-from <full base>\n"

func runPITR(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(pitrUsage)
	}
	command := args[0]
	flags := flag.NewFlagSet("pitr "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("config", "", "typed install configuration")
	var id, parent, base, target, cutoff string
	switch command {
	case "backup":
		flags.StringVar(&id, "id", "", "base backup ID")
		flags.StringVar(&parent, "parent", "", "incremental predecessor")
	case "recover":
		flags.StringVar(&id, "id", "", "recovery ID")
		flags.StringVar(&base, "base", "", "base backup ID")
		flags.StringVar(&target, "target", "", "UTC RFC3339 recovery point")
	case "prune":
		flags.StringVar(&cutoff, "keep-from", "", "verified full base cutoff")
	default:
		return errors.New(pitrUsage)
	}
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *path == "" ||
		(command == "backup" && id == "") || (command == "recover" && (id == "" || base == "" || target == "")) ||
		(command == "prune" && cutoff == "") {
		return errors.New(pitrUsage)
	}
	c, err := install.LoadOperational(*path)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	runner := install.ExecRunner{}
	switch command {
	case "backup":
		return install.PITRBackup(ctx, c, runner, id, parent, out)
	case "recover":
		return install.PITRRecover(ctx, c, runner, id, base, target, out)
	default:
		return install.PITRPrune(ctx, c, runner, cutoff, out)
	}
}
