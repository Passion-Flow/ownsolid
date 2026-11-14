//go:build license_issuer_integration

package licensing_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

// Output stays in memory, bounded and never included in failure messages: even a
// broken issuer must not leak private-key or claims bytes into the test transcript.
type issuerOutput struct{ bytes.Buffer }

func (b *issuerOutput) Write(raw []byte) (int, error) {
	if len(raw) > (16<<10)-b.Len() {
		return 0, errors.New("LICENSE_ISSUER_TEST_OUTPUT_LIMIT")
	}
	return b.Buffer.Write(raw)
}

func TestActualNativeIssuerOfflineImportEarlyRenewalAndExactRecovery(t *testing.T) {
	binary := os.Getenv("PROJECT_ONE_LICENSE_ISSUER_BINARY")
	if !filepath.IsAbs(binary) || filepath.Clean(binary) != binary {
		t.Fatal("explicit current native manufacturer issuer binary required")
	}
	resolved, e := filepath.EvalSymlinks(binary)
	info, statError := os.Stat(binary)
	if e != nil || resolved != binary || statError != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		t.Fatal("canonical executable manufacturer issuer required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal("private synthetic manufacturer fixture required")
	}
	run := func(args ...string) {
		t.Helper()
		command := exec.CommandContext(ctx, binary, args...)
		command.WaitDelay = 2 * time.Second
		var stdout, stderr issuerOutput
		command.Stdout, command.Stderr = &stdout, &stderr
		if e := command.Run(); e != nil {
			t.Fatal("actual native issuer failed; bounded output withheld")
		}
		var receipt struct {
			Status string `json:"status"`
			KeyID  string `json:"key_id"`
		}
		if json.Unmarshal(stdout.Bytes(), &receipt) != nil || receipt.Status == "" || receipt.KeyID != "synthetic-native-import" || stderr.Len() != 0 {
			t.Fatal("actual native issuer receipt invalid")
		}
	}
	private, public := filepath.Join(root, "manufacturer-private.json"), filepath.Join(root, "customer-trust.json")
	run("keygen", "--key-id", "synthetic-native-import", "--private-out", private, "--trust-out", public)
	// The customer path receives only the public file and signed artifacts.
	trustRaw, e := os.ReadFile(public)
	if e != nil {
		t.Fatal("native public trust readback")
	}
	trust, e := wire.ParseTrust(trustRaw)
	if e != nil {
		t.Fatal("native public trust format")
	}
	owner := fixture.NewUnlicensed(t)
	password := fixture.Bootstrap(t, owner)
	admin := fixture.Login(t, owner, "admin@example.invalid", password, "admin")
	base := time.Now().UTC().Truncate(time.Second).Add(10 * time.Second)
	clock := &clock{value: base}
	service, e := licensing.New(owner, trust, wire.DefaultPolicy(), clock.now)
	if e != nil {
		t.Fatal(e)
	}
	initial, e := service.StatusAdmin(ctx, admin)
	if e != nil || initial.State != licensing.Missing {
		t.Fatal("initial actual customer state")
	}
	f := scenario{owner: owner, admin: admin, service: service, trust: trust, clock: clock, base: base, deployment: initial.DeploymentID}
	issue := func(name string, claims wire.Claims, head string) licensing.ImportInput {
		t.Helper()
		claimsPath, artifactPath := filepath.Join(root, name+"-claims.json"), filepath.Join(root, name+"-license.json")
		raw, e := json.Marshal(claims)
		if e != nil || os.WriteFile(claimsPath, raw, 0600) != nil {
			t.Fatal("private synthetic claims fixture")
		}
		run("issue", "--private-key", private, "--claims", claimsPath, "--output", artifactPath)
		run("verify", "--trust", public, "--license", artifactPath)
		artifact, e := os.ReadFile(artifactPath)
		if e != nil {
			t.Fatal("native offline artifact readback")
		}
		return licensing.ImportInput{Artifact: string(artifact), Reason: "Synthetic native issuer offline acceptance", ExpectedCurrentLicenseID: head}
	}
	first := claims(f)
	firstInput := issue("first", first, "")
	accepted, e := service.Import(ctx, admin, firstInput, uuid.NewString())
	if e != nil || accepted.Repeated || accepted.State.Current == nil || accepted.State.Current.LicenseID != first.LicenseID || accepted.State.Pending != nil || !accepted.State.NewBusinessAllowed {
		t.Fatal("actual native artifact customer import")
	}
	next := first
	next.LicenseID, next.ReplacesLicenseID = uuid.NewString(), first.LicenseID
	next.NotBefore = first.ExpiresAt
	next.ExpiresAt = base.Add(50 * time.Second).Format(time.RFC3339)
	next.GraceUntil = base.Add(60 * time.Second).Format(time.RFC3339)
	renewalInput := issue("early-renewal", next, first.LicenseID)
	pending, e := service.Import(ctx, admin, renewalInput, uuid.NewString())
	if e != nil || pending.Repeated || pending.State.Current.LicenseID != first.LicenseID || pending.State.Pending == nil || pending.State.Pending.LicenseID != next.LicenseID || pending.State.AcceptedLicenses != 2 {
		t.Fatal("native early renewal replaced current authority")
	}
	repeat, e := service.Import(ctx, admin, renewalInput, uuid.NewString())
	if e != nil || !repeat.Repeated || repeat.State.HistoryHeadLicenseID != next.LicenseID || repeat.State.AcceptedLicenses != 2 {
		t.Fatal("explicit exact native artifact replay changed accepted history")
	}
	competing := next
	competing.LicenseID = uuid.NewString()
	_, e = service.Import(ctx, admin, issue("stale-head", competing, first.LicenseID), uuid.NewString())
	expect(t, e, "LICENSE_CURRENT_CHANGED")
	foreign := first
	foreign.LicenseID, foreign.DeploymentID = uuid.NewString(), uuid.NewString()
	_, e = service.Import(ctx, admin, issue("foreign-deployment", foreign, next.LicenseID), uuid.NewString())
	expect(t, e, "LICENSE_DEPLOYMENT_MISMATCH")
	// Reopen the actual service against the same persistent customer history.
	reopened, e := licensing.New(owner, trust, wire.DefaultPolicy(), clock.now)
	if e != nil {
		t.Fatal(e)
	}
	clock.set(base.Add(10 * time.Second))
	state, e := reopened.StatusAdmin(ctx, admin)
	if e != nil || state.Current == nil || state.Current.LicenseID != next.LicenseID || state.Pending != nil || state.DeploymentID != initial.DeploymentID || state.AcceptedLicenses != 2 {
		t.Fatal("native renewal activation or retained customer identity")
	}
	old, e := reopened.Import(ctx, admin, firstInput, uuid.NewString())
	if e != nil || !old.Repeated || old.State.Current.LicenseID != next.LicenseID {
		t.Fatal("old native artifact replay rolled back renewal")
	}
	clock.set(base.Add(time.Minute))
	expect(t, reopened.Check(ctx), "LICENSE_NEW_BUSINESS_UNAVAILABLE")
	download, e := reopened.Diagnostics(ctx, admin, uuid.NewString())
	if e != nil || len(download.Bytes) == 0 || len(download.Bytes) > licensing.MaximumDiagnosticsBytes || download.SHA256 != wire.Digest(download.Bytes) || bytes.Contains(download.Bytes, []byte("seed")) || bytes.Contains(download.Bytes, []byte(firstInput.Artifact)) {
		t.Fatal("expired native license diagnostics unavailable or unsafe")
	}
	var count, imports int
	if e := owner.DB.QueryRow(ctx, "SELECT count(*) FROM platform_license_artifacts").Scan(&count); e != nil || count != 2 {
		t.Fatal("native artifact history changed")
	}
	if e := owner.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='license.imported'").Scan(&imports); e != nil || imports != 2 {
		t.Fatal("native import replay appended an audit")
	}
	t.Log("native issuer -> offline signed files -> actual SQL import/current/pending -> explicit replay -> renewal activation/reopen -> expiry diagnostics passed; synthetic issuer and injected service clock only")
}
