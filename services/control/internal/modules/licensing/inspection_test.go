package licensing_test

import (
	"context"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/google/uuid"
)

func inspect(t *testing.T, f scenario, c wire.Claims) licensing.Inspection {
	t.Helper()
	raw, e := wire.Issue(c, f.private)
	if e != nil {
		t.Fatal(e)
	}
	out, e := f.service.Inspect(context.Background(), f.admin, licensing.InspectInput{Artifact: string(raw)}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return out
}

func failedCheck(out licensing.Inspection) string {
	for _, check := range out.Checks {
		if check.Result == "failed" {
			return check.Name
		}
	}
	return ""
}

// An inspection and an import read the same rules, so whatever one says the other does.
func TestInspectionAgreesWithImportAndChangesNothing(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	first := claims(f)
	first.ExpiresAt = f.base.Add(time.Hour).Format(time.RFC3339)
	first.GraceUntil = f.base.Add(2 * time.Hour).Format(time.RFC3339)

	found := inspect(t, f, first)
	if found.Outcome != licensing.OutcomeImportable || found.Code != "" || failedCheck(found) != "" || found.File == nil || found.File.LicenseID != first.LicenseID || found.KeyID != f.trust.KeyID || found.TrustedKeyID != f.trust.KeyID {
		t.Fatal("importable file", found)
	}
	if state, e := f.service.StatusAdmin(ctx, f.admin); e != nil || state.State != licensing.Missing || state.AcceptedLicenses != 0 || state.MaximumImports != wire.DefaultPolicy().MaximumImports {
		t.Fatal("an inspection changed the license", state, e)
	}

	replacing := claims(f)
	replacing.ReplacesLicenseID = uuid.NewString()
	refused := inspect(t, f, replacing)
	if refused.Outcome != licensing.OutcomeRefused || refused.Code != "LICENSE_PREDECESSOR_REQUIRED" || failedCheck(refused) != "renewal" {
		t.Fatal("a first license that replaces another", refused)
	}
	_, e := f.service.Import(ctx, f.admin, input(t, f, replacing, ""), uuid.NewString())
	expect(t, e, refused.Code)

	foreign := claims(f)
	foreign.DeploymentID = uuid.NewString()
	refused = inspect(t, f, foreign)
	if refused.Code != "LICENSE_DEPLOYMENT_MISMATCH" || failedCheck(refused) != "deployment" || refused.File == nil || refused.File.DeploymentID != foreign.DeploymentID || refused.State.DeploymentID != f.deployment {
		t.Fatal("a file for another deployment shows both", refused)
	}
	_, e = f.service.Import(ctx, f.admin, input(t, f, foreign, ""), uuid.NewString())
	expect(t, e, refused.Code)

	importLicense(t, f, first, "")
	if again := inspect(t, f, first); again.Outcome != licensing.OutcomeInstalled || again.Code != "" {
		t.Fatal("the installed file", again)
	}
	changed := first
	changed.CustomerReference = "Same identity, other bytes"
	if conflict := inspect(t, f, changed); conflict.Code != "LICENSE_ID_CONFLICT" || failedCheck(conflict) != "identity" {
		t.Fatal("a reused identity", conflict)
	}

	wrongParent := claims(f)
	wrongParent.ReplacesLicenseID = uuid.NewString()
	wrongParent.ExpiresAt = f.base.Add(3 * time.Hour).Format(time.RFC3339)
	wrongParent.GraceUntil = f.base.Add(4 * time.Hour).Format(time.RFC3339)
	if window := inspect(t, f, wrongParent); window.Code != "LICENSE_RENEWAL_WINDOW_INVALID" || window.Detail != "wrong_predecessor" {
		t.Fatal("a renewal of something else", window)
	}
	shorter := wrongParent
	shorter.ReplacesLicenseID = first.LicenseID
	shorter.ExpiresAt = first.ExpiresAt
	if window := inspect(t, f, shorter); window.Code != "LICENSE_RENEWAL_WINDOW_INVALID" || window.Detail != "expires_no_later" {
		t.Fatal("a renewal that adds nothing", window)
	}
	_, e = f.service.Import(ctx, f.admin, input(t, f, shorter, first.LicenseID), uuid.NewString())
	expect(t, e, "LICENSE_RENEWAL_WINDOW_INVALID")

	future := wrongParent
	future.ReplacesLicenseID = first.LicenseID
	future.NotBefore = f.base.Add(time.Minute).Format(time.RFC3339)
	if next := inspect(t, f, future); next.Outcome != licensing.OutcomeImportable {
		t.Fatal("a renewal that starts later", next)
	}
	importLicense(t, f, future, first.LicenseID)
	third := future
	third.LicenseID, third.ReplacesLicenseID = uuid.NewString(), future.LicenseID
	third.ExpiresAt = f.base.Add(5 * time.Hour).Format(time.RFC3339)
	third.GraceUntil = f.base.Add(6 * time.Hour).Format(time.RFC3339)
	if waiting := inspect(t, f, third); waiting.Code != "LICENSE_PENDING_SUCCESSOR_EXISTS" || failedCheck(waiting) != "renewal" {
		t.Fatal("a third license while one waits", waiting)
	}
}

func TestInspectionKeepsUnprovenTermsOffTheAnswer(t *testing.T) {
	f := setup(t)
	other, _, e := wire.GenerateKey("another-issuer")
	if e != nil {
		t.Fatal(e)
	}
	raw, e := wire.Issue(claims(f), other)
	if e != nil {
		t.Fatal(e)
	}
	out, e := f.service.Inspect(context.Background(), f.admin, licensing.InspectInput{Artifact: string(raw)}, uuid.NewString())
	if e != nil || out.Outcome != licensing.OutcomeRefused || out.Code != "LICENSE_ARTIFACT_INVALID" || failedCheck(out) != "key" || out.File != nil || out.KeyID != "another-issuer" || out.TrustedKeyID != f.trust.KeyID {
		t.Fatal("a file from another issuer", out, e)
	}
	out, e = f.service.Inspect(context.Background(), f.admin, licensing.InspectInput{Artifact: "not a license"}, uuid.NewString())
	if e != nil || failedCheck(out) != "format" || out.File != nil || out.KeyID != "" {
		t.Fatal("a file that is no license", out, e)
	}
	for _, artifact := range []string{"", string(make([]byte, wire.MaximumArtifactBytes+1))} {
		_, e = f.service.Inspect(context.Background(), f.admin, licensing.InspectInput{Artifact: artifact}, uuid.NewString())
		expect(t, e, "VALIDATION_FAILED")
	}
}
