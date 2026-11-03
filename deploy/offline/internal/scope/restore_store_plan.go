package scope

import (
	"context"
	"reflect"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type RestoreStoreJob struct {
	Role   string              `json:"role"`
	Import engine.DatabaseTask `json:"import"`
	Verify engine.DatabaseTask `json:"verify"`
}

// Payload bytes stay private. This plan contains only independently verified
// artifact facts and exact fixed identities needed for execution and cleanup.
type RestoreStorePlan struct {
	Native              *MVPNativeRestoreStageRecord `json:"native,omitempty"`
	FormatVersion       int                          `json:"format_version"`
	GenerationID        string                       `json:"generation_id"`
	RestoreAttemptID    string                       `json:"restore_attempt_id"`
	ScopeID             string                       `json:"scope_id"`
	RestorePlanSHA256   string                       `json:"restore_plan_sha256"`
	SQLProofSHA256      string                       `json:"sql_proof_sha256"`
	DatabaseProofSHA256 string                       `json:"database_proof_sha256"`
	DescriptorSHA256    string                       `json:"descriptor_sha256"`
	RuntimeSHA256       string                       `json:"runtime_sha256"`
	PayloadSHA256       string                       `json:"payload_sha256"`
	ContextSHA256       string                       `json:"context_sha256"`
	Initializer         string                       `json:"initializer"`
	Jobs                []RestoreStoreJob            `json:"jobs"`
}

type restoredStoreKinds struct {
	role, importKind, verifyKind string
	verifyReason                 bool
}

func restoredStoreCatalog() []restoredStoreKinds {
	return []restoredStoreKinds{{"models", engine.ModelFilesImport, engine.ModelFilesVerify, false}, {"datasets", engine.DatasetFilesImport, engine.DatasetFilesVerify, false}, {"training", engine.TrainingFilesImport, engine.TrainingFilesVerify, true}, {"preparations", engine.PreparationFilesImport, engine.PreparationFilesVerify, true}}
}
func (l *locked) makeRestoreStorePlan(ctx context.Context, restore RestorePlan, generation string) (RestoreStorePlan, []engine.ConfigurationFile, error) {
	var out RestoreStorePlan
	if ctx.Err() != nil {
		return out, nil, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	if !identityPattern.MatchString(generation) || generation == restore.AttemptID {
		return out, nil, failure("OFFLINE_SCOPE_RESTORE_STORE_NEW_GENERATION_REQUIRED")
	}
	for _, event := range l.events {
		if event.OperationID == generation || event.Volume != nil && event.Volume.ResourceOperationID == generation {
			return out, nil, failure("OFFLINE_SCOPE_RESTORE_STORE_GENERATION_ALREADY_USED")
		}
	}
	databaseProof, ok := restoreDatabaseProof(l.events)
	if !ok {
		return out, nil, failure("OFFLINE_SCOPE_RESTORE_STORE_DATABASE_QUALIFICATION_REQUIRED")
	}
	if e := l.verifyRecordedRestoreDatabase(); e != nil {
		return out, nil, e
	}
	base, files, e := l.makeRestoreDatabaseRecord(ctx, restore)
	if e != nil {
		return out, nil, e
	}
	if len(restore.Source.Stores) < 1 || len(restore.Source.Stores) > len(restoredStoreCatalog()) {
		return out, nil, failure("OFFLINE_SCOPE_RESTORE_STORE_SELECTED_PROFILES_REQUIRED")
	}
	_, original, e := l.readRestoreSQLSource(ctx, restore)
	if e != nil {
		return out, nil, e
	}
	root, e := openBackupDirectory(restore.Request.BackupRoot)
	if e != nil {
		return out, nil, e
	}
	defer root.Close()
	raw, e := regular(root, backupManifestFile, l.config.Verification.MaximumMetadataBytes)
	var manifest BackupManifest
	if e != nil || wire.Digest(raw) != restore.Request.ManifestSHA256 || wire.StrictJSON(raw, &manifest) != nil {
		return out, nil, failure("OFFLINE_SCOPE_RESTORE_STORE_MANIFEST_CHANGED")
	}
	selected := map[string]bool{}
	for _, role := range restore.Source.Stores {
		if selected[role] {
			return out, nil, failure("OFFLINE_SCOPE_RESTORE_STORE_DUPLICATE_PROFILE")
		}
		selected[role] = true
	}
	archiveFiles := map[string]engine.BackupArchiveFile{}
	for _, file := range manifest.Files {
		if _, duplicate := archiveFiles[file.Name]; duplicate {
			return out, nil, failure("OFFLINE_SCOPE_RESTORE_STORE_DUPLICATE_ARTIFACT")
		}
		archiveFiles[file.Name] = file
	}
	out = RestoreStorePlan{FormatVersion: 1, GenerationID: generation, RestoreAttemptID: restore.AttemptID, ScopeID: l.marker.ScopeID, RestorePlanSHA256: base.PlanSHA256, SQLProofSHA256: base.SQLProofSHA256, DatabaseProofSHA256: databaseProof, DescriptorSHA256: restore.Target.DescriptorSHA256, RuntimeSHA256: restore.Target.RuntimeSHA256, PayloadSHA256: base.PayloadSHA256, ContextSHA256: base.ContextSHA256, Initializer: base.Initializer, Jobs: []RestoreStoreJob{}}
	for _, kind := range restoredStoreCatalog() {
		if !selected[kind.role] {
			continue
		}
		delete(selected, kind.role)
		target, declared := volumeState(l.events).roles[kind.role]
		source := original.Jobs[kind.role].Store
		if !declared || target.Key != kind.role || target.Phase != phasePrepared || source == nil || source.UID != target.UID || source.GID != target.GID || source.UID != base.BaseTask.UID || source.GID != base.BaseTask.GID {
			return RestoreStorePlan{}, nil, failure("OFFLINE_SCOPE_RESTORE_STORE_NUMERIC_TARGET_OWNER_MISMATCH")
		}
		artifact, found := archiveFiles[kind.role+".tar"]
		if !found || artifact.Bytes > restore.Request.MaximumBytes {
			return RestoreStorePlan{}, nil, failure("OFFLINE_SCOPE_RESTORE_STORE_SOURCE_ARTIFACT_REQUIRED")
		}
		file, e := openRestoreStoreArchive(root, kind.role)
		if e != nil {
			return RestoreStorePlan{}, nil, failure("OFFLINE_SCOPE_RESTORE_STORE_ARCHIVE_UNAVAILABLE")
		}
		profile, e := engine.DescribeStoreRestoreArchive(ctx, file, engine.StoreRestoreArtifact{Role: kind.role, UID: source.UID, GID: source.GID, Bytes: artifact.Bytes, SHA256: artifact.SHA256, MaximumEntries: restore.Request.MaximumEntries})
		closeError := file.Close()
		if e != nil {
			return RestoreStorePlan{}, nil, e
		}
		if closeError != nil {
			return RestoreStorePlan{}, nil, failure("OFFLINE_SCOPE_RESTORE_STORE_ARCHIVE_CLOSE_FAILED")
		}
		job, e := assembleRestoreStoreJob(base.BaseTask, l.volumeOwner(target), profile, generation, kind)
		if e != nil {
			return RestoreStorePlan{}, nil, e
		}
		out.Jobs = append(out.Jobs, job)
	}
	if len(selected) > 0 || len(out.Jobs) != len(restore.Source.Stores) {
		return RestoreStorePlan{}, nil, failure("OFFLINE_SCOPE_RESTORE_STORE_SELECTED_PROFILE_INVALID")
	}
	return out, files, nil
}
func assembleRestoreStoreJob(base engine.DatabaseTask, target engine.Ownership, profile engine.StoreRestoreProfile, generation string, kind restoredStoreKinds) (RestoreStoreJob, error) {
	bad := func() (RestoreStoreJob, error) {
		return RestoreStoreJob{}, failure("OFFLINE_SCOPE_RESTORE_STORE_TASK_CONTEXT_INVALID")
	}
	if !identityPattern.MatchString(generation) || generation == base.Owner.OperationID || base.ValidateRestoredDatabaseTask() != nil || target.ScopeID != base.Owner.ScopeID || target.Role != kind.role || profile.Artifact.Role != kind.role || profile.Artifact.UID != base.UID || profile.Artifact.GID != base.GID {
		return bad()
	}
	admitted := false
	for _, entry := range restoredStoreCatalog() {
		if entry == kind {
			admitted = true
		}
	}
	if !admitted {
		return bad()
	}
	task := base
	task.Owner.OperationID = generation
	task.Owner.Role = "database-" + kind.importKind
	task.Kind = kind.importKind
	task.VolumeOwner = engine.Ownership{ScopeID: base.Owner.ScopeID, OperationID: generation, Role: engine.StoreRestoreConfigRole + "-" + generation}
	task.OutputOwner = &target
	task.SourceOwner = &engine.Ownership{ScopeID: base.Owner.ScopeID, OperationID: generation, Role: engine.StoreRestoreArchiveRole + "-" + kind.role + "-" + generation}
	task.StoreRestore = &profile
	verify := task
	verify.Owner.Role = "database-" + kind.verifyKind
	verify.Kind = kind.verifyKind
	verify.SourceOwner = nil
	verify.StoreRestore = nil
	if !kind.verifyReason {
		verify.Reason = ""
	}
	if task.ValidateRestoredStoreTask() != nil || verify.ValidateRestoredStoreTask() != nil {
		return bad()
	}
	return RestoreStoreJob{Role: kind.role, Import: task, Verify: verify}, nil
}

// Native V2 retains readonly verification templates for later current-store
// checks. The original full native actor already imported all four stores;
// these templates never authorize a second import or a legacy split restore.
func (l *locked) makeMVPNativeRestoreStorePlan(ctx context.Context, restore RestorePlan) (RestoreStorePlan, error) {
	var out RestoreStorePlan
	bad := func() (RestoreStorePlan, error) {
		return RestoreStorePlan{}, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_STORES_CONTEXT_CHANGED")
	}
	if ctx == nil || ctx.Err() != nil {
		return out, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	if l.marker.Profile != wire.MVPPostgreSQLCPUProfile || !validMVPNativeRestoreSource(restore.Source) {
		return bad()
	}
	bound, e := l.readBoundRestorePlan()
	if e != nil {
		return out, e
	}
	if !reflect.DeepEqual(bound, restore) {
		return bad()
	}
	databaseProof, ok := restoreDatabaseProof(l.events)
	if !ok {
		return out, failure("OFFLINE_SCOPE_RESTORE_STORE_DATABASE_QUALIFICATION_REQUIRED")
	}
	if e := l.verifyRecordedRestoreDatabase(); e != nil {
		return out, e
	}
	base, _, e := l.makeRestoreDatabaseRecord(ctx, restore)
	if e != nil {
		return out, e
	}
	execution, e := l.readMVPNativeRestoreExecution(ctx)
	if e != nil {
		return out, e
	}
	writer := execution.Writer
	if !validMVPNativeRestoreStageRecord(base.Native, mvpNativeRestoreDatabaseStage, restore.AttemptID) || base.Native.ExecutionSHA256 != execution.PreparationSHA256 || writer.NativePostgreSQLRecovery == nil || writer.Owner.ScopeID != l.marker.ScopeID || writer.Owner.OperationID != restore.AttemptID || writer.UID != base.BaseTask.UID || writer.GID != base.BaseTask.GID || engine.ValidateNativePostgreSQLRecoveryTask(writer) != nil {
		return bad()
	}
	out = RestoreStorePlan{
		Native:              &MVPNativeRestoreStageRecord{FormatVersion: 1, Stage: mvpNativeRestoreStoresStage, ExecutionSHA256: execution.PreparationSHA256, OriginalRestoreID: restore.AttemptID},
		FormatVersion:       2,
		GenerationID:        restore.AttemptID,
		RestoreAttemptID:    restore.AttemptID,
		ScopeID:             l.marker.ScopeID,
		RestorePlanSHA256:   base.PlanSHA256,
		SQLProofSHA256:      base.SQLProofSHA256,
		DatabaseProofSHA256: databaseProof,
		DescriptorSHA256:    restore.Target.DescriptorSHA256,
		RuntimeSHA256:       restore.Target.RuntimeSHA256,
		PayloadSHA256:       base.PayloadSHA256,
		ContextSHA256:       base.ContextSHA256,
		Initializer:         base.Initializer,
		Jobs:                []RestoreStoreJob{},
	}
	for i, kind := range restoredStoreCatalog() {
		target, found := volumeState(l.events).roles[kind.role]
		if !found || target.Key != kind.role || target.Phase != phasePrepared || target.UID != base.BaseTask.UID || target.GID != base.BaseTask.GID || writer.NativePostgreSQLRecovery.Stores[i].Owner != l.volumeOwner(target) {
			return bad()
		}
		task := base.BaseTask
		task.Kind = kind.verifyKind
		task.Owner.Role = "database-" + kind.verifyKind
		task.OutputOwner = new(engine.Ownership)
		*task.OutputOwner = l.volumeOwner(target)
		task.SourceOwner = nil
		task.StoreRestore = nil
		if !kind.verifyReason {
			task.Reason = ""
		}
		destination, e := task.RestoredStoreDestination()
		if e != nil || destination+"/private" != writer.NativePostgreSQLRecovery.Stores[i].Target || task.ValidateRestoredStoreTask() != nil {
			return bad()
		}
		out.Jobs = append(out.Jobs, RestoreStoreJob{Role: kind.role, Verify: task})
	}
	return out, nil
}
