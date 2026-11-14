package main

import (
	"errors"
	"flag"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/bootstrap"
)

func validateBootstrapPacketFlags(flags *flag.FlagSet, command, configuration, runtime, input, output, packet string) error {
	if command != bootstrap.PreparePacketCommand && command != bootstrap.PrepareMySQLPacketCommand && !bootstrap.PacketCommand(command) {
		supplied := packet != ""
		flags.Visit(func(f *flag.Flag) {
			if f.Name == "bootstrap-packet" {
				supplied = true
			}
		})
		if supplied {
			return errors.New("BOOTSTRAP_PACKET_COMMAND_INVALID")
		}
		return nil
	}
	allowed := map[string]bool{"config": true, "runtime-config": true, "bootstrap-packet": true}
	valid := configuration != "" && runtime != "" && packet != ""
	if command == bootstrap.PreparePacketCommand {
		allowed = map[string]bool{"bootstrap-input": true, "credential-output": true}
		valid = input != "" && output != ""
	}
	if command == bootstrap.PrepareMySQLPacketCommand {
		allowed = map[string]bool{"config": true, "runtime-config": true, "bootstrap-input": true, "credential-output": true}
		valid = configuration != "" && runtime != "" && input != "" && output != "" && packet == ""
	}
	flags.Visit(func(f *flag.Flag) {
		if !allowed[f.Name] {
			valid = false
		}
	})
	if !valid {
		return errors.New("BOOTSTRAP_PACKET_ARGUMENT_INVALID")
	}
	return nil
}
