package licensing_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

type clock struct {
	mu    sync.Mutex
	value time.Time
}

func (c *clock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.value }
func (c *clock) set(value time.Time) { c.mu.Lock(); defer c.mu.Unlock(); c.value = value }

type scenario struct {
	owner      *identity.Service
	admin      identity.Auth
	service    *licensing.Service
	private    wire.PrivateKey
	trust      wire.Trust
	clock      *clock
	base       time.Time
	deployment string
}

func setup(t *testing.T) scenario {
	t.Helper()
	owner := fixture.NewUnlicensed(t)
	password := fixture.Bootstrap(t, owner)
	a := fixture.Login(t, owner, "admin@example.invalid", password, "admin")
	key, trust, e := wire.GenerateKey("synthetic-license-root")
	if e != nil {
		t.Fatal(e)
	}
	base := time.Now().UTC().Truncate(time.Second).Add(10 * time.Second)
	clock := &clock{value: base}
	s, e := licensing.New(owner, trust, wire.DefaultPolicy(), clock.now)
	if e != nil {
		t.Fatal(e)
	}
	state, e := s.StatusAdmin(context.Background(), a)
	if e != nil || state.State != licensing.Missing {
		t.Fatal("actual missing-license state", state, e)
	}
	return scenario{owner, a, s, key, trust, clock, base, state.DeploymentID}
}
func claims(f scenario) wire.Claims {
	stamp := func(offset time.Duration) string { return f.base.Add(offset).Format(time.RFC3339) }
	return wire.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Synthetic isolated SQL customer", DeploymentID: f.deployment, IssuedAt: stamp(-time.Second), NotBefore: stamp(-time.Second), ExpiresAt: stamp(10 * time.Second), GraceUntil: stamp(20 * time.Second), Scope: wire.WholePlatformScope}
}

func TestM19StoredHashChainAndMissingSingletonRefuseWithoutRepair(t *testing.T) {
	for _, damage := range []string{"hash", "chain", "singleton"} {
		t.Run(damage, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			if damage == "singleton" {
				if _, e := f.owner.DB.Exec(ctx, "DELETE FROM platform_license_state WHERE id=1"); e != nil {
					t.Fatal(e)
				}
			} else {
				first := claims(f)
				if _, e := f.service.Import(ctx, f.admin, input(t, f, first, ""), uuid.NewString()); e != nil {
					t.Fatal(e)
				}
				if damage == "hash" {
					if _, e := f.owner.DB.Exec(ctx, "UPDATE platform_license_artifacts SET artifact_sha256=$1", strings.Repeat("0", 64)); e != nil {
						t.Fatal(e)
					}
				} else {
					next := first
					next.LicenseID = uuid.NewString()
					next.ReplacesLicenseID = first.LicenseID
					next.ExpiresAt = f.base.Add(30 * time.Second).Format(time.RFC3339)
					next.GraceUntil = f.base.Add(40 * time.Second).Format(time.RFC3339)
					if _, e := f.service.Import(ctx, f.admin, input(t, f, next, first.LicenseID), uuid.NewString()); e != nil {
						t.Fatal(e)
					}
					if _, e := f.owner.DB.Exec(ctx, "UPDATE platform_license_artifacts SET predecessor_id=NULL WHERE license_id=$1", next.LicenseID); e != nil {
						t.Fatal(e)
					}
				}
			}
			if e := f.service.Check(ctx); e == nil || e.Error() != "LICENSE_STORED_STATE_INVALID" {
				t.Fatal("damaged state admitted", e)
			}
			if _, e := f.service.Diagnostics(ctx, f.admin, uuid.NewString()); e == nil || e.Error() != "LICENSE_STORED_STATE_INVALID" {
				t.Fatal("corrupt diagnostic claimed valid", e)
			}
			var count int
			if e := f.owner.DB.QueryRow(ctx, "SELECT count(*) FROM platform_license_state").Scan(&count); e != nil || (damage == "singleton" && count != 0) {
				t.Fatal("missing state automatically repaired", count, e)
			}
		})
	}
}
func input(t *testing.T, f scenario, c wire.Claims, expected string) licensing.ImportInput {
	t.Helper()
	raw, e := wire.Issue(c, f.private)
	if e != nil {
		t.Fatal("synthetic signed fixture", e)
	}
	return licensing.ImportInput{Artifact: string(raw), Reason: "Synthetic isolated license acceptance", ExpectedCurrentLicenseID: expected}
}
func expect(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("expected %s, observed %v", code, e)
	}
}
func importLicense(t *testing.T, f scenario, c wire.Claims, expected string) licensing.ImportResult {
	t.Helper()
	result, e := f.service.Import(context.Background(), f.admin, input(t, f, c, expected), uuid.NewString())
	if e != nil {
		t.Fatal("actual SQL import", e)
	}
	return result
}

func TestLicenseRepositoryActualImportIdempotenceDeploymentAndReopen(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	expect(t, f.service.Check(ctx), "LICENSE_NEW_BUSINESS_UNAVAILABLE")
	c := claims(f)
	foreign := c
	foreign.DeploymentID = uuid.NewString()
	_, e := f.service.Import(ctx, f.admin, input(t, f, foreign, ""), uuid.NewString())
	expect(t, e, "LICENSE_DEPLOYMENT_MISMATCH")
	first := importLicense(t, f, c, "")
	if first.Repeated || first.State.State != licensing.Active || !first.State.NewBusinessAllowed || first.State.AcceptedLicenses != 1 {
		t.Fatal(first)
	}
	again := importLicense(t, f, c, "")
	if !again.Repeated || again.State.HistoryHeadLicenseID != c.LicenseID {
		t.Fatal("repeat changed authority", again)
	}
	var count, success, failed int
	if e = f.owner.DB.QueryRow(ctx, "SELECT count(*) FROM platform_license_artifacts").Scan(&count); e != nil {
		t.Fatal(e)
	}
	if e = f.owner.DB.QueryRow(ctx, "SELECT count(*) FILTER (WHERE action='license.imported'),count(*) FILTER (WHERE action='license.import_rejected') FROM audit_events").Scan(&success, &failed); e != nil || count != 1 || success != 1 || failed != 1 {
		t.Fatal("atomic audit/idempotence", count, success, failed, e)
	}
	reopened, e := licensing.New(f.owner, f.trust, wire.DefaultPolicy(), f.clock.now)
	if e != nil {
		t.Fatal(e)
	}
	state, e := reopened.StatusAdmin(ctx, f.admin)
	if e != nil || state.Current == nil || state.Current.LicenseID != c.LicenseID || state.DeploymentID != f.deployment {
		t.Fatal("persistent reopen", state, e)
	}
	c.CustomerReference = "Changed signed file with same ID"
	_, e = reopened.Import(ctx, f.admin, input(t, f, c, first.ImportedLicenseID), uuid.NewString())
	expect(t, e, "LICENSE_ID_CONFLICT")
	if e = reopened.Check(ctx); e != nil {
		t.Fatal("conflicting import damaged original", e)
	}
}

func TestLicenseRepositoryFutureRenewalGraceExpiryAndPersistedRollbackFence(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	first := claims(f)
	importLicense(t, f, first, "")
	next := first
	next.LicenseID = uuid.NewString()
	next.ReplacesLicenseID = first.LicenseID
	next.NotBefore = f.base.Add(10 * time.Second).Format(time.RFC3339)
	next.ExpiresAt = f.base.Add(50 * time.Second).Format(time.RFC3339)
	next.GraceUntil = f.base.Add(60 * time.Second).Format(time.RFC3339)
	pending := importLicense(t, f, next, first.LicenseID)
	if pending.State.Pending == nil || pending.State.Pending.LicenseID != next.LicenseID || pending.State.Current.LicenseID != first.LicenseID {
		t.Fatal("future renewal replaced active license", pending)
	}
	third := next
	third.LicenseID = uuid.NewString()
	third.ReplacesLicenseID = next.LicenseID
	third.NotBefore = f.base.Add(40 * time.Second).Format(time.RFC3339)
	third.ExpiresAt = f.base.Add(70 * time.Second).Format(time.RFC3339)
	third.GraceUntil = third.ExpiresAt
	_, e := f.service.Import(ctx, f.admin, input(t, f, third, next.LicenseID), uuid.NewString())
	expect(t, e, "LICENSE_PENDING_SUCCESSOR_EXISTS")
	for _, test := range []struct {
		offset  time.Duration
		phase   licensing.Phase
		allowed bool
	}{{11 * time.Second, licensing.Active, true}, {50 * time.Second, licensing.Grace, true}, {60 * time.Second, licensing.Expired, false}, {10 * time.Second, licensing.ClockSuspect, false}, {61 * time.Second, licensing.Expired, false}} {
		f.clock.set(f.base.Add(test.offset))
		state, e := f.service.StatusAdmin(ctx, f.admin)
		if e != nil || state.State != test.phase || state.NewBusinessAllowed != test.allowed || state.Current.LicenseID != next.LicenseID || state.Pending != nil {
			t.Fatal("actual persistent time state", test.phase, state, e)
		}
		if state.ObservedAt.Before(f.base.Add(60*time.Second)) && test.phase == licensing.ClockSuspect {
			t.Fatal("rollback rewrote high water")
		}
	}
	expect(t, f.service.Check(ctx), "LICENSE_NEW_BUSINESS_UNAVAILABLE")
	oldRepeat := importLicense(t, f, first, "")
	if !oldRepeat.Repeated || oldRepeat.State.Current.LicenseID != next.LicenseID {
		t.Fatal("old repeat rolled license back", oldRepeat)
	}
}

func TestLicenseRepositoryConcurrentRenewalAndCapacityNeverOverwrite(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	first := claims(f)
	importLicense(t, f, first, "")
	results := make(chan error, 2)
	var joined sync.WaitGroup
	for i := 0; i < 2; i++ {
		next := first
		next.LicenseID = uuid.NewString()
		next.ReplacesLicenseID = first.LicenseID
		next.ExpiresAt = f.base.Add(30 * time.Second).Format(time.RFC3339)
		next.GraceUntil = next.ExpiresAt
		request := input(t, f, next, first.LicenseID)
		joined.Add(1)
		go func() {
			defer joined.Done()
			_, e := f.service.Import(ctx, f.admin, request, uuid.NewString())
			results <- e
		}()
	}
	joined.Wait()
	close(results)
	success, conflicts := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if e.Error() == "LICENSE_CURRENT_CHANGED" {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatal("concurrent import not serialized", success, conflicts)
	}
	state, e := f.service.StatusAdmin(ctx, f.admin)
	if e != nil || state.AcceptedLicenses != 2 {
		t.Fatal(state, e)
	}
	bounded, e := licensing.New(f.owner, f.trust, wire.Policy{MaximumImports: 1, RollbackToleranceSeconds: 0}, f.clock.now)
	if e != nil {
		t.Fatal(e)
	}
	last := claims(f)
	last.ReplacesLicenseID = state.Current.LicenseID
	last.ExpiresAt = f.base.Add(40 * time.Second).Format(time.RFC3339)
	last.GraceUntil = last.ExpiresAt
	_, e = bounded.Import(ctx, f.admin, input(t, f, last, state.HistoryHeadLicenseID), uuid.NewString())
	expect(t, e, "LICENSE_IMPORT_CAPACITY_REQUIRED")
	if e = bounded.Check(ctx); e != nil {
		t.Fatal("lower configured capacity broke existing license", e)
	}
}

func TestLicenseRepositoryRealSessionRolePortalAndTamperedStorageFailClosed(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	c := claims(f)
	importLicense(t, f, c, "")
	invite, e := f.owner.Invite(ctx, f.admin, "", "createInvitation", uuid.NewString(), "ordinary-license@example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	password := fixture.Password()
	if e = f.owner.Register(ctx, fixture.InvitationToken(t, f.owner, invite.ID), "Ordinary synthetic user", password, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	ordinary := fixture.Login(t, f.owner, "ordinary-license@example.invalid", password, "portal")
	_, e = f.service.StatusAdmin(ctx, ordinary)
	expect(t, e, "ACCESS_DENIED")
	_, e = f.service.Import(ctx, ordinary, input(t, f, c, ""), uuid.NewString())
	expect(t, e, "ACCESS_DENIED")
	minimal, e := f.service.StatusPortal(ctx, ordinary)
	raw, _ := json.Marshal(minimal)
	if e != nil || minimal.State != licensing.Active || strings.Contains(string(raw), f.deployment) || strings.Contains(string(raw), c.CustomerReference) || strings.Contains(string(raw), c.LicenseID) {
		t.Fatal("Portal exposes customer license diagnostics", e)
	}
	if _, e = f.owner.DB.Exec(ctx, "UPDATE users SET is_platform_admin=false WHERE id=$1", f.admin.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	_, e = f.service.StatusAdmin(ctx, f.admin)
	expect(t, e, "AUTH_SESSION_EXPIRED")
	if _, e = f.owner.DB.Exec(ctx, "UPDATE users SET is_platform_admin=true WHERE id=$1", f.admin.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	var original []byte
	if e = f.owner.DB.QueryRow(ctx, "SELECT artifact FROM platform_license_artifacts WHERE license_id=$1", c.LicenseID).Scan(&original); e != nil {
		t.Fatal(e)
	}
	tampered := []byte("synthetic corrupt stored artifact")
	if _, e = f.owner.DB.Exec(ctx, "UPDATE platform_license_artifacts SET artifact=$1 WHERE license_id=$2", tampered, c.LicenseID); e != nil {
		t.Fatal(e)
	}
	expect(t, f.service.Check(ctx), "LICENSE_STORED_STATE_INVALID")
	var retained []byte
	if e = f.owner.DB.QueryRow(ctx, "SELECT artifact FROM platform_license_artifacts WHERE license_id=$1", c.LicenseID).Scan(&retained); e != nil || string(retained) != string(tampered) {
		t.Fatal("invalid stored bytes were silently repaired", e)
	}
	// Exact restoration below is restricted to this deliberately corrupted,
	// disposable test schema. The product exposes no such repair command.
	if _, e = f.owner.DB.Exec(ctx, "UPDATE platform_license_artifacts SET artifact=$1 WHERE license_id=$2", original, c.LicenseID); e != nil {
		t.Fatal(e)
	}
	if e = f.service.Check(ctx); e != nil {
		t.Fatal("original exact signed artifact could not recover", e)
	}
}
