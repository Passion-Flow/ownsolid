package prepare

import (
	"context"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
)

func TestRuntimeAbsenceRecoveryDispatchRequiresProtectedOperation(t *testing.T) {
	if !Supported("recover-absent-runtime") || !RuntimeAbsenceRecoveryCommand("recover-absent-runtime") || RuntimeAbsenceRecoveryCommand("create-runtime") || Supported("recover-any-runtime") {
		t.Fatal("fixed command selection")
	}
	if _, e := Run(context.Background(), "recover-absent-runtime", configuration.Config{}); e == nil || e.Error() != "OFFLINE_OPERATOR_RUNTIME_RECOVERY_OPERATION_REQUIRED" {
		t.Fatal("operationless recovery admitted", e)
	}
}
