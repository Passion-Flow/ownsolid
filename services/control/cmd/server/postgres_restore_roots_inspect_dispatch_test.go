package main

import (
	"os"
	"testing"
)

func TestPostgresRestoreRootsInspectionEarlyDispatch(t *testing.T) {
	original := os.Args
	os.Args = []string{"control", postgresRestoreRootsInspectCommand}
	defer func() { os.Args = original }()
	err := run()
	if err == nil || err.Error() != "POSTGRES_RESTORE_ROOTS_INSPECTION_ARGUMENT_INVALID" {
		t.Fatalf("readonly inspection must reject its own arguments before general configuration access: %v", err)
	}
}
