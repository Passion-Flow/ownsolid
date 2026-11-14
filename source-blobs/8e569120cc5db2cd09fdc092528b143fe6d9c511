package main

import (
	"bytes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/damengbackup"
	"strings"
	"testing"
)

func TestDamengRecoveryEarlyClosedFlags(t *testing.T) {
	for _, command := range []string{damengbackup.BackupCommand, damengbackup.RestoreCommand, damengbackup.VerifyBackupCommand, damengbackup.VerifyRestoreCommand} {
		a, e := damengRecoveryPaths(command, []string{"--request", "/private/request.json", "--env-file=/private/request.env"})
		if e != nil || a.request != "/private/request.json" {
			t.Fatal(e)
		}
		for _, args := range [][]string{nil, {"--request", "relative"}, {"--request", "/private/one", "--request", "/private/two"}, {"--request", "/private/one", "--env-file="}, {"--request", "/private/one", "--config", "sensitive-reference"}, {"--request", "/private/one", "leftover"}} {
			if _, e = damengRecoveryPaths(command, args); e == nil || strings.Contains(e.Error(), "sensitive") {
				t.Fatal("unknown/duplicate/foreign flag admitted or leaked")
			}
		}
	}
	if _, e := damengRecoveryPaths("serve", []string{"--request", "/private/request.json"}); e == nil {
		t.Fatal("not an early native command")
	}
}
func TestDamengRecoveryRejectsBeforeFileOrDatabase(t *testing.T) {
	for _, args := range [][]string{{"--request", "relative"}, {"--request", "/private/no-such-request", "--request", "/private/other"}, {"--request", "/private/no-such-request", "--owner-password", "sensitive-value"}} {
		var out bytes.Buffer
		e := runDamengRecoveryCommand(damengbackup.RestoreCommand, args, &out)
		if e == nil || e.Error() != "DAMENG_RECOVERY_ARGUMENT_INVALID" || out.Len() != 0 {
			t.Fatal("dependency reached before closed argument admission")
		}
	}
}
