package scope

import (
	"context"
	"path/filepath"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type ComposePlan struct {
	ScopeID       string           `json:"scope_id"`
	Profile       string           `json:"profile"`
	ReleaseID     string           `json:"release_id"`
	ModelSHA256   string           `json:"model_sha256"`
	Rendered      compose.Rendered `json:"rendered"`
	Qualification string           `json:"qualification"`
}
type modelRenderer interface {
	Render(context.Context, compose.RenderContext, compose.Model) (compose.Rendered, error)
}

func PlanCompose(ctx context.Context, c configuration.Config) (ComposePlan, error) {
	if c.Compose == nil || c.Engine == nil || c.Compose.ModelFile == "" {
		return ComposePlan{}, failure("OFFLINE_SCOPE_COMPOSE_MODEL_CONFIGURATION_REQUIRED")
	}
	engineClient, e := engine.New(*c.Engine)
	if e != nil {
		return ComposePlan{}, e
	}
	defer engineClient.Close()
	renderer, e := compose.New(c)
	if e != nil {
		return ComposePlan{}, e
	}
	return planCompose(ctx, c, engineClient, renderer)
}
func readModel(c configuration.Config) ([]byte, error) {
	return readPrivateInput(c, c.Compose.ModelFile, c.Verification.MaximumMetadataBytes, "OFFLINE_SCOPE_PRIVATE_MODEL_REFERENCE_REQUIRED")
}

func planCompose(ctx context.Context, c configuration.Config, client imageEngine, renderer modelRenderer) (ComposePlan, error) {
	if c.Compose == nil || c.Engine == nil || c.Compose.ModelFile == "" {
		return ComposePlan{}, failure("OFFLINE_SCOPE_COMPOSE_MODEL_CONFIGURATION_REQUIRED")
	}
	if ctx.Err() != nil {
		return ComposePlan{}, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	l, e := open(c, false)
	if e != nil {
		return ComposePlan{}, e
	}
	defer l.close()
	if len(l.events) < 3 || l.events[2].Outcome != "completed" {
		return ComposePlan{}, failure("OFFLINE_SCOPE_COMPLETED_PREPARATION_REQUIRED")
	}
	verified, e := bundle.VerifyPrepared(ctx, c.Verification, filepath.Join(c.ScopeRoot, "release"), c.ExpectedArchiveSHA256, c.ExpectedManifestSHA256)
	if e != nil {
		return ComposePlan{}, e
	}
	if verified.ReleaseID != l.marker.ReleaseID || verified.Profile != l.marker.Profile {
		return ComposePlan{}, failure("OFFLINE_SCOPE_RELEASE_CONTEXT_MISMATCH")
	}
	if e = validateImageAssets(l.events, verified.Manifest); e != nil {
		return ComposePlan{}, e
	}
	state := imageState(l.events)
	if state.engine == nil || len(state.pending) > 0 {
		return ComposePlan{}, failure("OFFLINE_SCOPE_COMPLETED_IMAGE_ADMISSION_REQUIRED")
	}
	if _, e = l.checkEngine(ctx, client); e != nil {
		return ComposePlan{}, e
	}
	var raw []byte
	if record, outcome, _ := inputsState(l.events); record != nil {
		if outcome != "completed" {
			return ComposePlan{}, failure("OFFLINE_SCOPE_INPUT_RECONCILIATION_REQUIRED")
		}
		p, se := l.readInputs(ctx, *record)
		if se != nil {
			return ComposePlan{}, se
		}
		raw = p.model
	} else {
		raw, e = readModel(c)
		if e != nil {
			return ComposePlan{}, e
		}
	}
	model, e := compose.DecodeModel(raw, c.Verification.MaximumMetadataBytes)
	if e != nil {
		return ComposePlan{}, e
	}
	allowed := map[string]bool{}
	for _, record := range state.completed {
		allowed[record.ManifestDigest] = true
	}
	for _, service := range model.Services {
		if !allowed[service.Image] {
			return ComposePlan{}, failure("OFFLINE_SCOPE_MODEL_IMAGE_NOT_ADMITTED")
		}
		var expected *ImageRecord
		for _, record := range state.completed {
			if record.ManifestDigest == service.Image {
				copy := record
				expected = &copy
				break
			}
		}
		image, found, e := client.Inspect(ctx, service.Image)
		if e != nil {
			return ComposePlan{}, e
		}
		if !found || expected == nil || !matchesImage(image, *expected) {
			return ComposePlan{}, failure("OFFLINE_SCOPE_RECORDED_IMAGE_MISSING_OR_CHANGED")
		}
	}
	rendered, e := renderer.Render(ctx, compose.RenderContext{ScopeID: l.marker.ScopeID, ScopeRoot: c.ScopeRoot, AllowedImages: allowed}, model)
	if e != nil {
		return ComposePlan{}, e
	}
	if rendered.Status != "passed_constrained_Compose_render_only" || rendered.Services != len(model.Services) || rendered.DocumentSHA256 != wire.Digest(rendered.Document) || len(rendered.Document) == 0 {
		return ComposePlan{}, failure("OFFLINE_SCOPE_RENDER_RESULT_INVALID")
	}
	if _, e = l.checkEngine(ctx, client); e != nil {
		return ComposePlan{}, e
	}
	if ctx.Err() != nil {
		return ComposePlan{}, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	qualification := "verified_scope_readonly_plan_not_reserved_installed_or_target_qualified"
	if l.marker.Profile == wire.FixtureProfile {
		qualification = "synthetic_scope_readonly_plan_never_platform_installation_or_target"
	}
	return ComposePlan{ScopeID: l.marker.ScopeID, Profile: l.marker.Profile, ReleaseID: l.marker.ReleaseID, ModelSHA256: wire.Digest(raw), Rendered: rendered, Qualification: qualification}, nil
}
