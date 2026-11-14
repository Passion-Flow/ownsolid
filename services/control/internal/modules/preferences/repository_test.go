package preferences

import (
	"context"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"testing"
)

func TestPreferenceRepositorySafeErrors(t *testing.T) {
	for _, code := range []string{"PREFERENCE_REVISION_CONFLICT", "AUTH_SESSION_EXPIRED", "ACCESS_DENIED", string(transaction.CommitUnknown)} {
		if e := preferenceError(errors.New(code)); e == nil || e.Error() != code {
			t.Fatal("safe code changed", code, e)
		}
	}
	for _, raw := range []string{"PREFERENCE_ private source", "driver private SQL", "STORAGE_ secret=value"} {
		if e := preferenceError(errors.New(raw)); e.Error() != "STORAGE_UNAVAILABLE" {
			t.Fatal("private diagnostic escaped")
		}
	}
	unknown := &transaction.Error{Kind: transaction.CommitUnknown}
	if preferenceError(unknown) != unknown {
		t.Fatal("commit provenance lost")
	}
	if preferenceError(context.Canceled).Error() != string(transaction.Cancelled) || preferenceError(context.DeadlineExceeded).Error() != string(transaction.Timeout) {
		t.Fatal("context classification lost")
	}
	if _, e := (*Service)(nil).Get(context.Background(), identity.Auth{}); e == nil {
		t.Fatal("missing owner accepted")
	}
}
