//go:build cgo && odbc_manager

package main

import (
	"errors"
	"testing"
)

func TestDamengRecoveryNativeDiagnosticsRedacted(t *testing.T) {
	for _, raw := range []string{"open /private/credential: denied", "DAMENG_RECOVERY_FAILED\n/private/credential", "DAMENG_RECOVERY_secret_value"} {
		if e := damengRecoveryPublicError(errors.New(raw)); e.Error() != "DAMENG_RECOVERY_OPERATION_FAILED" {
			t.Fatal("native diagnostic leaked")
		}
	}
	if e := damengRecoveryPublicError(errors.New("DAMENG_RECOVERY_OWNER_KERNEL_OPEN_FAILED")); e.Error() != "DAMENG_RECOVERY_OWNER_KERNEL_OPEN_FAILED" {
		t.Fatal("closed code lost")
	}
}
