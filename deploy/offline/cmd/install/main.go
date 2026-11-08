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
	"       ownsolid-install verify-bundle --bundle <archive.tar.gz> --verification <policy.json>\n" +
	"       ownsolid-install bundle-notices --bundle <archive.tar.gz> --verification <policy.json>\n" +
	"       ownsolid-install prepare-bundle --bundle <archive.tar.gz> --verification <policy.json> --output <new directory>\n" +
	"       ownsolid-install apply-bundle --bundle <archive.tar.gz> --verification <policy.json> --config <site.yaml>\n" +
	"       ownsolid-install upgrade-plan --bundle <archive.tar.gz> --verification <policy.json> --config <site.yaml>\n" +
	"       ownsolid-install upgrade-bundle --bundle <archive.tar.gz> --verification <policy.json> --config <site.yaml> --backup-output <directory>\n" +
	"       ownsolid-install upgrade-patch --baseline <previous.tar.gz> --bundle <patch.tar.gz> --verification <policy.json> --config <site.yaml> --backup-output <directory>\n" +
	"       ownsolid-install rollback-bundle --bundle <previous.tar.gz> --verification <policy.json> --config <site.yaml>\n" +
	"       ownsolid-install support-bundle --config <install.yaml> --output <new archive.tar.gz>\n" +
	"       ownsolid-install scheduled-backup --config <install.yaml>\n" +
	"       ownsolid-install backup --config <install.yaml> --output <directory>\n" +
	"       ownsolid-install restore --config <install.yaml> --from <backup directory>\n" +
	"       ownsolid-install recover-restore --config <install.yaml> [--rollback]\n" +
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
	if args[0] == "verify-bundle" || args[0] == "bundle-notices" || args[0] == "prepare-bundle" || args[0] == "apply-bundle" || args[0] == "upgrade-plan" || args[0] == "upgrade-bundle" || args[0] == "upgrade-patch" || args[0] == "rollback-bundle" {
		return runBundle(args[0], args[1:], out)
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
	output := flags.String("output", "", "backup directory or new support archive path")
	from := flags.String("from", "", "backup directory to restore")
	rollback := flags.Bool("rollback", false, "return to the original database and retain the candidate")
	level := flags.String("level", "", "agent traffic to refuse, lowest priority first")
	reason := flags.String("reason", "", "why, kept in the audit record")
	if e := flags.Parse(args); e != nil || flags.NArg() != 0 || *path == "" {
		return errors.New(usage)
	}
	shed := command == "agents shed"
	if (command == "backup" || command == "support-bundle") != (*output != "") || (command == "restore") != (*from != "") || shed != (*level != "") || shed != (*reason != "") || (*rollback && command != "recover-restore") {
		return errors.New(usage)
	}
	load := install.LoadOperational
	switch command {
	case "apply", "validate":
		load = install.Load
	case "preflight":
		load = install.LoadPreflight
	}
	c, e := load(*path)
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
	case "support-bundle":
		return install.SupportBundle(ctx, c, install.ExecRunner{}, *output, out)
	case "backup":
		return install.Backup(ctx, c, install.ExecRunner{}, *output, out)
	case "scheduled-backup":
		return install.ScheduledBackup(ctx, c, install.ExecRunner{}, out)
	case "restore":
		return install.Restore(ctx, c, install.ExecRunner{}, *from, out)
	case "recover-restore":
		return install.RecoverRestore(ctx, c, install.ExecRunner{}, *rollback, out)
	case "agents status":
		return install.AgentStatus(ctx, c, out)
	case "agents shed":
		return install.ShedAgents(ctx, c, *level, *reason, out)
	}
	return errors.New(usage)
}
