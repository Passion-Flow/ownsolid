package prepare

import (
	"context"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
)

func TestProcessRecoveryCommandsRequireProtectedOperationAndScope(t *testing.T) {
	request := configuration.ProcessRecoveryRequest{FormatVersion: 1, FailedControlStartOperationID: strings.Repeat("a", 32), Reason: "Synthetic explicit recovery"}
	for _, command := range []string{"resume-restored-database", "resume-restored-application"} {
		if !Supported(command) || !ProcessRecoveryCommand(command) {
			t.Fatal("fixed command missing")
		}
		if _, err := Run(context.Background(), command, configuration.Config{}); err == nil || err.Error() != "OFFLINE_OPERATOR_PROCESS_RECOVERY_OPERATION_REQUIRED" {
			t.Fatal("operation-less dispatch admitted", err)
		}
		if _, err := RunProcessRecovery(context.Background(), command, configuration.Config{}, request); err == nil {
			t.Fatal("missing owning scope admitted")
		}
	}
	if ProcessRecoveryCommand("resume-runtime") || Supported("resume-runtime") {
		t.Fatal("broad recovery command admitted")
	}
	if _, err := RunProcessRecovery(context.Background(), "resume-runtime", configuration.Config{}, request); err == nil {
		t.Fatal("arbitrary recovery dispatch admitted")
	}
}
