package transaction

// Completion uncertainty outranks both callback cancellation and cleanup failure.
// The native kernel sanitizes driver failures before assembling this graph; a
// missing callback error does not imply that every kernel failure is cancellation.
func damengSessionOutcome(e, callbackError error) error {
	safe := normalizeDameng(e)
	if outcome, ok := safe.(*Error); ok && outcome.Kind == CommitUnknown {
		return safe
	}
	kernelKind, _ := CommitOutcome(e)
	callbackKind, _ := CommitOutcome(callbackError)
	if kernelKind == CommitUnknown || callbackKind == CommitUnknown {
		return &Error{Kind: CommitUnknown}
	}
	if kernelKind == Unavailable || callbackKind == Unavailable {
		return &Error{Kind: Unavailable}
	}
	if callbackError != nil {
		return e
	}
	if outcome, ok := safe.(*Error); ok && (outcome.Kind == Cancelled || outcome.Kind == Timeout) && !IsCancellationOnly(e) {
		// Keep every sanitized leaf, including physical-owner retention and failed
		// disposal. Collapsing this graph would let shutdown suppress real failure.
		return e
	}
	return safe
}
