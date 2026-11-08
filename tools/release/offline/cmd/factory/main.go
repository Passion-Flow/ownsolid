// Command factory validates/manufactures/inspects an immutable offline inventory.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/internal/factory"
)

func run(args []string) error {
	if len(args) < 1 {
		return errors.New("RELEASE_COMMAND_REQUIRED")
	}
	flags := flag.NewFlagSet("offline-factory", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	config := flags.String("config", "", "Protected explicit JSON configuration")
	env := flags.String("env-file", "", "Optional protected explicit component environment mapping")
	var prepared, archiveSHA, manifestSHA, assetID *string
	if args[0] == "image-proof" {
		prepared = flags.String("prepared", "", "Complete private prepared release root")
		archiveSHA = flags.String("archive-sha256", "", "Independent expected original archive SHA256")
		manifestSHA = flags.String("manifest-sha256", "", "Independent expected manifest SHA256")
		assetID = flags.String("asset-id", "", "Exact signed image asset ID")
	}
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 {
		return errors.New("RELEASE_ARGUMENT_INVALID")
	}
	raw, e := factory.ReadConfigFile(*config)
	if e != nil {
		return e
	}
	var environment []byte
	if *env != "" {
		environment, e = factory.ReadConfigFile(*env)
		if e != nil {
			return e
		}
	}
	c, e := factory.LoadConfig(raw, environment)
	if e != nil {
		return e
	}
	command := args[0]
	switch command {
	case "validate-current", "build-current", "inspect-current":
		c = factory.WithCurrentProfile(c)
		command = strings.TrimSuffix(command, "-current")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutSeconds)*time.Second)
	defer cancel()
	switch command {
	case "validate":
		v, e := factory.Validate(ctx, c)
		json.NewEncoder(os.Stdout).Encode(v)
		return e
	case "build":
		v, e := factory.Build(ctx, c)
		if e == nil {
			json.NewEncoder(os.Stdout).Encode(v)
		}
		return e
	case "inspect":
		v, e := factory.Inspect(ctx, c, c.OutputFile)
		if e == nil {
			json.NewEncoder(os.Stdout).Encode(v)
		}
		return e
	case "image-proof":
		proof, _, e := factory.PreparedImageProof(ctx, c, *prepared, *archiveSHA, *manifestSHA, *assetID)
		if e != nil {
			return e
		}
		// Export only the existing signed subset format, after complete prepared
		// origin verification. No signing key or new release authority is used.
		return json.NewEncoder(os.Stdout).Encode(proof)
	default:
		return errors.New("RELEASE_COMMAND_INVALID")
	}
}
func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, factory.ErrorCode(e))
		os.Exit(1)
	}
}
