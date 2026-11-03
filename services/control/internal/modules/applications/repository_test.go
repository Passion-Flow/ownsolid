package applications

import (
	"context"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"testing"
)

func TestApplicationRepositorySafeErrors(t *testing.T) {
	for _, code := range []string{"APPLICATION_STORAGE_MISMATCH", "WORKFLOW_UNAVAILABLE", "AUTH_SESSION_EXPIRED", "ACCESS_DENIED", "INVALID_CURSOR", string(transaction.CommitUnknown)} {
		if e := applicationError(errors.New(code)); e == nil || e.Error() != code {
			t.Fatal("safe code changed", code, e)
		}
	}
	for _, raw := range []string{"APPLICATION_ private source", "driver private SQL", "STORAGE_ secret=value"} {
		if e := applicationError(errors.New(raw)); e.Error() != "STORAGE_UNAVAILABLE" {
			t.Fatal("private diagnostic escaped")
		}
	}
	unknown := &transaction.Error{Kind: transaction.CommitUnknown}
	if applicationError(unknown) != unknown {
		t.Fatal("commit provenance lost")
	}
	if applicationError(context.Canceled).Error() != string(transaction.Cancelled) || applicationError(context.DeadlineExceeded).Error() != string(transaction.Timeout) {
		t.Fatal("context classification lost")
	}
}
