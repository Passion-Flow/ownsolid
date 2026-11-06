package licensing

import (
	"context"

	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// The checks a license file goes through, in order. Inspection reports each one.
const (
	checkFormat     = "format"
	checkKey        = "key"
	checkSignature  = "signature"
	checkTerms      = "terms"
	checkDeployment = "deployment"
	checkIdentity   = "identity"
	checkCapacity   = "capacity"
	checkRenewal    = "renewal"
)

var checkOrder = []string{checkFormat, checkKey, checkSignature, checkTerms, checkDeployment, checkIdentity, checkCapacity, checkRenewal}

// How a check ended. A check after a failed one is not run.
const (
	checkPassed     = "passed"
	checkFailed     = "failed"
	checkNotChecked = "not_checked"
)

// What importing an inspected file would do.
const (
	OutcomeImportable = "importable"
	OutcomeInstalled  = "installed"
	OutcomeRefused    = "refused"
)

// InspectInput is a license file to examine.
type InspectInput struct {
	Artifact string `json:"artifact"`
}

// Check is one step of an inspection.
type Check struct {
	Name   string `json:"name"`
	Result string `json:"result"`
}

// FileTerms are the signed terms of a file. They appear only once the signature is proven.
type FileTerms struct {
	Summary
	DeploymentID      string `json:"deployment_id"`
	IssuedAt          string `json:"issued_at"`
	ReplacesLicenseID string `json:"replaces_license_id"`
}

// Inspection says what importing a file would do and, when it would be refused, why.
type Inspection struct {
	Outcome      string     `json:"outcome"`
	Code         string     `json:"code"`
	Detail       string     `json:"detail"`
	Checks       []Check    `json:"checks"`
	KeyID        string     `json:"key_id"`
	TrustedKeyID string     `json:"trusted_key_id"`
	File         *FileTerms `json:"file"`
	State        State      `json:"state"`
}

func checksUpTo(failed string) []Check {
	out := make([]Check, 0, len(checkOrder))
	result := checkPassed
	for _, name := range checkOrder {
		if name == failed {
			out = append(out, Check{name, checkFailed})
			result = checkNotChecked
			continue
		}
		out = append(out, Check{name, result})
	}
	return out
}

func refusedCheck(refusal wire.Refusal) string {
	switch refusal {
	case wire.RefusalKey:
		return checkKey
	case wire.RefusalSignature:
		return checkSignature
	case wire.RefusalTerms:
		return checkTerms
	}
	return checkFormat
}

// Inspect runs every check an import runs and changes nothing but the audit trail, so an
// administrator sees what a file would do before committing it and why a bad one is refused.
func (s *Service) Inspect(ctx context.Context, a identity.Auth, input InspectInput, requestID string) (Inspection, error) {
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return Inspection{}, fail("ACCESS_DENIED")
	}
	if input.Artifact == "" || len(input.Artifact) > wire.MaximumArtifactBytes {
		return Inspection{}, fail("VALIDATION_FAILED")
	}
	var out Inspection
	e := s.within(ctx, func(ctx context.Context, tx transaction.Executor) error {
		if e := s.actor(ctx, tx, a, true); e != nil {
			return e
		}
		state, history, now, e := s.readLocked(ctx, tx)
		if e != nil {
			return e
		}
		found, e := wire.Inspect([]byte(input.Artifact), s.trust)
		if e != nil {
			return fail("LICENSE_CONFIGURATION_REQUIRED")
		}
		out = Inspection{KeyID: found.KeyID, TrustedKeyID: s.trust.KeyID, State: state}
		target := ""
		if found.Refusal != "" {
			out.Outcome, out.Code, out.Checks = OutcomeRefused, "LICENSE_ARTIFACT_INVALID", checksUpTo(refusedCheck(found.Refusal))
		} else {
			target = found.Verified.Claims.LicenseID
			out.File = fileTerms(found.Verified)
			judged := s.judge(found.Verified, state, history, now, nil)
			out.Code, out.Detail, out.Checks = judged.code, judged.detail, checksUpTo(judged.check)
			switch {
			case judged.code != "":
				out.Outcome = OutcomeRefused
			case judged.installed:
				out.Outcome = OutcomeInstalled
			default:
				out.Outcome = OutcomeImportable
			}
		}
		result := "success"
		if out.Outcome == OutcomeRefused {
			result = "failure"
		}
		return audit.WriteWithin(ctx, tx, a.Me.User.ID, "", "admin", "license.inspected", result, "platform_license", target, out.Code, requestID)
	})
	if e != nil {
		return Inspection{}, e
	}
	return out, nil
}

func fileTerms(proof wire.Verified) *FileTerms {
	return &FileTerms{
		Summary:           *summary(proof),
		DeploymentID:      proof.Claims.DeploymentID,
		IssuedAt:          proof.Claims.IssuedAt,
		ReplacesLicenseID: proof.Claims.ReplacesLicenseID,
	}
}
