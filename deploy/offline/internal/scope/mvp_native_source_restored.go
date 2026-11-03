package scope

import (
	"context"
	"reflect"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// This binding retains historical native custody and current, independently
// admitted application facts. Neither can substitute for the other.
type mvpNativeRestoredBackupBinding struct {
	Admission                     RestoredApplicationAdmission          `json:"admission"`
	OriginalRestore               wire.NativePostgreSQLRestoreReference `json:"original_restore"`
	EffectiveControlPayloadSHA256 string                                `json:"effective_control_payload_sha256"`
}

func validMVPNativeRestoredOriginal(o mvpNativeRestoredBackupOriginal, scope string) bool {
	r, c, ref := o.Request, o.Completion, o.Reference
	var retained engine.NativePostgreSQLRestoreCompletion
	if wire.StrictJSON(c.Original, &retained) != nil {
		return false
	}
	retained.Original = append([]byte(nil), c.Original...)
	retained.OriginalSHA256 = c.OriginalSHA256
	if !reflect.DeepEqual(retained, c) {
		return false
	}
	p := o.Writer.NativePostgreSQLRecovery
	return engine.ValidateNativePostgreSQLRecoveryTask(o.Writer) == nil && r.Valid() && r.ApplicationProfile == wire.MVPPostgreSQLCPUApplicationProfile && r.TargetScopeID == scope && ref.Valid() && ref.Registry != nil &&
		ref.RequestSHA256 == wire.Digest(encode(r)) && c.Format == "projectone.postgres.restore-complete.v1" && c.RestoreID == r.RestoreID &&
		len(c.Original) > 0 && c.OriginalSHA256 == wire.Digest(c.Original) && ref.CompletionSHA256 == c.OriginalSHA256 &&
		c.ManifestSHA256 == r.SourceManifestSHA256 && ref.ManifestSHA256 == c.ManifestSHA256 && ref.IntentSHA256 == c.IntentSHA256 && ref.FilesSHA256 == c.FilesSHA256 && ref.TargetBindingSHA256 == c.TargetBindingSHA256 && ref.PostDataSHA256 == c.PostDataSHA256 && !c.CurrentLockHeld &&
		c.RuntimeContext.Valid() && c.RuntimeContext.ApplicationProfile == wire.MVPPostgreSQLCPUApplicationProfile && c.RuntimeContext.TargetScopeID == scope && c.RuntimeContext.RestoreID == r.RestoreID && c.RuntimeContext.BackupManifestSHA256 == r.SourceManifestSHA256 && c.RuntimeContext.SQLArchiveSHA256 == c.ArchiveSHA256 && c.RuntimeContext.TargetDatabase == r.TargetDatabase && c.RuntimeContext.TargetOperator == r.TargetOperator &&
		o.Writer.Kind == engine.NativePostgreSQLRestore && p != nil && p.Registry != nil && p.RestoreRequest != nil && reflect.DeepEqual(*p.RestoreRequest, r)
}

func (l *locked) bindMVPNativeRestoredBackup(ctx context.Context, p *mvpNativeBackupPlan, files map[string][]engine.ConfigurationFile) error {
	if !restoreState(l.events).reserved && dependencyAdmission(l.events) == nil {
		return nil
	}
	original, e := l.readMVPNativeRestoredBackupOriginal(ctx)
	if e != nil {
		return e
	}
	if !validMVPNativeRestoredOriginal(original, l.marker.ScopeID) || p.NativeRequest.Database != original.Completion.RuntimeContext.TargetDatabase || p.NativeRequest.Operator != original.Completion.RuntimeContext.TargetOperator || p.NativeRequest.TenantID != original.Completion.RuntimeContext.TenantID {
		return failure("OFFLINE_SCOPE_NATIVE_RESTORED_BACKUP_ORIGINAL_CHANGED")
	}
	admission, e := l.historicalBackupAdmission()
	if e != nil {
		return e
	}
	projection := tlsProjectionState(l.events)
	if projection.record == nil || admission.Dependency.EffectiveRuntimeSHA256 != p.RuntimeSHA256 {
		return failure("OFFLINE_SCOPE_NATIVE_RESTORED_BACKUP_CURRENT_REQUIRED")
	}
	tls, e := l.readTLSProjectionPlan(*projection.record)
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(cumulativeIndex(files["control-config"]), tls.ConfigurationFiles) || wire.Digest(encode(tls.ConfigurationFiles)) != tls.PayloadSHA256 {
		return failure("OFFLINE_SCOPE_NATIVE_RESTORED_BACKUP_CURRENT_PAYLOAD_CHANGED")
	}
	p.Restored = &mvpNativeRestoredBackupBinding{Admission: *admission, OriginalRestore: original.Reference, EffectiveControlPayloadSHA256: tls.PayloadSHA256}
	return nil
}

func (l *locked) admitMVPNativeRestoredBackup(ctx context.Context, c runtimeEngine, w runtimeOperator, v *verifiedRuntimeContext) error {
	if !restoreState(l.events).reserved && dependencyAdmission(l.events) == nil {
		return nil
	}
	original, e := l.readMVPNativeRestoredBackupOriginal(ctx)
	if e != nil {
		return e
	}
	if !validMVPNativeRestoredOriginal(original, l.marker.ScopeID) {
		return failure("OFFLINE_SCOPE_NATIVE_RESTORED_BACKUP_ORIGINAL_CHANGED")
	}
	_, files, e := l.admitRestoredBackup(ctx, c, w)
	if e != nil {
		return e
	}
	copyFiles := map[string][]engine.ConfigurationFile{}
	for role, f := range v.inputs.files {
		copyFiles[role] = f
	}
	copyFiles["control-config"] = files
	v.inputs.files = copyFiles
	return nil
}

func validMVPNativeRestoredBinding(p mvpNativeBackupPlan) bool {
	if p.Restored == nil {
		return true
	}
	b := p.Restored
	return validBackupAdmissionShape(&b.Admission) && b.Admission.Dependency.EffectiveRuntimeSHA256 == p.RuntimeSHA256 && b.OriginalRestore.Valid() && b.OriginalRestore.Registry != nil && wire.ValidSHA256(b.EffectiveControlPayloadSHA256)
}

func (l *locked) validateMVPNativeRestoredDomain(ctx context.Context, p *engine.NativePostgreSQLRecoveryProfile) error {
	if !restoreState(l.events).reserved && dependencyAdmission(l.events) == nil {
		return nil
	}
	original, e := l.readMVPNativeRestoredBackupOriginal(ctx)
	if e != nil {
		return e
	}
	if !validMVPNativeRestoredOriginal(original, l.marker.ScopeID) || !reflect.DeepEqual(p.Stores, original.Writer.NativePostgreSQLRecovery.Stores) || !reflect.DeepEqual(p.Registry, original.Writer.NativePostgreSQLRecovery.Registry) {
		return failure("OFFLINE_SCOPE_NATIVE_RESTORED_BACKUP_DOMAIN_CHANGED")
	}
	return nil
}

// Recheck real peer identities before a new write intent. A retained source
// admission does not permit a restarted database or a changed mounted payload.
func (l *locked) recheckMVPNativeBackupSource(ctx context.Context, c runtimeEngine, w runtimeOperator, p mvpNativeBackupPlan, x mvpNativeBackupExecution) error {
	for role, id := range x.Writer.Peers {
		object, found, e := c.InspectRuntime(ctx, p.ScopeID, role)
		if e != nil || !found || object.ID != id || object.State.ExitCode != 0 || object.State.OOMKilled || object.State.Paused || object.State.Restarting || object.State.Dead {
			return failure("OFFLINE_SCOPE_BACKUP_CURRENT_PROCESS_CHANGED")
		}
		if role == "postgres" {
			if !object.State.Running || object.State.Status != "running" || p.Restored != nil && (object.State.PID != p.Restored.Admission.PostgresPID || object.State.StartedAt != p.Restored.Admission.PostgresStartedAt) {
				return failure("OFFLINE_SCOPE_BACKUP_CURRENT_PROCESS_CHANGED")
			}
		} else if object.State.Running || object.State.PID != 0 || object.State.Status != "created" && object.State.Status != "exited" {
			return failure("OFFLINE_SCOPE_BACKUP_STOPPED_APPLICATION_REQUIRED")
		}
	}
	if p.Restored != nil {
		admission, e := l.historicalBackupAdmission()
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(admission, &p.Restored.Admission) {
			return failure("OFFLINE_SCOPE_NATIVE_RESTORED_BACKUP_CURRENT_PAYLOAD_CHANGED")
		}
		tls, files, e := l.currentControlReplacementFiles(ctx)
		if e != nil {
			return e
		}
		if wire.Digest(encode(cumulativeIndex(files))) != p.Restored.EffectiveControlPayloadSHA256 {
			return failure("OFFLINE_SCOPE_NATIVE_RESTORED_BACKUP_CURRENT_PAYLOAD_CHANGED")
		}
		input, outcome, _ := inputsState(l.events)
		if input == nil || outcome != "completed" {
			return failure("OFFLINE_SCOPE_BACKUP_INPUTS_REQUIRED")
		}
		prepared, e := l.readInputs(ctx, *input)
		if e != nil {
			return e
		}
		model, e := compose.DecodeModel(prepared.model, l.config.Verification.MaximumMetadataBytes)
		if e != nil {
			return e
		}
		if e = readMVPNativeCurrentControl(ctx, c, model, "control-config", tls.ConfigurationOwner, x.Writer.Peers["control"], tls.UID, tls.GID, files); e != nil {
			return e
		}
	}
	if p.Forward != nil {
		current, e := l.cumulativeMVPCurrentSource(ctx)
		if e != nil {
			return e
		}
		if current.PayloadIndexSHA256 != p.Forward.PayloadIndexSHA256 || current.RuntimeConfigurationSHA256 != p.Forward.RuntimeConfigurationSHA256 {
			return failure("OFFLINE_SCOPE_NATIVE_BACKUP_CURRENT_INSTALLED_CHANGED")
		}
		plan, e := l.readCumulativeUpgradePlan()
		if e != nil {
			return e
		}
		owner := cumulativeOwner(plan, false)
		if e = readMVPNativeCurrentControl(ctx, c, current.Model, owner.Role, owner, x.Writer.Peers["control"], current.UID, current.GID, current.Files["control-config"]); e != nil {
			return e
		}
	}
	return nil
}

// Exact mounted payload readback leaves domain-volume consumer checks to the
// initial full admission. The already-journalled native writer legitimately
// mounts the five source volumes during subsequent read-only observations.
func readMVPNativeCurrentControl(ctx context.Context, c runtimeEngine, model compose.Model, role string, owner engine.Ownership, id string, uid, gid int, files []engine.ConfigurationFile) error {
	destination := ""
	for _, mount := range model.Services["control"].Mounts {
		if mount.Source == role {
			if destination != "" || !mount.ReadOnly {
				return failure("OFFLINE_SCOPE_BACKUP_CURRENT_CONFIGURATION_MOUNT_REQUIRED")
			}
			destination = mount.Target
		}
	}
	if destination == "" {
		return failure("OFFLINE_SCOPE_BACKUP_CURRENT_CONFIGURATION_MOUNT_REQUIRED")
	}
	observation, e := readCurrentControlConfiguration(ctx, c, owner, id, destination, uid, gid, files)
	if e != nil || observation.Status != engine.ConfigurationPayloadVerified {
		return failure("OFFLINE_SCOPE_BACKUP_CURRENT_CONFIGURATION_READBACK_REQUIRED")
	}
	return nil
}
