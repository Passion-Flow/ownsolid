package compose

import (
	"encoding/json"
	"errors"
	"regexp"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Only the owning fixed-command boundary can construct this proof. Error text,
// caller assertions and unchanged runtime observations cannot supply it.
type processNotSubmitted struct{ contextSHA256, diagnosticCode string }

func (e *processNotSubmitted) Error() string { return e.diagnosticCode }

var processSubmissionCodePattern = regexp.MustCompile(`^OFFLINE_(COMPOSE|ENGINE)_[A-Z0-9_]+$`)

func unsubmittedProcessFailure(err error, contextSHA256 string) error {
	code := "OFFLINE_COMPOSE_PROCESS_NOT_SUBMITTED"
	if err != nil && len(err.Error()) <= 192 && processSubmissionCodePattern.MatchString(err.Error()) {
		code = err.Error()
	}
	return &processNotSubmitted{contextSHA256: contextSHA256, diagnosticCode: code}
}

func processSubmissionContext(scope RenderContext, model Model, recorded RuntimeStatus, start bool, selected []string) string {
	raw, _ := json.Marshal(struct {
		Scope    RenderContext
		Model    Model
		Recorded RuntimeStatus
		Start    bool
		Selected []string
	}{scope, model, recorded, start, selected})
	return wire.Digest(raw)
}

// ProcessNotSubmitted recognizes only a real adapter proof for this exact
// attempted action. It grants no permission to replay or change configuration.
func ProcessNotSubmitted(err error, scope RenderContext, model Model, recorded RuntimeStatus, start bool, selected []string) bool {
	var proof *processNotSubmitted
	return errors.As(err, &proof) && proof != nil && proof.contextSHA256 == processSubmissionContext(scope, model, recorded, start, selected)
}
