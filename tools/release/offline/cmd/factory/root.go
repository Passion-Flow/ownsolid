package main

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/internal/factory"
)

func runRoot(command string, args []string) error {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	if command == "root-signature" {
		candidate := flags.String("candidate", "", "protected successor root file")
		key := flags.String("key-file", "", "private Ed25519 root signing key file")
		if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *candidate == "" || *key == "" {
			return errors.New("RELEASE_ARGUMENT_INVALID")
		}
		signature, err := factory.SignRoot(*candidate, *key)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(signature)
	}
	trust := flags.String("trust-file", "", "independently pinned public root file")
	var candidates, signatures []string
	flags.Func("candidate", "protected successor root file, repeat in version order", func(value string) error {
		candidates = append(candidates, value)
		return nil
	})
	flags.Func("signatures", "protected signature list for the corresponding candidate", func(value string) error {
		signatures = append(signatures, value)
		return nil
	})
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *trust == "" || len(candidates) == 0 || len(candidates) != len(signatures) {
		return errors.New("RELEASE_ARGUMENT_INVALID")
	}
	updates := make([]factory.RootUpdate, len(candidates))
	for n := range candidates {
		updates[n] = factory.RootUpdate{Candidate: candidates[n], Signatures: signatures[n]}
	}
	version, err := factory.RotateRoot(*trust, updates, time.Now())
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"status": "root_rotated", "version": version})
}
