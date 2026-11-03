package scope

import (
	"context"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func (l *locked) makeRestoreDatabaseRecord(ctx context.Context, plan RestorePlan) (RestoreDatabaseRecord, []engine.ConfigurationFile, error) {
	var r RestoreDatabaseRecord
	files, e := l.makeRestoreQualificationInput(ctx, plan)
	if e != nil {
		return r, nil, e
	}
	input, _, _ := inputsState(l.events)
	p, e := l.readInputs(ctx, *input)
	if e != nil {
		return r, nil, e
	}
	model, e := compose.DecodeModel(p.model, l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		return r, nil, e
	}
	uid, gid, e := roleUser(model, "operator-config")
	runtime := runtimeState(l.events)
	if e != nil || runtime.record == nil || runtime.record.Services["control"].ID == "" {
		return r, nil, failure("OFFLINE_SCOPE_RESTORE_DATABASE_CONTROL_CONTEXT_REQUIRED")
	}
	peers := map[string]string{}
	for role, service := range runtime.record.Services {
		peers[role] = service.ID
	}
	owner := engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: plan.AttemptID, Role: "database-" + engine.DatabaseRestoredAuthInvalidate}
	volume := engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: plan.AttemptID, Role: engine.StoreRestoreConfigRole + "-" + plan.AttemptID}
	task := engine.DatabaseTask{Owner: owner, VolumeOwner: volume, Kind: engine.DatabaseRestoredAuthInvalidate, Reason: plan.Request.Reason, Image: model.Services["control"].Image, UID: uid, GID: gid, NetworkID: runtime.record.NetworkID, Peers: peers}
	if e := l.bindTaskLocalization(&task, p, model); e != nil {
		return r, nil, e
	}
	if old := restoreDatabaseState(l.events).record; old != nil && old.PlanSHA256 == restoreState(l.events).record.PlanSHA256 && old.BaseTask.Owner.OperationID == plan.AttemptID && old.BaseTask.LocalizationOwner == nil {
		// Preserve the original exact task specification of a retained operation.
		// A missing dependency can require explicit abort, never silent re-planning.
		task.LocalizationOwner = nil
	}
	proof, ok := restoreSQLProof(l.events)
	if !ok {
		return r, nil, failure("OFFLINE_SCOPE_RESTORE_QUALIFICATION_SQL_REQUIRED")
	}
	raw, e := restoreNamedFile(files, restoredRuntimeContextName)
	if e != nil {
		return r, nil, e
	}

	r = RestoreDatabaseRecord{PlanSHA256: restoreState(l.events).record.PlanSHA256, SQLProofSHA256: proof, PayloadSHA256: restoreConfigurationDigest(files), ContextSHA256: wire.Digest(raw), Initializer: p.descriptor.ImageID, BaseTask: task, Phase: "volume-create"}
	if requiresMVPNativeRestore(l.marker, plan) {
		preparation, e := l.readMVPNativeRestorePreparation()
		if e != nil {
			return r, nil, e
		}
		r.Native = &MVPNativeRestoreStageRecord{FormatVersion: 1, Stage: mvpNativeRestoreDatabaseStage, ExecutionSHA256: wire.Digest(encode(preparation)), OriginalRestoreID: plan.AttemptID}
		r.Phase = "task-create"
		r.Kind = engine.NativePostgreSQLRestoreVerify
	}
	if !validRestoreDatabaseRecord(&r, l.events, l.marker.ScopeID) {
		return r, nil, failure("OFFLINE_SCOPE_RESTORE_DATABASE_CONTEXT_INVALID")
	}
	return r, files, nil
}

func (l *locked) verifyRecordedRestoreDatabase() error {
	s := restoreDatabaseState(l.events)
	if s.record == nil {
		return nil
	}
	plan, e := l.readBoundRestorePlan()
	if e != nil {
		return failure("OFFLINE_SCOPE_RESTORE_DATABASE_PLAN_UNAVAILABLE")
	}
	expected, _, e := l.makeRestoreDatabaseRecord(context.Background(), plan)
	if e != nil {
		return e
	}
	if !restoreDatabaseIdentity(expected, *s.record) {
		return failure("OFFLINE_SCOPE_RESTORE_DATABASE_CONTEXT_CHANGED")
	}
	if s.record.Native != nil {
		return l.verifyMVPNativeRestoreHistory(context.Background(), "database")
	}
	return nil
}
