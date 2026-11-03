package v1

import "testing"

func TestLicensePolicyExplicitNondefaultAndBounds(t *testing.T) {
	for _, p := range []Policy{DefaultPolicy(), {1, 0}, {MaximumRetainedImports, MaximumRollbackToleranceSeconds}} {
		if e := p.Validate(); e != nil {
			t.Fatal("valid policy", p, e)
		}
	}
	for _, p := range []Policy{{0, 0}, {MaximumRetainedImports + 1, 0}, {1, -1}, {1, MaximumRollbackToleranceSeconds + 1}} {
		if p.Validate() == nil {
			t.Fatal("invalid policy accepted", p)
		}
	}
}
