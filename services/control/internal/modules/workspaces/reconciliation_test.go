package workspaces

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

func TestOperationReceiptInputAndAuthorityBeforeStorage(t *testing.T) {
	s := &Service{}
	for _, id := range []string{"", uuid.Nil.String(), "123", "9FE472EA-D595-424A-BBEF-AEA6EE6A3F4F"} {
		out, e := s.Operation(context.Background(), identity.Auth{}, id)
		if e == nil || e.Error() != "VALIDATION_FAILED" || out != (OperationReceipt{}) {
			t.Fatal("invalid receipt request reached storage", e)
		}
	}
	out, e := s.Operation(context.Background(), identity.Auth{}, uuid.NewString())
	if e == nil || e.Error() != "AUTH_SESSION_EXPIRED" || out != (OperationReceipt{}) {
		t.Fatal("missing actor reached storage", e)
	}
}
