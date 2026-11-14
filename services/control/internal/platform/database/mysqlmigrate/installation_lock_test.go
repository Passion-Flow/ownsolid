package mysqlmigrate

import (
	"context"
	"testing"
	"time"
)

func TestBorrowedInstallationLockRequiresActualSessionWithoutSchemaClaim(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if VerifyInstallationLock(ctx, nil, Catalog{}, Options{}) == nil {
		t.Fatal("missing actual session accepted")
	}
	if VerifyInstallationLock(nil, nil, Catalog{}, Options{}) == nil {
		t.Fatal("missing context accepted")
	}
}
