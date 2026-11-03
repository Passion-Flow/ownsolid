package main

import "testing"

func TestRuntimeAbsenceRecoveryCLIRequiresSeparateOperation(t *testing.T) {
	for _, args := range [][]string{
		{"recover-absent-runtime", "--config", "/nonexistent-qualification-config"},
		{"recover-absent-runtime", "--config", "/nonexistent-qualification-config", "--operation-env-file", "/nonexistent-operation-env"},
		{"create-runtime", "--config", "/nonexistent-qualification-config", "--operation-file", "/nonexistent-operation"},
	} {
		if e := run(args); e == nil || e.Error() != "OFFLINE_OPERATOR_OPERATION_ARGUMENT_INVALID" {
			t.Fatal("incorrect operation admission before configuration access", e)
		}
	}
}
