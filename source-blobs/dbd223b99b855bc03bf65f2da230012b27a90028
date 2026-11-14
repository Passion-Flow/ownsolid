//go:build cgo && odbc_manager

package migrate

import (
	"context"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
)

func TestBorrowedCatalogReadbackRequiresExplicitLiveLoan(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, input := range []struct {
		context context.Context
		code    string
	}{{nil, "DAMENG_SCHEMA_CONTEXT_REQUIRED"}, {context.Background(), "DAMENG_SCHEMA_DEADLINE_REQUIRED"}, {ctx, "DAMENG_SCHEMA_CONNECTION_POLICY_MISMATCH"}} {
		r, err := CheckCatalogWithin(input.context, nil, dameng.SchemaConfiguration{})
		if FailureCode(err) != input.code || r.FoundationReady || r.PlatformReady || r.LockReleased {
			t.Fatal("invalid borrowed connection established readiness or lock authority", err)
		}
	}
}
