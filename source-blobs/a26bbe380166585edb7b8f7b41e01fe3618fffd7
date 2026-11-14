//go:build !cgo || !odbc_manager

package damengserving

import (
	"context"
	"testing"
)

func TestNativeServingRefusesUnqualifiedBuildBeforeOpeningInputs(t *testing.T) {
	owner, e := OpenProtected(context.Background(), "/nonexistent/private/runtime.json", "/nonexistent/private/schema.json", 2)
	if owner != nil || e == nil || e.Error() != "DAMENG_NATIVE_SERVING_BUILD_REQUIRED" {
		t.Fatal("unqualified build selected native runtime", e)
	}
}
