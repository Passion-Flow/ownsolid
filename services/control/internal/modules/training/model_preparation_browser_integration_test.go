//go:build training_artifact_integration && model_lifecycle_admission_integration && model_lifecycle_browser_integration

package training_test

import (
	"context"
	"github.com/google/uuid"
	"os"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"testing"
)

func TestActualCpuModelPreparationHumanBrowser(t *testing.T) {
	for _, locale := range []string{"en", "zh-CN"} {
		t.Run(locale, func(t *testing.T) {
			t.Setenv("PROJECT_ONE_BROWSER_LOCALE", locale)
			testActualCpuPreparationHumanBrowser(t)
		})
	}
}
func testActualCpuPreparationHumanBrowser(t *testing.T) {
	if os.Getenv("PROJECT_ONE_BROWSER") != "1" {
		t.Fatal("explicit bounded browser profile required")
	}
	// Actual native accepted training bytes, source registry, PostgreSQL, TLS and pages.
	// The prior accepted/stopped report record is an explicit metadata test double.
	// This profile never accepts model files, approves a model or executes CUDA.
	g, review, _ := reviewMetadataFixture(t)
	stopped := preparationStoppedUndelivered(t, g)
	preparationHardware(t, g)
	identity := g.f.s.Identity
	password := fixture.Password()
	invitation, e := identity.Invite(context.Background(), g.f.admin, "", "createInvitation", uuid.NewString(), "reader@example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = identity.Register(context.Background(), fixture.InvitationToken(t, identity, invitation.ID), "Synthetic preparation reader", password, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	runPreparationBrowser(t, g, "preparation-flow.mjs", "preparation-browser", map[string]any{"member_email": "reader@example.invalid", "member_password": password, "review_id": review.ID, "stopped_id": stopped.ID, "scope": "Actual production React/TLS/Go/PostgreSQL/native source and accepted CPU adapter/create/cancel/recover. Prior evaluation accepted/stopped metadata is an explicit test double; report predictions are actual native1/3. No model file acceptance/approval/CUDA/derived serving qualification."})

	if count(t, g.f, "SELECT count(*) FROM model_preparation_reviews WHERE preparation_id=$1 AND decision='reject'", review.ID) != 1 || count(t, g.f, "SELECT count(*) FROM model_derivations") != 0 {
		t.Fatal("browser rejection registered a model or lost decision")
	}
}
