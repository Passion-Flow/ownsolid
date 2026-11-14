package v1

import "errors"

const MaximumRetainedImports = 1024
const MaximumRollbackToleranceSeconds = 300
const MaximumReasonRunes = 500

type Policy struct {
	MaximumImports           int `json:"maximum_imports" yaml:"maximum_imports"`
	RollbackToleranceSeconds int `json:"rollback_tolerance_seconds" yaml:"rollback_tolerance_seconds"`
}

func DefaultPolicy() Policy { return Policy{MaximumImports: 64, RollbackToleranceSeconds: 30} }
func (p Policy) Validate() error {
	if p.MaximumImports < 1 || p.MaximumImports > MaximumRetainedImports || p.RollbackToleranceSeconds < 0 || p.RollbackToleranceSeconds > MaximumRollbackToleranceSeconds {
		return errors.New("LICENSE_POLICY_INVALID")
	}
	return nil
}
