// Command simulator stands in for a fleet of ownsolid node agents. It enrolls simulated
// read-only nodes with real join tokens and runs thousands of them in one process against
// a real control plane, over the real agent protocol, with scripted faults. See README.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Passion-Flow/ownsolid/tools/dev/simulator/internal/config"
	"github.com/Passion-Flow/ownsolid/tools/dev/simulator/internal/fleet"
)

const usage = "usage: simulator enroll|run --config <file>"

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	if len(args) < 1 || args[0] != "enroll" && args[0] != "run" {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "configuration file")
	if flags.Parse(args[1:]) != nil || *path == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	c, e := config.Load(*path)
	if e == nil && args[0] == "enroll" {
		e = c.ValidateEnroll()
	}
	if e == nil && args[0] == "run" {
		e = c.ValidateRun()
	}
	if e != nil {
		slog.Error("simulator_failed", "code", e.Error())
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if args[0] == "enroll" {
		e = fleet.Enroll(ctx, c)
	} else {
		out, failure := fleet.Run(ctx, c)
		e = failure
		if e == nil {
			slog.Info("simulator_finished", "agents", out.Agents, "connected", out.Connected, "on_stream", out.OnStream, "revoked", out.Revoked, "tasks_succeeded", out.Tasks.Succeeded, "report", c.ReportFile)
		}
	}
	if e != nil {
		slog.Error("simulator_failed", "code", e.Error())
		return 1
	}
	return 0
}
