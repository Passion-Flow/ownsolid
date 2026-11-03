package artifactstores

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestWorkerQuarantinePreservesExactSafeJobFailure(t *testing.T) {
	for _, code := range []string{"AUTH_SESSION_EXPIRED", "OBJECT_AUTHORITY_INVALID", "ACCESS_DENIED", "RESOURCE_NOT_FOUND",
		"OBJECT_EXPIRED", "OBJECT_UPLOAD_EXPIRED", "OBJECT_REMOTE_OUTCOME_UNKNOWN", "OBJECT_RECONCILE_REQUIRED",
		"OBJECT_DIGEST_MISMATCH", "OBJECT_CHANGED", "OBJECT_UPLOAD_NOT_FOUND"} {
		t.Run(code, func(t *testing.T) {
			e := workerFailureResult(fail(code), nil)
			if !workerJobQuarantined(e) || e.Error() != code || errors.Unwrap(e) == nil || errors.Unwrap(e).Error() != code {
				t.Fatal("acknowledged job failure lost its safe error contract", e)
			}
			if workerJobQuarantined(fail(code)) || workerJobQuarantined(fmt.Errorf("wrapped: %w", e)) ||
				workerJobQuarantined(errors.Join(e, fail("STORAGE_UNAVAILABLE"))) {
				t.Fatal("unproven or joined critical error was suppressed")
			}
		})
	}
}

func TestWorkerQuarantineRequiresAcknowledgedAnnotation(t *testing.T) {
	for _, annotation := range []error{fail("OBJECT_OPERATION_STALE"), fail("STORAGE_UNAVAILABLE"), context.Canceled,
		&transaction.Error{Kind: transaction.CommitRolledBack}, &transaction.Error{Kind: transaction.CommitUnknown}} {
		e := workerFailureResult(fail("AUTH_SESSION_EXPIRED"), annotation)
		if e == nil || workerJobQuarantined(e) {
			t.Fatal("failed, zero-row or uncertain annotation proved quarantine", e)
		}
		if containsOutcome(annotation, transaction.CommitUnknown, 0) && !containsOutcome(e, transaction.CommitUnknown, 0) {
			t.Fatal("annotation uncertainty was discarded", e)
		}
	}
}

func TestWorkerQuarantineNeverSuppressesInfrastructureOrLicenseFailure(t *testing.T) {
	for _, cause := range []error{nil, context.Canceled, context.DeadlineExceeded,
		fail("LICENSE_STORED_STATE_INVALID"), fail("LICENSE_CONFIGURATION_REQUIRED"), fail("LICENSE_NEW_BUSINESS_UNAVAILABLE"),
		fail("STORAGE_UNAVAILABLE"), fail("OBJECT_STORAGE_UNAVAILABLE"), fail("OBJECT_STORE_CHANGED"),
		fail("OBJECT_UNRECOGNIZED_FUTURE_ERROR"), errors.New("sensitive provider failure"),
		&transaction.Error{Kind: transaction.CommitUnknown}, &transaction.Error{Kind: transaction.CommitRolledBack}} {
		e := workerFailureResult(cause, nil)
		if workerJobQuarantined(e) || (cause != nil && e == nil) {
			t.Fatal("critical failure was suppressed", e)
		}
		if cause != nil && safe(e).Error() != safe(cause).Error() {
			t.Fatal("critical safe error contract changed", e)
		}
	}
	if workerJobQuarantined((*quarantinedOperationError)(nil)) || workerJobQuarantined(&quarantinedOperationError{}) {
		t.Fatal("empty marker proved quarantine")
	}
}

func TestWorkerShutdownPreservesCriticalAndUncertainFailure(t *testing.T) {
	unknown := &transaction.Error{Kind: transaction.CommitUnknown}
	for _, cause := range []error{unknown, errors.Join(context.Canceled, unknown), fail("STORAGE_UNAVAILABLE"),
		fail("LICENSE_STORED_STATE_INVALID"), errors.Join(context.Canceled, fail("ACCESS_DENIED"))} {
		if !workerReportable(cause, true) || !workerReportable(cause, false) {
			t.Fatal("shutdown masked a critical or uncertain failure", cause)
		}
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded,
		errors.Join(context.Canceled, context.DeadlineExceeded)} {
		if workerReportable(cause, true) || !workerReportable(cause, false) {
			t.Fatal("pure cancellation classification changed", cause)
		}
	}
	if workerReportable(nil, false) || workerReportable(workerFailureResult(fail("AUTH_SESSION_EXPIRED"), nil), false) {
		t.Fatal("empty or durably quarantined job invoked fatal reporting")
	}
}
