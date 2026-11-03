package modelpreparations

import (
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"testing"
)

func TestPreparationRuntimeMachineErrorsRemainStable(t *testing.T) {
	for _, code := range []string{"AGENT_UNAUTHORIZED", "AGENT_LEASE_STALE", "AGENT_PROFILE_REJECTED", string(transaction.CommitUnknown)} {
		if got := preparationRepositoryError(errors.New(code)); got == nil || got.Error() != code {
			t.Fatalf("runtime boundary changed %s into %v", code, got)
		}
	}
	for _, raw := range []string{"AGENT raw credential=secret", "driver SQLSTATE 1062 with private bytes"} {
		if got := preparationRepositoryError(errors.New(raw)); got == nil || got.Error() != "STORAGE_UNAVAILABLE" {
			t.Fatal("unsafe error escaped runtime boundary")
		}
	}
}
