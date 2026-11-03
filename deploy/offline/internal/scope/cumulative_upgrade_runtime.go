package scope

import (
	"context"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

// These are separately named original-custody and effective-source facts. This
// read-only projection is not native SQL/live-health/backup completion authority.
type cumulativeMVPForwardSource struct {
	Original                   Marker
	OriginalDescriptorSHA256   string
	Installed                  view.InstalledRelease
	Target                     wire.Manifest
	PreparedRoot               string
	ArchiveSHA256              string
	ManifestSHA256             string
	Model                      compose.Model
	Files                      map[string][]engine.ConfigurationFile
	PayloadIndexSHA256         string
	RuntimeConfigurationSHA256 string
	RegistryOwner              engine.Ownership
	RegistryTarget             string
	UID, GID                   int
}

func (l *locked) cumulativeMVPCurrentSource(ctx context.Context) (cumulativeMVPForwardSource, error) {
	var out cumulativeMVPForwardSource
	s := cumulativeUpgradeState(l.events)
	if ctx == nil || ctx.Err() != nil || l.marker.Profile != wire.CumulativeCPUProfile || !s.activated || s.pending != "" || s.record == nil || s.record.Installed == nil || s.record.Installed.Profile != wire.MVPPostgreSQLCPUProfile || s.record.Installed.SchemaVersion != wire.MVPPostgreSQLCPUV1SchemaVersion {
		return out, failure("OFFLINE_SCOPE_COMPLETED_MVP_FORWARD_REQUIRED")
	}
	if e := l.verifyCumulativeUpgradePlan(); e != nil {
		return out, e
	}
	p, e := l.readCumulativeUpgradePlan()
	if e != nil {
		return out, e
	}
	if !cumulativeRegistryCompleted(l.events) {
		return out, failure("OFFLINE_SCOPE_MVP_REGISTRY_PREPARATION_REQUIRED")
	}
	files, model, e := l.cumulativePayloads(ctx, p)
	if e != nil {
		return out, e
	}
	raw, e := restoreNamedFile(files["control-config"], "runtime.yaml")
	if e != nil {
		return out, e
	}
	input, outcome, _ := inputsState(l.cumulativeOriginal().events)
	if input == nil || outcome != "completed" {
		return out, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_ORIGINAL_INPUT_REQUIRED")
	}
	indices := struct{ Control, Operator []InputFile }{cumulativeIndex(files["control-config"]), cumulativeIndex(files["operator-config"])}
	return cumulativeMVPForwardSource{Original: l.marker, OriginalDescriptorSHA256: input.DescriptorSHA256, Installed: *s.record.Installed, Target: p.Target, PreparedRoot: p.Request.PreparedRoot, ArchiveSHA256: p.Request.ArchiveSHA256, ManifestSHA256: p.Request.ManifestSHA256, Model: model, Files: files, PayloadIndexSHA256: wire.Digest(encode(indices)), RuntimeConfigurationSHA256: wire.Digest(raw), RegistryOwner: cumulativeRegistryOwner(p), RegistryTarget: p.RegistryMountTarget, UID: p.UID, GID: p.GID}, nil
}

// Current runtime construction consumes the completed replacement at this exact
// prefix. It never rewrites the original input descriptor or earlier events.
func (l *locked) applyCumulativeRuntime(ctx context.Context, model compose.Model, p *preparedInputs, images map[string]ImageRecord) (compose.Model, error) {
	state := cumulativeUpgradeState(l.events)
	if !state.replaced {
		return model, nil
	}
	plan, e := l.readCumulativeUpgradePlan()
	if e != nil {
		return model, e
	}
	files, next, e := l.cumulativePayloads(ctx, plan)
	if e != nil {
		return model, e
	}
	if wire.Digest(encode(next)) != plan.ModelSHA256 {
		return model, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_MODEL_CHANGED")
	}
	// Keep p.record, p.descriptor and p.model as original source identity. Only the
	// private current payload map is overlaid; backup chooses its own image below.
	p.files = map[string][]engine.ConfigurationFile{}
	original, _, _ := inputsState(l.events)
	source, e := l.readInputs(ctx, *original)
	if e != nil {
		return model, e
	}
	for role, values := range source.files {
		p.files[role] = values
	}
	p.files["control-config"] = files["control-config"]
	p.files["operator-config"] = files["operator-config"]
	images["cumulative-upgrade-control"] = plan.ControlImage
	return next, nil
}
func (l *locked) verifyCumulativeCurrentConfiguration(ctx context.Context, c runtimeEngine, model compose.Model, observed compose.RuntimeStatus) error {
	if !cumulativeUpgradeState(l.events).replaced {
		return nil
	}
	p, e := l.readCumulativeUpgradePlan()
	if e != nil {
		return e
	}
	if p.Target.Profile == wire.MVPPostgreSQLCPUProfile {
		if e := verifyCumulativeRegistry(ctx, c, p, observed.Services["control"].ID); e != nil {
			return e
		}
	}
	owner := cumulativeOwner(p, false)
	if _, found, e := c.InspectVolume(ctx, owner); e != nil || !found {
		return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_CONFIGURATION_MISSING")
	}
	id := observed.Services["control"].ID
	if e = c.VerifyVolumeConsumers(ctx, owner, []string{id}); e != nil {
		return e
	}
	if e = c.VerifyVolumeConsumers(ctx, cumulativeOwner(p, true), nil); e != nil {
		return e
	}
	files, _, e := l.cumulativePayloads(ctx, p)
	if e != nil {
		return e
	}
	target := ""
	for _, m := range model.Services["control"].Mounts {
		if m.Source == owner.Role {
			target = m.Target
		}
	}
	reader, ok := c.(interface {
		InspectCurrentControlConfiguration(context.Context, engine.Ownership, string, string, int, int, []engine.ConfigurationFile) (engine.ConfigurationObservation, error)
		InspectFailedControlConfiguration(context.Context, engine.Ownership, string, string, int, int, []engine.ConfigurationFile) (engine.ConfigurationObservation, error)
	})
	if !ok {
		return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_CURRENT_READER_REQUIRED")
	}
	var observation engine.ConfigurationObservation
	if observed.Services["control"].State == "exited" && observed.Services["control"].ExitCode > 0 {
		observation, e = reader.InspectFailedControlConfiguration(ctx, owner, id, target, p.UID, p.GID, files["control-config"])
	} else {
		observation, e = reader.InspectCurrentControlConfiguration(ctx, owner, id, target, p.UID, p.GID, files["control-config"])
	}
	if e != nil || observation.Status != engine.ConfigurationPayloadVerified {
		return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_CURRENT_CONFIGURATION_CHANGED")
	}
	return nil
}
func (l *locked) bindCumulativeBackupPlan(p *BackupPlan) error {
	s := cumulativeUpgradeState(l.events)
	if s.record == nil {
		return nil
	}
	if !s.activated {
		return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_ACTIVATION_REQUIRED_FOR_NEW_BACKUP")
	}
	plan, e := l.readCumulativeUpgradePlan()
	if e != nil {
		return e
	}
	p.ReleaseID = plan.Target.ReleaseID
	p.Profile = plan.Target.Profile
	p.ManifestSHA256 = plan.Request.ManifestSHA256
	p.Initializer = plan.ControlImage.ManifestDigest
	for role, job := range p.Jobs {
		if job.Store != nil {
			copy := *job.Store
			copy.Image = p.Initializer
			job.Store = &copy
			p.Jobs[role] = job
		}
	}
	return nil
}
