//go:build training_artifact_integration && model_lifecycle_admission_integration && training_operator_integration

package training_test

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/preferences"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"testing"
)

func prepareCumulativePortalRecovery(t *testing.T, g preparationScenario) *preferences.Preference {
	t.Helper()
	a := g.f.s.Identity
	if !a.Config.PortalRuntime() {
		return nil
	}
	var count int
	if e := a.DB.QueryRow(t.Context(), "SELECT count(*) FROM terminal_notifications").Scan(&count); e != nil || (a.Config.PortalOnly() && count != 0) {
		t.Fatal("migration backfilled historical terminal success", e, count)
	}
	actor := fixture.Login(t, a, "admin@example.invalid", g.f.password, "portal")
	service := preferences.Service{DB: a.DB, Identity: a}
	p, e := service.Get(t.Context(), actor)
	if e != nil {
		t.Fatal(e)
	}
	p, e = service.Update(t.Context(), actor, preferences.UpdateRequest{Theme: "dark", ExpectedRevision: p.Revision}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return &p
}
func verifyCumulativePortalRecovery(t *testing.T, state *preferences.Preference, g preparationScenario, pool *pgxpool.Pool) {
	t.Helper()
	if state == nil {
		return
	}
	var theme string
	var revision int64
	var count int
	if e := pool.QueryRow(t.Context(), "SELECT theme,revision FROM user_preferences WHERE user_id=$1", g.f.admin.Me.User.ID).Scan(&theme, &revision); e != nil || theme != state.Theme || revision != state.Revision {
		t.Fatal("matched restore lost fixed preference", e)
	}
	if e := pool.QueryRow(t.Context(), "SELECT count(*)-count(DISTINCT run_id) FROM terminal_notifications").Scan(&count); e != nil || count != 0 {
		t.Fatal("matched restore duplicated terminal notice", e)
	}
	if g.f.s.Identity.Config.LocalizationOnly() {
		t.Log("Actual M16 matched four-store+SQL+SSO-keyring restore retained fixed preference revision and truthful unique terminal notices")
		return
	}
	t.Log("Actual M15 matched four-store+SQL+SSO-keyring restore retained fixed preference revision and truthful unique terminal notices after populated84-table/20-entry native upgrade repeated to portal86/21")
}
