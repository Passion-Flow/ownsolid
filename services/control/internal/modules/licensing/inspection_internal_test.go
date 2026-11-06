package licensing

import (
	"testing"

	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

func TestRenewalRefusalNamesTheBrokenRule(t *testing.T) {
	previous := wire.Claims{LicenseID: "a", DeploymentID: "d", IssuedAt: "2026-01-01T00:00:00Z", NotBefore: "2026-01-02T00:00:00Z", ExpiresAt: "2027-01-01T00:00:00Z", GraceUntil: "2027-02-01T00:00:00Z"}
	next := wire.Claims{LicenseID: "b", ReplacesLicenseID: "a", DeploymentID: "d", IssuedAt: "2026-06-01T00:00:00Z", NotBefore: "2026-06-01T00:00:00Z", ExpiresAt: "2028-01-01T00:00:00Z", GraceUntil: "2028-02-01T00:00:00Z"}
	if detail := renewalRefusal(previous, next); detail != "" || !renewal(previous, next) {
		t.Fatal("a proper renewal was refused", detail)
	}
	for _, item := range []struct {
		want   string
		change func(*wire.Claims)
	}{
		{renewalWrongPredecessor, func(c *wire.Claims) { c.ReplacesLicenseID = "z" }},
		{renewalWrongDeployment, func(c *wire.Claims) { c.DeploymentID = "other" }},
		{renewalIssuedBefore, func(c *wire.Claims) { c.IssuedAt = "2025-12-31T00:00:00Z" }},
		{renewalStartsBefore, func(c *wire.Claims) { c.NotBefore = "2026-01-01T00:00:00Z" }},
		{renewalStartsAfterGrace, func(c *wire.Claims) { c.NotBefore = "2027-02-02T00:00:00Z" }},
		{renewalExpiresNoLater, func(c *wire.Claims) { c.ExpiresAt = previous.ExpiresAt }},
	} {
		changed := next
		item.change(&changed)
		if detail := renewalRefusal(previous, changed); detail != item.want || renewal(previous, changed) {
			t.Fatal(item.want, detail)
		}
	}
}

func TestChecksStopAtTheFirstFailure(t *testing.T) {
	all := checksUpTo("")
	if len(all) != len(checkOrder) {
		t.Fatal(all)
	}
	for _, check := range all {
		if check.Result != checkPassed {
			t.Fatal(all)
		}
	}
	stopped := checksUpTo(checkDeployment)
	for i, check := range stopped {
		want := checkPassed
		switch {
		case check.Name == checkDeployment:
			want = checkFailed
		case i > 4:
			want = checkNotChecked
		}
		if check.Result != want {
			t.Fatal(i, check)
		}
	}
}

func TestEveryRefusalMapsToItsCheck(t *testing.T) {
	for refusal, want := range map[wire.Refusal]string{
		wire.RefusalUnreadable: checkFormat,
		wire.RefusalKey:        checkKey,
		wire.RefusalSignature:  checkSignature,
		wire.RefusalTerms:      checkTerms,
	} {
		if got := refusedCheck(refusal); got != want {
			t.Fatal(refusal, got)
		}
	}
}
