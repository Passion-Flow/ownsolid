package recovery

import (
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

func TestBorrowedRestoreRefusesMissingTransactionOrCurrentOwner(t *testing.T) {
	for _, owner := range []*identity.Service{nil, {}} {
		if e := InvalidateRestoredWithin(t.Context(), owner, nil, "Synthetic explicit native restore"); e == nil || e.Error() != "STORAGE_UNAVAILABLE" {
			t.Fatal("missing actual transaction accepted", e)
		}
	}
	if e := InvalidateRestoredWithin(t.Context(), nil, nil, " "); e == nil || e.Error() != "OPERATOR_REASON_REQUIRED" {
		t.Fatal("unsafe borrowed restore reason accepted", e)
	}
}
