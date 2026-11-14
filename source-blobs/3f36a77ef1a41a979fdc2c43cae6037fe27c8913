package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestConnectorCheckRejectsArgumentsWithoutPrivateText(t *testing.T) {
	for _, args := range [][]string{nil, {"--config", "/secret/not-present"}, {"--password", "secret"}, {"--transaction-probe=maybe"}, {"--config", "/secret/not-present", "--print-launch-settings", "--print-launch-environment"}, {"--config", "/secret/not-present", "--print-launch-settings", "--transaction-probe"}} {
		var out bytes.Buffer
		if run(args, &out) != 1 {
			t.Fatal("invalid check succeeded")
		}
		var r result
		if json.Unmarshal(out.Bytes(), &r) != nil || r.Success || r.Code == "" || r.CleanupState != "not_created" {
			t.Fatal("invalid failure receipt")
		}
		if strings.Contains(out.String(), "/secret") || strings.Contains(out.String(), "password") {
			t.Fatal("input leaked")
		}
	}
}
