package engine

import (
	"errors"
	"regexp"
)

var diagnosticCodePattern = regexp.MustCompile(`^OFFLINE_ENGINE_[A-Z0-9_]+$`)

// diagnosticFailure is created only by this adapter's fixed error-code sites.
// Raw transport errors, daemon output and caller-provided strings are excluded.
type diagnosticFailure struct{ code string }

func (e *diagnosticFailure) Error() string { return e.code }

func DiagnosticCode(err error) string {
	var known *diagnosticFailure
	if errors.As(err, &known) && known != nil && len(known.code) <= 192 && diagnosticCodePattern.MatchString(known.code) {
		return known.code
	}
	return "OFFLINE_ENGINE_UNCLASSIFIED_FAILURE"
}
