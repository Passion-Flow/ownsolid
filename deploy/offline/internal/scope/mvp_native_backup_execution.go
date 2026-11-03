package scope

import (
	"context"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const mvpNativeExecutionFile = "native-execution.json"
const mvpNativeVerifierPlanFile = "native-verifier.json"
const mvpNativeWriterCompletionFile = "native-writer-completion.json"
const mvpNativeWriterStdoutFile = "native-writer-stdout.json"

type mvpNativeBackupExecution struct {
	MaintenanceHead  []byte                  `json:"maintenance_head"`
	FormatVersion    int                     `json:"format_version"`
	DescriptorSHA256 string                  `json:"descriptor_sha256"`
	RuntimeSHA256    string                  `json:"runtime_sha256"`
	Initializer      string                  `json:"initializer"`
	Writer           engine.DatabaseTask     `json:"writer"`
	Volumes          map[string]BackupVolume `json:"volumes"`
	PayloadSHA256    map[string]string       `json:"payload_sha256"`
}

type mvpNativeBackupEngine interface {
	backupEngine
	ObserveDatabaseTaskOutput(context.Context, engine.DatabaseTask, string) (*engine.TaskOutputObserver, error)
	ReadNativePostgreSQLTaskCompletion(context.Context, engine.DatabaseTask, string) (engine.TaskOutputObservation, error)
	StreamNativePostgreSQLBackupArchive(context.Context, engine.DatabaseTask, string, int64, int, int, io.Writer) (engine.NativePostgreSQLArchive, error)
	PutNativePostgreSQLScaffold(context.Context, engine.DatabaseTask, string) (engine.ConfigurationObservation, error)
	InspectNativePostgreSQLScaffold(context.Context, engine.DatabaseTask, string) (engine.ConfigurationObservation, error)
	RemoveNativePostgreSQLVolume(context.Context, engine.Ownership) error
}

func (l *locked) makeMVPNativeBackupExecution(ctx context.Context, v verifiedRuntimeContext, r configuration.BackupRequest, id string) (mvpNativeBackupPlan, mvpNativeBackupExecution, map[string][]engine.ConfigurationFile, error) {
	var p mvpNativeBackupPlan
	var execution mvpNativeBackupExecution
	bad := failure("OFFLINE_SCOPE_NATIVE_BACKUP_SOURCE_BINDING_INVALID")
	current, e := l.mvpNativeCurrentSource(ctx)
	if e != nil {
		return p, execution, nil, e
	}
	if e = l.validateMVPNativeCurrentPayload(ctx, v); e != nil {
		return p, execution, nil, e
	}
	raw, e := restoreNamedFile(v.inputs.files["operator-config"], configuration.NativePostgreSQLRecoveryDescriptorFile)
	if e != nil {
		return p, execution, nil, e
	}
	descriptor, e := configuration.LoadNativePostgreSQLRecoveryDescriptor(raw)
	if e != nil {
		return p, execution, nil, e
	}
	uid, gid, e := roleUser(v.model, "operator-config")
	if e != nil {
		return p, execution, nil, e
	}
	request, e := descriptor.BindBackup(l.marker.ScopeID, id, wire.Digest(l.lastHead), current.ManifestSHA256, strings.TrimPrefix(v.model.Services["control"].Image, "sha256:"), strings.TrimPrefix(v.model.Services["postgres"].Image, "sha256:"), r.Reason)
	if e != nil {
		return p, execution, nil, e
	}
	// Reference paths are the original admitted base-model targets. Current
	// effective payload bytes were already read by the owning runtime admission.
	baseModel, e := compose.DecodeModel(v.inputs.model, l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		return p, execution, nil, e
	}
	projection, e := projectMVPNativeConfiguration(v.inputs, baseModel, descriptor)
	if e != nil {
		return p, execution, nil, e
	}
	own := func(role string) engine.Ownership {
		return engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: id, Role: role + "-" + id}
	}
	profile := &engine.NativePostgreSQLRecoveryProfile{BackupRequest: &request, ConfigurationOwner: own(engine.NativePostgreSQLConfigurationRole), BackupOwner: own(engine.NativePostgreSQLArtifactRole), ScratchOwner: own(engine.NativePostgreSQLRecoveryScratchRole), Scaffold: &engine.NativePostgreSQLVerifierPlaceholder{Owner: own(engine.NativePostgreSQLScaffoldRole), Target: descriptor.RegistryScaffoldRoot}}
	state := volumeState(l.events)
	for i, role := range []string{"models", "datasets", "training", "preparations"} {
		volume, ok := state.roles[role]
		if !ok || volume.Phase != phasePrepared || volume.UID != uid || volume.GID != gid {
			return p, execution, nil, bad
		}
		profile.Stores[i] = engine.NativePostgreSQLStoreMount{Owner: l.volumeOwner(volume), Target: descriptor.StoreRoots[i]}
	}
	registry, e := l.mvpNativeBackupRegistryOwner(ctx, uid, gid, descriptor.RegistryRoot)
	if e != nil {
		return p, execution, nil, e
	}
	profile.Registry = &engine.NativePostgreSQLRegistryMount{Owner: registry, Target: descriptor.RegistryRoot}

	if e = l.validateMVPNativeRestoredDomain(ctx, profile); e != nil {
		return p, execution, nil, e
	}
	// Snapshot export does not read PreparedReleaseRoot. Keep its configured
	// identity unchanged without creating a blank or duplicated prepared release.
	peers := map[string]string{}
	for role, service := range v.creation.record.Services {
		peers[role] = service.ID
	}
	task := engine.DatabaseTask{Owner: engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: id, Role: "database-" + engine.NativePostgreSQLBackup}, VolumeOwner: own(engine.NativePostgreSQLRecoveryInputRole), Kind: engine.NativePostgreSQLBackup, Image: v.model.Services["control"].Image, UID: uid, GID: gid, NetworkID: v.creation.record.NetworkID, Peers: peers, NativePostgreSQLRecovery: profile}
	if engine.ValidateNativePostgreSQLRecoveryTask(task) != nil {
		return p, execution, nil, bad
	}
	execution = mvpNativeBackupExecution{MaintenanceHead: append([]byte(nil), l.lastHead...), FormatVersion: 1, DescriptorSHA256: v.inputs.record.DescriptorSHA256, RuntimeSHA256: wire.Digest(encode(*v.creation.record)), Initializer: v.inputs.descriptor.ImageID, Writer: task, Volumes: map[string]BackupVolume{}, PayloadSHA256: map[string]string{}}
	owners := map[string]engine.Ownership{"native-input": task.VolumeOwner, "native-projection": profile.ConfigurationOwner, "native-artifact": profile.BackupOwner, "native-scratch": profile.ScratchOwner, "native-scaffold": profile.Scaffold.Owner, "native-verify-input": own(engine.NativePostgreSQLVerifierInputRole), "native-verify-scratch": own(engine.NativePostgreSQLVerifierScratchRole)}
	payloads := map[string][]engine.ConfigurationFile{"native-input": append(append([]engine.ConfigurationFile(nil), projection.OperatorFiles...), engine.ConfigurationFile{Name: engine.NativePostgreSQLRecoveryRequestFile, Contents: encode(request)}), "native-projection": projection.ConfigurationFiles, "native-verify-input": {{Name: engine.NativePostgreSQLVerifierRequestFile, Contents: encode(request)}}}
	for role, owner := range owners {
		execution.Volumes[role] = BackupVolume{Owner: owner, UID: uid, GID: gid}
		execution.PayloadSHA256[role] = mvpNativePayloadSHA(payloads[role])
	}
	p = mvpNativeBackupPlan{NativeUID: uid, NativeGID: gid, FormatVersion: 2, ScopeID: l.marker.ScopeID, AttemptID: id, ReleaseID: current.ReleaseID, Profile: current.Profile, ConfigurationSHA256: l.marker.ConfigurationSHA256, ReleaseManifestSHA256: current.ManifestSHA256, ReleaseArchiveSHA256: current.ArchiveSHA256, DescriptorSHA256: execution.DescriptorSHA256, RuntimeSHA256: execution.RuntimeSHA256, Request: r, NativeRequest: request, ExecutionSHA256: wire.Digest(encode(execution))}
	roles := []string{}
	for role := range v.inputs.files {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		for _, file := range v.inputs.files[role] {
			p.ConfigurationFiles = append(p.ConfigurationFiles, BackupConfigurationFile{Role: role, File: InputFile{Name: file.Name, Bytes: int64(len(file.Contents)), SHA256: wire.Digest(file.Contents)}})
		}
	}
	if e = l.bindMVPNativeForwardBackup(ctx, &p); e != nil {
		return p, execution, nil, e
	}
	if e = l.bindMVPNativeRestoredBackup(ctx, &p, v.inputs.files); e != nil {
		return p, execution, nil, e
	}
	if e = l.bindMVPNativeBackupReleasePolicy(&p, v.inputs.files); e != nil {
		return p, execution, nil, e
	}
	var inputBytes, configurationBytes int64
	for _, files := range payloads {
		for _, file := range files {
			inputBytes += int64(len(file.Contents))
		}
	}
	for _, file := range projection.ConfigurationFiles {
		configurationBytes += int64(len(file.Contents))
	}
	p.Storage, e = makeMVPNativeBackupStorage(p, inputBytes, configurationBytes, l.config.Verification.MaximumMetadataBytes, l.config.Verification.MaximumManifestBytes, projection.RegistryManifestMaximumBytes, l.maximumJournalBytes())
	if e != nil {
		return p, execution, nil, e
	}
	p.Steps = mvpNativeBackupSteps(execution)
	if !validMVPNativeBackupPlan(p) || !validMVPNativeMaintenanceHead(p, execution.MaintenanceHead) || len(p.Steps) > 128 {
		return p, execution, nil, bad
	}
	return p, execution, payloads, nil
}

func mvpNativeBackupSteps(e mvpNativeBackupExecution) []BackupStep {
	steps := []BackupStep{{Phase: "reserve"}}
	roles := []string{}
	for role := range e.Volumes {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		for _, phase := range []string{"volume-create", "utility-create", "payload-write", "utility-remove"} {
			steps = append(steps, BackupStep{Phase: phase, Role: role})
		}
	}
	for _, step := range []BackupStep{{Phase: "task-create", Role: "native-writer"}, {Phase: "task-execute", Role: "native-writer"}, {Phase: "archive-publish", Role: "native-archive"}, {Phase: "task-remove", Role: "native-writer"}, {Phase: "archive-publish", Role: "native-verifier-plan"}, {Phase: "task-create", Role: "native-verifier"}, {Phase: "task-execute", Role: "native-verifier"}, {Phase: "task-remove", Role: "native-verifier"}} {
		steps = append(steps, step)
	}
	for _, role := range roles {
		steps = append(steps, BackupStep{Phase: "volume-remove", Role: role})
	}
	return append(steps, BackupStep{Phase: "manifest-publish"}, BackupStep{Phase: "complete"})
}

func (l *locked) mvpNativeTaskID(p mvpNativeBackupPlan, role string) string {
	state := backupState(l.events)
	if state.record == nil || state.record.AttemptID != p.AttemptID {
		return ""
	}
	for i, step := range p.Steps {
		if step.Phase == "task-create" && step.Role == role {
			return state.completed[i].ObjectID
		}
	}
	return ""
}

func (l *locked) mvpNativeVerifierTask(p mvpNativeBackupPlan, e mvpNativeBackupExecution, candidate string) (engine.DatabaseTask, error) {
	task := engine.DatabaseTask{Owner: engine.Ownership{ScopeID: p.ScopeID, OperationID: p.AttemptID, Role: "database-" + engine.NativePostgreSQLBackupVerify}, VolumeOwner: e.Volumes["native-verify-input"].Owner, Kind: engine.NativePostgreSQLBackupVerify, Image: e.Writer.Image, UID: p.NativeUID, GID: p.NativeGID, NativePostgreSQLBackupVerifier: &engine.NativePostgreSQLBackupVerifierProfile{Request: p.NativeRequest, CandidateSHA256: candidate, BackupOwner: e.Writer.NativePostgreSQLRecovery.BackupOwner, BackupParent: true, ScratchOwner: e.Volumes["native-verify-scratch"].Owner}}
	if !wire.ValidSHA256(candidate) {
		return engine.DatabaseTask{}, failure("OFFLINE_SCOPE_NATIVE_BACKUP_CANDIDATE_REQUIRED")
	}
	return task, nil
}

// No task start is repeated after its durable started/unknown intent. The writer
// can be observed through its original complete file; missing verifier stdout
// remains an unresolved original observation rather than a manufactured proof.
func (l *locked) executeMVPNativeTask(ctx context.Context, c mvpNativeBackupEngine, p mvpNativeBackupPlan, task engine.DatabaseTask, id string, files []engine.ConfigurationFile, reconcile bool) ([]byte, error) {
	var output *engine.TaskOutputObserver
	var e error
	if !reconcile {
		output, e = c.ObserveDatabaseTaskOutput(ctx, task, id)
		if e != nil {
			return nil, e
		}
		defer output.Close()
		if _, e = c.StartDatabaseTask(ctx, task, id, files, l.config.Compose.Process.ReservePercent); e != nil {
			return nil, e
		}
	}
	deadline := time.NewTimer(time.Duration(p.Request.ObserveSeconds) * time.Second)
	defer deadline.Stop()
	for {
		current, found, e := c.InspectDatabaseTask(ctx, task)
		if e != nil || !found || current.ID != id {
			return nil, failure("OFFLINE_SCOPE_NATIVE_BACKUP_TASK_UNRESOLVED")
		}
		if current.State.Status == "created" {
			return nil, failure("OFFLINE_SCOPE_BACKUP_NEVER_STARTED_OUTCOME_UNRESOLVED")
		}
		if current.State.Status == "exited" {
			if current.State.ExitCode != 0 || current.State.OOMKilled {
				return nil, failure("OFFLINE_SCOPE_BACKUP_TASK_FAILED")
			}
			if task.Kind == engine.NativePostgreSQLBackup {
				observed, e := c.ReadNativePostgreSQLTaskCompletion(ctx, task, id)
				if e != nil || observed.NativePostgreSQLBackupCompletion == nil {
					return nil, failure("OFFLINE_SCOPE_NATIVE_BACKUP_COMPLETION_REQUIRED")
				}
				if output != nil {
					attached, e := output.Wait(ctx)
					if e == nil && attached.NativePostgreSQLBackupCompletion != nil {
						root, e := openBackupDirectory(p.Request.DestinationRoot)
						if e != nil {
							return nil, e
						}
						e = confirmMVPNativePublication(root, mvpNativeWriterStdoutFile, attached.NativePostgreSQLBackupCompletion.Original, l.config.Verification.MaximumMetadataBytes)
						root.Close()
						if e != nil {
							return nil, e
						}
					}
				}
				return observed.NativePostgreSQLBackupCompletion.Original, nil
			}
			if output == nil {
				// An actual captured stdout may have been published before its
				// completion journal append was lost. Reopen only that exact retained
				// observation; never start/attach a replacement verifier or synthesize it.
				if task.NativePostgreSQLBackupVerifier == nil {
					return nil, failure("OFFLINE_SCOPE_NATIVE_BACKUP_ORIGINAL_OBSERVATION_REQUIRED")
				}
				root, e := openBackupDirectory(p.Request.DestinationRoot)
				if e != nil {
					return nil, e
				}
				defer root.Close()
				raw, e := regular(root, mvpNativeObservationFile, l.config.Verification.MaximumMetadataBytes)
				if e != nil {
					return nil, failure("OFFLINE_SCOPE_NATIVE_BACKUP_ORIGINAL_OBSERVATION_REQUIRED")
				}
				observed, e := engine.DecodeNativePostgreSQLBackupObservation(raw, l.config.Verification.MaximumMetadataBytes, p.AttemptID, wire.Digest(encode(p.NativeRequest)), task.NativePostgreSQLBackupVerifier.CandidateSHA256, p.ReleaseManifestSHA256)
				if e != nil || observed.NativePostgreSQL == nil {
					return nil, failure("OFFLINE_SCOPE_NATIVE_BACKUP_ORIGINAL_OBSERVATION_REQUIRED")
				}
				if _, _, e = readMVPNativeCandidate(root, l.config, p, *observed.NativePostgreSQL); e != nil {
					return nil, e
				}
				return observed.Original, nil
			}
			observed, e := output.Wait(ctx)
			if e != nil || observed.NativePostgreSQLBackup == nil {
				return nil, failure("OFFLINE_SCOPE_NATIVE_BACKUP_ORIGINAL_OBSERVATION_REQUIRED")
			}
			return observed.NativePostgreSQLBackup.Original, nil
		}
		timer := time.NewTimer(time.Duration(p.Request.PollMilliseconds) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
		case <-deadline.C:
			timer.Stop()
			return nil, failure("OFFLINE_SCOPE_BACKUP_TASK_DEADLINE")
		case <-timer.C:
		}
	}
}

// Verify the exact retained native execution plan before touching its resources.
func readMVPNativeExecution(root *os.Root, c configuration.Config, p mvpNativeBackupPlan) (mvpNativeBackupExecution, error) {
	var execution mvpNativeBackupExecution
	raw, e := regular(root, mvpNativeExecutionFile, c.Verification.MaximumMetadataBytes)
	if e != nil || wire.Digest(raw) != p.ExecutionSHA256 || wire.StrictJSON(raw, &execution) != nil || execution.FormatVersion != 1 || execution.DescriptorSHA256 != p.DescriptorSHA256 || execution.RuntimeSHA256 != p.RuntimeSHA256 || execution.Writer.NativePostgreSQLRecovery == nil || execution.Writer.NativePostgreSQLRecovery.BackupRequest == nil || !reflect.DeepEqual(*execution.Writer.NativePostgreSQLRecovery.BackupRequest, p.NativeRequest) || execution.Writer.Owner.ScopeID != p.ScopeID || execution.Writer.Owner.OperationID != p.AttemptID || execution.Writer.UID != p.NativeUID || execution.Writer.GID != p.NativeGID || engine.ValidateNativePostgreSQLRecoveryTask(execution.Writer) != nil || !reflect.DeepEqual(p.Steps, mvpNativeBackupSteps(execution)) {
		return execution, failure("OFFLINE_SCOPE_NATIVE_BACKUP_EXECUTION_CHANGED")
	}
	if !validMVPNativeExecutionOwners(p, execution) || !validMVPNativeMaintenanceHead(p, execution.MaintenanceHead) {
		return execution, failure("OFFLINE_SCOPE_NATIVE_BACKUP_EXECUTION_CHANGED")
	}
	descriptorRaw, e := regular(root, mvpNativeSourceDescriptorFile, c.Verification.MaximumMetadataBytes)
	var descriptor InputsDescriptor
	if e != nil || wire.Digest(descriptorRaw) != p.DescriptorSHA256 || wire.StrictJSON(descriptorRaw, &descriptor) != nil || execution.Initializer != descriptor.ImageID {
		return execution, failure("OFFLINE_SCOPE_NATIVE_BACKUP_EXECUTION_CHANGED")
	}
	return execution, nil
}

func validMVPNativeMaintenanceHead(p mvpNativeBackupPlan, raw []byte) bool {
	var head journalHead
	return len(raw) > 0 && len(raw) <= maximumEventBytes && wire.Digest(raw) == p.NativeRequest.MaintenanceSHA256 && wire.StrictJSON(raw, &head) == nil && head.FormatVersion == scopeFormatVersion && head.ScopeID == p.ScopeID && head.ConfigurationSHA256 == p.ConfigurationSHA256 && head.Sequence >= 3 && head.JournalBytes > 0 && head.JournalBytes <= configuration.MaximumJournalBytes && wire.ValidSHA256(head.EventSHA256)
}

func validMVPNativeExecutionOwners(p mvpNativeBackupPlan, e mvpNativeBackupExecution) bool {
	profile := e.Writer.NativePostgreSQLRecovery
	if profile == nil || profile.Scaffold == nil || profile.Registry == nil || profile.PreparedRelease != nil || !strings.HasPrefix(e.Initializer, "sha256:") || !wire.ValidSHA256(strings.TrimPrefix(e.Initializer, "sha256:")) {
		return false
	}
	own := func(role string) engine.Ownership {
		return engine.Ownership{ScopeID: p.ScopeID, OperationID: p.AttemptID, Role: role + "-" + p.AttemptID}
	}
	expected := map[string]engine.Ownership{
		"native-input":          own(engine.NativePostgreSQLRecoveryInputRole),
		"native-projection":     own(engine.NativePostgreSQLConfigurationRole),
		"native-artifact":       own(engine.NativePostgreSQLArtifactRole),
		"native-scratch":        own(engine.NativePostgreSQLRecoveryScratchRole),
		"native-scaffold":       own(engine.NativePostgreSQLScaffoldRole),
		"native-verify-input":   own(engine.NativePostgreSQLVerifierInputRole),
		"native-verify-scratch": own(engine.NativePostgreSQLVerifierScratchRole),
	}
	if len(e.Volumes) != len(expected) || len(e.PayloadSHA256) != len(expected) || e.Writer.VolumeOwner != expected["native-input"] || profile.ConfigurationOwner != expected["native-projection"] || profile.BackupOwner != expected["native-artifact"] || profile.ScratchOwner != expected["native-scratch"] || profile.Scaffold.Owner != expected["native-scaffold"] {
		return false
	}
	for role, owner := range expected {
		volume, ok := e.Volumes[role]
		if !ok || volume.Owner != owner || volume.UID != p.NativeUID || volume.GID != p.NativeGID || !wire.ValidSHA256(e.PayloadSHA256[role]) {
			return false
		}
	}
	for i, role := range []string{"models", "datasets", "training", "preparations"} {
		if profile.Stores[i].Owner.ScopeID != p.ScopeID || profile.Stores[i].Owner.Role != role {
			return false
		}
	}
	return profile.Registry.Owner.ScopeID == p.ScopeID && profile.Registry.Owner.Role == engine.NativePostgreSQLRegistryRole
}
