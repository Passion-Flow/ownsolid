//go:build repository_integration

package models_test

import (
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	modelhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/models/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestActualModelImportPolicy(t *testing.T) {
	for _, name := range []string{"effective_catalog", "admission_bounds"} {
		t.Run(name, func(t *testing.T) {
			x := modelReadSetup(t)
			p := config.DefaultRuntimePolicy()
			p.Models.MaximumAttempts = 1
			p.Models.MaximumBundleBytes = 1600000000
			x.owner.Config.Runtime = &p
			before := x.audits(t)
			if name == "effective_catalog" {
				// Real service/actor; direct transport handler projection, not an HTTPS journey.
				h := modelhttp.Handler{Service: x.s}
				r := httptest.NewRequest("GET", "/api/v1/admin/models/catalog", nil).WithContext(x.ctx)
				value, status, e := h.Handle(r, x.admin, uuid.NewString())
				if e != nil || status != 200 {
					t.Fatal("current policy catalog", e)
				}
				m := value.(map[string]any)
				if m["maximum_bundle_bytes"] != 1600000000 || m["maximum_attempts"] != 1 || m["minimum_bundle_bytes"] != modelartifact.MinimumArchiveBytes() {
					t.Fatal("effective bounds differ from service policy", m)
				}
			} else {
				for _, size := range []int64{wire.ModelBytes(), modelartifact.MinimumArchiveBytes() - 1, 1600000001} {
					v, e := x.s.StartImport(x.ctx, x.admin, models.ImportRequest{WorkspaceID: x.f.Workspace, Name: "Policy admission refusal", CatalogDigest: wire.ModelCatalogDigest(), ExpectedBytes: size, LicenseAcknowledged: true, Reason: "Synthetic policy boundary"}, uuid.NewString(), uuid.NewString())
					modelReadError(t, e, "VALIDATION_FAILED")
					if v.ID != "" {
						t.Fatal("invalid admission published DTO")
					}
				}
			}
			if x.audits(t) != before {
				t.Fatal("catalog/refused admission wrote an audit")
			}
		})
	}
}
