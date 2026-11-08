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

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/install"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

func runBundle(command string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	archive := flags.String("bundle", "", "signed offline release archive")
	policy := flags.String("verification", "", "protected public verification configuration")
	config := flags.String("config", "", "typed site configuration without delivery inputs")
	output := flags.String("output", "", "new directory for verified payloads on the configured mount")
	timeout := flags.Duration("timeout", 30*time.Minute, "time limit for verifying or applying the release")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *archive == "" || *policy == "" ||
		(command == "apply-bundle") != (*config != "") || (command == "prepare-bundle") != (*output != "") || *timeout <= 0 {
		return errors.New(usage)
	}
	verification, err := install.LoadBundleVerification(*policy)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	if command == "apply-bundle" {
		return install.ApplyBundle(ctx, *config, *archive, verification, install.ExecRunner{}, out)
	}
	var result bundle.Result
	if command == "prepare-bundle" {
		result, err = bundle.UnpackCurrent(ctx, verification, *archive, *output)
	} else {
		result, err = bundle.InspectCurrent(ctx, verification, *archive)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(result)
}
