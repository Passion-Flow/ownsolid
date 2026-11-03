package main

import (
	"bytes"
	"strings"
	"testing"
)

type privateReadTrap struct{ t *testing.T }

func (r privateReadTrap) Read([]byte) (int, error) {
	r.t.Fatal("invalid invocation read input")
	return 0, nil
}

func TestComposePrivateConfigurationCLIRefusesBeforeInputs(t *testing.T) {
	for _, args := range [][]string{nil, {"--action", "unknown"}, {"--action", "install", "--action", "inspect"}, {"--action", "install", "--env-file", "relative"}, {"--action", "install", "--shell", "anything"}, {"--action", "install", "extra"}} {
		if e := runComposePrivateConfigurationCommand(args, privateReadTrap{t}, &bytes.Buffer{}); e == nil {
			t.Fatal("accepted invalid arguments")
		}
	}
	for _, raw := range []string{"{}", strings.Repeat("x", 65537), `{"format_version":1,"source_uid":null}`} {
		output := &bytes.Buffer{}
		if e := runComposePrivateConfigurationCommand([]string{"--action", "install"}, strings.NewReader(raw), output); e == nil || output.Len() != 0 {
			t.Fatal("invalid request produced result")
		}
	}
}
