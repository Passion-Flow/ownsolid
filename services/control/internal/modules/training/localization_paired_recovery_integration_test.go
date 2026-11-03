//go:build training_artifact_integration && model_lifecycle_admission_integration && training_operator_integration

package training_test

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/localization"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"testing"
)

type cumulativeLocalizationState struct {
	locales map[string]string
	user    string
	version int64
}

func localeRows(t *testing.T, p *pgxpool.Pool) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, table := range []string{"users", "invitations", "mail_deliveries"} {
		var raw string
		if e := p.QueryRow(t.Context(), "SELECT COALESCE(jsonb_agg(jsonb_build_array(id,locale) ORDER BY id)::text,'[]') FROM "+table).Scan(&raw); e != nil {
			t.Fatal(e)
		}
		out[table] = raw
	}
	return out
}
func prepareCumulativeLocalizationRecovery(t *testing.T, g preparationScenario) *cumulativeLocalizationState {
	t.Helper()
	a := g.f.s.Identity
	if !a.Config.LocalizationOnly() {
		return nil
	}
	a.Config.Localization = config.LocalizationConfiguration{PackageDirectory: os.Getenv("PROJECT_ONE_STATIC_LOCALE_PACKAGE_DIRECTORY"), RequiredLocales: []string{"en", "fr", "ar"}}
	if a.Config.Localization.PackageDirectory == "" {
		t.Fatal("current static package required")
	}
	var e error
	a.Locales, e = localization.New(a.Config.Localization, a.Config.Policy().Localization)
	if e != nil {
		t.Fatal(e)
	}
	a.Mail.Templates, e = mail.LoadTemplatesWithRegistry(a.Locales)
	if e != nil {
		t.Fatal(e)
	}
	actor := fixture.Login(t, a, "admin@example.invalid", g.f.password, "portal")
	changed, e := a.UpdateMe(t.Context(), actor, actor.Me.User.DisplayName, "fr", actor.Me.User.Version, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	admin := fixture.Login(t, a, "admin@example.invalid", g.f.password, "admin")
	if _, e = a.Invite(t.Context(), admin, "", "createInvitation", uuid.NewString(), "current-locale-restore@example.invalid", "ar", 0, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	return &cumulativeLocalizationState{locales: localeRows(t, a.DB), user: changed.User.ID, version: changed.User.Version}
}
func verifyCumulativeLocalizationRecovery(t *testing.T, state *cumulativeLocalizationState, g preparationScenario, p *pgxpool.Pool) {
	t.Helper()
	if state == nil {
		return
	}
	rows := localeRows(t, p)
	for table, want := range state.locales {
		if rows[table] != want {
			t.Fatal("matched restore changed durable locale IDs or values", table)
		}
	}
	var locale string
	var version int64
	if e := p.QueryRow(t.Context(), "SELECT locale,version FROM users WHERE id=$1", state.user).Scan(&locale, &version); e != nil || locale != "fr" || version != state.version+1 {
		t.Fatal("current locale/version lost", e)
	}
	t.Log("Actual M16 matched four-store+SQL+SSO-keyring restore retained exact user/invitation/outbox locale IDs and values, exact profile revision+1 authority fence and account22 after populated delivered M15 upgrade")
}
