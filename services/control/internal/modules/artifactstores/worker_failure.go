package artifactstores

import (
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Only an acknowledged exact-row failure annotation can create this marker.
// Public callers still receive the original safe error and no result projection.
type quarantinedOperationError struct{ cause error }

func (e *quarantinedOperationError) Error() string { return e.cause.Error() }
func (e *quarantinedOperationError) Unwrap() error { return e.cause }

func workerJobFailure(e error) bool {
	if e == nil {
		return false
	}
	switch e.Error() {
	case "AUTH_SESSION_EXPIRED", "OBJECT_AUTHORITY_INVALID", "ACCESS_DENIED", "RESOURCE_NOT_FOUND",
		"OBJECT_EXPIRED", "OBJECT_UPLOAD_EXPIRED", "OBJECT_REMOTE_OUTCOME_UNKNOWN", "OBJECT_RECONCILE_REQUIRED",
		"OBJECT_DIGEST_MISMATCH", "OBJECT_CHANGED", "OBJECT_UPLOAD_NOT_FOUND":
		return true
	}
	return false
}

func workerFailureResult(cause, annotation error) error {
	if annotation != nil {
		// Preserve uncertain SQL outcomes for fatal reporting and zero projections.
		return errors.Join(safe(cause), safe(annotation))
	}
	clean := safe(cause)
	if workerJobFailure(clean) {
		return &quarantinedOperationError{cause: clean}
	}
	return clean
}

func workerJobQuarantined(e error) bool {
	// A join/wrapper may carry another critical failure; only our direct marker
	// from ProcessNext proves that this invocation completed its annotation.
	value, ok := e.(*quarantinedOperationError)
	return ok && value != nil && workerJobFailure(value.cause)
}

func workerReportable(e error, stopping bool) bool {
	return e != nil && !workerJobQuarantined(e) && (!stopping || !transaction.IsCancellationOnly(e))
}
