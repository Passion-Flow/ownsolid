package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/bootstrap"
)

func TestBootstrapPacketCommandBoundaryAndActualOfflineGeneration(t *testing.T) {
	original := os.Args
	defer func() { os.Args = original }()
	for _, args := range [][]string{
		{bootstrap.PreparePacketCommand, "--config", "must-not-open", "--bootstrap-input", "private", "--credential-output", "private"},
		{bootstrap.ApplyPacketCommand, "--config", "must-not-open"},
		{bootstrap.CheckPacketCommand, "--config", "must-not-open", "--runtime-config", "private"},
		{bootstrap.ApplyPacketCommand, "--config", "must-not-open", "--runtime-config", "private", "--bootstrap-packet", "private", "--credential-output", "unused"},
		{bootstrap.PrepareMySQLPacketCommand, "--bootstrap-input", "private", "--credential-output", "private"},
		{bootstrap.PrepareMySQLPacketCommand, "--config", "must-not-open", "--runtime-config", "private", "--bootstrap-input", "private"},
		{bootstrap.PrepareMySQLPacketCommand, "--config", "must-not-open", "--runtime-config", "private", "--bootstrap-input", "private", "--credential-output", "private", "--bootstrap-packet", "unused"},
	} {
		os.Args = append([]string{"control"}, args...)
		if e := run(); e == nil || e.Error() != "BOOTSTRAP_PACKET_ARGUMENT_INVALID" {
			t.Fatal("packet boundary opened configuration/database", e)
		}
	}
	os.Args = []string{"control", "migrate", "--config", "must-not-open", "--bootstrap-packet="}
	if e := run(); e == nil || e.Error() != "BOOTSTRAP_PACKET_COMMAND_INVALID" {
		t.Fatal("unused packet flag silently accepted", e)
	}
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private root")
	}
	input := filepath.Join(root, "request.json")
	output := filepath.Join(root, "packet.json")
	if os.WriteFile(input, []byte(`{"format_version":1,"administrator_email":"owner@example.invalid","display_name":"Owner","installation_name":"Installation","workspace_name":"Workspace"}`), 0600) != nil {
		t.Fatal("private request")
	}
	os.Args = []string{"control", bootstrap.PreparePacketCommand, "--bootstrap-input", input, "--credential-output", output}
	if e := run(); e != nil {
		t.Fatal("actual offline dispatcher", e)
	}
	if info, e := os.Stat(output); e != nil || info.Mode() != 0600 || info.Size() < 1 {
		t.Fatal("private packet missing")
	}
	if e := run(); e == nil {
		t.Fatal("offline dispatcher overwrote credential")
	}
}

func TestMySQLPacketPreparationHasExactSeparateFlagContract(t *testing.T) {
	f := flag.NewFlagSet(bootstrap.PrepareMySQLPacketCommand, flag.ContinueOnError)
	configuration := f.String("config", "", "")
	runtime := f.String("runtime-config", "", "")
	input := f.String("bootstrap-input", "", "")
	output := f.String("credential-output", "", "")
	if e := f.Parse([]string{"--config", "private-operator", "--runtime-config", "private-runtime", "--bootstrap-input", "private-request", "--credential-output", "new-private-packet"}); e != nil {
		t.Fatal(e)
	}
	if e := validateBootstrapPacketFlags(f, bootstrap.PrepareMySQLPacketCommand, *configuration, *runtime, *input, *output, ""); e != nil {
		t.Fatal("exact MySQL variant refused", e)
	}
	if e := validateBootstrapPacketFlags(f, bootstrap.PreparePacketCommand, *configuration, *runtime, *input, *output, ""); e == nil {
		t.Fatal("MySQL authority flags accepted for PG offline packet")
	}
}
