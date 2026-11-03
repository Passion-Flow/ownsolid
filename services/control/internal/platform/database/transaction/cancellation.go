package transaction

import (
	"context"
	"errors"
)

// maximumCancellationErrorDepth bounds malformed internal unwrap graphs. A graph
// beyond this boundary is a failure, never proof of normal cancellation.
const maximumCancellationErrorDepth = 64

// IsCancellationOnly reports whether every leaf is an actual context cancellation
// or deadline. In particular, joining cancellation with an uncertain commit or
// another maintenance failure must not suppress that failure during shutdown.
// Nil and empty/malformed error graphs are not cancellation evidence.
func IsCancellationOnly(err error) bool {
	return cancellationOnly(err, 0)
}

func cancellationOnly(err error, depth int) bool {
	if err == nil || depth >= maximumCancellationErrorDepth {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !cancellationOnly(child, depth+1) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return cancellationOnly(wrapped.Unwrap(), depth+1)
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
