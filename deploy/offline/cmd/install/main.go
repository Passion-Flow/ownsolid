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

const usage = "usage: ownsolid-install validate|preflight|apply|status --config <install.yaml>\n" +
	"       ownsolid-install backup --config <install.yaml> --output <directory>\n" +
	"       ownsolid-install restore --config <install.yaml> --from <backup directory>\n" +
	"       ownsolid-install agents status --config <install.yaml>\n" +
	"       ownsolid-install agents shed --config <install.yaml> --level none|bulk|telemetry|heartbeats --reason <text>\n" +
	"       ownsolid-install capabilities"

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
	if args[0] == "capabilities" {
		if len(args) != 1 {
			return errors.New(usage)
		}
		return install.WriteCapabilities(out)
	}
	command := args[0]
	args = args[1:]
	if command == "agents" {
		if len(args) == 0 {
			return errors.New(usage)
		}
		command += " " + args[0]
		args = args[1:]
	}
	flags := flag.NewFlagSet("ownsolid-install", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("config", "", "typed install configuration")
	output := flags.String("output", "", "existing directory that receives the backup")
	from := flags.String("from", "", "backup directory to restore")
	level := flags.String("level", "", "agent traffic to refuse, lowest priority first")
	reason := flags.String("reason", "", "why, kept in the audit record")
	if e := flags.Parse(args); e != nil || flags.NArg() != 0 || *path == "" {
		return errors.New(usage)
	}
	shed := command == "agents shed"
	if (command == "backup") != (*output != "") || (command == "restore") != (*from != "") || shed != (*level != "") || shed != (*reason != "") {
		return errors.New(usage)
	}
	c, e := install.Load(*path)
	if e != nil {
		return e
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	switch command {
	case "validate":
		_, e = fmt.Fprintln(out, "INSTALL_CONFIG_VALID")
		return e
	case "preflight":
		return install.Preflight(ctx, c, install.ExecRunner{}, out)
	case "apply":
		return install.Apply(ctx, c, install.ExecRunner{}, out)
	case "status":
		return install.Status(ctx, c, install.ExecRunner{}, out)
	case "backup":
		return install.Backup(ctx, c, install.ExecRunner{}, *output, out)
	case "restore":
		return install.Restore(ctx, c, install.ExecRunner{}, *from, out)
	case "agents status":
		return install.AgentStatus(ctx, c, out)
	case "agents shed":
		return install.ShedAgents(ctx, c, *level, *reason, out)
	}
	return errors.New(usage)
}
