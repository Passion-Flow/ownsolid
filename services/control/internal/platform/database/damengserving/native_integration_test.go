//go:build cgo && odbc_manager && dameng_integration

package damengserving

import (
	"context"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// This never provisions a database, grants privileges, seeds business rows or
// downloads a driver. The customer/operator supplies one already migrated,
// exclusive synthetic schema with exact restricted runtime grants and binding.
func TestActualNativeServingLeaseAndFreshOwner(t *testing.T) {
	runtimeFile := os.Getenv("PROJECT_ONE_SERVING_DAMENG_RUNTIME_CONFIGURATION")
	schemaFile := os.Getenv("PROJECT_ONE_SERVING_DAMENG_SCHEMA_CONFIGURATION")
	schema, e := dameng.LoadSchemaConfiguration(schemaFile, "")
	if e != nil || !regexp.MustCompile(`^M20_SERVING_[A-F0-9]{32}_TEST$`).MatchString(schema.Schema) {
		t.Fatal("SERVING_NATIVE_DISPOSABLE_SCHEMA_REQUIRED")
	}
	bounded := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(t.Context(), time.Duration(schema.TotalSeconds)*time.Second)
	}
	closeOwner := func(owner Runtime) {
		t.Helper()
		if owner == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(owner.CleanupSeconds())*time.Second)
		defer cancel()
		if e := owner.Close(ctx); e != nil {
			t.Error("SERVING_NATIVE_CLEANUP_REQUIRES_RECONCILIATION")
		}
	}
	ctx, cancel := bounded()
	first, e := OpenProtected(ctx, runtimeFile, schemaFile, 2)
	cancel()
	t.Cleanup(func() { closeOwner(first) })
	if e != nil || first == nil || first.Repository() == nil || first.Repository().Dialect() != transaction.Dameng {
		t.Fatal("SERVING_NATIVE_OPEN_FAILED", e)
	}
	ctx, cancel = bounded()
	e = first.Verify(ctx)
	cancel()
	if e != nil {
		t.Fatal("SERVING_NATIVE_READINESS_FAILED", e)
	}
	ctx, cancel = bounded()
	competing, e := OpenProtected(ctx, runtimeFile, schemaFile, 2)
	cancel()
	t.Cleanup(func() { closeOwner(competing) })
	if e == nil || e.Error() != "INSTALLATION_PROCESS_ALREADY_RUNNING" {
		t.Fatal("SERVING_NATIVE_EXCLUSIVE_LOCK_NOT_ENFORCED", e)
	}
	closeOwner(competing)
	closeOwner(first)
	if t.Failed() {
		t.FailNow()
	}
	ctx, cancel = bounded()
	fresh, e := OpenProtected(ctx, runtimeFile, schemaFile, 2)
	cancel()
	t.Cleanup(func() { closeOwner(fresh) })
	if e != nil || fresh == nil {
		t.Fatal("SERVING_NATIVE_FRESH_OWNER_FAILED", e)
	}
	ctx, cancel = bounded()
	e = fresh.Verify(ctx)
	cancel()
	if e != nil {
		t.Fatal("SERVING_NATIVE_FRESH_READINESS_FAILED", e)
	}
}
