package identity

import (
	"context"
	"testing"
)

func TestOrderedCurrentActorRejectsMalformedContextBeforeStorage(t *testing.T) {
	var absent *Service
	for _, ctx := range []context.Context{nil, context.Background()} {
		if e := absent.RecheckOrderedWithin(ctx, nil, Auth{}); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
			t.Fatal("malformed ordered actor reached missing storage", e)
		}
	}
}
