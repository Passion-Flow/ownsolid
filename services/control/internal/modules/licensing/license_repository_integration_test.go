//go:build repository_integration

package licensing_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

type pairedLicenseScenario struct {
	scenario
	fixture repositoryfixture.Fixture
	ctx     context.Context
}

func pairedLicenseAuth(t *testing.T, f pairedLicenseScenario, user, audience string) identity.Auth {
	t.Helper()
	var issued identity.IssuedSession
	e := f.fixture.Store.Within(f.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var epoch int64
		if e := q.QueryRow(ctx, f.fixture.Statement(`SELECT auth_epoch FROM users WHERE id=$1`, `SELECT auth_epoch FROM users WHERE id=?`, user)).Scan(&epoch); e != nil {
			return e
		}
		var e error
		issued, e = f.owner.IssueSessionWithin(ctx, q, user, epoch, audience, "", uuid.NewString(), nil)
		return e
	})
	if e != nil {
		t.Fatal("actual ordinary License actor issuance", e)
	}
	a, e := f.owner.Authenticate(f.ctx, issued.Token, audience)
	if e != nil {
		t.Fatal("actual ordinary License actor authentication", e)
	}
	return a
}
func pairedLicenseFixture(t *testing.T) pairedLicenseScenario {
	t.Helper()
	fixture := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	t.Cleanup(cancel)
	owner := &identity.Service{Storage: fixture.Store}
	key, trust, e := wire.GenerateKey("synthetic-paired-license-root")
	if e != nil {
		t.Fatal(e)
	}
	base := time.Now().UTC().Truncate(time.Second).Add(10 * time.Second)
	clock := &clock{value: base}
	service, e := licensing.New(owner, trust, wire.DefaultPolicy(), clock.now)
	if e != nil {
		t.Fatal(e)
	}
	owner.BusinessAdmission = service
	f := pairedLicenseScenario{scenario: scenario{owner: owner, service: service, private: key, trust: trust, clock: clock, base: base}, fixture: fixture, ctx: ctx}
	f.admin = pairedLicenseAuth(t, f, fixture.User, "admin")
	state, e := service.StatusAdmin(ctx, f.admin)
	if e != nil || state.State != licensing.Missing {
		t.Fatal("actual paired initial License observation", e)
	}
	f.deployment = state.DeploymentID
	return f
}
func pairedLicenseExec(t *testing.T, f pairedLicenseScenario, pg, my string, args ...any) {
	t.Helper()
	if _, e := f.fixture.Store.Exec(f.ctx, f.fixture.Statement(pg, my, args...)); e != nil {
		t.Fatal("actual paired License fixture SQL", e)
	}
}
func pairedLicenseCount(t *testing.T, f pairedLicenseScenario, action string) int {
	t.Helper()
	var n int
	if e := f.fixture.Store.QueryRow(f.ctx, f.fixture.Statement(`SELECT COUNT(*) FROM audit_events WHERE action=$1`, `SELECT COUNT(*) FROM audit_events WHERE action=?`, action)).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func pairedLicenseStored(t *testing.T, f pairedLicenseScenario) string {
	t.Helper()
	rows, e := f.fixture.Store.Query(f.ctx, f.fixture.Statement(
		`SELECT sequence,license_id::text,artifact_sha256,artifact,COALESCE(predecessor_id::text,'') FROM platform_license_artifacts ORDER BY sequence LIMIT $1`,
		`SELECT sequence,license_id,artifact_sha256,artifact,COALESCE(predecessor_id,'') FROM platform_license_artifacts ORDER BY sequence LIMIT ?`, wire.MaximumRetainedImports+1))
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	values := []struct {
		Sequence    int
		ID, SHA     string
		Artifact    []byte
		Predecessor string
	}{}
	for rows.Next() {
		var row struct {
			Sequence    int
			ID, SHA     string
			Artifact    []byte
			Predecessor string
		}
		if e := rows.Scan(&row.Sequence, &row.ID, &row.SHA, &row.Artifact, &row.Predecessor); e != nil {
			t.Fatal(e)
		}
		values = append(values, row)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	raw, _ := json.Marshal(values)
	// Canonical bytes stay inside the test process;only their digest/equality is used.
	return wire.Digest(raw)
}
func TestActualPairedLicenseSignedLifecycleAndCommittedAudits(t *testing.T) {
	f := pairedLicenseFixture(t)
	expect(t, f.service.Check(f.ctx), "LICENSE_NEW_BUSINESS_UNAVAILABLE")
	c := claims(f.scenario)
	c.CustomerReference = "Synthetic customer \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629"
	wrong := c
	wrong.DeploymentID = uuid.NewString()
	_, e := f.service.Import(f.ctx, f.admin, input(t, f.scenario, wrong, ""), uuid.NewString())
	expect(t, e, "LICENSE_DEPLOYMENT_MISMATCH")
	if pairedLicenseCount(t, f, "license.import_rejected") != 1 {
		t.Fatal("domain denial audit rolled back")
	}
	result, e := f.service.Import(f.ctx, f.admin, input(t, f.scenario, c, ""), uuid.NewString())
	if e != nil || result.Repeated || result.ImportedLicenseID != c.LicenseID || result.State.State != licensing.Active {
		t.Fatal("actual paired signed import", e)
	}
	before := pairedLicenseStored(t, f)
	again, e := f.service.Import(f.ctx, f.admin, input(t, f.scenario, c, ""), uuid.NewString())
	if e != nil || !again.Repeated || pairedLicenseStored(t, f) != before || pairedLicenseCount(t, f, "license.imported") != 1 {
		t.Fatal("exact signed identity replay changed history/audit", e)
	}
	changed := c
	changed.CustomerReference = "Changed signed bytes under retained ID"
	_, e = f.service.Import(f.ctx, f.admin, input(t, f.scenario, changed, c.LicenseID), uuid.NewString())
	expect(t, e, "LICENSE_ID_CONFLICT")
	_, e = f.service.Import(f.ctx, f.admin, licensing.ImportInput{Artifact: "{}", Reason: "Explicit invalid signed fixture", ExpectedCurrentLicenseID: c.LicenseID}, uuid.NewString())
	expect(t, e, "LICENSE_ARTIFACT_INVALID")
	if pairedLicenseCount(t, f, "license.import_rejected") != 3 || pairedLicenseStored(t, f) != before {
		t.Fatal("committed denials changed accepted history or lost audit")
	}
	portal := pairedLicenseAuth(t, f, f.fixture.User, "portal")
	safe, e := f.service.StatusPortal(f.ctx, portal)
	raw, _ := json.Marshal(safe)
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	if e != nil || len(fields) != 3 || fields["state"] != "active" || fields["new_business_allowed"] != true || fields["warning"] != false || strings.Contains(string(raw), f.deployment) {
		t.Fatal("Portal License state leaked private diagnostics", e)
	}
	download, e := f.service.Diagnostics(f.ctx, f.admin, uuid.NewString())
	if e != nil || download.SHA256 != wire.Digest(download.Bytes) || download.ContentType != "application/json" || download.FileName != "license-diagnostics.json" || pairedLicenseCount(t, f, "license.diagnostics_exported") != 1 {
		t.Fatal("actual atomic bounded License diagnostics", e)
	}
	var diagnostic struct {
		FormatVersion int             `json:"format_version"`
		State         licensing.State `json:"state"`
	}
	if json.Unmarshal(download.Bytes, &diagnostic) != nil || diagnostic.FormatVersion != wire.FormatVersion || diagnostic.State.DeploymentID != f.deployment || diagnostic.State.Current == nil || diagnostic.State.Current.LicenseID != c.LicenseID {
		t.Fatal("diagnostic current signed state mismatch")
	}
	reopened, e := licensing.New(f.owner, f.trust, wire.DefaultPolicy(), f.clock.now)
	if e != nil {
		t.Fatal(e)
	}
	state, e := reopened.StatusAdmin(f.ctx, f.admin)
	if e != nil || state.Current == nil || state.Current.LicenseID != c.LicenseID || pairedLicenseStored(t, f) != before {
		t.Fatal("actual reopened License persistence", e)
	}
}
func TestActualPairedLicenseFuturePhasesAndMicrosecondClockFence(t *testing.T) {
	f := pairedLicenseFixture(t)
	c := claims(f.scenario)
	c.NotBefore = f.base.Add(5 * time.Second).Format(time.RFC3339)
	result, e := f.service.Import(f.ctx, f.admin, input(t, f.scenario, c, ""), uuid.NewString())
	if e != nil || result.State.State != licensing.NotYetValid || result.State.Pending == nil || result.State.Current != nil {
		t.Fatal("actual future-only License state", e)
	}
	for _, test := range []struct {
		offset  time.Duration
		phase   licensing.Phase
		allowed bool
	}{{0, licensing.NotYetValid, false}, {5 * time.Second, licensing.Active, true}, {10 * time.Second, licensing.Grace, true}, {20 * time.Second, licensing.Expired, false}, {-31 * time.Second, licensing.ClockSuspect, false}, {21 * time.Second, licensing.Expired, false}} {
		f.clock.set(f.base.Add(test.offset))
		state, e := f.service.StatusAdmin(f.ctx, f.admin)
		if e != nil || state.State != test.phase || state.NewBusinessAllowed != test.allowed || state.ObservedAt.Before(f.base) {
			t.Fatal("actual persistent paired License phase/fence", e)
		}
		e = f.service.Check(f.ctx)
		if test.allowed && e != nil || !test.allowed && (e == nil || e.Error() != "LICENSE_NEW_BUSINESS_UNAVAILABLE") {
			t.Fatal("actual License new-business phase admission", e)
		}
		if e := f.service.VerifyStored(f.ctx); e != nil {
			t.Fatal("valid expired/future/clock state became integrity failure", e)
		}
	}
	policy := wire.DefaultPolicy()
	policy.RollbackToleranceSeconds = 0
	zero, e := licensing.New(f.owner, f.trust, policy, f.clock.now)
	if e != nil {
		t.Fatal(e)
	}
	f.clock.set(f.base.Add(21*time.Second + 789*time.Nanosecond))
	state, e := zero.StatusAdmin(f.ctx, f.admin)
	if e != nil || state.State != licensing.Expired || !state.ObservedAt.Equal(f.base.Add(21*time.Second)) {
		t.Fatal("microsecond storage caused false zero-tolerance rollback", e)
	}
	f.clock.set(f.base.Add(21*time.Second - time.Microsecond))
	state, e = zero.StatusAdmin(f.ctx, f.admin)
	if e != nil || state.State != licensing.ClockSuspect || !state.ObservedAt.Equal(f.base.Add(21*time.Second)) {
		t.Fatal("real microsecond rollback was not fenced", e)
	}
}
func TestActualPairedLicenseCorruptHistoryAndMissingStateRefuse(t *testing.T) {
	for _, damage := range []string{"hash", "artifact", "sequence", "chain", "singleton"} {
		t.Run(damage, func(t *testing.T) {
			f := pairedLicenseFixture(t)
			if damage == "singleton" {
				pairedLicenseExec(t, f, `DELETE FROM platform_license_state WHERE id=1`, `DELETE FROM platform_license_state WHERE id=1`)
			} else {
				c := claims(f.scenario)
				importLicense(t, f.scenario, c, "")
				switch damage {
				case "hash":
					pairedLicenseExec(t, f, `UPDATE platform_license_artifacts SET artifact_sha256=$1`, `UPDATE platform_license_artifacts SET artifact_sha256=?`, strings.Repeat("0", 64))
				case "artifact":
					pairedLicenseExec(t, f, `UPDATE platform_license_artifacts SET artifact=$1`, `UPDATE platform_license_artifacts SET artifact=?`, []byte("Explicit synthetic corruption"))
				case "sequence":
					pairedLicenseExec(t, f, `UPDATE platform_license_artifacts SET sequence=2`, `UPDATE platform_license_artifacts SET sequence=2`)
				case "chain":
					next := c
					next.LicenseID = uuid.NewString()
					next.ReplacesLicenseID = c.LicenseID
					next.ExpiresAt = f.base.Add(30 * time.Second).Format(time.RFC3339)
					next.GraceUntil = f.base.Add(40 * time.Second).Format(time.RFC3339)
					importLicense(t, f.scenario, next, c.LicenseID)
					pairedLicenseExec(t, f, `UPDATE platform_license_artifacts SET predecessor_id=NULL WHERE license_id=$1`, `UPDATE platform_license_artifacts SET predecessor_id=NULL WHERE license_id=?`, next.LicenseID)
				}
			}
			before := pairedLicenseStored(t, f)
			expect(t, f.service.VerifyStored(f.ctx), "LICENSE_STORED_STATE_INVALID")
			expect(t, f.service.Check(f.ctx), "LICENSE_STORED_STATE_INVALID")
			state, e := f.service.StatusAdmin(f.ctx, f.admin)
			expect(t, e, "LICENSE_STORED_STATE_INVALID")
			if state.Current != nil || state.DeploymentID != "" {
				t.Fatal("corruption returned partial current state")
			}
			download, e := f.service.Diagnostics(f.ctx, f.admin, uuid.NewString())
			expect(t, e, "LICENSE_STORED_STATE_INVALID")
			if len(download.Bytes) != 0 || pairedLicenseStored(t, f) != before || pairedLicenseCount(t, f, "license.diagnostics_exported") != 0 {
				t.Fatal("corrupt history repaired or phantom diagnostics returned")
			}
			if damage == "singleton" {
				var n int
				if e := f.fixture.Store.QueryRow(f.ctx, f.fixture.Statement(`SELECT COUNT(*) FROM platform_license_state`, `SELECT COUNT(*) FROM platform_license_state`)).Scan(&n); e != nil || n != 0 {
					t.Fatal("missing deployment was silently recreated", e)
				}
			}
		})
	}
}
