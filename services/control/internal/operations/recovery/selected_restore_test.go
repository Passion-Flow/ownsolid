package recovery

import (
	"context"
	"errors"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestSelectedRestoreRefusesMissingAuthorityAndUnsafeReason(t *testing.T) {
	if e := InvalidateRestored(context.Background(), nil, " "); e == nil || e.Error() != "OPERATOR_REASON_REQUIRED" {
		t.Fatal(e)
	}
	if e := InvalidateRestored(context.Background(), nil, "Synthetic isolated restore"); e == nil || e.Error() != "STORAGE_UNAVAILABLE" {
		t.Fatal(e)
	}
	if e := ReconcileInferenceRemoval(context.Background(), nil, wire.InferenceRemovalReceipt{}, "Synthetic receipt"); e == nil || e.Error() != "VALIDATION_FAILED" {
		t.Fatal(e)
	}
	if e := models.InvalidateAuthoritiesWithin(context.Background(), nil); e == nil {
		t.Fatal("missing real transaction accepted")
	}
}
func TestSelectedRestorePreservesUnknownAndRedactsVendorDetail(t *testing.T) {
	for _, kind := range []transaction.ErrorKind{transaction.CommitUnknown, transaction.CommitRolledBack, transaction.Deadlock, transaction.LockTimeout} {
		if e := recoveryError(&transaction.Error{Kind: kind}); e.Error() != string(kind) {
			t.Fatal("outcome lost", e)
		}
	}
	for _, e := range []error{errors.New("secret SQL statement"), errors.New("untrusted:password"), errors.New("")} {
		if actual := recoveryError(e); actual.Error() != "STORAGE_UNAVAILABLE" {
			t.Fatal("unsafe error projected", actual)
		}
	}
}
