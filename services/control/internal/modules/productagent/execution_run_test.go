package productagent

import (
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/google/uuid"
)

func TestExecutionRunIdentityValidation(t *testing.T) {
	var runs *ExecutionRuns
	id := uuid.NewString()
	digest := strings.Repeat("a", 64)
	for _, invalid := range []struct{ id, intent string }{{"", digest}, {id, ""}, {id, strings.Repeat("A", 64)}} {
		if result, err := runs.Start(t.Context(), identity.Auth{}, invalid.id, invalid.intent); result != (ExecutionRun{}) || err == nil || err.Error() != "VALIDATION_FAILED" {
			t.Fatalf("invalid start: %+v, %v", result, err)
		}
	}
	if result, err := runs.Start(t.Context(), identity.Auth{}, id, digest); result != (ExecutionRun{}) || err != ErrCommandDenied {
		t.Fatalf("missing authority: %+v, %v", result, err)
	}
	if _, err := NewExecutionRuns(nil); err == nil {
		t.Fatal("constructed runs without a budget authority")
	}
	if result, err := runs.RunCommand(t.Context(), identity.Auth{}, id, uuid.NewString(), "open home", string([]byte{0xff})); result != (Result{}) || err == nil || err.Error() != "VALIDATION_FAILED" {
		t.Fatalf("invalid UTF-8 cursor: %+v, %v", result, err)
	}
}

func TestExecutionDigestsSeparateArgumentsAndOutcome(t *testing.T) {
	command := commandFingerprint("open_home", "")
	for _, other := range []string{
		commandFingerprint("open_home", "cursor"),
		commandFingerprint("open_keys", ""),
		executionDigest("command_completion_v1", "run", "execution", "completed"),
		executionDigest("command_completion_v1", "run", "execution", "failed"),
	} {
		if !budgetDigest(other) || other == command {
			t.Fatal("a different typed action or outcome shared the command fingerprint")
		}
	}
}
