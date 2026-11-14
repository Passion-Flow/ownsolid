package imageregistry

import "github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

// This fixed bound rejects malformed internal error graphs as ordinary
// unavailability. It never establishes that a mixed graph is cancellation-only.
const maximumOutcomeErrorDepth = 64

func singleTransactionKind(e error, depth int) (transaction.ErrorKind, bool) {
	if e == nil || depth >= maximumOutcomeErrorDepth {
		return "", false
	}
	if _, joined := e.(interface{ Unwrap() []error }); joined {
		return "", false
	}
	if typed, ok := e.(*transaction.Error); ok {
		return typed.Kind, true
	}
	if wrapped, ok := e.(interface{ Unwrap() error }); ok {
		return singleTransactionKind(wrapped.Unwrap(), depth+1)
	}
	return "", false
}
