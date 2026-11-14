package transaction

// maximumCommitOutcomeNodes bounds both depth and width of internal error
// graphs. Malformed or oversized graphs cannot establish ordinary cancellation.
const maximumCommitOutcomeNodes = 64

// CommitOutcome preserves uncertainty before known rollback. It also returns
// Unavailable for malformed graphs so callers fail closed without traversing a
// cyclic graph through errors.Is. It never returns a vendor cause or message.
func CommitOutcome(err error) (ErrorKind, bool) {
	if err == nil {
		return "", false
	}
	pending := []error{err}
	rolledBack := false
	visited := 0
	for len(pending) != 0 {
		if visited >= maximumCommitOutcomeNodes {
			return Unavailable, true
		}
		visited++
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if current == nil {
			return Unavailable, true
		}
		if joined, ok := current.(interface{ Unwrap() []error }); ok {
			children := joined.Unwrap()
			if len(children) == 0 || len(children) > maximumCommitOutcomeNodes-visited-len(pending) {
				return Unavailable, true
			}
			for i := len(children) - 1; i >= 0; i-- {
				pending = append(pending, children[i])
			}
			continue
		}
		if wrapped, ok := current.(interface{ Unwrap() error }); ok {
			pending = append(pending, wrapped.Unwrap())
			continue
		}
		switch current.Error() {
		case string(CommitUnknown):
			return CommitUnknown, true
		case string(CommitRolledBack):
			rolledBack = true
		}
	}
	if rolledBack {
		return CommitRolledBack, true
	}
	return "", false
}
