// Command install is ownsolid-install, the one install path for every topology size.
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

const usage = "usage: ownsolid-install validate|apply|status --config <install.yaml>"

func main() {
	if e := run(os.Args[1:], os.Stdout); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) < 1 {
		return errors.New(usage)
	}
	flags := flag.NewFlagSet("ownsolid-install", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("config", "", "typed install configuration")
	if e := flags.Parse(args[1:]); e != nil || flags.NArg() != 0 || *path == "" {
		return errors.New(usage)
	}
	c, e := install.Load(*path)
	if e != nil {
		return e
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	switch args[0] {
	case "validate":
		_, e = fmt.Fprintln(out, "INSTALL_CONFIG_VALID")
		return e
	case "apply":
		return install.Apply(ctx, c, install.ExecRunner{}, out)
	case "status":
		return install.Status(ctx, c, install.ExecRunner{}, out)
	}
	return errors.New(usage)
}
